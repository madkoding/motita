package schedule

import (
	"context"
	"sync"
	"time"

	"github.com/madkoding/motita/internal/logx"
)

// Firer runs one scheduled task and reports how it ended.
//
// It is injected, and that is the whole design: this package decides WHAT is due and
// WHEN, and the caller decides what firing MEANS. The gateway's firer starts an agent
// turn; a test's firer appends to a slice. Neither package has to know about the other,
// and the arithmetic is testable without a timer.
//
// A non-nil error is a firing that failed, and its text is what the record keeps as the
// outcome: a schedule that failed silently is a schedule nobody can repair.
type Firer func(ctx context.Context, s Schedule) (string, error)

// watcherTick is the default resolution at which a cadence is noticed.
//
// It is NOT a cadence: a task set to "every 1h" may start up to this late, and that is
// the intended trade. A resolution of a second would wake the process sixty times for
// nothing on a machine that already runs commands.
const watcherTick = 30 * time.Second

// Watcher notices what is due and fires it.
type Watcher struct {
	store *Store
	fire  Firer
	log   *logx.Logger
	tick  time.Duration
	// now is the clock. It is a field rather than a call to time.Now so a pass is
	// deterministic in a test: the alternative is a test that sleeps and then hopes.
	now func() time.Time

	// mu guards inPass, which is the overlap guard. A firing can take minutes (it is an
	// agent turn), and the tick keeps coming: without this the same task would start
	// again while the first is still going.
	mu     sync.Mutex
	inPass bool
}

// NewWatcher builds a watcher over a store.
//
// A nil logger is replaced with the global one, so a caller that has not wired logging
// yet still sees the lines rather than losing them.
func NewWatcher(store *Store, fire Firer, log *logx.Logger) *Watcher {
	if log == nil {
		log = logx.Global()
	}
	return &Watcher{store: store, fire: fire, log: log, tick: watcherTick, now: time.Now}
}

// Run ticks until the context is cancelled. It blocks, so its caller is the one that
// decides whether that is a goroutine.
//
// Each pass runs in ITS OWN goroutine so a slow firing never stalls the ticker: the
// overlap guard in FireDue is what keeps that safe, by refusing a second pass while the
// first is still inside a firer.
func (w *Watcher) Run(ctx context.Context) {
	tick := w.tick
	if tick <= 0 {
		tick = watcherTick
	}
	ticker := time.NewTicker(tick)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			go w.FireDue(ctx)
		}
	}
}

// SetTick changes the resolution at which a cadence is noticed. It exists for a test that
// must see a firing happen in seconds rather than in half a minute, and it must be called
// before Run: the ticker is built once, so a change made after the loop started would be
// a change nobody sees.
func (w *Watcher) SetTick(d time.Duration) { w.tick = d }

// FireDue is one pass: it fires every task that is due and returns their ids.
//
// It is SYNCHRONOUS and therefore testable, which is why the asynchrony is in Run and
// not here. A pass refuses to start while another is running, and that refusal returns
// an empty list rather than waiting, because waiting would queue passes behind a firing
// that may take minutes.
func (w *Watcher) FireDue(ctx context.Context) []string {
	w.mu.Lock()
	if w.inPass {
		w.mu.Unlock()
		return nil
	}
	w.inPass = true
	w.mu.Unlock()
	defer func() {
		w.mu.Lock()
		w.inPass = false
		w.mu.Unlock()
	}()

	all, err := w.store.LoadAll()
	if err != nil {
		// Reported and swallowed: a store that cannot be read right now is not a reason
		// to stop scheduling for the life of the process.
		w.log.Warn("could not read the schedule store", "error", err.Error())
		return nil
	}

	now := w.now()
	var fired []string
	for _, s := range all {
		if !s.Due(now) {
			continue
		}
		outcome, err := w.fire(ctx, s)
		if err != nil {
			outcome = err.Error()
			w.log.Warn("a scheduled task failed", "id", s.ID, "error", err.Error())
		}
		// The record is written AFTER the firing, and it is what stops the task firing
		// again on the next tick: a firing that did not update it would fire forever.
		s.LastRun = now
		s.RunCount++
		s.LastOutcome = outcome
		if err := w.store.Save(s); err != nil {
			w.log.Warn("could not record the firing", "id", s.ID, "error", err.Error())
		}
		fired = append(fired, s.ID)
	}
	return fired
}

// busy reports whether a pass is in flight, for tests and for the loop's own guard.
func (w *Watcher) busy() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.inPass
}
