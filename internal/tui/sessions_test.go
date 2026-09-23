package tui

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// switcherRunner is a Runner that ALSO offers conversations, which is the capability these tests
// are about. It is deliberately not the package's existing fakeRunner: whether a runner offers
// sessions is a property of the runner, and testing it with one that does not would test nothing.
type switcherRunner struct {
	*fakeRunner
	sessions []SessionInfo
	current  string
	err      error
}

// switcher builds the double with the embedded fakeRunner ready, and a TUI whose output can be
// read back, which is how the package's own tests inspect what was drawn.
func switcher(r *switcherRunner) *TUI {
	if r.fakeRunner == nil {
		r.fakeRunner = &fakeRunner{}
	}
	ui := newFakeTUI("", r)
	return ui
}

func (r *switcherRunner) ListSessions(context.Context) ([]SessionInfo, error) {
	if r.err != nil {
		return nil, r.err
	}
	return r.sessions, nil
}

func (r *switcherRunner) SwitchSession(_ context.Context, id string) error {
	if r.err != nil {
		return r.err
	}
	r.current = id
	return nil
}

func (r *switcherRunner) CurrentSession() string { return r.current }

// TestSessionSwitcherIsOptional: it is an OPTIONAL capability, resolved by a type assertion, the
// same pattern askSource and taskObserver already use. Requiring it on Runner would mean every
// existing double and every future front end grows two methods it may have no sessions to offer -
// and the many small runners the tests build would all become wrong for no reason.
func TestSessionSwitcherIsOptional(t *testing.T) {
	var r Runner = &fakeRunner{}
	if _, ok := r.(SessionSwitcher); ok {
		t.Fatal("a runner that offers no sessions must not be reported as offering them")
	}
}

// TestSessionsAreListedWithTheirState: the user has to see which conversations exist and which one
// is busy, because attaching to a conversation mid-run is a different experience from attaching to
// an idle one.
func TestSessionsAreListedWithTheirState(t *testing.T) {
	var r Runner = &switcherRunner{sessions: []SessionInfo{
		{ID: "default", Running: true},
		{ID: "sabc", Running: false},
	}}
	sw, ok := r.(SessionSwitcher)
	if !ok {
		t.Fatal("a runner that offers sessions must be reported as offering them")
	}
	all, err := sw.ListSessions(context.Background())
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("listed %d sessions, both must be there", len(all))
	}
	if !all[0].Running {
		t.Error("a session with a run in flight must say so: attaching to it is a different thing")
	}
}

// TestAttachingChangesTheSessionAndSaysSo: switching has to be visible. A silent switch would leave
// the user answering a question in a conversation they did not mean to be in.
func TestAttachingChangesTheSessionAndSaysSo(t *testing.T) {
	r := &switcherRunner{sessions: []SessionInfo{{ID: "default"}, {ID: "sabc"}}, current: "default"}
	ui := switcher(r)
	if err := ui.attachTo(context.Background(), "sabc"); err != nil {
		t.Fatalf("attachTo: %v", err)
	}
	if got := r.current; got != "sabc" {
		t.Errorf("the runner is on session %q, the attach must have happened", got)
	}
}

// TestAttachingClearsTheView: the conversation changed underneath the view, so what is on screen
// belongs to the OLD one. Showing the previous conversation's lines beside the new one's status bar
// is a view that lies about which conversation it is.
func TestAttachingClearsTheView(t *testing.T) {
	r := &switcherRunner{sessions: []SessionInfo{{ID: "default"}, {ID: "sabc"}}, current: "default"}
	ui := switcher(r)
	ui.addMessage(AuthorUser, "something said in the old conversation")

	if err := ui.attachTo(context.Background(), "sabc"); err != nil {
		t.Fatalf("attachTo: %v", err)
	}
	if len(ui.messages) != 0 {
		t.Errorf("the view still holds %d messages from the previous conversation", len(ui.messages))
	}
	// And the painter must repaint from scratch: the diffing writer compares against the last
	// frame, so a cleared conversation with a stale frame would leave the old lines on screen.
	if ui.paintedScreen {
		t.Error("the frame must be marked unpainted, or the old conversation stays on the terminal")
	}
}

// TestAttachingToAnUnknownSessionIsReported: the gateway knows the real list, the interface does
// not, and a session that vanished between the listing and the attach must be reported rather than
// leaving the interface pointing at nothing.
func TestAttachingToAnUnknownSessionIsReported(t *testing.T) {
	r := &switcherRunner{
		sessions: []SessionInfo{{ID: "default"}},
		err:      errors.New(`there is no session "gone"`),
	}
	ui := switcher(r)
	if err := ui.attachTo(context.Background(), "gone"); err == nil {
		t.Fatal("attaching to a session the gateway does not hold must fail")
	}
}

// TestAttachingWithoutTheCapabilityIsReported: an ordinary runner - the embedded interface's - has
// no conversations to move between, and saying so is better than a switch that silently does
// nothing.
func TestAttachingWithoutTheCapabilityIsReported(t *testing.T) {
	ui := newFakeTUI("", &fakeRunner{})
	if err := ui.attachTo(context.Background(), "sabc"); err == nil {
		t.Fatal("attaching through a runner with no sessions must be reported")
	}
}

// TestACommandExistsToSeeTheSessions: the capability is useless if there is no way to reach it, and
// the help table is what tells a user it exists.
func TestACommandExistsToSeeTheSessions(t *testing.T) {
	for _, want := range []string{"/sessions", "/attach"} {
		found := false
		for _, c := range commands {
			if c.Name == want {
				found = true
				if c.Help == "" {
					t.Errorf("%s must describe itself: it is how a user discovers the feature", want)
				}
			}
		}
		if !found {
			t.Errorf("%s is not in the command table, so nothing tells the user the feature exists", want)
		}
	}
}

// TestTheListingMarksTheCurrentSession: the user has to be able to see WHICH conversation they are
// on, or the list is a list of ids with no way to tell where they are.
func TestTheListingMarksTheCurrentSession(t *testing.T) {
	r := &switcherRunner{sessions: []SessionInfo{{ID: "default"}, {ID: "sabc"}}, current: "sabc"}
	ui := switcher(r)

	ui.printSessions(context.Background())

	drawn := stripANSI(outputOf(ui))
	if !strings.Contains(drawn, "* sabc") {
		t.Errorf("the current session is not marked in:\n%s", drawn)
	}
	if !strings.Contains(drawn, "  default") {
		t.Errorf("the other session is not listed in:\n%s", drawn)
	}
}

// TestTheListingSaysWhenARunIsInFlight: attaching to a busy conversation is a different thing from
// attaching to a quiet one, and it is the reason the field exists.
func TestTheListingSaysWhenARunIsInFlight(t *testing.T) {
	r := &switcherRunner{sessions: []SessionInfo{{ID: "default", Running: true}}}
	ui := switcher(r)
	ui.printSessions(context.Background())

	if drawn := stripANSI(outputOf(ui)); !strings.Contains(drawn, "in flight") {
		t.Errorf("a run in flight is not reported in:\n%s", drawn)
	}
}

// TestTheListingReportsAFailedAsk: the gateway is the authority on which conversations exist, so a
// listing that could not be fetched must say so rather than showing nothing.
func TestTheListingReportsAFailedAsk(t *testing.T) {
	r := &switcherRunner{err: errors.New("connection refused")}
	ui := switcher(r)
	ui.printSessions(context.Background())

	if drawn := stripANSI(outputOf(ui)); !strings.Contains(drawn, "could not be asked") {
		t.Errorf("a failed listing is not reported in:\n%s", drawn)
	}
}

// TestTheListingIsUnavailableWithoutTheCapability: the same courtesy the other messages give.
func TestTheListingIsUnavailableWithoutTheCapability(t *testing.T) {
	ui := newFakeTUI("", &fakeRunner{})
	ui.printSessions(context.Background())

	if drawn := stripANSI(outputOf(ui)); !strings.Contains(drawn, "several sessions") {
		t.Errorf("the lack of the capability is not reported in:\n%s", drawn)
	}
}

// TestTheListingSaysWhenThereIsNothing: a gateway that reports no conversations at all is a state
// that should not be reachable, and saying so beats an empty list that looks like a rendering bug.
func TestTheListingSaysWhenThereIsNothing(t *testing.T) {
	r := &switcherRunner{}
	ui := switcher(r)
	ui.printSessions(context.Background())

	if drawn := stripANSI(outputOf(ui)); !strings.Contains(drawn, "which should not be possible") {
		t.Errorf("an empty listing is not reported in:\n%s", drawn)
	}
}

// TestTheAttachCommandThroughTheTable: the other tests call attachTo directly, which leaves the
// COMMAND - the part a user actually reaches - unexecuted. The table is the dispatch path, so this
// exercises the closure that runs when someone types /attach.
func TestTheAttachCommandThroughTheTable(t *testing.T) {
	r := &switcherRunner{sessions: []SessionInfo{{ID: "default"}, {ID: "sabc"}}, current: "default"}
	ui := switcher(r)
	ui.addMessage(AuthorUser, "before")

	if quit := commandActions["/attach"](ui, context.Background(), "sabc"); quit {
		t.Error("/attach must not quit the interface")
	}
	if r.current != "sabc" {
		t.Fatalf("the command did not move the session, it is on %q", r.current)
	}
	if drawn := stripANSI(outputOf(ui)); !strings.Contains(drawn, "attached to session sabc") {
		t.Errorf("the move was not announced:\n%s", drawn)
	}
}

// TestTheAttachCommandReportsAFailure: the message path is what tells the user the switch did not
// happen. Without it they keep typing into a conversation they did not choose.
func TestTheAttachCommandReportsAFailure(t *testing.T) {
	r := &switcherRunner{err: errors.New("connection refused")}
	ui := switcher(r)

	if quit := commandActions["/attach"](ui, context.Background(), "sabc"); quit {
		t.Error("/attach must not quit the interface")
	}
	if drawn := stripANSI(outputOf(ui)); !strings.Contains(drawn, "connection refused") {
		t.Errorf("the failure was not reported:\n%s", drawn)
	}
}

// TestTheAttachCommandWithoutAnArgumentExplainsItself: "/attach" alone is a typo, and the answer
// has to be the usage line rather than a switch to a conversation named "".
func TestTheAttachCommandWithoutAnArgumentExplainsItself(t *testing.T) {
	r := &switcherRunner{sessions: []SessionInfo{{ID: "default"}}, current: "default"}
	ui := switcher(r)

	if quit := commandActions["/attach"](ui, context.Background(), "   "); quit {
		t.Error("/attach must not quit the interface")
	}
	if r.current != "default" {
		t.Errorf("a bare /attach moved the session to %q", r.current)
	}
	if drawn := stripANSI(outputOf(ui)); !strings.Contains(drawn, "usage: /attach") {
		t.Errorf("the usage line was not shown:\n%s", drawn)
	}
}

// TestTheSessionsCommandThroughTheTable: same reason as /attach - the table is what the user
// reaches, and the closure has to run.
func TestTheSessionsCommandThroughTheTable(t *testing.T) {
	r := &switcherRunner{sessions: []SessionInfo{{ID: "default"}}, current: "default"}
	ui := switcher(r)

	if quit := commandActions["/sessions"](ui, context.Background(), ""); quit {
		t.Error("/sessions must not quit the interface")
	}
	if drawn := stripANSI(outputOf(ui)); !strings.Contains(drawn, "sessions:") {
		t.Errorf("the listing did not run:\n%s", drawn)
	}
}
