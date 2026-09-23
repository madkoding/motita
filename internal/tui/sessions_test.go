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
	// transcript is the conversation of each session, keyed by id. It is what coming BACK to a
	// conversation reads, so a double without it can only exercise the empty case.
	transcript map[string][]Turn
	// conversationErr fails the READ alone, so "the switch happened but the conversation could
	// not be read" is a branch a test can reach without also failing the switch.
	conversationErr error
}

func (r *switcherRunner) Conversation(context.Context) ([]Turn, error) {
	if r.conversationErr != nil {
		return nil, r.conversationErr
	}
	return r.transcript[r.current], nil
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

// messagesSnapshot is the conversation as the view holds it.
//
// It is a test helper and not an exported method: production code has no reason to read the
// view's messages, and adding the method for a test's convenience would be production code
// written for the test.
func (t *TUI) messagesSnapshot() []Message {
	t.draw.Lock()
	defer t.draw.Unlock()
	return append([]Message(nil), t.messages...)
}

// TestAttachingReadsTheConversationItIsReturningTo: coming back to a session means seeing what
// happened while you were away. A cleared view with the new status bar is the one outcome that
// makes "attach" worse than useless - the user is told they are in a conversation and shown none
// of it.
//
// The content is read from the GATEWAY and not from anything the interface kept, which is the same
// rule as everywhere else: the interface's claim is that everything it shows comes from the
// gateway.
//
// It goes through the package's own newFakeTUI rather than tui.New: New writes frames to
// os.Stdout, and a test that draws on the real terminal is a test whose output nobody can read.
func TestAttachingReadsTheConversationItIsReturningTo(t *testing.T) {
	r := &switcherRunner{
		sessions: []SessionInfo{{ID: "default"}, {ID: "sabc"}},
		current:  "default",
		transcript: map[string][]Turn{
			"sabc": {
				{User: "count the files"},
				{Agent: "there are twelve"},
			},
		},
	}
	ui := switcher(r)

	if err := ui.attachTo(context.Background(), "sabc"); err != nil {
		t.Fatalf("attachTo: %v", err)
	}

	got := ui.messagesSnapshot()
	if len(got) != 3 {
		// Two turns read back, and the line that says which conversation was entered.
		t.Fatalf("the attached view holds %d messages, the two turns and the announcement must be drawn: %+v", len(got), got)
	}
	if got[0].Author != AuthorUser || got[0].Text != "count the files" {
		t.Errorf("the first line is %+v, it must be what the user said", got[0])
	}
	if got[1].Author != AuthorAgent || got[1].Text != "there are twelve" {
		t.Errorf("the second line is %+v, it must be what the agent answered", got[1])
	}
	if !strings.Contains(got[2].Text, "sabc") {
		t.Errorf("the attach must say which conversation was entered, got %+v", got[2])
	}
}

// TestAttachingToAnEmptySessionSaysSo: a conversation with nothing in it must not look like a
// failed attach. An empty screen is ambiguous - it could be a conversation with no turns or a view
// that never loaded - and one line removes the ambiguity.
func TestAttachingToAnEmptySessionSaysSo(t *testing.T) {
	r := &switcherRunner{sessions: []SessionInfo{{ID: "fresh"}}, current: "default"}
	ui := switcher(r)

	if err := ui.attachTo(context.Background(), "fresh"); err != nil {
		t.Fatalf("attachTo: %v", err)
	}
	got := ui.messagesSnapshot()
	if len(got) == 0 || !strings.Contains(got[len(got)-1].Text, "fresh") {
		t.Fatalf("attaching to an empty conversation must say which one it is; view holds %+v", got)
	}
}

// TestAttachingReplacesTheConversationItLeft: the lines on screen belong to the conversation being
// LEFT. Keeping them beside the new one's status bar is a view that lies about which conversation
// it is showing, and it is the same failure as the blank screen seen from the other side.
func TestAttachingReplacesTheConversationItLeft(t *testing.T) {
	r := &switcherRunner{
		sessions:   []SessionInfo{{ID: "default"}, {ID: "sabc"}},
		current:    "default",
		transcript: map[string][]Turn{"sabc": {{Agent: "the answer from sabc"}}},
	}
	ui := switcher(r)
	ui.addMessage(AuthorUser, "something said in the old conversation")

	if err := ui.attachTo(context.Background(), "sabc"); err != nil {
		t.Fatalf("attachTo: %v", err)
	}
	for _, m := range ui.messagesSnapshot() {
		if strings.Contains(m.Text, "old conversation") {
			t.Fatalf("the previous conversation is still on screen: %+v", ui.messagesSnapshot())
		}
	}
	// And the frame is rebuilt: the diffing painter compares against the rows it last wrote, so
	// a replaced conversation with a stale frame would leave the previous conversation's lines
	// on the terminal - which is exactly the lie this whole method avoids.
	if strings.Contains(strings.Join(ui.lastFrame, "\n"), "old conversation") {
		t.Fatalf("the last frame still holds the previous conversation:\n%s", strings.Join(ui.lastFrame, "\n"))
	}
}

// TestAttachingReportsAConversationThatCouldNotBeRead: the switch happened and the read did not,
// so the user is IN a conversation whose content could not be fetched. That is a failure worth
// reporting: silently showing the empty view would look exactly like a conversation with no turns.
func TestAttachingReportsAConversationThatCouldNotBeRead(t *testing.T) {
	r := &switcherRunner{
		sessions:        []SessionInfo{{ID: "default"}, {ID: "sabc"}},
		current:         "default",
		conversationErr: errors.New("connection refused"),
	}
	ui := switcher(r)

	err := ui.attachTo(context.Background(), "sabc")
	if err == nil {
		t.Fatal("a conversation that could not be read must be reported")
	}
	if !strings.Contains(err.Error(), "sabc") {
		t.Errorf("the failure must name the session it is about, got: %v", err)
	}
	if r.current != "sabc" {
		t.Errorf("the switch did happen and must not be undone, the runner is on %q", r.current)
	}
}

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
