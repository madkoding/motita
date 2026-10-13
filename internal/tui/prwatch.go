package tui

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// The gateway follows the CI of a session's pull request on its own; this is how the terminal
// hears of it. The list of sessions already says what the gateway is doing about each one, so the
// interface asks for it now and then and speaks up when something moves: the CI passing is the cue
// to go and merge, and a CI it gave up on is the cue to look.

var (
	// prPollEvery is how often the interface asks the gateway; a var so tests need not wait.
	prPollEvery = 15 * time.Second
	// prNoticeFor is how long a notice stays in the footer.
	prNoticeFor = 90 * time.Second
)

// prSeen is what was last known of a session's pull request.
type prSeen struct {
	state    string
	attempts int
}

// prNoticeText says what to tell the user about a session whose pull request moved from prev to
// cur, or "" when it is not news. prev is nil the first time a session is seen: what happened
// before the interface looked is not news. The text is a format and its arguments, so the caller
// translates it.
func prNoticeText(prev *prSeen, cur SessionInfo) (format string, args []any) {
	if prev == nil || cur.PR == "" {
		return "", nil
	}
	changed := prev.state != cur.PR
	// Sending the agent is news again at every new attempt, not only when the state changes.
	if f, ok := prAttemptNotices[cur.PR]; ok && (changed || cur.PRAttempts > prev.attempts) {
		return f, []any{cur.PRNumber, cur.PRAttempts, cur.PRMax}
	}
	if !changed {
		return "", nil
	}
	return prStateNotice(cur)
}

// prAttemptNotices are the notices of the states that send the agent, each a format of the pull
// request's number, the attempt and the most attempts.
var prAttemptNotices = map[string]string{
	"fixing":    "PR #%d: the CI failed, fixing it (attempt %d of %d)",
	"reviewing": "PR #%d: new comments or reviews, answering them (attempt %d of %d)",
	"resolving": "PR #%d: it conflicts with its base branch, resolving it (attempt %d of %d)",
}

// prStateNotices are the notices for the states that are news when a pull request reaches them,
// each a format of the pull request's number.
var prStateNotices = map[string]string{
	"passed":   "PR #%d: the CI passed, you can go and merge it",
	"base_red": "PR #%d: the CI also fails on the base branch, not caused by this change",
	"no_ci":    "PR #%d: this repository has no CI to wait for",
	"merged":   "PR #%d was merged",
	"closed":   "PR #%d was closed without being merged",
}

// prStateNotice is the notice for a pull request that has just reached its state; the format is
// "" for a state that is not news.
func prStateNotice(cur SessionInfo) (format string, args []any) {
	if cur.PR == "gave_up" {
		return "PR #%d: the CI is still failing after %d attempts, it needs you", []any{cur.PRNumber, cur.PRAttempts}
	}
	return prStateNotices[cur.PR], []any{cur.PRNumber}
}

// watchPRs asks the gateway for the sessions until ctx ends and raises a notice for each pull
// request that moved.
func (t *TUI) watchPRs(ctx context.Context) {
	sw, ok := t.Runner.(SessionSwitcher)
	if !ok {
		return
	}
	seen := map[string]prSeen{}
	tick := time.NewTicker(prPollEvery)
	defer tick.Stop()
	for {
		if all, err := sw.ListSessions(ctx); err == nil {
			t.notePRs(seen, all)
			t.noteWorking(all)
		}
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}

// notePRs folds one list of sessions into what was seen and raises the notices it earns.
func (t *TUI) notePRs(seen map[string]prSeen, all []SessionInfo) {
	var news []string
	for _, s := range all {
		var prev *prSeen
		if p, ok := seen[s.ID]; ok {
			prev = &p
		}
		seen[s.ID] = prSeen{state: s.PR, attempts: s.PRAttempts}
		if format, args := prNoticeText(prev, s); format != "" {
			news = append(news, t.trf(format, args...))
		}
	}
	// What moved in one look is one notice: several sessions ending together would otherwise
	// replace each other in the footer and ring the bell as many times.
	switch len(news) {
	case 0:
	case 1:
		t.raiseNotice(news[0])
	default:
		t.raiseNotice(t.trf("%d pull requests moved: %s", len(news), strings.Join(news, " · ")))
	}
}

// raiseNotice shows a line in the footer for a while. It is called from beside the input loop, so
// the field is guarded by the painter's lock.
//
// It also rings the terminal's bell: a notice that arrives while the terminal is in another tab or
// window is exactly the one nobody is looking at, and the terminal turns the bell into a mark on the
// tab or a sound.
func (t *TUI) raiseNotice(text string) {
	t.draw.Lock()
	t.liveNotice = text
	t.draw.Unlock()
	t.drawFrame()
	t.draw.Lock()
	fmt.Fprint(t.Out, "\a")
	t.draw.Unlock()
	time.AfterFunc(prNoticeFor, func() {
		t.draw.Lock()
		cleared := t.liveNotice == text
		if cleared {
			t.liveNotice = ""
		}
		t.draw.Unlock()
		if cleared {
			t.drawFrame()
		}
	})
}
