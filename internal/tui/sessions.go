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

	t.drawFrame()
	return nil
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
