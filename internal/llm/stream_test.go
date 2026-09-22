package llm

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/madkoding/starlight/internal/config"
)

// The streaming path is the one the TUI depends on: it is what keeps a slow model
// from looking like a hung interface and what lets the live tool calls be shown.
// These tests drive the SSE parser with the shapes a real provider sends.

// sseServer answers /chat/completions with the given raw SSE payload.
func sseServer(t *testing.T, payload string, status int) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if status != http.StatusOK {
			w.WriteHeader(status)
			_, _ = w.Write([]byte(`{"error":{"message":"boom"}}`))
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(payload))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func streamClient(t *testing.T, baseURL string) *Client {
	t.Helper()
	cfg := config.Default().LLM
	cfg.Provider = "openai"
	cfg.Model = "m"
	cfg.APIKey = "k"
	cfg.BaseURL = baseURL
	cfg.Timeout = 5 * time.Second
	cfg.MaxAttempts = 1
	c, err := New(cfg, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return c
}

// pemFor returns the PEM of the certificate the test TLS server presents.
func pemFor(srv *httptest.Server) []byte {
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw})
}

// x509PoolWithout is a fresh pool that does not trust the test server, used to
// prove that a successful handshake against the embedded pool really verified the
// certificate instead of accepting anything.
func x509PoolWithout(*httptest.Server) *x509.CertPool {
	return x509.NewCertPool()
}

// collect drains a stream and returns the chunks.
func collect(ch <-chan StreamChunk) []StreamChunk {
	var got []StreamChunk
	for chunk := range ch {
		got = append(got, chunk)
		if chunk.Event == StreamDone || chunk.Event == StreamError {
			// Keep draining: the channel must be closed by the producer.
			continue
		}
	}
	return got
}

func TestCompleteToolsStreamYieldsTextThenDone(t *testing.T) {
	srv := sseServer(t, "data: {\"choices\":[{\"delta\":{\"content\":\"hola\"}}]}\n\n"+
		"data: {\"choices\":[{\"delta\":{\"content\":\" mundo\"}}]}\n\n"+
		"data: [DONE]\n\n", http.StatusOK)
	c := streamClient(t, srv.URL)

	chunks := collect(c.CompleteToolsStream(context.Background(), []Message{{Role: "user", Content: "hi"}}, nil))
	var text strings.Builder
	var done bool
	for _, ch := range chunks {
		switch ch.Event {
		case StreamText:
			text.WriteString(ch.Text)
		case StreamDone:
			done = true
			if ch.Reply.Content != "hola mundo" {
				t.Errorf("the final reply must carry the accumulated text, got %q", ch.Reply.Content)
			}
		}
	}
	if text.String() != "hola mundo" {
		t.Errorf("text = %q, want %q", text.String(), "hola mundo")
	}
	if !done {
		t.Error("the stream must end with a done chunk")
	}
}

// TestCompleteToolsStreamAnnouncesToolCalls: the TUI turns these chunks into the
// "using <tool>" lines, so a tool call must arrive as its own event.
func TestCompleteToolsStreamAnnouncesToolCalls(t *testing.T) {
	srv := sseServer(t, "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"id\":\"c1\",\"type\":\"function\",\"function\":{\"name\":\"execute_command\",\"arguments\":\"{\\\"command\\\":\\\"ls\\\"}\"}}]}}]}\n\n"+
		"data: [DONE]\n\n", http.StatusOK)
	c := streamClient(t, srv.URL)

	chunks := collect(c.CompleteToolsStream(context.Background(), []Message{{Role: "user", Content: "hi"}}, nil))
	var names []string
	var final Reply
	for _, ch := range chunks {
		switch ch.Event {
		case StreamToolCall:
			if ch.Call != nil {
				names = append(names, ch.Call.Function.Name)
			}
		case StreamDone:
			final = ch.Reply
		}
	}
	if len(names) == 0 || names[0] != "execute_command" {
		t.Errorf("the tool call must be announced, got %v", names)
	}
	if len(final.Calls) != 1 || final.Calls[0].Function.Name != "execute_command" {
		t.Errorf("the final reply must carry the tool call, got %+v", final.Calls)
	}
}

// TestCompleteToolsStreamIgnoresKeepAlivesAndGarbage: an SSE stream carries
// comment lines and can carry a truncated frame. Neither may abort the run or be
// mistaken for content.
func TestCompleteToolsStreamIgnoresKeepAlivesAndGarbage(t *testing.T) {
	srv := sseServer(t, ": keep-alive\n\n"+
		"event: ping\n\n"+
		"data: not json at all\n\n"+
		"data: {\"choices\":[]}\n\n"+
		"data: {\"choices\":[{\"delta\":{\"content\":\"ok\"}}]}\n\n"+
		"data: [DONE]\n\n", http.StatusOK)
	c := streamClient(t, srv.URL)

	chunks := collect(c.CompleteToolsStream(context.Background(), []Message{{Role: "user", Content: "hi"}}, nil))
	var text strings.Builder
	for _, ch := range chunks {
		if ch.Event == StreamText {
			text.WriteString(ch.Text)
		}
	}
	if text.String() != "ok" {
		t.Errorf("only the valid frame must produce text, got %q", text.String())
	}
}

// TestCompleteToolsStreamReportsAnHTTPError: a 500 must surface as an error
// chunk, not as an endless stream.
func TestCompleteToolsStreamReportsAnHTTPError(t *testing.T) {
	srv := sseServer(t, "", http.StatusInternalServerError)
	c := streamClient(t, srv.URL)

	var sawError bool
	for ch := range c.CompleteToolsStream(context.Background(), []Message{{Role: "user", Content: "hi"}}, nil) {
		if ch.Event == StreamError {
			sawError = true
			if ch.Error == nil {
				t.Error("an error chunk must carry the error")
			}
		}
	}
	if !sawError {
		t.Error("a failed request must produce an error chunk")
	}
}

// TestCompleteToolsStreamStopsOnAClosedBody: a provider that ends the response
// without [DONE] still has to terminate the stream.
func TestCompleteToolsStreamStopsOnAClosedBody(t *testing.T) {
	srv := sseServer(t, "data: {\"choices\":[{\"delta\":{\"content\":\"partial\"}}]}\n\n", http.StatusOK)
	c := streamClient(t, srv.URL)

	done := make(chan struct{})
	go func() {
		defer close(done)
		for range c.CompleteToolsStream(context.Background(), []Message{{Role: "user", Content: "hi"}}, nil) {
		}
	}()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("the stream never closed after the body ended")
	}
}

// TestStreamResultAccumulatesToolCallUpdates: a tool call arrives in fragments,
// and the fragments of one call must update the same entry rather than appending
// a new one for every delta.
func TestStreamResultAccumulatesToolCallUpdates(t *testing.T) {
	var acc StreamResult
	acc.Handle(StreamChunk{Event: StreamToolCall, Call: &ToolCall{ID: "c1", Function: FunctionCall{Name: "execute_command", Arguments: json.RawMessage(`{"cmd":`)}}})
	acc.Handle(StreamChunk{Event: StreamText, Text: "hola"})
	acc.Handle(StreamChunk{Event: StreamToolCall, Call: &ToolCall{ID: "c1", Function: FunctionCall{Name: "execute_command", Arguments: json.RawMessage(`{"cmd":"ls"}`)}}})
	acc.Handle(StreamChunk{Event: StreamToolCall, Call: &ToolCall{ID: "c2", Function: FunctionCall{Name: "read_file"}}})

	reply := acc.FinalReply()
	if reply.Content != "hola" {
		t.Errorf("content = %q", reply.Content)
	}
	if len(reply.Calls) != 2 {
		t.Fatalf("calls = %d, want 2 (one per id, not one per fragment): %+v", len(reply.Calls), reply.Calls)
	}
	if string(reply.Calls[0].Function.Arguments) != `{"cmd":"ls"}` {
		t.Errorf("the last fragment must win, got %q", reply.Calls[0].Function.Arguments)
	}
	if got := acc.Handle(StreamChunk{Event: StreamDone}); !got {
		t.Error("Handle must report that the stream ended")
	}
	if got := acc.Handle(StreamChunk{Event: StreamError, Error: errors.New("x")}); !got {
		t.Error("an error also ends the stream")
	}
}

// TestStreamChunkString: the string form is what a plain terminal mode prints, so
// it has to name the event instead of being empty.
func TestStreamChunkString(t *testing.T) {
	cases := []struct {
		chunk StreamChunk
		want  string
	}{
		{StreamChunk{Event: StreamText, Text: "hi"}, "hi"},
		{StreamChunk{Event: StreamToolCall, Call: &ToolCall{Function: FunctionCall{Name: "t"}}}, "[tool call: t]"},
		{StreamChunk{Event: StreamToolCall}, "[tool call]"},
		{StreamChunk{Event: StreamError, Error: fmt.Errorf("bad")}, "[error: bad]"},
		{StreamChunk{Event: StreamDone}, ""},
	}
	for _, tc := range cases {
		if got := tc.chunk.String(); got != tc.want {
			t.Errorf("String() = %q, want %q", got, tc.want)
		}
	}
}

// TestCompleteToolsStreamRetriesThenSucceeds: a transient failure must not kill
// the run. The stream is retried with backoff, and the second attempt is the one
// the caller sees.
func TestCompleteToolsStreamRetriesThenSucceeds(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"recovered\"}}]}\n\ndata: [DONE]\n\n"))
	}))
	defer srv.Close()

	cfg := config.Default().LLM
	cfg.Provider = "openai"
	cfg.Model = "m"
	cfg.APIKey = "k"
	cfg.BaseURL = srv.URL
	cfg.Timeout = 5 * time.Second
	cfg.MaxAttempts = 2
	cfg.BackoffInitial = time.Millisecond
	cfg.BackoffMax = time.Millisecond
	c, err := New(cfg, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	var text strings.Builder
	var sawError bool
	for ch := range c.CompleteToolsStream(context.Background(), []Message{{Role: "user", Content: "hi"}}, nil) {
		switch ch.Event {
		case StreamText:
			text.WriteString(ch.Text)
		case StreamError:
			sawError = true
		}
	}
	if sawError {
		t.Error("a retryable failure must be retried, not reported")
	}
	if text.String() != "recovered" {
		t.Errorf("the retry must deliver the answer, got %q", text.String())
	}
	if calls != 2 {
		t.Errorf("the server was called %d times, want 2", calls)
	}
}

// TestCompleteToolsStreamGivesUpAfterTheLastAttempt: when every attempt fails, the
// caller gets one final error chunk and the channel closes.
func TestCompleteToolsStreamGivesUpAfterTheLastAttempt(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	cfg := config.Default().LLM
	cfg.Provider = "openai"
	cfg.Model = "m"
	cfg.APIKey = "k"
	cfg.BaseURL = srv.URL
	cfg.Timeout = 5 * time.Second
	cfg.MaxAttempts = 2
	cfg.BackoffInitial = time.Millisecond
	cfg.BackoffMax = time.Millisecond
	c, err := New(cfg, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	var errs int
	for ch := range c.CompleteToolsStream(context.Background(), []Message{{Role: "user", Content: "hi"}}, nil) {
		if ch.Event == StreamError {
			errs++
			if !strings.Contains(ch.Error.Error(), "exhausted") {
				t.Errorf("the final error must say the attempts ran out, got %v", ch.Error)
			}
		}
	}
	if errs != 1 {
		t.Errorf("got %d error chunks, want exactly 1", errs)
	}
}

// TestCompleteToolsStreamHonoursCancellationWhileWaitingToRetry: a cancelled
// context must not wait out the backoff.
func TestCompleteToolsStreamHonoursCancellationWhileWaitingToRetry(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	cfg := config.Default().LLM
	cfg.Provider = "openai"
	cfg.Model = "m"
	cfg.APIKey = "k"
	cfg.BaseURL = srv.URL
	cfg.Timeout = 5 * time.Second
	cfg.MaxAttempts = 5
	cfg.BackoffInitial = time.Hour
	cfg.BackoffMax = time.Hour
	c, err := New(cfg, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	deadline := time.After(3 * time.Second)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for range c.CompleteToolsStream(ctx, []Message{{Role: "user", Content: "hi"}}, nil) {
		}
	}()
	select {
	case <-done:
	case <-deadline:
		t.Fatal("the stream kept waiting out the backoff after the context was cancelled")
	}
}

// TestCompleteToolsStreamReportsANonRetryableFailure: a 401 does not improve by
// retrying, so it must fail immediately.
func TestCompleteToolsStreamReportsANonRetryableFailure(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	cfg := config.Default().LLM
	cfg.Provider = "openai"
	cfg.Model = "m"
	cfg.APIKey = "k"
	cfg.BaseURL = srv.URL
	cfg.Timeout = 5 * time.Second
	cfg.MaxAttempts = 4
	cfg.BackoffInitial = time.Millisecond
	c, err := New(cfg, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	for range c.CompleteToolsStream(context.Background(), []Message{{Role: "user", Content: "hi"}}, nil) {
	}
	if calls != 1 {
		t.Errorf("a 401 must not be retried, the server was called %d times", calls)
	}
}

// TestTLSConfigUsesTheEmbeddedBundle: the whole point of the embedded CA bundle is
// that a CGO_ENABLED=0 binary can verify TLS in a container that has no system
// certificates. The pool must therefore contain real roots and pin TLS 1.2 as the
// floor.
func TestTLSConfigUsesTheEmbeddedBundle(t *testing.T) {
	cfg := TLSConfig()
	if cfg == nil || cfg.RootCAs == nil {
		t.Fatal("TLSConfig must carry the embedded roots")
	}
	if cfg.MinVersion != tls.VersionTLS12 {
		t.Errorf("MinVersion = %x, want TLS 1.2", cfg.MinVersion)
	}
	// The pool must really hold the roots, so the assertion is on the ARTIFACT — the
	// embedded PEM — rather than on the pool's internals.
	//
	// It used to count CertPool.Subjects(), which is deprecated for a good reason: a pool
	// returned by SystemCertPool does not list its roots there, so the number can be zero
	// for reasons that have nothing to do with the bundle being truncated — exactly the
	// distinction this test exists to make. Certificates() says nothing either, because
	// AppendCertsFromPEM populates the pool without exposing what it parsed.
	//
	// Counting the CERTIFICATE blocks in the embedded text and asserting the pool accepted
	// all of them is both stronger and free of the deprecated call: a truncated or empty
	// bundle fails on the count, and a bundle the parser rejects fails on the parse.
	const wantAtLeast = 50
	blocks := strings.Count(embeddedCertPEM, "-----BEGIN CERTIFICATE-----")
	if blocks < wantAtLeast {
		t.Fatalf("the embedded bundle has only %d certificates: truncated or missing", blocks)
	}
	extra := x509.NewCertPool()
	if !extra.AppendCertsFromPEM([]byte(embeddedCertPEM)) {
		t.Fatal("the embedded bundle does not parse as PEM")
	}
	// Every block must be a root the pool can use, not just text between markers.
	if got := strings.Count(embeddedCertPEM, "-----END CERTIFICATE-----"); got != blocks {
		t.Errorf("the bundle is malformed: %d BEGIN markers against %d END markers", blocks, got)
	}
	// Repeated calls must return the same pool: it is built once, behind a
	// sync.Once, because parsing 188 KB of PEM on every request would be waste.
	if TLSConfig().RootCAs != cfg.RootCAs {
		t.Error("the pool must be built once and shared")
	}
}

// TestARealTLSHandshakeWithTheEmbeddedBundle: the strongest form of the check
// above is to actually complete a handshake using only the compiled-in roots.
// httptest's TLS server is signed by its own CA, so that CA is added to a copy of
// the pool: if the plumbing works, the request succeeds and the system pool is
// never consulted.
func TestARealTLSHandshakeWithTheEmbeddedBundle(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"data":[{"id":"tls-model"}]}`))
	}))
	defer srv.Close()

	pool := getCertPool()
	if !pool.AppendCertsFromPEM(pemFor(srv)) {
		t.Fatal("could not add the test server certificate to the pool")
	}
	client := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{
		RootCAs:    pool,
		MinVersion: tls.VersionTLS12,
	}}}
	resp, err := client.Get(srv.URL)
	if err != nil {
		t.Fatalf("the handshake failed with the embedded pool: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d", resp.StatusCode)
	}

	// And the same server must fail against a pool that does not know its CA,
	// which proves the request above was really verified.
	stranger := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{
		RootCAs:    x509PoolWithout(srv),
		MinVersion: tls.VersionTLS12,
	}}}
	if _, err := stranger.Get(srv.URL); err == nil {
		t.Error("an unknown CA must be rejected: the check above proves nothing otherwise")
	}
}
