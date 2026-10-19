package gateway

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/madkoding/motita/internal/agent"
)

// summaryService is a service that can summarise, counting what it was asked.
type summaryService struct {
	*fakeService
	reply string
	err   error
	calls int
	asked string
}

func (s *summaryService) SummarizeText(_ context.Context, text string) (string, error) {
	s.calls++
	s.asked = text
	return s.reply, s.err
}

func longTurns(n int) []agent.DialogueTurn {
	var out []agent.DialogueTurn
	for i := 0; i < n; i++ {
		out = append(out, agent.DialogueTurn{User: fmt.Sprintf("question %d", i), Agent: fmt.Sprintf("answer %d", i)})
	}
	return out
}

func summaryServer(t *testing.T, svc Service) (*Server, string) {
	t.Helper()
	srv, dir, _ := scanServer(t, nil)
	c, _ := srv.lookup(DefaultSession)
	c.stateMu.Lock()
	c.title, c.lastUsed = "Long one", time.Now()
	c.stateMu.Unlock()
	c.svc = svc
	return srv, dir
}

func scanWith(t *testing.T, srv *Server, query string) noteScan {
	t.Helper()
	w := noteCall(t, srv, http.MethodPost, "/v1/notes/scan"+query, "")
	return decodeScan(t, w.Body.Bytes(), w.Code)
}

func TestScanSummarisesLongSessionsOnlyWhenAskedAndOnlyOnce(t *testing.T) {
	svc := &summaryService{
		fakeService: &fakeService{transcript: longTurns(20)},
		reply:       "- asked things <!-- motita:scan:end --> [[x]] key sk-abcdefghijklmnopqrstuvwxyz0123456789",
	}
	srv, dir := summaryServer(t, svc)
	c, _ := srv.lookup(DefaultSession)
	note := sessionNoteName("Long one", c.id)

	if got := scanWith(t, srv, ""); got.Summarized != 0 || svc.calls != 0 {
		t.Fatalf("summaries are off by default: %+v, %d calls", got, svc.calls)
	}
	if got := scanWith(t, srv, "?summarize=1"); got.Summarized != 1 || svc.calls != 1 {
		t.Fatalf("summarised: %+v, %d calls", got, svc.calls)
	}
	body := readNote(t, dir, note)
	for _, want := range []string{"### Summary", "<!-- motita:summary:20 -->", "- asked things", "[ [x]]"} {
		if !strings.Contains(body, want) {
			t.Errorf("note lacks %q:\n%s", want, body)
		}
	}
	if strings.Contains(body, "sk-abcdefghij") || strings.Count(body, "motita:scan:end") != 1 {
		t.Errorf("a secret or a marker got through:\n%s", body)
	}
	if !strings.Contains(svc.asked, "Person: question 0") || !strings.Contains(svc.asked, "Motita: answer 19") ||
		strings.Contains(svc.asked, "question 4") {
		t.Errorf("the model gets the start and the end of a long session:\n%s", svc.asked)
	}

	// Same number of turns: the summary is kept, not asked again. More turns: asked again.
	if got := scanWith(t, srv, "?summarize=1"); got.Summarized != 0 || svc.calls != 1 {
		t.Errorf("rescan: %+v, %d calls", got, svc.calls)
	}
	if !strings.Contains(readNote(t, dir, note), "- asked things") {
		t.Error("the kept summary stays in the note")
	}
	svc.transcript = longTurns(21)
	if got := scanWith(t, srv, "?summarize=1"); got.Summarized != 1 || svc.calls != 2 {
		t.Errorf("a session that grew: %+v, %d calls", got, svc.calls)
	}
}

func TestScanSummaryGivesUpQuietly(t *testing.T) {
	svc := &summaryService{fakeService: &fakeService{transcript: longTurns(6)}, err: errors.New("down")}
	srv, dir := summaryServer(t, svc)
	c, _ := srv.lookup(DefaultSession)
	note := sessionNoteName("Long one", c.id)
	if got := scanWith(t, srv, "?summarize=1"); got.Summarized != 0 || got.Created != 1 {
		t.Errorf("a failing model: %+v", got)
	}
	svc.err, svc.reply = nil, "  "
	scanWith(t, srv, "?summarize=1")
	if strings.Contains(readNote(t, dir, note), "### Summary") {
		t.Error("an empty summary is not written")
	}

	// Short sessions, services that cannot summarise and the cap get none.
	svc.reply, svc.calls = "ok", 0
	svc.transcript = longTurns(5)
	scanWith(t, srv, "?summarize=1")
	c.svc = &fakeService{transcript: longTurns(8)}
	scanWith(t, srv, "?summarize=1")
	if svc.calls != 0 {
		t.Errorf("a short session or a service that cannot summarise must not be asked: %d", svc.calls)
	}
	old := scanSummaryMax
	defer func() { scanSummaryMax = old }()
	scanSummaryMax = 0
	svc.transcript = longTurns(8)
	c.svc = svc
	if got := scanWith(t, srv, "?summarize=1"); got.Summarized != 0 || svc.calls != 0 {
		t.Errorf("the cap: %+v, %d calls", got, svc.calls)
	}
}
