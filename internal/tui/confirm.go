package tui

import (
	"context"
	"fmt"
	"strings"

	"github.com/madkoding/motita/internal/agent"
)

// confirmState is the window a consequential command is approved in.
//
// It is the second thing in the interface that CAPTURES the keys, after the questions window,
// and it exists because the alternative is worse in both directions: refusing silently makes the
// agent useless for the work it was pointed at, and running without asking is the behaviour this
// whole mechanism was built to stop.
//
// The window is drawn while a run is IN FLIGHT. That is the point of it: the command is proposed
// by the agent in the middle of a turn, and the user answers without ending the turn — which is
// why it is driven from the run loop (`awaitRun`) and not from the input loop.
type confirmState struct {
	// req is what the agent is asking to do, carrying the EXACT line. The user approves that
	// text, so that text is what the window shows — never a summary of it.
	req agent.ApprovalRequest
	// reply carries the answer back to the agent. It is buffered, so the interface never
	// blocks on a reader that has already been cancelled.
	reply chan bool
}

// answering reports whether the confirmation window is open.
func (t *TUI) answeringConfirm() bool {
	return t.confirm != nil
}

// confirmRows is how many rows the window needs, which the layout reserves before drawing.
func (t *TUI) confirmRows() int {
	if !t.answeringConfirm() {
		return 0
	}
	return len(t.confirmLines(0))
}

// confirmLines renders the window, capped to at most `max` rows (zero meaning no limit).
//
// The line is drawn FIRST and in full, because it is the thing being decided. A window that
// showed a truncated command would be asking the user to approve something they cannot read,
// which is worse than not asking at all.
func (t *TUI) confirmLines(max int) []string {
	if !t.answeringConfirm() {
		return nil
	}
	width := t.bodyWidth()
	var out []string

	out = append(out, t.confirmLine(t.color(colAccent, colBase, "The agent wants to run:"), width))
	// The command is WRAPPED rather than clipped: a long line is exactly the one worth reading
	// to the end, and the user is approving this text and not a summary of it.
	for _, l := range wrapVisible(t.confirm.req.Command, width-4) {
		out = append(out, t.confirmLine("  "+l, width))
	}
	if t.confirm.req.Reason != "" {
		for _, l := range wrapVisible(t.confirm.req.Reason, width-4) {
			out = append(out, t.confirmLine("  "+t.muted(l), width))
		}
	}
	out = append(out, t.confirmLine(t.muted("y = yes, run it · Enter or n = no"), width))

	if max > 0 && len(out) > max {
		// The cap keeps the LAST row, so the keys hint always survives the cut: the user has
		// to be able to see how to get out of the window.
		out = append(out[:max-1], out[len(out)-1])
	}
	return out
}

// confirmLine draws one window row with the interface margin.
func (t *TUI) confirmLine(s string, width int) string {
	return t.plainLine(clipLine("│ "+s, width))
}

// askConfirm hands a command to the interface and waits for the answer.
//
// It is the Approver the agent is given for a run: the agent blocks here — it cannot continue
// without the answer — and the answer comes from the run loop, which is reading the keys while
// this is waiting.
//
// The context is watched on both sides of the handoff, so a cancelled run (Ctrl+C, a closed
// input, a shutdown) does not leave a goroutine parked forever on a window nobody will answer.
func (t *TUI) askConfirm(ctx context.Context, req agent.ApprovalRequest) (bool, error) {
	c := &confirmState{req: req, reply: make(chan bool, 1)}

	select {
	case t.approvalChannel() <- c:
	case <-ctx.Done():
		return false, ctx.Err()
	}

	select {
	case approved := <-c.reply:
		return approved, nil
	case <-ctx.Done():
		// A cancelled run REFUSES the command. It is the cautious answer, and it is the honest
		// one: the user did not say yes, and silence is not consent for a consequential action.
		return false, ctx.Err()
	}
}

// approvalChannel returns the channel the run loop listens on, creating it exactly once.
//
// The Once is what makes it safe: this is called from the run loop AND from the agent's own
// goroutine, and a lazy `if nil` check between them is a data race whose worst outcome is each
// caller holding a different channel — the turn hangs with the window never opening.
func (t *TUI) approvalChannel() chan *confirmState {
	t.approvalsOnce.Do(func() { t.approvals = make(chan *confirmState) })
	return t.approvals
}

// approverFor returns the callback the runner installs on the agent for the next turn.
func (t *TUI) approverFor() agent.Approver {
	return t.askConfirm
}

// installApprover hands the runner the channel a consequential command is confirmed through,
// when the runner is one that accepts one.
//
// It is an optional interface rather than a method on Runner so the many small runners in the
// tests — which never run a real agent and therefore never ask anything — keep working unchanged.
func (t *TUI) installApprover() {
	if setter, ok := t.Runner.(interface{ SetApprover(agent.Approver) }); ok {
		setter.SetApprover(t.approverFor())
	}
}

// answerConfirm reads keys until the user answers, then hands the answer back to the agent.
//
// It runs in the RUN LOOP (the main goroutine), which is what makes it possible at all: the
// agent is blocked in a background goroutine waiting on the answer, and the keys are being read
// here. The window closes as soon as the answer is given, so the turn continues immediately.
func (t *TUI) answerConfirm(ctx context.Context, c *confirmState) {
	t.confirm = c
	t.drawFrame()
	// The window is always closed on the way out, whatever the reason: an open window after the
	// turn ended would capture the keys of a user who is trying to type their next message.
	defer func() {
		t.confirm = nil
		t.drawFrame()
	}()

	for {
		line, ok := t.readLine(ctx)
		if !ok {
			// The input ended or the run was cancelled. The command is NOT approved: the user
			// did not say yes, and a consequential action must not run on a guess.
			t.recordDecision(c.req, false)
			c.reply <- false
			return
		}
		switch line {
		// The Spanish spellings are tolerated as well as the advertised keys: a Spanish typist
		// reaches for them, and accepting an answer the hint did not name costs nothing.
		case "s", "S", "y", "Y", "si", "sí", "yes": // spanish-fixture: accepted input, a Spanish typist reaches for these
			t.recordDecision(c.req, true)
			c.reply <- true
			return
		case "n", "N", "no", keyEsc, keyEnter:
			// Enter is the CAUTIOUS answer, not the eager one. A stray Enter is far more likely
			// than a considered one, and the action behind this window is the one the user
			// should have to say yes to on purpose.
			t.recordDecision(c.req, false)
			c.reply <- false
			return
		}
		// Anything else is not an answer: it is redrawn away so the window stays on screen and
		// the keys hint stays visible.
		t.drawFrame()
	}
}

// confirmHintText is the sentence the conversation keeps after a decision, so the transcript
// records what was approved and what was not.
func confirmHintText(req agent.ApprovalRequest, approved bool) string {
	verb := "rejected"
	if approved {
		verb = "approved"
	}
	return fmt.Sprintf("[%s by the user] %s", verb, strings.TrimSpace(req.Command))
}

// recordDecision writes that sentence into the conversation.
//
// It is called from the ONE place the answer is decided, so the two branches cannot record
// different things, and it is a method on the TUI rather than a bare helper so the note lands in
// the transcript the user reads instead of only being reachable from a test.
func (t *TUI) recordDecision(req agent.ApprovalRequest, approved bool) {
	t.addMessage(AuthorAgent, confirmHintText(req, approved))
}
