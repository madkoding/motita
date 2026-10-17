package gateway

import (
	"context"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"
)

// shortBodyTimeout makes bodyIdleTimeout short for one test.
func shortBodyTimeout(t *testing.T, d time.Duration) {
	t.Helper()
	old := bodyIdleTimeout
	bodyIdleTimeout = d
	t.Cleanup(func() { bodyIdleTimeout = old })
}

// TestAStalledBodyWithoutATokenIsDropped: a peer with no token that declares a body and never sends
// it must get its answer and lose the connection, instead of holding it open for as long as it likes.
func TestAStalledBodyWithoutATokenIsDropped(t *testing.T) {
	shortBodyTimeout(t, 200*time.Millisecond)
	srv := newTestServer(t, &fakeService{})

	c, err := net.Dial("tcp", srv.Addr())
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer c.Close()
	path := sessionPath(srv, DefaultSession, "/task")
	if _, err := io.WriteString(c, "POST "+path+" HTTP/1.1\r\nHost: 127.0.0.1\r\nContent-Length: 1000\r\n\r\n"); err != nil {
		t.Fatalf("write: %v", err)
	}
	_ = c.SetReadDeadline(time.Now().Add(5 * time.Second))
	got, err := io.ReadAll(c)
	if err != nil {
		t.Fatalf("the server held the stalled connection open: %v (read %q)", err, got)
	}
	if !strings.Contains(string(got), "401") {
		t.Fatalf("the answer = %q, want a 401", got)
	}
}

// slowBody sends a body in parts, with a pause before every part but the first.
type slowBody struct {
	parts []string
	pause time.Duration
	sent  int
}

func (b *slowBody) Read(p []byte) (int, error) {
	if b.sent == len(b.parts) {
		return 0, io.EOF
	}
	if b.sent > 0 {
		time.Sleep(b.pause)
	}
	n := copy(p, b.parts[b.sent])
	b.sent++
	return n, nil
}

// TestASlowBodyThatKeepsMovingIsNotCutOff: each read of the body moves the deadline on, so an
// upload that takes longer than the timeout in all, but never stalls for that long, still arrives.
func TestASlowBodyThatKeepsMovingIsNotCutOff(t *testing.T) {
	const timeout = 300 * time.Millisecond
	shortBodyTimeout(t, timeout)
	srv := newTestServer(t, &fakeService{task: func(context.Context, string, func(string, ...any)) (string, error) {
		return "finished", nil
	}})

	// No length: the body is sent chunked, one part per chunk.
	body := &slowBody{parts: []string{`{"task":`, `"x"`, `}`}, pause: 2 * timeout / 3}
	req, err := http.NewRequest(http.MethodPost, srv.BaseURL()+sessionPath(srv, DefaultSession, "/task"), body)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+testToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer resp.Body.Close()
	got, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(got), "event: done") {
		t.Fatalf("the slow upload was cut off: %q", got)
	}
}

// TestAStreamOutlivesTheBodyDeadline: the end of the body clears the deadline. Left set, it would
// fire in net/http's background read and cancel the request context of the stream that follows.
func TestAStreamOutlivesTheBodyDeadline(t *testing.T) {
	const timeout = 200 * time.Millisecond
	shortBodyTimeout(t, timeout)
	srv := newTestServer(t, &fakeService{task: func(context.Context, string, func(string, ...any)) (string, error) {
		time.Sleep(3 * timeout)
		return "finished", nil
	}})
	raw := rawStream(t, srv, http.MethodPost, sessionPath(srv, DefaultSession, "/task"), `{"task":"x"}`)
	if !strings.Contains(raw, "event: done") || !strings.Contains(raw, "finished") {
		t.Fatalf("the stream was cut off: %q", raw)
	}
}
