package gateway

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/madkoding/motita/internal/agent"
	"github.com/madkoding/motita/internal/notes"
	"github.com/madkoding/motita/internal/redact"
)

// The scan can ask the model for a summary of a long session. It is opt-in because the session's
// text goes to the provider, and bounded because every summary is a model call.
const (
	scanSummaryMinTurns = 6
	scanSummaryTimeout  = 45 * time.Second
	scanSummaryTurnText = 400
	scanSummaryHead     = 3
	scanSummaryTail     = 10
)

// scanSummaryMax is how many summaries one scan asks the model for. A variable so a test can lower it.
var scanSummaryMax = 20

// summariser is what a conversation's service offers when it can summarise a text with the model.
// It is optional: a service without it simply gets no summary.
type summariser interface {
	SummarizeText(ctx context.Context, text string) (string, error)
}

var summaryBlock = regexp.MustCompile(`(?s)<!-- motita:summary:(\d+) -->\n(.*?)\n<!-- motita:summary:end -->`)

// summaryInput is the text a summary is asked for: the first turns and the last ones, clipped and
// with secrets removed.
func summaryInput(turns []agent.DialogueTurn) string {
	if len(turns) > scanSummaryHead+scanSummaryTail {
		turns = append(append([]agent.DialogueTurn{}, turns[:scanSummaryHead]...), turns[len(turns)-scanSummaryTail:]...)
	}
	var b strings.Builder
	for _, t := range turns {
		if strings.TrimSpace(t.User) != "" {
			b.WriteString("Person: " + clip(redact.String(t.User), scanSummaryTurnText) + "\n")
		}
		if strings.TrimSpace(t.Agent) != "" {
			b.WriteString("Motita: " + clip(redact.String(t.Agent), scanSummaryTurnText) + "\n")
		}
	}
	return b.String()
}

// summaryOf finds the summary a session note already holds for this many turns, so a long session
// is not summarised again until it has grown.
func summaryOf(v *notes.Vault, name string, turns int) (string, bool) {
	body, _, err := v.Read(name)
	if err != nil {
		return "", false
	}
	m := summaryBlock.FindStringSubmatch(body)
	if m == nil || m[1] != fmt.Sprint(turns) {
		return "", false
	}
	return m[2], true
}

// summaries decides, for each long session, which summary goes in its note.
type summaries struct {
	on   bool
	left int
	done int
}

// of answers the summary for a session, or "" when it gets none: summaries are off, the session is
// short, the service cannot, the cap was reached or the model failed.
func (m *summaries) of(ctx context.Context, v *notes.Vault, name string, ss scanSession) string {
	if !m.on || len(ss.turns) < scanSummaryMinTurns {
		return ""
	}
	if kept, ok := summaryOf(v, name, len(ss.turns)); ok {
		return kept
	}
	sm, ok := ss.svc.(summariser)
	if !ok || m.left <= 0 {
		return ""
	}
	m.left--
	cctx, cancel := context.WithTimeout(ctx, scanSummaryTimeout)
	defer cancel()
	out, err := sm.SummarizeText(cctx, summaryInput(ss.turns))
	out = strings.TrimSpace(redact.String(out))
	if err != nil || out == "" {
		return ""
	}
	m.done++
	out = strings.NewReplacer(notes.BlockStart, "", notes.BlockEnd, "", "[[", "[ [", "<!--", "").Replace(out)
	return out
}

// summarySection is how a summary sits in a session note.
func summarySection(text string, turns int) string {
	if text == "" {
		return ""
	}
	return fmt.Sprintf("\n### Summary\n<!-- motita:summary:%d -->\n%s\n<!-- motita:summary:end -->\n", turns, text)
}
