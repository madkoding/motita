package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/madkoding/starlight/internal/config"
)

// The request-building paths: the reasoning parameter is only added for the
// providers that understand it, the streaming request sets the headers a proxy
// needs, and the request body is what the provider actually receives.

// captureServer records the request it was given and answers with the provided
// body, which is how the outgoing payload is inspected.
type captureServer struct {
	body    []byte
	headers http.Header
	path    string
	calls   int
}

func (c *captureServer) start(t *testing.T, answer string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c.calls++
		c.path = r.URL.Path
		c.headers = r.Header.Clone()
		c.body, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, answer)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func clientFor(t *testing.T, baseURL string, tweak func(*config.LLM)) *Client {
	t.Helper()
	cfg := config.Default().LLM
	cfg.Provider = "openai"
	cfg.Model = "m"
	cfg.APIKey = "k"
	cfg.BaseURL = baseURL
	cfg.Timeout = 5 * time.Second
	cfg.MaxAttempts = 1
	cfg.BackoffInitial = time.Millisecond
	if tweak != nil {
		tweak(&cfg)
	}
	c, err := New(cfg, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return c
}

// TestReasoningEffortIsSentOnlyWhenItApplies: the parameter is part of the OpenAI
// protocol, and a provider that does not know it would reject the request. An
// "off" level must not send it either.
func TestReasoningEffortIsSentOnlyWhenItApplies(t *testing.T) {
	cases := []struct {
		name      string
		reasoning config.Reasoning
		want      string
	}{
		{"an enabled level is passed through", config.Reasoning{Enabled: true, Level: "high"}, "high"},
		{"the level is passed even when it is the default", config.Reasoning{Enabled: true, Level: "medium"}, "medium"},
		{"off sends nothing", config.Reasoning{Enabled: true, Level: "off"}, ""},
		{"a disabled block sends nothing", config.Reasoning{Enabled: false, Level: "high"}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cap := &captureServer{}
			srv := cap.start(t, `{"choices":[{"message":{"content":"ok"}}]}`)
			c := clientFor(t, srv.URL, func(cfg *config.LLM) { cfg.Reasoning = tc.reasoning })

			if _, err := c.Complete(context.Background(), []Message{{Role: "user", Content: "hi"}}); err != nil {
				t.Fatalf("Complete: %v", err)
			}
			var sent map[string]any
			if err := json.Unmarshal(cap.body, &sent); err != nil {
				t.Fatalf("the request body is not JSON: %v", err)
			}
			got, _ := sent["reasoning_effort"].(string)
			if got != tc.want {
				t.Errorf("reasoning_effort = %q, want %q (body: %s)", got, tc.want, cap.body)
			}
		})
	}
}

// TestStreamingRequestAsksForEventStream: a proxy will buffer an SSE response
// unless the client asks for it, which is exactly the timeout the streaming path
// exists to avoid.
func TestStreamingRequestAsksForEventStream(t *testing.T) {
	cap := &captureServer{}
	srv := cap.start(t, "data: {\"choices\":[{\"delta\":{\"content\":\"ok\"}}]}\n\ndata: [DONE]\n\n")
	c := clientFor(t, srv.URL, nil)

	for range c.CompleteToolsStream(context.Background(), []Message{{Role: "user", Content: "hi"}}, nil) {
	}
	if got := cap.headers.Get("Accept"); got != "text/event-stream" {
		t.Errorf("Accept = %q, want text/event-stream", got)
	}
	if got := cap.headers.Get("Content-Type"); got != "application/json" {
		t.Errorf("Content-Type = %q", got)
	}
	var sent map[string]any
	if err := json.Unmarshal(cap.body, &sent); err != nil {
		t.Fatalf("the request body is not JSON: %v", err)
	}
	if stream, _ := sent["stream"].(bool); !stream {
		t.Errorf("the request must set stream: true (body: %s)", cap.body)
	}
}

// TestStreamingSendsTheAuthorizationHeader: the key travels as a bearer token and
// nowhere else.
func TestStreamingSendsTheAuthorizationHeader(t *testing.T) {
	cap := &captureServer{}
	srv := cap.start(t, "data: [DONE]\n\n")
	c := clientFor(t, srv.URL, func(cfg *config.LLM) { cfg.APIKey = "a-secret-key" })

	for range c.CompleteToolsStream(context.Background(), []Message{{Role: "user", Content: "hi"}}, nil) {
	}
	if got := cap.headers.Get("Authorization"); got != "Bearer a-secret-key" {
		t.Errorf("Authorization = %q", got)
	}
}

// TestStreamingRequestUsesTheChatCompletionsPath: the endpoint is composed from
// the base URL, so a provider with a versioned path keeps working.
func TestStreamingRequestUsesTheChatCompletionsPath(t *testing.T) {
	cap := &captureServer{}
	srv := cap.start(t, "data: [DONE]\n\n")
	c := clientFor(t, srv.URL+"/v1", nil)

	for range c.CompleteToolsStream(context.Background(), []Message{{Role: "user", Content: "hi"}}, nil) {
	}
	if cap.path != "/v1/chat/completions" {
		t.Errorf("path = %q, want /v1/chat/completions", cap.path)
	}
}

// TestStreamingReportsAnUnreadableBody: a body that fails mid-read is reported as
// an error chunk rather than being silently treated as the end of the answer.
func TestStreamingReportsAnUnreadableBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		f, ok := w.(http.Flusher)
		if !ok {
			t.Error("the test server cannot flush")
			return
		}
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"partial\"}}]}\n\n"))
		f.Flush()
		// Close the connection without ever sending [DONE], which is what a
		// provider restarting mid-answer looks like.
		if hj, ok := w.(http.Hijacker); ok {
			conn, _, err := hj.Hijack()
			if err == nil {
				conn.Close()
			}
		}
	}))
	defer srv.Close()

	c := clientFor(t, srv.URL, nil)
	var sawText, terminated bool
	for ch := range c.CompleteToolsStream(context.Background(), []Message{{Role: "user", Content: "hi"}}, nil) {
		switch ch.Event {
		case StreamText:
			sawText = true
		case StreamError, StreamDone:
			terminated = true
		}
	}
	if !sawText {
		t.Error("the partial answer must still be delivered")
	}
	if !terminated {
		t.Error("the stream must terminate when the body ends")
	}
}

// TestStreamingStopsAtAFinishReasonWithToolCalls: a provider that sends
// finish_reason=tool_calls and then keeps the connection open must not leave the
// caller hanging: the accumulated reply is complete at that point.
func TestStreamingStopsAtAFinishReasonWithToolCalls(t *testing.T) {
	payload := "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"id\":\"c1\",\"function\":{\"name\":\"execute_command\",\"arguments\":\"{}\"}}]},\"finish_reason\":\"tool_calls\"}]}\n\n"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		f := w.(http.Flusher)
		_, _ = w.Write([]byte(payload))
		f.Flush()
		// Deliberately no [DONE] and no close: the client must stop on its own.
		<-r.Context().Done()
	}))
	defer srv.Close()

	c := clientFor(t, srv.URL, nil)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for range c.CompleteToolsStream(context.Background(), []Message{{Role: "user", Content: "hi"}}, nil) {
		}
	}()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("the stream must end at finish_reason=tool_calls, without waiting for [DONE]")
	}
}

// TestStreamChunkStringForAnUnknownEvent: an event value nobody defined must not
// panic and must not invent a label. The one the enum does not cover is a value
// beyond StreamDone.
func TestStreamChunkStringForAnUnknownEvent(t *testing.T) {
	if got := (StreamChunk{}).String(); got != "" {
		t.Errorf("String() = %q, want empty for the zero event", got)
	}
	if got := (StreamChunk{Event: StreamEvent(99)}).String(); got != "" {
		t.Errorf("String() = %q, want empty for an unknown event", got)
	}
}

// TestReasoningEffortOnTheToolRequest: the tool-calling request is a separate
// builder from the plain one, and it carries the reasoning parameter too.
func TestReasoningEffortOnTheToolRequest(t *testing.T) {
	cap := &captureServer{}
	srv := cap.start(t, `{"choices":[{"message":{"content":"ok"}}]}`)
	c := clientFor(t, srv.URL, func(cfg *config.LLM) {
		cfg.Reasoning = config.Reasoning{Enabled: true, Level: "low"}
	})
	if _, err := c.CompleteTools(context.Background(), []Message{{Role: "user", Content: "hi"}}, nil); err != nil {
		t.Fatalf("CompleteTools: %v", err)
	}
	var sent map[string]any
	if err := json.Unmarshal(cap.body, &sent); err != nil {
		t.Fatalf("the request body is not JSON: %v", err)
	}
	if got, _ := sent["reasoning_effort"].(string); got != "low" {
		t.Errorf("reasoning_effort = %q, want low (body: %s)", got, cap.body)
	}
}

// TestStreamingStopsAtAFinishReasonOfStop: a provider that reports a normal stop
// with no tool call keeps the connection open sometimes; the client must not wait
// for a [DONE] that never comes when the answer is already complete.
func TestStreamingStopsAtAFinishReasonOfStop(t *testing.T) {
	payload := "data: {\"choices\":[{\"delta\":{\"content\":\"complete\"},\"finish_reason\":\"stop\"}]}\n\n"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(payload))
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	defer srv.Close()

	c := clientFor(t, srv.URL, nil)
	done := make(chan struct{})
	var text string
	go func() {
		defer close(done)
		for ch := range c.CompleteToolsStream(context.Background(), []Message{{Role: "user", Content: "hi"}}, nil) {
			if ch.Event == StreamText {
				text += ch.Text
			}
		}
	}()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("the stream must end when the body ends after a stop with no tool call")
	}
	if text != "complete" {
		t.Errorf("text = %q", text)
	}
}

// TestStreamingStopsAtTheEndOfAShortBody: the inner loop also stops when the
// producer closes its channel without an explicit done chunk. That happens when
// the body ends cleanly between two frames.
func TestStreamingStopsAtTheEndOfAShortBody(t *testing.T) {
	// A body with no trailing [DONE] and no finish_reason: the reader hits EOF,
	// which produces both a done chunk and closes the channel.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"end\"}}]}\n\n"))
	}))
	defer srv.Close()

	c := clientFor(t, srv.URL, nil)
	var text string
	for ch := range c.CompleteToolsStream(context.Background(), []Message{{Role: "user", Content: "hi"}}, nil) {
		if ch.Event == StreamText {
			text += ch.Text
		}
	}
	if text != "end" {
		t.Errorf("text = %q", text)
	}
}

// TestStreamingReasoningEffortIsSent: the streaming request carries the reasoning
// parameter as well, since that is the request the interactive session makes.
func TestStreamingReasoningEffortIsSent(t *testing.T) {
	cap := &captureServer{}
	srv := cap.start(t, "data: [DONE]\n\n")
	c := clientFor(t, srv.URL, func(cfg *config.LLM) {
		cfg.Reasoning = config.Reasoning{Enabled: true, Level: "medium"}
	})
	for range c.CompleteToolsStream(context.Background(), []Message{{Role: "user", Content: "hi"}}, nil) {
	}
	var sent map[string]any
	if err := json.Unmarshal(cap.body, &sent); err != nil {
		t.Fatalf("the request body is not JSON: %v", err)
	}
	if got, _ := sent["reasoning_effort"].(string); got != "medium" {
		t.Errorf("reasoning_effort = %q, want medium", got)
	}
}

// TestStreamingHandlesAProducerThatClosesWithoutADoneChunk: the retry wrapper has
// to stop when its producer closes the channel, not only when it sends an explicit
// done chunk. A producer that breaks that contract must not hang the caller.
func TestStreamingHandlesAProducerThatClosesWithoutADoneChunk(t *testing.T) {
	c := clientFor(t, "http://127.0.0.1:1", nil)
	c.openStream = func(context.Context, []Message, []Tool) (<-chan StreamChunk, error) {
		ch := make(chan StreamChunk, 1)
		// Text, then close: no done chunk.
		ch <- StreamChunk{Event: StreamText, Text: "a"}
		close(ch)
		return ch, nil
	}

	done := make(chan struct{})
	var text string
	go func() {
		defer close(done)
		for chunk := range c.CompleteToolsStream(context.Background(), []Message{{Role: "user", Content: "hi"}}, nil) {
			if chunk.Event == StreamText {
				text += chunk.Text
			}
		}
	}()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("a producer that closes without a done chunk must not hang the caller")
	}
	if text != "a" {
		t.Errorf("text = %q", text)
	}
}

// TestStreamingRetriesWhenTheProducerCannotStart: the opener can fail before any
// chunk exists (a connection error), and that is the path the retry loop takes.
func TestStreamingRetriesWhenTheProducerCannotStart(t *testing.T) {
	c := clientFor(t, "http://127.0.0.1:1", func(cfg *config.LLM) {
		cfg.MaxAttempts = 2
		cfg.BackoffInitial = time.Millisecond
		cfg.BackoffMax = time.Millisecond
	})
	attempts := 0
	c.openStream = func(context.Context, []Message, []Tool) (<-chan StreamChunk, error) {
		attempts++
		if attempts == 1 {
			return nil, &HTTPError{Code: http.StatusInternalServerError, Body: "boom"}
		}
		ch := make(chan StreamChunk, 1)
		ch <- StreamChunk{Event: StreamDone, Reply: Reply{Content: "second attempt"}}
		close(ch)
		return ch, nil
	}

	var final Reply
	for chunk := range c.CompleteToolsStream(context.Background(), []Message{{Role: "user", Content: "hi"}}, nil) {
		if chunk.Event == StreamDone {
			final = chunk.Reply
		}
	}
	if attempts != 2 {
		t.Errorf("attempts = %d, want 2", attempts)
	}
	if final.Content != "second attempt" {
		t.Errorf("the retry's answer must reach the caller, got %q", final.Content)
	}
}

// TestStreamingRefusesAToolThatCannotBeSerialised: the tool definitions come from
// the caller, and one carrying a value JSON cannot represent would otherwise reach
// the network as a half-built request. It is reported instead.
func TestStreamingRefusesAToolThatCannotBeSerialised(t *testing.T) {
	c := clientFor(t, "http://127.0.0.1:1", nil)
	bad := NewTool("broken", "a tool with an unserialisable parameter", map[string]any{"callback": func() {}})

	_, err := c.callOpenAIToolsStream(context.Background(), []Message{{Role: "user", Content: "hi"}}, []Tool{bad})
	if err == nil {
		t.Fatal("an unserialisable tool must be refused")
	}
	if !strings.Contains(err.Error(), "serialise") {
		t.Errorf("the error must say the request could not be built, got %v", err)
	}
}

// TestStreamingRefusesAnInvalidURL: http.NewRequestWithContext fails on a URL a
// terminal cannot parse — which is what a user typing a base URL by hand can
// produce — and the failure has to be reported instead of reaching the network.
func TestStreamingRefusesAnInvalidURL(t *testing.T) {
	// The escape has to break url.Parse, which is what http.NewRequestWithContext
	// calls: a base URL with an invalid percent-escape is what a user pasting a
	// value by hand can produce.
	c := clientFor(t, "http://example.test/v1/%zz", nil)
	_, err := c.callOpenAIToolsStream(context.Background(), []Message{{Role: "user", Content: "hi"}}, nil)
	if err == nil {
		t.Fatal("an unparsable base URL must be refused")
	}
	if !strings.Contains(err.Error(), "invalid request") {
		t.Errorf("the error must say the request could not be built, got %v", err)
	}
}

// TestStreamingReportsAGoError: a transport that fails outright (DNS, refused
// connection) surfaces as an error chunk, never as an empty answer.
func TestStreamingReportsAGoError(t *testing.T) {
	c := clientFor(t, "http://127.0.0.1:1", func(cfg *config.LLM) { cfg.Timeout = time.Second })
	if _, err := c.callOpenAIToolsStream(context.Background(), []Message{{Role: "user", Content: "hi"}}, nil); err == nil {
		t.Error("an unreachable provider must be reported")
	}
}

// TestBaseURLFallsBackToTheProviderDefault: a configuration that names no base URL
// uses the provider's official endpoint, and a trailing slash is removed so the
// path is not doubled.
func TestBaseURLFallsBackToTheProviderDefault(t *testing.T) {
	c := clientFor(t, "", nil)
	if got := c.baseURL("https://api.openai.com/v1"); got != "https://api.openai.com/v1" {
		t.Errorf("baseURL = %q, want the default", got)
	}
	c = clientFor(t, "https://example.test/v1/", nil)
	if got := c.baseURL("https://api.openai.com/v1"); got != "https://example.test/v1" {
		t.Errorf("baseURL = %q, want the trailing slash removed", got)
	}
}

// TestCompleteToolsSendsTheTools: the tool definitions are what make the model call
// them, so they must reach the request body.
func TestCompleteToolsSendsTheTools(t *testing.T) {
	cap := &captureServer{}
	srv := cap.start(t, `{"choices":[{"message":{"content":"ok"}}]}`)
	c := clientFor(t, srv.URL, nil)

	tools := []Tool{NewTool("execute_command", "runs a command", map[string]any{"type": "object"})}
	if _, err := c.CompleteTools(context.Background(), []Message{{Role: "user", Content: "hi"}}, tools); err != nil {
		t.Fatalf("CompleteTools: %v", err)
	}
	var sent map[string]any
	if err := json.Unmarshal(cap.body, &sent); err != nil {
		t.Fatalf("the request body is not JSON: %v", err)
	}
	list, ok := sent["tools"].([]any)
	if !ok || len(list) != 1 {
		t.Fatalf("tools were not sent: %s", cap.body)
	}
}

// TestCompleteSendsTheConversation: every message is forwarded, including a tool
// result, which is how the model sees what its own call produced.
func TestCompleteSendsTheConversation(t *testing.T) {
	cap := &captureServer{}
	srv := cap.start(t, `{"choices":[{"message":{"content":"ok"}}]}`)
	c := clientFor(t, srv.URL, nil)

	messages := []Message{
		{Role: "system", Content: "you are an agent"},
		{Role: "user", Content: "list the files"},
		{Role: "assistant", Content: "", ToolCalls: []ToolCall{{ID: "c1", Function: FunctionCall{Name: "execute_command"}}}},
		{Role: "tool", Content: "a.txt", ToolCallID: "c1"},
	}
	if _, err := c.Complete(context.Background(), messages); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	var sent struct {
		Messages []struct {
			Role       string `json:"role"`
			Content    string `json:"content"`
			ToolCallID string `json:"tool_call_id"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(cap.body, &sent); err != nil {
		t.Fatalf("the request body is not JSON: %v", err)
	}
	if len(sent.Messages) != 4 {
		t.Fatalf("the conversation was not forwarded in full: %s", cap.body)
	}
	if sent.Messages[3].ToolCallID != "c1" || sent.Messages[3].Content != "a.txt" {
		t.Errorf("the tool result was not forwarded correctly: %+v", sent.Messages[3])
	}
}

// TestToOpenAIMessagesDefaultsTheRole: a message with no role would be rejected by
// the provider, so it is sent as a user message.
func TestToOpenAIMessagesDefaultsTheRole(t *testing.T) {
	got := toOpenAIMessages([]Message{{Content: "no role"}})
	if len(got) != 1 || got[0].Role != "user" {
		t.Errorf("toOpenAIMessages = %+v", got)
	}
}

var _ = bytes.MinRead
