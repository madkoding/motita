package gateway

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/madkoding/motita/internal/agent"
)

// askingService answers every task by asking two questions, each with its options.
func askingService() *fakeService {
	svc := recordingService(nil)
	svc.asked = []agent.AskItem{
		{Text: "which login?", Assumption: "email and password", Options: []string{"email", "GitHub"}},
		{Text: "where do sessions live?", Options: []string{"cookies", "Redis"}},
	}
	return svc
}

func questionAnswerReq(t *testing.T, srv *Server, turn, body string) int {
	t.Helper()
	path := sessionPath(srv, DefaultSession, "/checkpoints/"+turn+"/question-answers")
	return send(t, srv, http.MethodPut, path, testToken, body).Result().StatusCode
}

// Every question of a turn reaches the client with its options and assumption, in the done event
// and in the turn's checkpoint, and a plan never carries a task's questions.
func TestTheDoneEventCarriesEveryQuestion(t *testing.T) {
	svc := askingService()
	svc.plan = func(context.Context, string, func(string, ...any)) (string, error) { return "a plan", nil }
	srv := newTestServer(t, svc)
	raw := rawStream(t, srv, http.MethodPost, sessionPath(srv, DefaultSession, "/task"), `{"task":"add login"}`)
	for _, want := range []string{`"questions":[{"text":"which login?","assumption":"email and password",` +
		`"options":["email","GitHub"]}`, `"where do sessions live?"`} {
		if !strings.Contains(raw, want) {
			t.Errorf("the done event lacks %s:\n%s", want, raw)
		}
	}
	cps := checkpointsOf(t, srv)
	if len(cps) == 0 || cps[0].Outcome == nil || len(cps[0].Outcome.Questions) != 2 {
		t.Fatalf("the checkpoint must keep the questions, got %+v", cps)
	}
	raw = rawStream(t, srv, http.MethodPost, sessionPath(srv, DefaultSession, "/plan"), `{"prompt":"x"}`)
	if strings.Contains(raw, `"questions"`) {
		t.Errorf("a plan must not carry a task's questions:\n%s", raw)
	}
}

// The answers to a turn's questions are kept with it and survive a restart.
func TestQuestionAnswersAreKeptWithTheTurn(t *testing.T) {
	dir := t.TempDir()
	srv := newTestServer(t, askingService(), func(o *Options) { o.SessionDir = dir })
	runTask(t, srv, "add login")
	if got := questionAnswerReq(t, srv, "0", `{"answers":["GitHub",""],"request":"add login\n\nanswers"}`); got != http.StatusOK {
		t.Fatalf("answering = %d", got)
	}
	if err := srv.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	again := newTestServer(t, &fakeService{}, func(o *Options) { o.SessionDir = dir })
	a := checkpointsOf(t, again)[0].QuestionAnswers
	if a == nil || a.Answers[0] != "GitHub" || a.Answers[1] != "" || a.Request != "add login\n\nanswers" {
		t.Fatalf("after a restart the answers = %+v", a)
	}
}

// An answer is taken once, for a turn that asked, with one entry per question and its turn.
func TestQuestionAnswersRefuseWhatTheyCannotKeep(t *testing.T) {
	srv := newTestServer(t, askingService())
	runTask(t, srv, "add login")
	for _, tc := range []struct {
		name, turn, body string
		want             int
	}{
		{"not a number", "x", `{}`, http.StatusBadRequest},
		{"no such turn", "7", `{"answers":["a","b"],"request":"r"}`, http.StatusNotFound},
		{"not JSON", "0", `{`, http.StatusBadRequest},
		{"one answer short", "0", `{"answers":["a"],"request":"r"}`, http.StatusBadRequest},
		{"no request", "0", `{"answers":["a","b"],"request":" "}`, http.StatusBadRequest},
		{"answered", "0", `{"answers":["a",""],"request":"r"}`, http.StatusOK},
		{"answered twice", "0", `{"answers":["a",""],"request":"r"}`, http.StatusConflict},
	} {
		if got := questionAnswerReq(t, srv, tc.turn, tc.body); got != tc.want {
			t.Errorf("%s: %d, want %d", tc.name, got, tc.want)
		}
	}
}

// A turn that asked nothing has no questions to answer.
func TestQuestionAnswersNeedQuestions(t *testing.T) {
	srv := newTestServer(t, recordingService(nil))
	runTask(t, srv, "explain")
	if got := questionAnswerReq(t, srv, "0", `{"answers":[],"request":"r"}`); got != http.StatusNotFound {
		t.Fatalf("answering a turn without questions = %d, want 404", got)
	}
}
