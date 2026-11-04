package agent

import (
	"context"
	"strings"

	"github.com/madkoding/motita/internal/steer"
)

// Steer hands the running task a message from the person. The model call in flight is cut (see
// package steer) and the round is asked again with the message in front of it, so a message typed
// mid-task steers the work at once instead of waiting for the reply being written. It reports
// whether a run was there to read it: a message to an idle agent is not steering, and the caller
// queues it instead.
func (a *Agent) Steer(text string) bool {
	return a.steer.Push(text)
}

// UnreadSteering returns the messages the last run accepted and ended without reading, for the
// caller to send as the next turn.
func (a *Agent) UnreadSteering() []string {
	return a.steer.Unread()
}

// steerableCall is the context of one model call of the turn's outermost run: cut when the person
// writes. A subtask's calls are not cut, since only the outermost run reads the messages.
func (r *taskRun) steerableCall() (context.Context, func()) {
	if r.depth != 0 {
		return r.ctx, func() {}
	}
	return r.a.steer.Call(r.ctx)
}

// absorbSteering puts the person's mid-task messages in front of the next round, in the journal
// the action phase reads, and in the trail the person sees.
func (r *taskRun) absorbSteering() {
	if r.depth != 0 {
		return
	}
	notes := r.a.steer.Take()
	if len(notes) == 0 {
		return
	}
	for _, n := range notes {
		r.a.report("you: %s", n)
	}
	r.journal = append(r.journal, roundRecord{
		round:    r.round,
		kind:     roundProgress,
		commands: "(the person added: " + truncate(collapse(strings.Join(notes, " / ")), 160) + ")",
		detail:   steer.Note(notes),
	})
}
