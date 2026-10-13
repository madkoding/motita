package tui

import (
	"bytes"
	"strings"
	"testing"
)

func TestPRStateSaysWhatTheCIIsDoing(t *testing.T) {
	tu := &TUI{}
	for state, want := range map[string]string{
		"following": "[PR #7: CI running]",
		"fixing":    "[PR #7: CI failed, being fixed]",
		"passed":    "[PR #7: CI passed, ready to merge]",
		"gave_up":   "[PR #7: CI failed, needs you]",
		"base_red":  "[PR #7: CI also red on the base branch]",
		"merged":    "[PR #7: merged]",
		"reviewing": "[PR #7: new review, being answered]",
		"resolving": "[PR #7: conflict, being resolved]",
	} {
		if got := tu.prState(state, 7); got != want {
			t.Errorf("%s: %q, want %q", state, got, want)
		}
	}
	if tu.prState("unknown", 7) != "" {
		t.Error("an unknown state says nothing")
	}
}

func TestPRNoticeTextOnlyForNews(t *testing.T) {
	seen := func(state string, attempts int) *prSeen { return &prSeen{state: state, attempts: attempts} }
	cur := func(state string, attempts int) SessionInfo {
		return SessionInfo{ID: "s", PR: state, PRNumber: 7, PRAttempts: attempts, PRMax: 5}
	}
	if f, _ := prNoticeText(nil, cur("passed", 0)); f != "" {
		t.Error("a session met for the first time is quiet")
	}
	if f, _ := prNoticeText(seen("following", 0), cur("passed", 0)); f == "" {
		t.Error("the CI passing is news")
	}
	if f, _ := prNoticeText(seen("passed", 0), cur("passed", 0)); f != "" {
		t.Error("the same state again is not")
	}
	if f, _ := prNoticeText(seen("fixing", 1), cur("fixing", 1)); f != "" {
		t.Error("the same attempt is not news")
	}
	if f, a := prNoticeText(seen("following", 1), cur("fixing", 2)); f == "" || a[1] != 2 {
		t.Errorf("a new attempt is news: %q %v", f, a)
	}
	if f, a := prNoticeText(seen("passed", 1), cur("reviewing", 2)); !strings.Contains(f, "comments") || a[1] != 2 {
		t.Errorf("an answer to a review is news: %q %v", f, a)
	}
	if f, _ := prNoticeText(seen("following", 0), cur("resolving", 1)); !strings.Contains(f, "conflicts") {
		t.Errorf("a conflict being resolved is news: %q", f)
	}
	for _, st := range []string{"gave_up", "base_red", "no_ci", "merged", "closed"} {
		if f, _ := prNoticeText(seen("following", 0), cur(st, 0)); f == "" {
			t.Errorf("%s is news", st)
		}
	}
	if f, _ := prNoticeText(seen("following", 0), cur("following", 0)); f != "" {
		t.Error("following is not news")
	}
}

func TestALiveNoticeLeadsTheFooter(t *testing.T) {
	tu := newFakeTUI("", &fakeRunner{})
	var out bytes.Buffer
	tu.Out = &out
	tu.raiseNotice("PR #7: the CI passed, you can go and merge it")
	if !strings.Contains(out.String(), "\a") {
		t.Error("a notice rings the terminal's bell")
	}
	tu.draw.Lock()
	bar := stripANSI(tu.bottomBar(120))
	tu.draw.Unlock()
	if !strings.Contains(bar, "PR #7: the CI passed") {
		t.Errorf("the footer must say it: %q", bar)
	}
	narrow := stripANSI(tu.bottomBar(60))
	if !strings.Contains(narrow, "PR #7") {
		t.Errorf("a narrow terminal loses keys, not the news: %q", narrow)
	}
}

func TestSeveralPRNoticesAreOneNoticeAndOneBell(t *testing.T) {
	tu := newFakeTUI("", &fakeRunner{})
	var out bytes.Buffer
	tu.Out = &out
	seen := map[string]prSeen{"a": {state: "following"}, "b": {state: "following"}}
	tu.notePRs(seen, []SessionInfo{
		{ID: "a", PR: "passed", PRNumber: 1},
		{ID: "b", PR: "gave_up", PRNumber: 2, PRAttempts: 5},
	})
	tu.draw.Lock()
	notice := tu.liveNotice
	tu.draw.Unlock()
	if !strings.Contains(notice, "2 pull requests moved") || !strings.Contains(notice, "PR #1") ||
		!strings.Contains(notice, "PR #2") {
		t.Errorf("one notice for both: %q", notice)
	}
	if n := strings.Count(out.String(), "\a"); n != 1 {
		t.Errorf("one bell, got %d", n)
	}
}
