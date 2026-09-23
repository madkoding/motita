package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// TestMain lets the test binary act as the tool itself when the marker is set:
// main() parses the flags and blocks on ListenAndServe, which only makes sense in
// a process of its own.
func TestMain(m *testing.M) {
	if os.Getenv("MOCKLLM_TEST_MAIN") == "1" {
		main()
		return
	}
	os.Exit(m.Run())
}

// TestPhaseDetectsEachStep: the simulated model must recognise which phase of the
// flow the agent is in from the prompt, because that is what makes it answer with
// the right JSON.
func TestPhaseDetectsEachStep(t *testing.T) {
	cases := map[string]string{
		"## ANALYSIS OF THE TASK\n...": "analyze",
		"success_criteria appears":     "analyze",
		"## ACTION PLAN\n...":          "plan",
		"expected_result appears":      "plan",
		"## ACTION\n...":               "execute",
		"reasoning appears":            "execute",
		"something else entirely":      "unknown",
	}
	for prompt, want := range cases {
		if got := phase(prompt); got != want {
			t.Errorf("phase(%q) = %q, expected %q", prompt, got, want)
		}
	}
}

// TestContentJSONGetsItWrongThenRight: the script is the point of this mock: the
// first execution attempt must write contents the anchor rejects and the second
// one the contents it accepts.
func TestContentJSONGetsItWrongThenRight(t *testing.T) {
	first := contentJSON("execute", 1)
	second := contentJSON("execute", 2)

	if !strings.Contains(first, "content-invalid") {
		t.Errorf("the first attempt must write invalid contents: %s", first)
	}
	if !strings.Contains(second, "content-valid") || strings.Contains(second, "content-invalid") {
		t.Errorf("the second attempt must write valid contents: %s", second)
	}

	// The answers must be valid JSON: the agent decodes them.
	for _, text := range []string{contentJSON("analyze", 1), contentJSON("plan", 1), first, second} {
		var decoded map[string]any
		if err := json.Unmarshal([]byte(text), &decoded); err != nil {
			t.Errorf("the answer must be JSON: %v (%s)", err, text)
		}
	}
}

// TestContentJSONForTheUnknownPhase: an unrecognised prompt still gets a usable
// answer (the execution one), so the run does not stall.
func TestContentJSONForTheUnknownPhase(t *testing.T) {
	text := contentJSON("unknown", 1)
	if !strings.Contains(text, "actions") {
		t.Errorf("the fallback must be an execution answer: %s", text)
	}
}

// TestRespondInEveryDialect: the mock offers the three dialects so any provider
// can be exercised; each one must produce the shape that provider expects.
func TestRespondInEveryDialect(t *testing.T) {
	for dialect, marker := range map[string]string{
		"openai":    `"choices"`,
		"anthropic": `"content"`,
		"gemini":    `"candidates"`,
	} {
		t.Run(dialect, func(t *testing.T) {
			rec := httptest.NewRecorder()
			respond(rec, dialect, "the payload")

			if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
				t.Errorf("Content-Type = %q", ct)
			}
			body := rec.Body.String()
			if !strings.Contains(body, marker) {
				t.Errorf("the %s answer must carry %s: %s", dialect, marker, body)
			}
			if !strings.Contains(body, "the payload") {
				t.Errorf("the payload must travel: %s", body)
			}
		})
	}
}

// TestHealthzAnswersOK: the readiness endpoint is what the container waits for.
func TestHealthzAnswersOK(t *testing.T) {
	rec := httptest.NewRecorder()
	handle(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))

	if rec.Code != http.StatusOK {
		t.Errorf("code = %d", rec.Code)
	}
	if rec.Body.String() != "ok" {
		t.Errorf("body = %q", rec.Body.String())
	}
}

// TestHandleAnswersWithJSONPerRequest: the handler must recognise the phase from
// the request body and count the execution attempts, which is what drives the
// "wrong then right" script.
func TestHandleAnswersWithJSONPerRequest(t *testing.T) {
	atomic.StoreInt32(&executionAttempts, 0)

	// First: the analysis phase.
	rec := httptest.NewRecorder()
	handle(rec, httptest.NewRequest(http.MethodPost, "/v1/chat/completions",
		strings.NewReader(`{"messages":[{"role":"user","content":"## ANALYSIS OF THE TASK"}]}`)))
	if !strings.Contains(rec.Body.String(), "understandable") {
		t.Errorf("the analysis answer = %s", rec.Body.String())
	}

	// Then: the execution phase, twice, to see the script change.
	var bodies []string
	for i := 0; i < 2; i++ {
		rec = httptest.NewRecorder()
		handle(rec, httptest.NewRequest(http.MethodPost, "/v1/chat/completions",
			strings.NewReader(`{"messages":[{"role":"user","content":"## ACTION"}]}`)))
		bodies = append(bodies, rec.Body.String())
	}
	if !strings.Contains(bodies[0], "content-invalid") {
		t.Errorf("the first attempt = %s", bodies[0])
	}
	if !strings.Contains(bodies[1], "content-valid") {
		t.Errorf("the second attempt = %s", bodies[1])
	}
}

// TestHandleDetectsTheDialectFromThePath: the mock is mounted on the path each
// provider uses, and the answer shape follows from it.
func TestHandleDetectsTheDialectFromThePath(t *testing.T) {
	atomic.StoreInt32(&executionAttempts, 0)

	rec := httptest.NewRecorder()
	handle(rec, httptest.NewRequest(http.MethodPost, "/v1/messages",
		strings.NewReader(`{"messages":[{"role":"user","content":"## ACTION PLAN"}]}`)))
	if !strings.Contains(rec.Body.String(), `"content"`) {
		t.Errorf("the Anthropic shape was expected: %s", rec.Body.String())
	}

	rec = httptest.NewRecorder()
	handle(rec, httptest.NewRequest(http.MethodPost, "/v1beta/models/x:generateContent",
		strings.NewReader(`{"messages":[{"role":"user","content":"## ACTION PLAN"}]}`)))
	if !strings.Contains(rec.Body.String(), `"candidates"`) {
		t.Errorf("the Gemini shape was expected: %s", rec.Body.String())
	}
}

// TestScriptedReply: a prompt carrying the marker gets the fixed answer, which is
// how a test can steer the simulated model.
func TestScriptedReply(t *testing.T) {
	if got := scriptedReply("please RESPOND_WITH:   the exact text  "); got != "the exact text" {
		t.Errorf("scriptedReply = %q", got)
	}
	if got := scriptedReply("no marker here"); got != "" {
		t.Errorf("scriptedReply = %q, expected empty", got)
	}
	// The marker wins over the phase detection.
	rec := httptest.NewRecorder()
	handle(rec, httptest.NewRequest(http.MethodPost, "/v1/chat/completions",
		strings.NewReader(`{"messages":[{"role":"user","content":"## ACTION\nRESPOND_WITH: fixed answer"}]}`)))
	if !strings.Contains(rec.Body.String(), "fixed answer") {
		t.Errorf("the fixed answer must be used: %s", rec.Body.String())
	}
}

// TestHandleWithAnUnreadableBody: a body that cannot be read must not panic; the
// phase simply comes out as unknown.
func TestHandleWithAnUnreadableBody(t *testing.T) {
	atomic.StoreInt32(&executionAttempts, 0)
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", io.NopCloser(brokenReader{}))
	req.ContentLength = -1

	rec := httptest.NewRecorder()
	handle(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("code = %d", rec.Code)
	}
}

// brokenReader fails on every read, to exercise the error paths.
type brokenReader struct{}

func (brokenReader) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }

// TestTheReplyDelayIsOptionalAndDefaultsToOff: the delay exists so a run can be observed in
// flight, and it must change NOTHING for every other caller. A mock that silently slowed down
// would make every e2e run slower for no reason, and the default is what keeps that from
// happening.
func TestTheReplyDelayIsOptionalAndDefaultsToOff(t *testing.T) {
	if got := envDelayMS(); got != 0 {
		t.Fatalf("with no environment set the delay is %d, want 0", got)
	}
	t.Setenv("MOCKLLM_DELAY_MS", "25")
	if got := envDelayMS(); got != 25 {
		t.Errorf("MOCKLLM_DELAY_MS=25 read back as %d", got)
	}
	// A malformed or negative value is treated as off rather than as a huge sleep, which would
	// look like a hung gateway.
	for _, bad := range []string{"", "soon", "-5"} {
		t.Setenv("MOCKLLM_DELAY_MS", bad)
		if got := envDelayMS(); got != 0 {
			t.Errorf("MOCKLLM_DELAY_MS=%q read back as %d, want 0", bad, got)
		}
	}
}

// TestAConfiguredDelayActuallySlowsAReply: the flag is only useful if it reaches the handler, so
// this measures the wall clock rather than trusting the wiring.
func TestAConfiguredDelayActuallySlowsAReply(t *testing.T) {
	atomic.StoreInt64(&replyDelayMS, 30)
	t.Cleanup(func() { atomic.StoreInt64(&replyDelayMS, 0) })

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions",
		strings.NewReader("## ANALYSIS OF THE TASK"))

	started := time.Now()
	handle(rec, req)
	if elapsed := time.Since(started); elapsed < 25*time.Millisecond {
		t.Fatalf("the reply took %v with a 30ms delay configured, so the delay is not applied", elapsed)
	}
}
