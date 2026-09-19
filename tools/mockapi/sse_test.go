package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The streaming half of the protocol. The plan loop sends `stream: true`, so a mock
// that only speaks plain JSON makes the end-to-end test fail for a reason that has
// nothing to do with the agent. These tests pin the event stream the client parses:
// `data:` frames, a finish reason on the last one, and the [DONE] sentinel.

// sseFrames returns the JSON payload of every `data:` frame except [DONE].
func sseFrames(t *testing.T, body string) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		payload := strings.TrimPrefix(line, "data: ")
		if payload == "[DONE]" {
			continue
		}
		var frame map[string]any
		if err := json.Unmarshal([]byte(payload), &frame); err != nil {
			t.Fatalf("a frame is not JSON (%q): %v", payload, err)
		}
		out = append(out, frame)
	}
	return out
}

func deltaOf(t *testing.T, frame map[string]any) map[string]any {
	t.Helper()
	choices, ok := frame["choices"].([]any)
	if !ok || len(choices) == 0 {
		t.Fatalf("a frame must carry choices: %+v", frame)
	}
	choice, _ := choices[0].(map[string]any)
	delta, _ := choice["delta"].(map[string]any)
	return delta
}

func finishOf(t *testing.T, frame map[string]any) string {
	t.Helper()
	choices, _ := frame["choices"].([]any)
	if len(choices) == 0 {
		return ""
	}
	choice, _ := choices[0].(map[string]any)
	reason, _ := choice["finish_reason"].(string)
	return reason
}

// TestStreamingRequestIsAnsweredWithAnEventStream: the client asks for it, so the
// mock must set the content type a proxy needs to avoid buffering the answer.
func TestStreamingRequestIsAnsweredWithAnEventStream(t *testing.T) {
	body := `{"model":"mock","stream":true,"messages":[{"role":"user","content":"hi"}]}`
	rec := httptest.NewRecorder()
	handle(rec, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body)))

	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "text/event-stream" {
		t.Errorf("Content-Type = %q, want text/event-stream", ct)
	}
	if !strings.HasSuffix(rec.Body.String(), "data: [DONE]\n\n") {
		t.Errorf("the stream must end with the sentinel: %q", rec.Body.String())
	}
}

// TestStreamingToolCallArrivesAsADelta: the first answer asks for tools, and it has
// to do so through the stream, with the finish reason that ends the client's read.
func TestStreamingToolCallArrivesAsADelta(t *testing.T) {
	body := `{"model":"mock","stream":true,"messages":[{"role":"user","content":"hi"}],
		"tools":[{"type":"function","function":{"name":"run_command"}}]}`
	rec := httptest.NewRecorder()
	handle(rec, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body)))

	frames := sseFrames(t, rec.Body.String())
	if len(frames) != 1 {
		t.Fatalf("a tool call is one frame, got %d: %s", len(frames), rec.Body.String())
	}
	delta := deltaOf(t, frames[0])
	if _, ok := delta["tool_calls"]; !ok {
		t.Errorf("the frame must carry the tool calls: %+v", delta)
	}
	if got := finishOf(t, frames[0]); got != "tool_calls" {
		t.Errorf("finish_reason = %q, want tool_calls", got)
	}
}

// TestStreamingTextArrivesInPieces: a provider does not send the whole answer as one
// chunk, and the client has to accumulate the fragments. Two frames with a `stop`
// on the last one is the shape being pinned.
func TestStreamingTextArrivesInPieces(t *testing.T) {
	body := `{"model":"mock","stream":true,"messages":[{"role":"tool","content":"a.txt","tool_call_id":"c1"}]}`
	rec := httptest.NewRecorder()
	handle(rec, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body)))

	frames := sseFrames(t, rec.Body.String())
	if len(frames) < 2 {
		t.Fatalf("the answer must arrive in more than one frame, got %d", len(frames))
	}

	var text strings.Builder
	for i, f := range frames {
		piece, _ := deltaOf(t, f)["content"].(string)
		text.WriteString(piece)
		want := ""
		if i == len(frames)-1 {
			want = "stop"
		}
		if got := finishOf(t, f); got != want {
			t.Errorf("frame %d: finish_reason = %q, want %q", i, got, want)
		}
	}
	if !strings.HasPrefix(text.String(), "E2E-RESULT") {
		t.Errorf("the accumulated text must carry the result marker, got %q", text.String())
	}
}

// TestStreamingResponseWithNoChoicesStillTerminates: a malformed completion must
// still close the stream, or the client waits for a frame that never comes.
func TestStreamingResponseWithNoChoicesStillTerminates(t *testing.T) {
	rec := httptest.NewRecorder()
	writeSSE(rec, map[string]any{"id": "x"})

	body := rec.Body.String()
	if !strings.Contains(body, "data: [DONE]") {
		t.Errorf("a completion with no choices must still end the stream: %q", body)
	}
	if len(sseFrames(t, body)) != 0 {
		t.Errorf("no frame must be produced, got %q", body)
	}
}

// TestStreamingAnEmptyAnswerStillCarriesAFinishReason: an assistant turn with no
// text still needs its final frame, otherwise the client cannot tell a finished
// answer from a stalled connection.
func TestStreamingAnEmptyAnswerStillCarriesAFinishReason(t *testing.T) {
	rec := httptest.NewRecorder()
	// An assistant message with no content at all, which is what a provider sends
	// when the answer is empty.
	writeSSE(rec, map[string]any{
		"id":    "chatcmpl-empty",
		"model": "mock",
		"choices": []map[string]any{{
			"index":         0,
			"finish_reason": "stop",
			"message":       map[string]any{"role": "assistant", "content": ""},
		}},
	})

	frames := sseFrames(t, rec.Body.String())
	if len(frames) != 1 {
		t.Fatalf("an empty answer is one frame, got %d: %s", len(frames), rec.Body.String())
	}
	if got := finishOf(t, frames[0]); got != "stop" {
		t.Errorf("finish_reason = %q, want stop", got)
	}
}
