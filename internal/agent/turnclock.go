package agent

import (
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// WHERE A TURN'S TIME GOES. A slow turn used to be diagnosed by adding up the "LLM phase" lines
// of its log by hand, and the waits that are not the model - the commands, the gate, a question
// nobody had answered yet - were in no line at all. turnClock adds them up as they happen, and
// the turn ends with one line that says how its time divided (see logTurnTimings).

// turnClock is the time a turn spent in each place, and how often.
type turnClock struct {
	model, commands, approvals, anchor          atomic.Int64 // nanoseconds
	modelCalls, commandRuns, asked, reusedAsked atomic.Int64
}

// since adds the time from start to now to d, and one to n.
func since(d, n *atomic.Int64, start time.Time) {
	d.Add(int64(time.Since(start)))
	n.Add(1)
}

// reset starts the clock over, for a new turn.
func (c *turnClock) reset() {
	for _, v := range []*atomic.Int64{&c.model, &c.commands, &c.approvals, &c.anchor,
		&c.modelCalls, &c.commandRuns, &c.asked, &c.reusedAsked} {
		v.Store(0)
	}
}

// ms is a duration counter in milliseconds.
func ms(v *atomic.Int64) int64 { return time.Duration(v.Load()).Milliseconds() }

// logTurnTimings writes how the turn that began at start divided its time. "other" is what no
// counter holds: the tree fingerprints, the prompts being built, the transcript being saved.
func (a *Agent) logTurnTimings(start time.Time) {
	c := &a.clock
	total := time.Since(start).Milliseconds()
	counted := ms(&c.model) + ms(&c.commands) + ms(&c.approvals) + ms(&c.anchor)
	a.log.Info("turn timings", "total_ms", total,
		"model_ms", ms(&c.model), "model_calls", c.modelCalls.Load(),
		"commands_ms", ms(&c.commands), "commands", c.commandRuns.Load(),
		"approval_wait_ms", ms(&c.approvals), "approvals_asked", c.asked.Load(),
		"approvals_reused", c.reusedAsked.Load(),
		"anchor_ms", ms(&c.anchor), "other_ms", max(total-counted, 0))
}

// turnApprovals are the lines a person approved in this turn. A line the run proposes again in the
// same turn - a push retried after a hook was fixed, an install run again - is the line the person
// just said yes to, word for word, and asking again only made them answer the same question twice.
// It never reaches past the turn, never covers a line that differs by a character, and a background
// agent keeps its own (empty) set.
type turnApprovals struct {
	mu    sync.Mutex
	lines map[string]bool
}

// keep records a line the person approved.
func (t *turnApprovals) keep(line string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.lines == nil {
		t.lines = map[string]bool{}
	}
	t.lines[strings.TrimSpace(line)] = true
}

// has reports whether the person approved line in this turn.
func (t *turnApprovals) has(line string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.lines[strings.TrimSpace(line)]
}

// reset forgets every approval, for a new turn.
func (t *turnApprovals) reset() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.lines = nil
}
