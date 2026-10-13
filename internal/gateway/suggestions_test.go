package gateway

import (
	"context"
	"net/http"
	"testing"

	"github.com/madkoding/motita/internal/agent"
)

// suggestingService answers every task with a report that makes two suggestions.
func suggestingService() *fakeService {
	svc := recordingService(nil)
	svc.structured = &agent.Report{Summary: "fixed", Suggestions: []agent.ReportSuggestion{
		{Title: "test the empty header"}, {Title: "log the dropped rows"},
	}}
	return svc
}

func answerReq(t *testing.T, srv *Server, turn, body string) *http.Response {
	t.Helper()
	w := send(t, srv, http.MethodPut, sessionPath(srv, DefaultSession, "/checkpoints/"+turn+"/answers"), testToken, body)
	return w.Result()
}

// The buttons pressed on a report's suggestions are kept with its turn, and survive a restart:
// a conversation opened again draws them as pressed instead of offering them again.
func TestSuggestionAnswersAreKeptWithTheTurn(t *testing.T) {
	dir := t.TempDir()
	srv := newTestServer(t, suggestingService(), func(o *Options) { o.SessionDir = dir })
	runTask(t, srv, "fix the parser")

	res := answerReq(t, srv, "0",
		`{"picks":["this","none"],"verdicts":["apply","skip"],"request":"Apply:\n- test the empty header"}`)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("answering = %d", res.StatusCode)
	}
	if err := srv.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	again := newTestServer(t, &fakeService{}, func(o *Options) { o.SessionDir = dir })
	cps := checkpointsOf(t, again)
	a := cps[0].Answers
	if a == nil || !keptAnswers(a) {
		t.Fatalf("after a restart the answers = %+v", a)
	}
}

// keptAnswers reports whether a are the answers the restart test gave before closing the server.
func keptAnswers(a *suggestionAnswers) bool {
	picked := a.Picks[0] == "this" && a.Picks[1] == "none"
	return picked && a.Verdicts[1] == "skip" && a.Request == "Apply:\n- test the empty header"
}

// An answer is taken once, for a turn that made suggestions, and only in the shape they offer.
func TestSuggestionAnswersRefuseWhatTheyCannotKeep(t *testing.T) {
	srv := newTestServer(t, suggestingService())
	runTask(t, srv, "fix the parser")
	for _, tc := range []struct {
		name, turn, body string
		want             int
	}{
		{"not a number", "x", `{}`, http.StatusBadRequest},
		{"no such turn", "7", `{"picks":["none",""],"verdicts":["skip","skip"]}`, http.StatusNotFound},
		{"not JSON", "0", `{`, http.StatusBadRequest},
		{"one answer short", "0", `{"picks":["none"],"verdicts":["skip"]}`, http.StatusBadRequest},
		{"unknown pick", "0", `{"picks":["maybe",""],"verdicts":["skip","skip"]}`, http.StatusBadRequest},
		{"unknown verdict", "0", `{"picks":["none",""],"verdicts":["skip","later"]}`, http.StatusBadRequest},
		{"no button pressed", "0", `{"picks":["",""],"verdicts":["skip","skip"]}`, http.StatusBadRequest},
		{"accepted with no request", "0", `{"picks":["rest",""],"verdicts":["apply","apply"]}`, http.StatusBadRequest},
		{"a request for nothing", "0", `{"picks":["none",""],"verdicts":["skip","skip"],"request":"x"}`,
			http.StatusBadRequest},
		{"dismissed", "0", `{"picks":["none",""],"verdicts":["skip","skip"]}`, http.StatusOK},
		{"answered twice", "0", `{"picks":["none",""],"verdicts":["skip","skip"]}`, http.StatusConflict},
	} {
		if got := answerReq(t, srv, tc.turn, tc.body).StatusCode; got != tc.want {
			t.Errorf("%s: %d, want %d", tc.name, got, tc.want)
		}
	}
}

// A turn with no suggestions has nothing to answer.
func TestSuggestionAnswersNeedSuggestions(t *testing.T) {
	srv := newTestServer(t, recordingService(nil))
	runTask(t, srv, "explain")
	if got := answerReq(t, srv, "0", `{"picks":["none"],"verdicts":["skip"]}`).StatusCode; got != http.StatusNotFound {
		t.Fatalf("answering a turn without suggestions = %d, want 404", got)
	}
}
