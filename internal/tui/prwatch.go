package tui

import (
	"context"
	"fmt"
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
	same := prev.state == cur.PR
	switch cur.PR {
	case "passed":
		if !same {
			return "PR #%d: the CI passed, you can go and merge it", []any{cur.PRNumber}
		}
	case "fixing":
		if !same || cur.PRAttempts > prev.attempts {
			return "PR #%d: the CI failed, fixing it (attempt %d of %d)", []any{cur.PRNumber, cur.PRAttempts, cur.PRMax}
		}
	case "gave_up":
		if !same {
			return "PR #%d: the CI is still failing after %d attempts, it needs you", []any{cur.PRNumber, cur.PRAttempts}
		}
	case "base_red":
		if !same {
			return "PR #%d: the CI also fails on the base branch, not caused by this change", []any{cur.PRNumber}
		}
	case "no_ci":
		if !same {
			return "PR #%d: this repository has no CI to wait for", []any{cur.PRNumber}
		}
	case "merged":
		if !same {
			return "PR #%d was merged", []any{cur.PRNumber}
		}
	case "closed":
		if !same {
			return "PR #%d was closed without being merged", []any{cur.PRNumber}
		}
	}
	return "", nil
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
	for _, s := range all {
		var prev *prSeen
		if p, ok := seen[s.ID]; ok {
			prev = &p
		}
		seen[s.ID] = prSeen{state: s.PR, attempts: s.PRAttempts}
		if format, args := prNoticeText(prev, s); format != "" {
			t.raiseNotice(t.trf(format, args...))
		}
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
