package schedule

import (
	"context"
	"errors"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/madkoding/motita/internal/logx"
)

// recordingFirer is the seam the whole design rests on: the watcher decides WHAT is
// due and the caller decides what firing means, so a test can drive the scheduler
// with no agent, no network and no timer.
type recordingFirer struct {
	mu     sync.Mutex
	fired  []string
	result string
	err    error
	block  chan struct{}
}

func (f *recordingFirer) fire(ctx context.Context, s Schedule) (string, error) {
	if f.block != nil {
		<-f.block
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.fired = append(f.fired, s.ID)
	return f.result, f.err
}

func (f *recordingFirer) ids() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.fired...)
}

func newTestWatcher(t *testing.T, firer Firer) (*Watcher, *Store) {
	t.Helper()
	st, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	w := NewWatcher(st, firer, logx.Global())
	return w, st
}

// A pass fires exactly what is due, records the run on the record, and leaves the rest
// alone. The record is the evidence: a firing that does not update it fires again on
// the next tick, forever.
func TestAFiringPassRunsWhatIsDueAndRecordsIt(t *testing.T) {
	now := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	f := &recordingFirer{result: "the run finished"}
	w, st := newTestWatcher(t, f.fire)
	w.now = func() time.Time { return now }

	mustSave(t, st, Schedule{ID: "due", Title: "d", Task: "t", Kind: KindTask,
		Every: Duration(time.Hour), Enabled: true, Created: now.Add(-2 * time.Hour)})
	mustSave(t, st, Schedule{ID: "later", Title: "l", Task: "t", Kind: KindTask,
		Every: Duration(time.Hour), Enabled: true, Created: now})
	mustSave(t, st, Schedule{ID: "paused", Title: "p", Task: "t", Kind: KindTask,
		Every: Duration(time.Hour), Enabled: false, Created: now.Add(-2 * time.Hour)})

	fired := w.FireDue(context.Background())
	if len(fired) != 1 || fired[0] != "due" {
		t.Fatalf("FireDue fired %v, want exactly [due]", fired)
	}

	rec, err := st.Load("due")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if rec.RunCount != 1 || !rec.LastRun.Equal(now) || rec.LastOutcome != "the run finished" {
		t.Fatalf("the record was not updated: %+v", rec)
	}
	// A second pass at the same instant fires nothing: the cadence restarted.
	if again := w.FireDue(context.Background()); len(again) != 0 {
		t.Fatalf("a second pass fired %v, want nothing", again)
	}
}

// A firing that fails is RECORDED as failed and does not take the scheduler down with
// it: one broken task must not stop the others, which is the whole reason they are
// separate records.
func TestAFailedFiringIsRecordedAndOthersStillRun(t *testing.T) {
	now := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	f := &recordingFirer{err: errors.New("the agent could not start")}
	w, st := newTestWatcher(t, f.fire)
	w.now = func() time.Time { return now }

	mustSave(t, st, Schedule{ID: "broken", Title: "b", Task: "t", Kind: KindTask,
		Every: Duration(time.Hour), Enabled: true, Created: now.Add(-2 * time.Hour)})

	fired := w.FireDue(context.Background())
	if len(fired) != 1 {
		t.Fatalf("FireDue fired %v, want the one due task", fired)
	}
	rec, err := st.Load("broken")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if rec.LastOutcome != "the agent could not start" {
		t.Errorf("LastOutcome = %q, want the failure text", rec.LastOutcome)
	}
}

// A pass that is still running must not be joined by another. Without this guard a
// slow firing and the next tick overlap, and the same task starts twice.
func TestASecondPassRefusesToOverlapTheFirst(t *testing.T) {
	now := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	f := &recordingFirer{block: make(chan struct{})}
	w, st := newTestWatcher(t, f.fire)
	w.now = func() time.Time { return now }
	mustSave(t, st, Schedule{ID: "slow", Title: "s", Task: "t", Kind: KindTask,
		Every: Duration(time.Hour), Enabled: true, Created: now.Add(-2 * time.Hour)})

	done := make(chan struct{})
	go func() { defer close(done); w.FireDue(context.Background()) }()

	// Wait until the first firing is inside the firer.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && !w.busy() {
		time.Sleep(time.Millisecond)
	}
	if !w.busy() {
		t.Fatal("the pass never reported itself as running")
	}

	// The second pass returns at once, having fired nothing.
	if fired := w.FireDue(context.Background()); len(fired) != 0 {
		t.Fatalf("an overlapping pass fired %v, want nothing", fired)
	}
	close(f.block)
	<-done
}

// A cancelled context stops the loop. The loop is the only goroutine, and a loop that
// outlives its context is a leak in a process that is meant to be restartable.
func TestTheWatchLoopStopsWithItsContext(t *testing.T) {
	w, _ := newTestWatcher(t, (&recordingFirer{}).fire)
	w.tick = time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())

	stopped := make(chan struct{})
	go func() { defer close(stopped); w.Run(ctx) }()
	time.Sleep(10 * time.Millisecond)
	cancel()

	select {
	case <-stopped:
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return after its context was cancelled")
	}
}

// A store the loop cannot read is reported and the loop keeps going: a transient
// filesystem problem must not silently end scheduling for the life of the process.
func TestAPassThatCannotReadTheStoreFiresNothing(t *testing.T) {
	w, _ := newTestWatcher(t, (&recordingFirer{}).fire)
	if err := os.RemoveAll(w.store.dir); err != nil {
		t.Fatal(err)
	}
	if fired := w.FireDue(context.Background()); len(fired) != 0 {
		t.Fatalf("a pass with no store fired %v", fired)
	}
}

func mustSave(t *testing.T, st *Store, s Schedule) {
	t.Helper()
	if err := st.Save(s); err != nil {
		t.Fatalf("Save(%s): %v", s.ID, err)
	}
}

// SetTick is how a test drives the loop without waiting half a minute - and how the
// end-to-end script does the same. A tick of zero or less would build a ticker with no
// period, so run() falls back to the default rather than spinning.
func TestSetTickIsHonouredByTheLoop(t *testing.T) {
	f := &recordingFirer{result: "ok"}
	w, st := newTestWatcher(t, f.fire)
	created := time.Now().Add(-2 * time.Hour)
	mustSave(t, st, Schedule{ID: "due", Title: "d", Task: "t", Kind: KindTask,
		Every: Duration(time.Hour), Enabled: true, Created: created})
	w.SetTick(5 * time.Millisecond)

	ctx, cancel := context.WithCancel(context.Background())
	stopped := make(chan struct{})
	go func() { defer close(stopped); w.Run(ctx) }()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && len(f.ids()) == 0 {
		time.Sleep(2 * time.Millisecond)
	}
	cancel()
	<-stopped

	if len(f.ids()) != 1 {
		t.Fatalf("the loop fired %v, want exactly one firing", f.ids())
	}
}
