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

// TurnTimings is how a turn divided its time, in milliseconds, with the counts behind each part.
// Other is what no counter holds: the tree fingerprints, the prompts being built, the transcript
// being saved.
type TurnTimings struct {
	TotalMS         int64 `json:"total_ms"`
	ModelMS         int64 `json:"model_ms"`
	ModelCalls      int64 `json:"model_calls"`
	CommandsMS      int64 `json:"commands_ms"`
	Commands        int64 `json:"commands"`
	ApprovalWaitMS  int64 `json:"approval_wait_ms"`
	ApprovalsAsked  int64 `json:"approvals_asked"`
	ApprovalsReused int64 `json:"approvals_reused"`
	AnchorMS        int64 `json:"anchor_ms"`
	OtherMS         int64 `json:"other_ms"`
}

// turnTimings is how the turn that began at start has divided its time so far.
func (a *Agent) turnTimings(start time.Time) *TurnTimings {
	c := &a.clock
	t := &TurnTimings{TotalMS: time.Since(start).Milliseconds(),
		ModelMS: ms(&c.model), ModelCalls: c.modelCalls.Load(),
		CommandsMS: ms(&c.commands), Commands: c.commandRuns.Load(),
		ApprovalWaitMS: ms(&c.approvals), ApprovalsAsked: c.asked.Load(), ApprovalsReused: c.reusedAsked.Load(),
		AnchorMS: ms(&c.anchor)}
	t.OtherMS = max(t.TotalMS-t.ModelMS-t.CommandsMS-t.ApprovalWaitMS-t.AnchorMS, 0)
	return t
}

// logTurnTimings writes how a turn divided its time.
func (a *Agent) logTurnTimings(t *TurnTimings) {
	a.log.Info("turn timings", "total_ms", t.TotalMS,
		"model_ms", t.ModelMS, "model_calls", t.ModelCalls,
		"commands_ms", t.CommandsMS, "commands", t.Commands,
		"approval_wait_ms", t.ApprovalWaitMS, "approvals_asked", t.ApprovalsAsked,
		"approvals_reused", t.ApprovalsReused,
		"anchor_ms", t.AnchorMS, "other_ms", t.OtherMS)
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
