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

// SessionSwitcher is a Runner that can list conversations and move between them.
//
// It is OPTIONAL, and resolved with a type assertion, which is the pattern this package already
// uses for askSource and taskObserver. The reason is the same one: a front end that has no
// conversations to offer - the many small runners the tests build, an implementation backed by a
// single local session - must not be forced to grow methods that would have nothing to say.
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
}

// attachTo moves the interface to another conversation, when its runner can.
//
// It reports a failure rather than returning silently: a switch that did not happen leaves the user
// typing into a conversation they did not choose, and the message is the only thing that says so.
func (t *TUI) attachTo(ctx context.Context, id string) error {
	sw, ok := t.Runner.(SessionSwitcher)
	if !ok {
		return errors.New("this interface is not attached to a gateway that holds several sessions")
	}
	if err := sw.SwitchSession(ctx, id); err != nil {
		return err
	}
	// The conversation changed under the view, so what is on screen belongs to the old one.
	t.resetView()
	return nil
}

// resetView clears what is on screen because the CONVERSATION changed underneath it.
//
// It is not ResetConversation: nothing is cleared on the gateway, the view is simply moved to a
// different conversation's content. The distinction matters - using the reset path would DELETE the
// conversation the user just navigated away from, which is the opposite of what they asked for.
func (t *TUI) resetView() {
	t.draw.Lock()
	defer t.draw.Unlock()
	t.messages = nil
	t.scroll = 0
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
