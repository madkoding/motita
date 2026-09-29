package llm

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The task loop shows the model's reasoning WHILE it is written. These tests pin the pieces
// that make that possible, and the provider routing that used to send Anthropic and Gemini
// streams to the OpenAI endpoint.

// TestCompleteStreamDeliversTextAndThinking: the answer arrives in fragments, the reasoning
// tokens arrive apart from it, and only the answer is returned.
func TestCompleteStreamDeliversTextAndThinking(t *testing.T) {
	var body string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b := make([]byte, r.ContentLength)
		r.Body.Read(b)
		body = string(b)
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"reasoning_content\":\"let me think\"}}]}\n\n")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"reasoning\":\" more\"}}]}\n\n")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"{\\\"a\\\":\"}}]}\n\n")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"1}\"},\"finish_reason\":\"stop\"}]}\n\n")
	}))
	defer srv.Close()
	c := streamClient(t, srv.URL)

	var text, thought strings.Builder
	got, err := c.CompleteStream(context.Background(), []Message{{Role: "user", Content: "hi"}},
		func(s string, thinking bool) {
			if thinking {
				thought.WriteString(s)
			} else {
				text.WriteString(s)
			}
		})
	if err != nil || got != `{"a":1}` {
		t.Fatalf("got %q err %v", got, err)
	}
	if thought.String() != "let me think more" || text.String() != `{"a":1}` {
		t.Errorf("thought %q text %q", thought.String(), text.String())
	}
	if strings.Contains(body, `"tools"`) {
		t.Errorf("a completion with no tools must not send the field: %s", body)
	}
}

// TestCompleteStreamFallsBackToAPlainCompletion: an endpoint that answers `stream: true`
// with nothing usable still gets its answer, through the non-streaming call.
func TestCompleteStreamFallsBackToAPlainCompletion(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b := make([]byte, r.ContentLength)
		r.Body.Read(b)
		if strings.Contains(string(b), `"stream":true`) {
			// Not SSE at all: the parser finds no data line and the stream ends empty.
			fmt.Fprint(w, `{"choices":[{"message":{"content":"ignored"}}]}`)
			return
		}
		fmt.Fprint(w, `{"choices":[{"message":{"content":"plain answer"}}]}`)
	}))
	defer srv.Close()
	c := streamClient(t, srv.URL)
	got, err := c.CompleteStream(context.Background(), []Message{{Role: "user", Content: "hi"}}, func(string, bool) {})
	if err != nil || got != "plain answer" {
		t.Fatalf("got %q err %v", got, err)
	}

	// A stream that fails outright falls back the same way.
	failing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprint(w, `{"error":{"message":"no"}}`)
	}))
	defer failing.Close()
	if _, err := streamClient(t, failing.URL).CompleteStream(context.Background(),
		[]Message{{Role: "user", Content: "hi"}}, func(string, bool) {}); err == nil {
		t.Error("an endpoint that refuses both ways must report the failure")
	}
}

// TestCompleteStreamStopsWhenCancelled: a cancelled run does not ask a second time.
func TestCompleteStreamStopsWhenCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		cancel()
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	if _, err := streamClient(t, srv.URL).CompleteStream(ctx, []Message{{Role: "user", Content: "hi"}},
		func(string, bool) {}); err == nil {
		t.Fatal("a cancelled completion must fail")
	}
	if calls != 1 {
		t.Errorf("calls = %d: a cancelled run must not be asked again without streaming", calls)
	}
}

// TestAnthropicAndGeminiStreamThroughTheirOwnAPI: both used to be sent to /chat/completions.
func TestAnthropicAndGeminiStreamThroughTheirOwnAPI(t *testing.T) {
	anthropic := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "chat/completions") {
			t.Errorf("anthropic was sent to %s", r.URL.Path)
		}
		b := make([]byte, r.ContentLength)
		r.Body.Read(b)
		if strings.Contains(string(b), `"tools"`) {
			fmt.Fprint(w, `{"content":[{"type":"text","text":"calling"},{"type":"tool_use","id":"t1","name":"read_file","input":{"path":"a"}}],"stop_reason":"tool_use"}`)
			return
		}
		fmt.Fprint(w, `{"content":[{"type":"text","text":"anthropic says hi"}]}`)
	}))
	defer anthropic.Close()
	c := testClient(t, "anthropic", anthropic.URL, nil)
	got, err := c.CompleteStream(context.Background(), []Message{{Role: "user", Content: "hi"}}, func(string, bool) {})
	if err != nil || got != "anthropic says hi" {
		t.Fatalf("got %q err %v", got, err)
	}
	var calls int
	var done bool
	for chunk := range c.CompleteToolsStream(context.Background(), []Message{{Role: "user", Content: "hi"}},
		[]Tool{NewTool("read_file", "reads", map[string]any{"path": StringProperty("p")})}) {
		switch chunk.Event {
		case StreamToolCall:
			calls++
		case StreamDone:
			done = true
		}
	}
	if calls != 1 || !done {
		t.Errorf("tool calls = %d done = %v", calls, done)
	}

	gemini := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "chat/completions") {
			t.Errorf("gemini was sent to %s", r.URL.Path)
		}
		fmt.Fprint(w, `{"candidates":[{"content":{"parts":[{"text":"gemini says hi"}]},"finishReason":"STOP"}]}`)
	}))
	defer gemini.Close()
	got, err = testClient(t, "gemini", gemini.URL, nil).CompleteStream(context.Background(),
		[]Message{{Role: "user", Content: "hi"}}, func(string, bool) {})
	if err != nil || got != "gemini says hi" {
		t.Fatalf("gemini: got %q err %v", got, err)
	}

	// A provider that fails to answer fails the stream before it opens.
	down := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer down.Close()
	for chunk := range testClient(t, "anthropic", down.URL, nil).CompleteToolsStream(context.Background(),
		[]Message{{Role: "user", Content: "hi"}}, nil) {
		if chunk.Event != StreamError {
			t.Errorf("event = %v, want an error", chunk.Event)
		}
	}
}
