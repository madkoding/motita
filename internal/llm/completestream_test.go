package llm

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
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

// TestCompleteStreamReadsDataLinesWithNoSpace: the space after "data:" is optional in SSE. A
// server that leaves it out is still streamed, and not asked a second time without streaming.
func TestCompleteStreamReadsDataLinesWithNoSpace(t *testing.T) {
	var stream, plain int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b := make([]byte, r.ContentLength)
		r.Body.Read(b)
		if strings.Contains(string(b), `"stream":true`) {
			stream++
			fmt.Fprint(w, "data:{\"choices\":[{\"delta\":{\"content\":\"hi\"},\"finish_reason\":\"stop\"}]}\n\ndata:[DONE]\n\n")
			return
		}
		plain++
		fmt.Fprint(w, `{"choices":[{"message":{"content":"hi"}}]}`)
	}))
	defer srv.Close()
	var deltas int
	got, err := streamClient(t, srv.URL).CompleteStream(context.Background(),
		[]Message{{Role: "user", Content: "hi"}}, func(string, bool) { deltas++ })
	if err != nil {
		t.Fatal(err)
	}
	if got != "hi" || deltas != 1 || stream != 1 || plain != 0 {
		t.Errorf("got %q err %v deltas %d stream %d plain %d", got, err, deltas, stream, plain)
	}
}

// TestLLMTimeoutBoundsEachGapOfAStream: llm.timeout bounds the wait for the headers and each gap
// between two pieces of a stream, not the whole answer. A long answer that keeps coming is never
// cut, and a stream that goes silent fails as a timeout, which is worth retrying.
func TestLLMTimeoutBoundsEachGapOfAStream(t *testing.T) {
	const timeout = 300 * time.Millisecond
	streamText := func(srv *httptest.Server) (string, error) {
		c := streamClient(t, srv.URL)
		c.cfg.Timeout, c.http.Timeout = timeout, timeout
		c.cfg.TransientBudget = 0 // a timeout here is the case under test, not something to wait out
		var text strings.Builder
		for chunk := range c.CompleteToolsStream(context.Background(), []Message{{Role: "user", Content: "hi"}}, nil) {
			switch chunk.Event {
			case StreamText:
				text.WriteString(chunk.Text)
			case StreamError:
				return text.String(), chunk.Error
			default:
				// The end and the other events do not matter here.
			}
		}
		return text.String(), nil
	}
	// A server sees the client leave only once it has read the request.
	silent := func(r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		<-r.Context().Done()
	}

	long := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		for range 12 {
			fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"x\"}}]}\n\n")
			w.(http.Flusher).Flush()
			time.Sleep(timeout / 6)
		}
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer long.Close()
	if got, err := streamText(long); err != nil || got != strings.Repeat("x", 12) {
		t.Errorf("a stream longer than the timeout: got %q err %v", got, err)
	}

	stalled := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"x\"}}]}\n\n")
		w.(http.Flusher).Flush()
		silent(r)
	}))
	defer stalled.Close()
	start := time.Now()
	got, err := streamText(stalled)
	if got != "x" || !errors.Is(err, context.DeadlineExceeded) || time.Since(start) > 3*time.Second {
		t.Errorf("a silent stream: got %q err %v after %v", got, err, time.Since(start))
	}

	mute := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { silent(r) }))
	defer mute.Close()
	start = time.Now()
	if _, err := streamText(mute); !errors.As(err, new(*netFailure)) || time.Since(start) > 3*time.Second {
		t.Errorf("a server that never answers: err %v after %v", err, time.Since(start))
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
// fakeAnthropicHandler answers on Anthropic's own API: a tool call when tools are offered,
// a plain text otherwise.
func fakeAnthropicHandler(t *testing.T) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "chat/completions") {
			t.Errorf("anthropic was sent to %s", r.URL.Path)
		}
		b := make([]byte, r.ContentLength)
		r.Body.Read(b)
		if strings.Contains(string(b), `"tools"`) {
			fmt.Fprint(
				w,
				`{"content":[{"type":"text","text":"calling"},{"type":"tool_use","id":"t1","name":"read_file",`+
					`"input":{"path":"a"}}],"stop_reason":"tool_use"}`,
			)
			return
		}
		fmt.Fprint(w, `{"content":[{"type":"text","text":"anthropic says hi"}]}`)
	}
}

func TestAnthropicAndGeminiStreamThroughTheirOwnAPI(t *testing.T) {
	anthropic := httptest.NewServer(fakeAnthropicHandler(t))
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
		default:
			// Other events do not matter here.
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
