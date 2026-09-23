package tui

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// SessionInfo is one conversation, as an interface needs to draw it.
type SessionInfo struct {
	ID string
	// Running is true while a turn is in flight in that conversation. It matters to the user:
	// attaching to a conversation mid-run means a stream of work rather than a quiet prompt.
	Running bool
	// Current marks the one this interface is on, so the list can point at it.
	Current bool
}

// Turn is one turn of a conversation, as the interface draws it.
//
// It is the interface's own shape rather than the agent's DialogueTurn, because a front end must
// not depend on the wire type - the gateway declares its interfaces structurally precisely so the
// two packages never import each other, and this is the same rule seen from the other side.
type Turn struct {
	User  string
	Agent string
}

// SessionSwitcher is a Runner that can list conversations, move between them, and READ the one it
// moves to.
//
// It is OPTIONAL, and resolved with a type assertion, which is the pattern this package already
// uses for askSource and taskObserver. The reason is the same one: a front end that has no
// conversations to offer - the many small runners the tests build, an implementation backed by a
// single local session - must not be forced to grow methods that would have nothing to say.
//
// Reading is part of the same capability and not a separate one: switching to a conversation and
// showing nothing is not a half-feature, it is a broken feature. "Attach" means the user is back in
// that conversation, and a conversation the user cannot see is one they cannot be in.
//
// The gateway client implements it, so the text interface can be a remote of N conversations.
type SessionSwitcher interface {
	// ListSessions returns every conversation the gateway holds.
	ListSessions(ctx context.Context) ([]SessionInfo, error)
	// SwitchSession changes which conversation this front end speaks for.
	//
	// It is a MOVE and not a copy: a front end is on one conversation at a time, because that is
	// what the user is looking at. Two conversations side by side is a different interface.
	SwitchSession(ctx context.Context, id string) error
	// CurrentSession is the conversation this front end is on.
	CurrentSession() string
	// Conversation returns the turns of the current conversation, oldest first.
	Conversation(ctx context.Context) ([]Turn, error)
}

// RunController is a Runner that can ask the GATEWAY about the run in flight, follow it, and stop
// it.
//
// It is a separate OPTIONAL capability from SessionSwitcher, resolved with the same type assertion
// this package already uses for askSource and taskObserver. The reason is concrete: the interface
// drives a run through local state - t.cancelRun and t.runningCtx - and that state only exists for
// a run this interface STARTED. A run that outlived its client belongs to the gateway, so following
// it and stopping it are questions for the gateway, and a runner that is not backed by one has
// nothing to answer with.
type RunController interface {
	// LiveRun reports the run in flight in the current session, if there is one.
	//
	// Running is false with no error when the conversation is simply idle: that is an answer, and
	// the common one.
	LiveRun(ctx context.Context) (LiveRun, bool, error)
	// FollowRun attaches to that run and reports its progress through the callback until it ends.
	//
	// It returns an error when the run could not be followed: a session whose run ended between the
	// question and the attach, or a connection that could not hold. The caller reports it rather
	// than showing a turn that is not there.
	//
	// A cancelled context stops the follow AND asks the gateway to stop the run - cancelling only
	// locally would leave the gateway working while the interface stopped showing it.
	FollowRun(ctx context.Context, progress func(string, ...any)) (string, error)
	// CancelRun asks the gateway to stop the run in the current session, and reports whether there
	// was one to stop.
	CancelRun(ctx context.Context) (bool, error)
}

// LiveRun describes a run in flight, as the interface draws it.
type LiveRun struct {
	RunID string
	// LastSeq is the newest event the gateway holds. It is what a client resumes from, and the
	// interface does not have to interpret it - it only has to be able to pass it back.
	LastSeq uint64
}

// attachTo moves the interface to another conversation, when its runner can, and DRAWS it.
//
// The order matters and is the whole point: the switch happens first, then the conversation is read
// from the gateway through the NEW session, then the view is replaced. Reading first would read the
// conversation being left behind; replacing the view without reading is the empty screen that made
// a bare switch useless.
//
// A turn in flight is followed rather than ignored, which is the case this design CREATED instead
// of an edge case - see followLiveRun.
func (t *TUI) attachTo(ctx context.Context, id string) error {
	sw, ok := t.Runner.(SessionSwitcher)
	if !ok {
		return errors.New("this interface is not attached to a gateway that holds several sessions")
	}
	if err := sw.SwitchSession(ctx, id); err != nil {
		return err
	}

	turns, err := sw.Conversation(ctx)
	if err != nil {
		return fmt.Errorf("attached to session %s, but its conversation could not be read: %w", id, err)
	}

	// The view is REPLACED rather than appended: the lines on screen belong to the conversation
	// that was left, and keeping them next to the new conversation's status bar is a view that
	// lies about which conversation it is showing.
	t.resetView()
	t.draw.Lock()
	for _, turn := range turns {
		if turn.User != "" {
			t.messages = append(t.messages, Message{Author: AuthorUser, Text: turn.User})
		}
		if turn.Agent != "" {
			t.messages = append(t.messages, Message{Author: AuthorAgent, Text: turn.Agent})
		}
	}
	t.draw.Unlock()

	if len(turns) == 0 {
		// An empty screen is ambiguous - a conversation with no turns and a view that never
		// loaded look identical - and one line removes the ambiguity.
		t.addMessage(AuthorSystem, "attached to session "+id+", which has no turns yet.")
	} else {
		t.addMessage(AuthorSystem, "attached to session "+id+". "+turnCount(len(turns)))
	}

	t.followRunIfAny(ctx)
	t.drawFrame()
	return nil
}

// followRunIfAny watches the run in flight in the session just entered, when there is one.
//
// It REPORTS and does not fail: the conversation was read and the user is IN the session, so
// aborting the whole attach over the remaining question would send them back to the session they
// just left.
//
// It is written so that a run CANNOT BLOCK THE KEY LOOP, and that is the whole point of its shape.
// This is reached from inside the key loop (someone typed /attach) and from before the interface is
// even running (-session at startup), and a run takes as long as it takes: blocking here would
// freeze the interface for the entire turn, with no keys read and no way out but killing the
// process. So the run is followed on its own goroutine.
//
// The QUESTION is asked synchronously and the follow is not. The distinction is what makes the
// behaviour observable at all: attachTo returns only once it is known whether a run is in flight,
// so a caller can rely on "the interface is now following that turn" the moment the call returns -
// while the turn itself reports from somewhere else. One round trip to a local gateway is bounded
// and quick; the turn is unbounded, and it is the turn that must not be waited on.
func (t *TUI) followRunIfAny(ctx context.Context) {
	rc, ok := t.Runner.(RunController)
	if !ok {
		// Not a runner backed by a gateway. It has nothing to say about a run, and saying nothing
		// is right: there is no run here that this interface did not start.
		return
	}

	live, running, err := rc.LiveRun(ctx)
	switch {
	case err != nil:
		// A gateway that cannot answer is worth saying out loud, and it is not a reason to undo the
		// attach: the user is in the conversation and can read it.
		t.addMessage(AuthorSystem, "the gateway could not be asked whether a turn is in flight: "+err.Error())
	case running:
		go t.followLiveRun(ctx, rc, live)
	}
}

// followLiveRun follows a run that this interface did NOT start, until it ends or the user stops it.
//
// It runs on ITS OWN goroutine - see followRunIfAny - and that is what keeps the keys alive while
// the turn runs. It is otherwise modelled on runTask, the path that already drives a turn this
// interface started: the same beginTurn/awaitRun/endTurn shape, the same pending block that the
// progress lines overwrite and the outcome settles, and the SAME cancelRun field that Escape reads.
// Sharing that field is what makes one key mean the same thing whether the run was started here or
// elsewhere.
func (t *TUI) followLiveRun(ctx context.Context, rc RunController, live LiveRun) {
	runCtx, cancel := context.WithCancel(ctx)

	// Installed where Escape and Ctrl+C look for it, so the key does what it says in both cases.
	// Without this, Escape during a followed run falls through to scrollToBottom and the turn keeps
	// going while the user believes they stopped it.
	//
	// It cancels THE LOCAL CONTEXT, which ends the follow and, through the runner's own contract,
	// asks the gateway to stop the run - see RunController.FollowRun. Cancelling only locally would
	// leave the gateway working while the interface stopped showing it, which is the worst of both.
	t.setCancel(cancel, runCtx)

	progress := make(chan string, 16)
	done := make(chan runOutcome, 1)
	go func() {
		res, err := rc.FollowRun(runCtx, progressSender(runCtx, progress))
		done <- runOutcome{result: res, err: err}
	}()

	// The block that fills as the run reports, exactly like a local turn: the turn is marked in
	// flight and each line the run emits is written into the conversation as it arrives.
	//
	// The lines are FROZEN into the view rather than overwritten by the next one, which is the
	// difference from a turn this interface started. The user arrived in the middle of this run:
	// the lines already emitted are the record of what happened while they were away, and
	// overwriting them would throw away exactly what they came back to read.
	t.beginTurn()
	t.advance()

	outcome := t.awaitRun(runCtx, progress, done, func(p string) {
		t.addProgress(p)
	})

	t.clearCancel()

	var text string
	switch {
	case errors.Is(outcome.err, context.Canceled):
		text = "cancelled."
	case outcome.err != nil:
		text = fmt.Sprintf("the turn could not be followed: %v", outcome.err)
	case outcome.result != "":
		text = outcome.result
	default:
		text = "the turn finished."
	}
	t.addMessage(AuthorAgent, text)
	t.endTurn()
}

// addProgress writes one line the followed run emitted into the conversation.
//
// The line goes in as a FROZEN block: it is what the turn said at that moment, and the next line
// belongs beside it rather than on top of it. It is the same treatment a plan's phases get - the
// conversation keeps the account of the run - and it is why coming back to a session mid-turn
// shows the work rather than only its latest line.
func (t *TUI) addProgress(text string) {
	t.draw.Lock()
	t.messages = append(t.messages, Message{Author: AuthorAgent, Text: text, Frozen: true})
	t.draw.Unlock()
	t.advance()
}

// turnCount describes how much was read back, so the user can tell a conversation with three turns
// from one with thirty before scrolling.
func turnCount(n int) string {
	if n == 1 {
		return "1 turn in the conversation."
	}
	return fmt.Sprintf("%d turns in the conversation.", n)
}

// resetView clears what is on screen because the CONVERSATION changed underneath it.
//
// It is not ResetConversation: nothing is cleared on the gateway, the view is simply moved to a
// different conversation's content. The distinction matters - using the reset path would DELETE the
// conversation the user just navigated away from, which is the opposite of what they asked for.
//
// The filter goes with it. A search left running would hide the conversation just entered behind a
// query the user typed about the one they left - indistinguishable, on screen, from a conversation
// with nothing in it.
func (t *TUI) resetView() {
	t.draw.Lock()
	defer t.draw.Unlock()
	t.messages = nil
	t.scroll = 0
	t.query = ""
	t.searching = false
	// The frame is marked unpainted so the next paint is written whole. The painter compares
	// against lastFrame, so a cleared conversation with a stale frame would leave the previous
	// conversation's lines on the terminal.
	t.paintedScreen = false
	t.lastFrame = nil
}

// printSessions shows the conversations the gateway holds.
//
// Everything the interface knows comes from the gateway: it does not remember which conversations
// exist, because a list it kept would be a list that drifts from the one that is real.
func (t *TUI) printSessions(ctx context.Context) {
	sw, ok := t.Runner.(SessionSwitcher)
	if !ok {
		t.addMessage(AuthorSystem, "this interface is not attached to a gateway that holds several sessions.")
		return
	}
	all, err := sw.ListSessions(ctx)
	if err != nil {
		t.addMessage(AuthorSystem, "the gateway could not be asked which sessions it holds: "+err.Error())
		return
	}
	if len(all) == 0 {
		t.addMessage(AuthorSystem, "the gateway reports no sessions, which should not be possible.")
		return
	}
	current := sw.CurrentSession()
	var b strings.Builder
	b.WriteString("sessions:\n")
	for _, s := range all {
		marker := "  "
		// The current one is marked by IDENTITY rather than by the Current field, so a runner
		// that does not fill it in still renders a list the user can read.
		if s.ID == current {
			marker = "* "
		}
		state := ""
		if s.Running {
			state = " (a run is in flight)"
		}
		fmt.Fprintf(&b, "%s%s%s\n", marker, s.ID, state)
	}
	b.WriteString("\n/attach <id> to move to one.")
	t.addPreformatted(AuthorSystem, b.String())
}
