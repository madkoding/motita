package tui

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

// guardedSwitcher is a switcherRunner whose list can change while the interface is reading it.
type guardedSwitcher struct {
	*switcherRunner
	lmu  sync.Mutex
	list []SessionInfo
	fail error
	asks int
}

func (g *guardedSwitcher) ListSessions(context.Context) ([]SessionInfo, error) {
	g.lmu.Lock()
	defer g.lmu.Unlock()
	g.asks++
	if g.fail != nil {
		return nil, g.fail
	}
	return append([]SessionInfo(nil), g.list...), nil
}

func (g *guardedSwitcher) set(list []SessionInfo, fail error) {
	g.lmu.Lock()
	g.list, g.fail = list, fail
	g.lmu.Unlock()
}

func (g *guardedSwitcher) asked() int {
	g.lmu.Lock()
	defer g.lmu.Unlock()
	return g.asks
}

func faster(t *testing.T, poll, stay time.Duration) {
	t.Helper()
	oldPoll, oldStay := prPollEvery, prNoticeFor
	prPollEvery, prNoticeFor = poll, stay
	t.Cleanup(func() { prPollEvery, prNoticeFor = oldPoll, oldStay })
}

func liveNoticeOf(tu *TUI) string {
	tu.draw.Lock()
	defer tu.draw.Unlock()
	return tu.liveNotice
}

func eventually(t *testing.T, what string, ok func() bool) {
	t.Helper()
	for i := 0; i < 300; i++ {
		if ok() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func TestTheInterfaceAsksTheGatewayAndSpeaksWhenACIEnds(t *testing.T) {
	faster(t, 5*time.Millisecond, time.Minute)
	g := &guardedSwitcher{switcherRunner: &switcherRunner{fakeRunner: &fakeRunner{}}}
	g.set([]SessionInfo{{ID: "s", PR: "following", PRNumber: 7}}, nil)
	tu := newFakeTUI("", g)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); tu.watchPRs(ctx) }()

	eventually(t, "the first look", func() bool { return g.asked() >= 2 })
	if n := liveNoticeOf(tu); n != "" {
		t.Fatalf("what was there before the first look is not news: %q", n)
	}
	// A gateway that cannot be asked is not a reason to stop asking.
	g.set(nil, errors.New("the gateway is restarting"))
	before := g.asked()
	eventually(t, "another look after the failure", func() bool { return g.asked() > before+1 })
	g.set([]SessionInfo{{ID: "s", PR: "passed", PRNumber: 7}}, nil)
	eventually(t, "the notice", func() bool { return strings.Contains(liveNoticeOf(tu), "PR #7") })

	cancel()
	<-done
}

func TestWatchingNeedsAGatewayThatHoldsSessions(t *testing.T) {
	tu := newFakeTUI("", &fakeRunner{})
	done := make(chan struct{})
	go func() { defer close(done); tu.watchPRs(context.Background()) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("a runner with no sessions has nothing to watch, and must not be waited on")
	}
}

func TestRunWatchesPullRequestsOfAGatewayAndStopsWithIt(t *testing.T) {
	faster(t, 5*time.Millisecond, time.Minute)
	g := &guardedSwitcher{switcherRunner: &switcherRunner{fakeRunner: &fakeRunner{}}}
	g.set([]SessionInfo{{ID: "s"}}, nil)
	tu := newFakeTUI("", g)
	tu.Run(context.Background())
	if g.asked() == 0 {
		t.Error("the interface asks the gateway while it runs")
	}
}

func TestANoticeLeavesTheFooterAfterAWhileUnlessReplaced(t *testing.T) {
	faster(t, time.Minute, 30*time.Millisecond)
	tu := newFakeTUI("", &fakeRunner{})
	tu.raiseNotice("first")
	eventually(t, "the notice to go", func() bool { return liveNoticeOf(tu) == "" })

	// A newer notice is not taken down by the timer of the one before it.
	faster(t, time.Minute, 60*time.Millisecond)
	tu.raiseNotice("one")
	time.Sleep(30 * time.Millisecond)
	tu.raiseNotice("two")
	time.Sleep(45 * time.Millisecond)
	if n := liveNoticeOf(tu); n != "two" {
		t.Errorf("the older timer must leave the newer notice alone: %q", n)
	}
	eventually(t, "the newer notice to go", func() bool { return liveNoticeOf(tu) == "" })
}

func TestNothingMovedIsSilent(t *testing.T) {
	tu := newFakeTUI("", &fakeRunner{})
	seen := map[string]prSeen{}
	tu.notePRs(seen, []SessionInfo{{ID: "a", PR: "following"}})
	tu.notePRs(seen, []SessionInfo{{ID: "a", PR: "following"}})
	if n := liveNoticeOf(tu); n != "" {
		t.Errorf("no news, no notice: %q", n)
	}
	tu.notePRs(seen, []SessionInfo{{ID: "a", PR: "passed", PRNumber: 3}})
	if n := liveNoticeOf(tu); !strings.Contains(n, "PR #3") || strings.Contains(n, "pull requests moved") {
		t.Errorf("one thing moved, one thing is said: %q", n)
	}
}

func TestEveryStateOfAPullRequestHasWords(t *testing.T) {
	tu := newFakeTUI("", &fakeRunner{})
	for state, want := range map[string]string{
		"no_ci":  "[PR #7: no CI]",
		"closed": "[PR #7: closed]",
	} {
		if got := tu.prState(state, 7); got != want {
			t.Errorf("%s: %q, want %q", state, got, want)
		}
	}
	if got := tu.prState("passed", 0); got != "[PR: CI passed, ready to merge]" {
		t.Errorf("a pull request whose number is not known yet: %q", got)
	}
}

func TestTheSessionListSaysWhatTheCIIsDoing(t *testing.T) {
	r := &switcherRunner{sessions: []SessionInfo{{ID: "default", Current: true, PR: "passed", PRNumber: 7}}, current: "default"}
	out, err := switcher(r).sessionsText(context.Background())
	if err != nil || !strings.Contains(out, "[PR #7: CI passed, ready to merge]") {
		t.Errorf("%q %v", out, err)
	}
}
