package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// TestToolCallCarriesArgumentsAsAJSONString: the OpenAI specification sends
// `arguments` as a string, not as an object, and that is the shape that
// historically broke clients. This mock exists to keep sending it that way.
func TestToolCallCarriesArgumentsAsAJSONString(t *testing.T) {
	call := toolCall("call_1", "run_command", map[string]string{"cmd": "arch"})

	fn, ok := call["function"].(map[string]any)
	if !ok {
		t.Fatalf("the call must carry a function: %#v", call)
	}
	if fn["name"] != "run_command" {
		t.Errorf("name = %v", fn["name"])
	}
	raw, ok := fn["arguments"].(string)
	if !ok {
		t.Fatalf("arguments must be a string, got %#v", fn["arguments"])
	}
	var decoded map[string]string
	if err := json.Unmarshal([]byte(raw), &decoded); err != nil {
		t.Fatalf("the arguments string must contain JSON: %v (%s)", err, raw)
	}
	if decoded["cmd"] != "arch" {
		t.Errorf("cmd = %q", decoded["cmd"])
	}
}

// TestToolCallsResponseAsksForBothTools: the scripted first answer asks the agent
// to run a command and to read a file, which is the whole point of the E2E test.
func TestToolCallsResponseAsksForBothTools(t *testing.T) {
	resp := toolCallsResponse()
	choices, _ := resp["choices"].([]map[string]any)
	if len(choices) != 1 {
		t.Fatalf("choices = %#v", resp["choices"])
	}
	msg, _ := choices[0]["message"].(map[string]any)
	if msg["role"] != "assistant" {
		t.Errorf("role = %v", msg["role"])
	}
	if msg["content"] != nil {
		t.Errorf("a tool-call answer carries no text: %v", msg["content"])
	}
	calls, _ := msg["tool_calls"].([]map[string]any)
	if len(calls) != 2 {
		t.Fatalf("two tool calls were expected: %#v", calls)
	}
	if choices[0]["finish_reason"] != "tool_calls" {
		t.Errorf("finish_reason = %v", choices[0]["finish_reason"])
	}
}

// TestFinalResponseCitesTheToolResults: the second answer must quote what the
// tools returned, because that is what the E2E test greps for.
func TestFinalResponseCitesTheToolResults(t *testing.T) {
	results := []message{
		{Role: "tool", Name: "run_command", Content: "i386\n32\n"},
		{Role: "tool", Name: "read_file", Content: "PRETTY_NAME=\"Debian\"\n"},
	}
	resp := finalResponse(results)

	choices, _ := resp["choices"].([]map[string]any)
	msg, _ := choices[0]["message"].(map[string]any)
	content, _ := msg["content"].(string)

	if !strings.HasPrefix(content, "E2E-RESULT") {
		t.Errorf("the answer must carry the marker: %q", content)
	}
	if !strings.Contains(content, "run_command=i386 32") {
		t.Errorf("the command result must be cited and flattened: %q", content)
	}
	if !strings.Contains(content, "read_file=PRETTY_NAME=") {
		t.Errorf("the file result must be cited: %q", content)
	}
	if choices[0]["finish_reason"] != "stop" {
		t.Errorf("finish_reason = %v", choices[0]["finish_reason"])
	}
}

// TestHealthzAnswersOK: the readiness endpoint the container waits for.
func TestHealthzAnswersOK(t *testing.T) {
	rec := httptest.NewRecorder()
	handle(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if rec.Code != http.StatusOK || rec.Body.String() != "ok" {
		t.Errorf("code = %d body = %q", rec.Code, rec.Body.String())
	}
}

// TestUnknownPathIsRefused: a request to anywhere else must be a 404 with an
// explanatory message, so a misconfigured base URL is obvious.
func TestUnknownPathIsRefused(t *testing.T) {
	rec := httptest.NewRecorder()
	handle(rec, httptest.NewRequest(http.MethodPost, "/v1/something-else", nil))
	if rec.Code != http.StatusNotFound {
		t.Errorf("code = %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "unknown path") {
		t.Errorf("body = %q", rec.Body.String())
	}
}

// TestInvalidJSONIsRefused: a body that is not JSON must be a 400, not a crash.
func TestInvalidJSONIsRefused(t *testing.T) {
	rec := httptest.NewRecorder()
	handle(rec, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader("{not json")))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("code = %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "invalid json") {
		t.Errorf("body = %q", rec.Body.String())
	}
}

// TestFirstRequestGetsToolCalls: with no tool result yet, the answer asks for the
// tools (that is what starts the agent loop).
func TestFirstRequestGetsToolCalls(t *testing.T) {
	body := `{"model":"mock","messages":[{"role":"user","content":"tell me the architecture"}],
		"tools":[{"type":"function","function":{"name":"run_command"}},{"type":"function","function":{"name":"read_file"}}]}`
	rec := httptest.NewRecorder()
	handle(rec, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body)))

	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "tool_calls") {
		t.Errorf("the first answer must ask for tools: %s", rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q", ct)
	}
}

// TestAfterTheToolResultsTheAnswerIsFinal: once the messages carry tool results,
// TestHandleWithAnUnreadableBody: a body that cannot be read must be a 400 with a
// message, not a crash.
func TestHandleWithAnUnreadableBody(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", io.NopCloser(brokenReader{}))
	req.ContentLength = -1

	rec := httptest.NewRecorder()
	handle(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("code = %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "unreadable request") {
		t.Errorf("body = %q", rec.Body.String())
	}
}

// TestHandleWithNoTools: a request carrying no tool definitions must still be
// answered (the listing of tools is only logged).
func TestHandleWithNoTools(t *testing.T) {
	rec := httptest.NewRecorder()
	handle(rec, httptest.NewRequest(http.MethodPost, "/v1/chat/completions",
		strings.NewReader(`{"model":"mock","messages":[]}`)))
	if rec.Code != http.StatusOK {
		t.Errorf("code = %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "tool_calls") {
		t.Errorf("the answer must start the loop: %s", rec.Body.String())
	}
}

// TestFinalResponseWithNoResults: with no tool results there is nothing to cite
// and the marker must still be emitted (so the E2E test can tell the loop closed).
func TestFinalResponseWithNoResults(t *testing.T) {
	resp := finalResponse(nil)
	choices, _ := resp["choices"].([]map[string]any)
	msg, _ := choices[0]["message"].(map[string]any)
	content, _ := msg["content"].(string)
	if !strings.HasPrefix(content, "E2E-RESULT") {
		t.Errorf("content = %q", content)
	}
}

// brokenReader fails on every read, to exercise the error path.
type brokenReader struct{}

func (brokenReader) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }

// TestAfterTheToolResultsTheAnswerIsFinal: once the messages carry tool results,
// the mock must close the loop with the final text (this is the branch that makes
// the E2E test finish).
func TestAfterTheToolResultsTheAnswerIsFinal(t *testing.T) {
	body := `{"model":"mock","messages":[
		{"role":"user","content":"tell me the architecture"},
		{"role":"assistant","content":null},
		{"role":"tool","name":"run_command","tool_call_id":"call_1","content":"i386"},
		{"role":"tool","name":"read_file","tool_call_id":"call_2","content":"Debian"}
	]}`
	rec := httptest.NewRecorder()
	handle(rec, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body)))

	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d", rec.Code)
	}
	text := rec.Body.String()
	if !strings.Contains(text, "E2E-RESULT") {
		t.Errorf("the final answer must carry the marker: %s", text)
	}
	if strings.Contains(text, "tool_calls") {
		t.Errorf("the loop must be closed, not asked again: %s", text)
	}
	if !strings.Contains(text, "i386") {
		t.Errorf("the results must be cited: %s", text)
	}
}

// TestListenAndServeReallyServes: the real implementation must bind and serve, so
// the tool works when it is run for the E2E test. It is exercised on a port of its
// own and stopped straight away.
func TestListenAndServeReallyServes(t *testing.T) {
	srv := &http.Server{
		Addr:              "127.0.0.1:18098",
		Handler:           http.HandlerFunc(handle),
		ReadHeaderTimeout: time.Second,
	}
	done := make(chan error, 1)
	go func() { done <- listenAndServe(srv) }()
	defer srv.Close()

	url := "http://127.0.0.1:18098/healthz"
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := http.Get(url)
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return // it is up
			}
		}
		time.Sleep(40 * time.Millisecond)
	}
	t.Fatalf("the real server did not answer on %s", url)
}
