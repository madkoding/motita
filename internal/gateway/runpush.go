package gateway

import (
	"context"
	"net/url"
	"strings"

	"github.com/madkoding/motita/internal/i18n"
	"github.com/madkoding/motita/internal/webpush"
)

// pushUnwatched tells the phones that a run needs the person or has ended, but only when nobody is
// attached to it. A run does not depend on a window: it keeps working in the gateway with every
// page closed. What it cannot do alone is ask - an approval waits for an answer - and a page that
// is not open cannot show it, so without this the run sat blocked until someone happened to look.
// A person who is watching already sees the event, and a notification on top would be noise.
//
// kind is "approval", "done" or "error"; detail is the command or the error. The send does not wait
// for the push services and ends when the gateway closes.
func (s *Server) pushUnwatched(c *conversation, rn *run, kind, detail string) {
	// A goal is pushed whoever is attached: a page left open on a desk is not someone watching.
	if s.push == nil || rn.quiet || (rn.subscriberCount() > 0 && !rn.goal) {
		return
	}
	if rn.goal && kind != "approval" {
		kind = "goal-" + kind
	}
	c.stateMu.Lock()
	title := c.title
	c.stateMu.Unlock()
	if isPlaceholderTitle(title) {
		title = ""
	}
	s.pushWG.Go(func() {
		ctx, cancel := context.WithTimeout(s.baseCtx, pushTimeout)
		defer cancel()
		if _, err := s.push.Send(ctx, func(lang string) webpush.Message {
			return runMessage(i18n.Lang(lang), kind, detail, title, c.id)
		}); err != nil {
			s.warn("could not send a phone notification", "session", c.id, "error", err.Error())
		}
	})
}

// runMessage is the notification for a run that needs the person or has ended, in lang. A tap
// opens the session.
func runMessage(lang i18n.Lang, kind, detail, title, session string) webpush.Message {
	var head, body string
	switch kind {
	case "approval":
		head, body = "Motita needs your approval", i18n.Tf(lang, "It is waiting to run: %s", detail)
	case "error":
		head, body = "Motita stopped", detail
	case "goal-error":
		head, body = "Your goal stopped", detail
	case "goal-done":
		head, body = "Your goal is done", i18n.T(lang, "Open the session to read what was done and how it was checked.")
	default:
		head, body = "Motita finished", i18n.T(lang, "The run is done: open the session to read the answer.")
	}
	head = i18n.T(lang, head)
	if title != "" {
		head += " — " + title
	}
	return webpush.Message{
		Title: head,
		Body:  body,
		// One notification per session: the next news replaces the last instead of stacking.
		Tag: "motita-run-" + session,
		URL: "/?session=" + url.QueryEscape(session),
	}
}

// isGoalTask reports a turn typed as a /goal (internal/agent goal.go keeps the prefix in the task).
func isGoalTask(task string) bool {
	t := strings.TrimSpace(task)
	return strings.HasPrefix(t, "/goal ")
}
