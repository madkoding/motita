package tui

import (
	"context"
	"strings"
	"time"
)

// turn is a run in flight as the input loop sees it: the channels it reports through, and what to
// do with each report.
//
// It exists so a run does not have to own the loop. A turn used to be awaited in place - the input
// loop called the runner and blocked until it returned - so no key was read while the agent worked:
// Escape could not stop it, the conversation could not be scrolled, and the only way out of a turn
// that would not end was Ctrl+C, which closes the whole program. Measured on a real terminal:
// Escape pressed two seconds into a task did nothing, and the task ran its three attempts to the
// end.
//
// Now, with the terminal in character mode, the turn's reports are applied BY THE INPUT LOOP,
// between keystrokes (see readByteOrCancel). Every change to the view still happens on one
// goroutine, so the conversation is never written by two at once - the property the synchronous
// version had for free, and the reason this is a set of channels the loop selects on rather than a
// goroutine that writes into the view.
type turn struct {
	progress   <-chan string
	done       <-chan runOutcome
	onProgress func(string)
	onDone     func(runOutcome)
	// leave ends this interface's wait for the turn, and nothing else. It is NOT what Escape
	// calls: behind a gateway the run lives on, as it must when a client goes away, and stopping
	// it is the separate act Escape performs (see stopper).
	leave context.CancelFunc
	// tick animates the spinner while nothing is reported: a model that thinks for a minute before
	// its first word still has to look alive.
	tick *time.Ticker
}

// spinInterval is how often the spinner moves while a turn runs.
var spinInterval = 200 * time.Millisecond

// startTurn hands a turn to whoever drives it.
//
// In character mode the input loop drives it, and this returns at once. Without character mode
// there is no key to read until a whole line is typed, so nothing would be gained by returning:
// the turn is awaited here, the way it always was.
func (t *TUI) startTurn(runCtx context.Context, tr *turn) {
	// A snapshot of the run's agents is view state, not a line of the turn: it is taken here, once,
	// for every kind of turn, before the turn's own handler can write it into the conversation.
	handle := tr.onProgress
	tr.onProgress = func(p string) {
		if t.takeAgents(p) {
			t.advance()
			return
		}
		handle(p)
	}
	if !t.charMode {
		tr.onDone(t.awaitRun(runCtx, tr.progress, tr.done, tr.onProgress))
		return
	}
	tr.tick = time.NewTicker(spinInterval)
	t.current = tr
}

// endCurrent settles the turn that just reported its outcome, and starts the message the user
// queued while it ran.
func (t *TUI) endCurrent(ctx context.Context, out runOutcome) {
	tr := t.current
	t.current = nil
	tr.tick.Stop()
	// A confirmation still open belongs to a turn that is over: nobody is waiting for the answer,
	// and a window left open would capture the keys of the next message.
	if t.confirm != nil {
		t.decideConfirm(false)
	}
	tr.onDone(out)
	q := t.queued
	if q == "" {
		return
	}
	t.queued = ""
	// A turn the user STOPPED does not start the next one on its own: Escape means "stop", not
	// "skip to the next". The message goes back into the input, where one Enter sends it, unless
	// the user has started typing something else there.
	if out.err == context.Canceled {
		if t.draft == "" {
			t.draft = q
		}
		t.addMessage(AuthorSystem, "Stopped. Your queued message was not sent; it is in the input box if it was empty.")
		return
	}
	t.submit(ctx, q)
}

// stopper is what Escape calls to stop a turn this interface started. Behind a gateway, stopping
// the local wait is not enough: the gateway keeps a run going when its client goes away (that is
// what lets a phone follow it), so the gateway is asked to stop it too - the same two moves a
// followed run makes (see stopAndCancel).
func (t *TUI) stopper(cancel context.CancelFunc) context.CancelFunc {
	if rc, ok := t.Runner.(RunController); ok {
		return t.stopAndCancel(rc, cancel)
	}
	return cancel
}

// finishTurn applies the reports of the turn in flight until it ends. It is the way out of the
// interface: the input is gone or the user asked to leave, and the turn is either let finish (a
// closed input, such as a script that piped one task) or has already been told to stop.
//
// Nothing queued is started on the way out, and a confirmation is refused: nobody is left to
// answer it.
func (t *TUI) finishTurn(ctx context.Context) {
	t.queued = ""
	approvals := t.approvalChannel()
	for t.current != nil {
		select {
		case p := <-t.current.progress:
			t.current.onProgress(p)
		case c := <-approvals:
			t.recordDecision(c.req, false)
			c.reply <- false
		case out := <-t.current.done:
			t.endCurrent(ctx, out)
		}
	}
}

// leaveTurn stops waiting for the turn in flight, if any, on the way out of the interface.
//
// Leaving is not stopping. Behind a gateway the run keeps going when its client goes away - that
// is what lets it be followed from the phone, or found again with /attach - and only Escape asks
// the gateway to stop it. Without a gateway the run belonged to this process and ends with it.
func (t *TUI) leaveTurn(ctx context.Context) {
	if t.current == nil {
		return
	}
	t.current.leave()
	t.finishTurn(ctx)
}

// queue keeps a message typed while a turn runs, to send when it ends.
//
// One message, not a list: a second one replaces the first, and the conversation says so. A
// queue the user cannot see is a queue that sends things they forgot they typed.
func (t *TUI) queue(line string) {
	note := "Queued: it will be sent when the current task finishes. Esc stops the current one."
	if t.queued != "" {
		note = "Queued, replacing the message queued before. Esc stops the current task."
	}
	t.queued = line
	t.addMessage(AuthorSystem, note+"\n"+glyphPrompt+" "+line)
}

// submit sends a message in the current mode. The two screens that are not conversations turn a
// typed message into a task rather than refusing it: someone who just listed the models and then
// types what they want done meant a task, and "press Enter to refresh" would be a dead end.
func (t *TUI) submit(ctx context.Context, line string) {
	if t.screen == ScreenPlan {
		t.runPlan(ctx, line)
		return
	}
	if t.screen != ScreenTask {
		t.setScreen(ScreenTask)
	}
	t.runTask(ctx, line)
}

// busyCommands are the commands that cannot run while a turn is in flight: they replace the
// conversation the turn is writing into, hand the terminal to the setup, or rate a turn that has
// not ended. Everything else - help, search, scrolling, switching the mode for the next message -
// works while the agent works.
var busyCommands = map[string]bool{
	"/models": true, "/config": true, "/new": true, "/attach": true, "/good": true, "/bad": true, "/update": true,
}

// refuseWhileBusy answers a command that has to wait for the turn to end.
func (t *TUI) refuseWhileBusy(name string) bool {
	if t.current == nil || !busyCommands[name] {
		return false
	}
	t.addMessage(AuthorSystem, strings.TrimPrefix(name, "/")+" has to wait: motita is still working. Press Esc to stop it first.")
	return true
}

// loopCtxOr is the context the input loop runs under, for a turn started outside a key press (the
// answers to the agent's questions), or the fallback when no loop is running.
func (t *TUI) loopCtxOr(fallback context.Context) context.Context {
	if t.loopCtx != nil {
		return t.loopCtx
	}
	return fallback
}
