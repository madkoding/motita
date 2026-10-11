package tui

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

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

// TestTheSessionListIsOrderedByRecency: the user opening /sessions is asking "where was I?", and the
// answer is the most recent conversation. A list in arbitrary order - which is what a map gives -
// makes them read every line to find it.
//
// The signature differs from the plan's sketch, and deliberately: the listing ASKS THE GATEWAY, so
// it needs a context and can fail. The plan's version takes neither, and a function that cannot
// report a failed ask would have to bury it in the text.
func TestTheSessionListIsOrderedByRecency(t *testing.T) {
	now := time.Now()
	r := &switcherRunner{
		sessions: []SessionInfo{
			{ID: "old", LastUsed: now.Add(-2 * time.Hour)},
			{ID: "recent", LastUsed: now.Add(-1 * time.Minute)},
			{ID: "middle", LastUsed: now.Add(-30 * time.Minute)},
		},
		current: "old",
	}
	ui := switcher(r)
	out, err := ui.sessionsText(context.Background())
	if err != nil {
		t.Fatalf("sessionsText: %v", err)
	}

	if i, j := strings.Index(out, "recent"), strings.Index(out, "middle"); i > j {
		t.Errorf("the most recent session is not listed first:\n%s", out)
	}
	if i, j := strings.Index(out, "middle"), strings.Index(out, "old"); i > j {
		t.Errorf("the list is not ordered by recency:\n%s", out)
	}
}

// TestASessionWithARunInFlightOutranksARecentOne: a conversation that is working right now is the
// one the user is most likely returning to, whatever its clock says. Ordering it by recency alone
// would bury the session with something happening in it.
func TestASessionWithARunInFlightOutranksARecentOne(t *testing.T) {
	now := time.Now()
	r := &switcherRunner{
		sessions: []SessionInfo{
			{ID: "quiet", LastUsed: now},
			{ID: "busy", LastUsed: now.Add(-3 * time.Hour), Running: true},
		},
		current: "quiet",
	}
	ui := switcher(r)
	out, err := ui.sessionsText(context.Background())
	if err != nil {
		t.Fatalf("sessionsText: %v", err)
	}
	if i, j := strings.Index(out, "busy"), strings.Index(out, "quiet"); i > j {
		t.Errorf("a session with a run in flight must come first:\n%s", out)
	}
}

// TestTheCurrentSessionIsMarked: the user has to see which conversation they are already in, or
// /attach sends them to where they already are.
func TestTheCurrentSessionIsMarked(t *testing.T) {
	r := &switcherRunner{sessions: []SessionInfo{{ID: "default"}, {ID: "sabc"}}, current: "sabc"}
	ui := switcher(r)
	out, err := ui.sessionsText(context.Background())
	if err != nil {
		t.Fatalf("sessionsText: %v", err)
	}
	if !strings.Contains(out, "* sabc") {
		t.Errorf("the current session is not marked in:\n%s", out)
	}
}

// TestASessionWithARunInFlightIsCalledOut: attaching to a conversation that is working is a
// different experience from one that is idle, and the user is choosing between them.
func TestASessionWithARunInFlightIsCalledOut(t *testing.T) {
	r := &switcherRunner{sessions: []SessionInfo{{ID: "busy", Running: true}}}
	ui := switcher(r)
	out, err := ui.sessionsText(context.Background())
	if err != nil {
		t.Fatalf("sessionsText: %v", err)
	}
	if !strings.Contains(out, "in flight") {
		t.Error("a session with a run in flight must say so: it is what the user is choosing between")
	}
}

// TestTheListingShowsTheBranchOfEachSession: a session works on a branch, and which one is the
// difference between two entries carrying the same title. The branch is appended AFTER the age,
// so an entry can carry both - a session used three hours ago on a branch says both things
// rather than one replacing the other.
func TestTheListingShowsTheBranchOfEachSession(t *testing.T) {
	r := &switcherRunner{sessions: []SessionInfo{
		{ID: "onbranch", Branch: "feat/sidebar", LastUsed: time.Now().Add(-3 * time.Hour)},
		{ID: "plain"},
	}}
	ui := switcher(r)
	out, err := ui.sessionsText(context.Background())
	if err != nil {
		t.Fatalf("sessionsText: %v", err)
	}
	if !strings.Contains(out, "[feat/sidebar]") {
		t.Errorf("the list does not show the branch a session is on:\n%s", out)
	}
	if !strings.Contains(out, "3h ago") {
		t.Errorf("the age and the branch must both appear, not one instead of the other:\n%s", out)
	}
	if strings.Contains(out, "plain  [") {
		t.Errorf("a session with no branch must not be given one:\n%s", out)
	}
}

// TestTheSessionListSaysHowLongAgoEachWasUsed: "2h ago" is what someone needs to pick a
// conversation. A timestamp to the second is a number they have to subtract from the current time
// themselves, which is exactly the work the list exists to save them.
func TestTheSessionListSaysHowLongAgoEachWasUsed(t *testing.T) {
	now := time.Now()
	r := &switcherRunner{sessions: []SessionInfo{
		{ID: "justnow", LastUsed: now.Add(-10 * time.Second)},
		{ID: "minutes", LastUsed: now.Add(-5 * time.Minute)},
		{ID: "hours", LastUsed: now.Add(-3 * time.Hour)},
		{ID: "days", LastUsed: now.Add(-50 * time.Hour)},
	}}
	ui := switcher(r)
	out, err := ui.sessionsText(context.Background())
	if err != nil {
		t.Fatalf("sessionsText: %v", err)
	}
	for _, want := range []string{"just now", "5m ago", "3h ago", "2d ago"} {
		if !strings.Contains(out, want) {
			t.Errorf("the list does not say %q:\n%s", want, out)
		}
	}
}

// TestASessionNeverUsedHasNoAge: a conversation that has never been used has no age to report, and
// an age worked out from a zero time would read as decades ago - a wrong number drawn from a
// missing one.
func TestASessionNeverUsedHasNoAge(t *testing.T) {
	r := &switcherRunner{sessions: []SessionInfo{{ID: "idle"}}}
	ui := switcher(r)
	out, err := ui.sessionsText(context.Background())
	if err != nil {
		t.Fatalf("sessionsText: %v", err)
	}
	if strings.Contains(out, "ago") || strings.Contains(out, "just now") {
		t.Errorf("a session with no LastUsed must not be given an age:\n%s", out)
	}
}

// TestTheSessionListTellsTheUserHowToGoBack: the list answers "where was I?" and the next thing the
// user wants is the way back. Naming the command is what makes the answer actionable.
func TestTheSessionListTellsTheUserHowToGoBack(t *testing.T) {
	r := &switcherRunner{sessions: []SessionInfo{{ID: "default"}}, current: "default"}
	ui := switcher(r)
	out, err := ui.sessionsText(context.Background())
	if err != nil {
		t.Fatalf("sessionsText: %v", err)
	}
	if !strings.Contains(out, "/attach") {
		t.Errorf("the list must say how to go back to one:\n%s", out)
	}
}

// TestTheSessionListIsUnavailableWithoutTheCapability: the capability is optional, and the text
// path reports that rather than panicking.
func TestTheSessionListIsUnavailableWithoutTheCapability(t *testing.T) {
	ui := newFakeTUI("", &fakeRunner{})
	if _, err := ui.sessionsText(context.Background()); err == nil {
		t.Fatal("a runner that holds no sessions must be reported")
	}
}

// TestTheSessionListReportsAFailedAsk: the gateway is the authority on which conversations exist,
// so a listing that could not be fetched is an error and not an empty list - those are two very
// different statements.
func TestTheSessionListReportsAFailedAsk(t *testing.T) {
	r := &switcherRunner{err: errors.New("connection refused")}
	ui := switcher(r)
	if _, err := ui.sessionsText(context.Background()); err == nil {
		t.Fatal("a failed listing must be reported, not rendered as no sessions")
	}
}

// TestTheSessionListReportsAGatewayHoldingNothing: a gateway with no conversations at all is a
// state that should not be reachable, and saying so beats an empty list that looks like a
// rendering bug.
func TestTheSessionListReportsAGatewayHoldingNothing(t *testing.T) {
	ui := switcher(&switcherRunner{})
	if _, err := ui.sessionsText(context.Background()); err == nil {
		t.Fatal("a gateway reporting no sessions must be reported")
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
	if drawn := stripANSI(outputOf(ui)); !strings.Contains(drawn, "sessions (most recent first):") {
		t.Errorf("the listing did not run:\n%s", drawn)
	}
}
