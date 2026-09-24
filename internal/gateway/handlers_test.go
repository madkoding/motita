package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/madkoding/motita/internal/agent"
	"github.com/madkoding/motita/internal/session"
)

// post performs an authenticated POST with a body and returns the recorder.
func post(t *testing.T, srv *Server, path, body, token string) *httptest.ResponseRecorder {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, srv.BaseURL()+path, strings.NewReader(body))
	if err != nil {
		t.Fatalf("building the request: %v", err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	return w
}

func TestSessionAnswersTheFigures(t *testing.T) {
	svc := &fakeService{
		summary: session.Snapshot{Model: "gpt-4o-mini", Window: 128000, Tokens: 1200, Used: 0.0093, Messages: 4},
	}
	srv := newTestServer(t, svc)

	w := get(t, srv, sessionPath(srv, DefaultSession, ""), testToken)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	var snap session.Snapshot
	if err := json.Unmarshal(w.Body.Bytes(), &snap); err != nil {
		t.Fatalf("body = %q: %v", w.Body.String(), err)
	}
	if snap.Window != 128000 || snap.Tokens != 1200 || snap.Model != "gpt-4o-mini" {
		t.Errorf("snapshot = %+v", snap)
	}
}

// An unstarted conversation answers zeroes, which is what a status bar renders as "nothing yet"
// rather than as a percentage of nothing.
func TestSessionAnswersZeroesBeforeAConversationStarts(t *testing.T) {
	srv := newTestServer(t, &fakeService{})
	w := get(t, srv, sessionPath(srv, DefaultSession, ""), testToken)
	var snap session.Snapshot
	if err := json.Unmarshal(w.Body.Bytes(), &snap); err != nil {
		t.Fatalf("body = %q: %v", w.Body.String(), err)
	}
	if snap.Window != 0 {
		t.Errorf("window = %d, want 0 before anything was sent", snap.Window)
	}
}

func TestSessionReportIsTextual(t *testing.T) {
	svc := &fakeService{report: "model       gpt-4o-mini\ncontext     128000 tokens\n"}
	srv := newTestServer(t, svc)
	w := get(t, srv, sessionPath(srv, DefaultSession, "/report"), testToken)
	var out struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("body = %q: %v", w.Body.String(), err)
	}
	if !strings.Contains(out.Text, "128000 tokens") {
		t.Errorf("text = %q", out.Text)
	}
}

func TestResetStartsANewConversation(t *testing.T) {
	svc := &fakeService{}
	srv := newTestServer(t, svc)

	w := post(t, srv, sessionPath(srv, DefaultSession, "/reset"), "{}", testToken)
	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204 (body %q)", w.Code, w.Body.String())
	}
	if svc.reset != 1 {
		t.Errorf("ResetConversation was called %d times, want 1", svc.reset)
	}
}

func TestTheModelsReportIsTextual(t *testing.T) {
	svc := &fakeService{models: func(context.Context) (string, error) { return "provider : openai\n", nil }}
	srv := newTestServer(t, svc)

	w := get(t, srv, sessionPath(srv, DefaultSession, "/models"), testToken)
	var out struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("body = %q: %v", w.Body.String(), err)
	}
	if !strings.HasPrefix(out.Text, "provider : openai") {
		t.Errorf("text = %q", out.Text)
	}
}

// A catalogue failure is a 502 with the reason, not a 500: the agent is fine and the provider is
// what did not answer, and a client can act on that difference.
func TestAModelsFailureAnswersWithTheReason(t *testing.T) {
	svc := &fakeService{models: func(context.Context) (string, error) { return "", errors.New("no route to the provider") }}
	srv := newTestServer(t, svc)

	w := get(t, srv, sessionPath(srv, DefaultSession, "/models"), testToken)
	if w.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", w.Code)
	}
	var e struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &e); err != nil {
		t.Fatalf("body = %q: %v", w.Body.String(), err)
	}
	if !strings.Contains(e.Error, "no route to the provider") {
		t.Errorf("error = %q, the reason must reach the user", e.Error)
	}
}

func TestReasoningIsSet(t *testing.T) {
	svc := &fakeService{}
	srv := newTestServer(t, svc)

	w := post(t, srv, sessionPath(srv, DefaultSession, "/reasoning"), `{"level":"high"}`, testToken)
	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204 (body %q)", w.Code, w.Body.String())
	}
	if svc.reasoning != "high" {
		t.Errorf("level = %q, want high", svc.reasoning)
	}
}

// The level is passed through VERBATIM, including the empty string: validating the vocabulary is
// the runner's job (it cycles a fixed list) and a transport that second-guessed it would be a
// second place the valid levels live.
func TestReasoningPassesTheLevelThrough(t *testing.T) {
	svc := &fakeService{}
	srv := newTestServer(t, svc)
	if w := post(t, srv, sessionPath(srv, DefaultSession, "/reasoning"), `{"level":""}`, testToken); w.Code != http.StatusNoContent {
		t.Fatalf("status = %d", w.Code)
	}
	if svc.reasoning != "" {
		t.Errorf("level = %q, want it passed through as given", svc.reasoning)
	}
}

func TestAVerdictCarriesTheNote(t *testing.T) {
	var gotGood bool
	var gotNote string
	svc := &fakeService{verdict: func(good bool, note string) string {
		gotGood, gotNote = good, note
		return "recorded: bad"
	}}
	srv := newTestServer(t, svc)

	w := post(t, srv, sessionPath(srv, DefaultSession, "/verdict"), `{"good":false,"note":"the second step was wrong"}`, testToken)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d (body %q)", w.Code, w.Body.String())
	}
	if gotGood {
		t.Error("good arrived as true")
	}
	if gotNote != "the second step was wrong" {
		t.Errorf("note = %q: the note is what makes a verdict actionable", gotNote)
	}
	var out struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("body = %q: %v", w.Body.String(), err)
	}
	if out.Text != "recorded: bad" {
		t.Errorf("text = %q", out.Text)
	}
}

// The common verdict is the good one, so a body that omits the flag must mean good rather than
// being refused.
func TestAVerdictWithoutTheFlagMeansGood(t *testing.T) {
	var gotGood bool
	svc := &fakeService{verdict: func(good bool, _ string) string { gotGood = good; return "ok" }}
	srv := newTestServer(t, svc)
	if w := post(t, srv, sessionPath(srv, DefaultSession, "/verdict"), `{}`, testToken); w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	if gotGood {
		t.Error("good arrived as true, which is not what was sent")
	}
}

func TestRewardIsTextual(t *testing.T) {
	svc := &fakeService{reward: "no verdicts recorded yet.\n"}
	srv := newTestServer(t, svc)
	w := get(t, srv, sessionPath(srv, DefaultSession, "/reward"), testToken)
	var out struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("body = %q: %v", w.Body.String(), err)
	}
	if !strings.Contains(out.Text, "no verdicts recorded yet") {
		t.Errorf("text = %q", out.Text)
	}
}

func TestTheQuestionsAreReadAndCleared(t *testing.T) {
	svc := &fakeService{
		questions: []agent.AskItem{{Text: "which directory?", Assumption: "/tmp", Options: []string{"/tmp", "."}}},
		origin:    "clean the build",
	}
	srv := newTestServer(t, svc)

	w := get(t, srv, sessionPath(srv, DefaultSession, "/questions"), testToken)
	var got struct {
		Items  []agent.AskItem `json:"items"`
		Origin string          `json:"origin"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("body = %q: %v", w.Body.String(), err)
	}
	if len(got.Items) != 1 {
		t.Fatalf("items = %+v", got.Items)
	}
	if got.Items[0].Text != "which directory?" || got.Origin != "clean the build" {
		t.Errorf("items = %+v, origin = %q", got.Items, got.Origin)
	}
	// The options and the assumption are what make the window pickable and answerable in one
	// word: flattening them into the question text would lose the structure the window needs.
	if len(got.Items[0].Options) != 2 || got.Items[0].Assumption != "/tmp" {
		t.Errorf("the options and the assumption must survive: %+v", got.Items[0])
	}

	// Second read: empty. Taking CLEARS, so a repaint cannot reopen a window the user closed.
	w2 := get(t, srv, sessionPath(srv, DefaultSession, "/questions"), testToken)
	var again struct {
		Items []agent.AskItem `json:"items"`
	}
	if err := json.Unmarshal(w2.Body.Bytes(), &again); err != nil {
		t.Fatalf("body = %q: %v", w2.Body.String(), err)
	}
	if len(again.Items) != 0 {
		t.Errorf("the questions were served twice: %+v", again.Items)
	}
}

// An empty list, never null: a client must not have to tell "no questions" from "field missing".
func TestNoQuestionsIsAnEmptyList(t *testing.T) {
	srv := newTestServer(t, &fakeService{})
	w := get(t, srv, sessionPath(srv, DefaultSession, "/questions"), testToken)
	if got := strings.TrimSpace(w.Body.String()); got != `{"items":[],"origin":""}` {
		t.Errorf("body = %q", got)
	}
}

func TestABadBodyIsRejectedOnEveryEndpoointThatTakesOne(t *testing.T) {
	srv := newTestServer(t, &fakeService{})
	for _, path := range []string{sessionPath(srv, DefaultSession, "/reasoning"), sessionPath(srv, DefaultSession, "/verdict")} {
		t.Run(path, func(t *testing.T) {
			w := post(t, srv, path, "not json", testToken)
			if w.Code != http.StatusBadRequest {
				t.Errorf("status = %d, want 400", w.Code)
			}
		})
	}
}

// An oversized body is refused, and the code is whatever the reader produced. The assertion
// names the real behaviour instead of forcing the implementation to match a guess: see the note
// in decodeBody.
func TestAnOversizedBodyIsRejected(t *testing.T) {
	srv := newTestServer(t, &fakeService{}, func(o *Options) { o.MaxBodyKB = 1 })
	big := `{"note":"` + strings.Repeat("x", 4096) + `"}`
	w := post(t, srv, sessionPath(srv, DefaultSession, "/verdict"), big, testToken)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 for a body over the cap", w.Code)
	}
}
