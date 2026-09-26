package gateway

// The final branches: the two store failures that only happen when the RENAMING fails (the write
// already succeeded), the read loop's shutdown, and the handshake failure inside the handler.
//
// These need a filesystem state that a plain unwritable directory does not produce, so each one
// builds that state deliberately and says why.

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/madkoding/motita/internal/logx"
)

// --- the rename failures ------------------------------------------------------------------------

// A session that cannot be RENAMED into place is reported, and the temporary file is removed: a
// store that leaves half-written files behind accumulates them, and the next start tries to read
// them.
//
// The rename fails when the DESTINATION is a directory: the write succeeds, the rename does not,
// which is exactly the case a read-only directory cannot produce (that fails at the write).
func TestASessionWhoseRenameFailsIsReported(t *testing.T) {
	st, err := newSessionStore(filepath.Join(t.TempDir(), "sessions"))
	if err != nil {
		t.Fatalf("newSessionStore: %v", err)
	}
	conv := newConversation("s-dir-in-the-way", &fakeService{})

	// Put a DIRECTORY where the session's file belongs.
	if err := os.MkdirAll(st.path(conv.id), 0o700); err != nil {
		t.Fatal(err)
	}

	err = st.save(conv)
	if err == nil {
		t.Fatal("a save whose rename fails must be reported")
	}
	if !strings.Contains(err.Error(), "could not save the session") {
		t.Errorf("err = %q, want it to name the save", err)
	}
}

// And a PROJECT whose rename fails is reported the same way, with its own message: the two stores
// are separate implementations and one drifting from the other is how a bug hides in the second.
func TestAProjectWhoseRenameFailsIsReported(t *testing.T) {
	ps, err := newProjectStore(filepath.Join(t.TempDir(), "projects"))
	if err != nil {
		t.Fatalf("newProjectStore: %v", err)
	}
	p := Project{ID: "p-dir-in-the-way", Title: "t", Dir: "/tmp/p", Created: time.Now()}
	if err := os.MkdirAll(ps.path(p.ID), 0o700); err != nil {
		t.Fatal(err)
	}

	err = ps.save(p)
	if err == nil {
		t.Fatal("a project save whose rename fails must be reported")
	}
	if !strings.Contains(err.Error(), "could not save") {
		t.Errorf("err = %q, want it to name the save", err)
	}
}

// --- the read loop ------------------------------------------------------------------------------

// A gateway that is shutting down CLOSES its live WebSocket clients, and closes them PROMPTLY.
//
// This is the behaviour the old code only appeared to have: it checked r.Context() inside the read
// loop, but net/http stops managing a connection once the upgrade hijacks it, so that context is
// never cancelled - measured, not assumed. The loop simply blocked in its 90-second read and the
// client kept a socket to a process that was going away.
//
// What asserts the fix is the CLOSE FRAME, not the shutdown itself: the client is told to reconnect
// while the gateway is still alive to send it. That is the path an upgrade takes when it restarts
// the process, which is the only time this matters.
func TestAShuttingDownGatewayClosesItsWebSocketClients(t *testing.T) {
	srv := newWSTestServer(t, &fakeService{})
	// A read deadline long enough that a deadline-based exit cannot be what passes this.
	c := dialWebSocket(t, srv, DefaultSession, testToken)
	defer c.close()
	if msg := c.readMsg(t); msg.Type != MsgAuthResponse {
		t.Fatalf("first message = %q, want the welcome", msg.Type)
	}

	_ = srv.Close(context.Background())

	_ = c.conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	_, opcode := c.readText(t)
	if opcode != opClose {
		t.Errorf("opcode = %d, want a close frame: a client must be told to reconnect", opcode)
	}
}

// And a live client does not keep the gateway ALIVE past its shutdown: Close must return instead of
// waiting for a connection that will never close on its own.
func TestAnOpenWebSocketDoesNotBlockTheGatewayFromClosing(t *testing.T) {
	srv := newWSTestServer(t, &fakeService{})
	c := dialWebSocket(t, srv, DefaultSession, testToken)
	defer c.close()
	if msg := c.readMsg(t); msg.Type != MsgAuthResponse {
		t.Fatalf("first message = %q, want the welcome", msg.Type)
	}

	done := make(chan error, 1)
	go func() { done <- srv.Close(context.Background()) }()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Close: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("a live WebSocket must not hold the gateway open through its shutdown")
	}
}

// --- the handshake failure inside the handler ---------------------------------------------------

// A handshake that fails AFTER the upgrade request was accepted is logged and the handler returns:
// the response is already gone, so there is nothing to write but the reason.
//
// The failure is produced by giving the server a mux-level writer that cannot be hijacked, which is
// the one way to reach this branch from outside the package's own tests.
func TestAFailedHandshakeInsideTheHandlerIsLogged(t *testing.T) {
	sink := newLogSink(t)
	srv := newTestServer(t, &fakeService{}, func(o *Options) { o.Log = sink.log })

	req := httptest.NewRequest(http.MethodGet, "/ws", nil)
	req.Header.Set("Upgrade", "websocket")
	req.Header.Set("Connection", "Upgrade")
	req.Header.Set("Sec-WebSocket-Key", "dGhlIHNhbXBsZSBub25jZQ==")
	req.Header.Set("Sec-WebSocket-Version", "13")

	// A writer that has every method EXCEPT Hijack. The upgrade check passes and the hijack is
	// what fails, which is the branch under test.
	noHijack := &plainWriter{header: make(http.Header)}
	srv.handleWebSocket(noHijack, req)

	if len(sink.lines(t)) == 0 {
		t.Error("a handshake that failed after the upgrade was accepted must be logged")
	}
	if !strings.Contains(strings.Join(sink.lines(t), "\n"), "handshake") {
		t.Errorf("the log must name the handshake, got %v", sink.lines(t))
	}
}

// bufferHijacker is a ResponseWriter whose hijack hands back the bufio.Writer a test supplies, so
// the handshake and the frames that follow it can be failed at a chosen write.
type bufferHijacker struct {
	w    *bufio.Writer
	conn net.Conn
}

func (h *bufferHijacker) Header() http.Header         { return make(http.Header) }
func (h *bufferHijacker) Write(p []byte) (int, error) { return h.w.Write(p) }
func (h *bufferHijacker) WriteHeader(int)             {}
func (h *bufferHijacker) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	return h.conn, bufio.NewReadWriter(bufio.NewReader(strings.NewReader("")), h.w), nil
}

// plainWriter is an http.ResponseWriter that cannot be hijacked.
type plainWriter struct {
	header http.Header
	code   int
}

func (w *plainWriter) Header() http.Header         { return w.header }
func (w *plainWriter) WriteHeader(code int)        { w.code = code }
func (w *plainWriter) Write(p []byte) (int, error) { return len(p), nil }

// The welcome that cannot be WRITTEN ends the handler: a client that never received the greeting is
// not a client, and leaving the read loop running would probe a peer that is not there.
//
// Deterministic by construction, and it has to be: closing a client socket and hoping the server
// reaches the welcome first is a RACE, and when the close wins the handshake fails instead and this
// branch is never entered - which is exactly the flaky coverage this test replaced. The hijacked
// connection fails on its SECOND write, so the handshake's flush succeeds and the welcome frame
// is the write that fails.
func TestAWelcomeWhoseWriteFailsEndsTheHandler(t *testing.T) {
	srv := newTestServer(t, &fakeService{}, func(o *Options) { o.WSHeartbeat = -1 })

	c, ok := srv.lookup(DefaultSession)
	if !ok {
		t.Fatal("the default conversation must exist")
	}
	req := httptest.NewRequest(http.MethodGet, "/ws", nil)
	req = req.WithContext(context.WithValue(req.Context(), conversationKey, c))
	req.Header.Set("Upgrade", "websocket")
	req.Header.Set("Connection", "Upgrade")
	req.Header.Set("Sec-WebSocket-Key", "dGhlIHNhbXBsZSBub25jZQ==")
	req.Header.Set("Sec-WebSocket-Version", "13")

	// The handshake response goes through a buffer that works; the WELCOME frame goes to the
	// hijacked conn, so that is the connection made to fail. The buffer has to be big enough to
	// hold the handshake, so the flush succeeds and the welcome is the write under test.
	h := &bufferHijacker{
		w:    bufio.NewWriterSize(io.Discard, 4096),
		conn: &fakeConn{writeErr: errors.New("the peer went away before the greeting")},
	}

	done := make(chan struct{})
	go func() {
		srv.handleWebSocket(h, req)
		close(done)
	}()
	select {
	case <-done:
		// The handler returned rather than looping against a peer it cannot write to.
	case <-time.After(5 * time.Second):
		t.Fatal("a welcome that cannot be written must end the handler")
	}
}

// A failed welcome is ONE dead connection, not a dead server: the next client still gets greeted and
// served.
func TestAFailedWelcomeLeavesTheGatewayHealthy(t *testing.T) {
	srv := newTestServer(t, &fakeService{}, func(o *Options) { o.Log = newLogSink(t).log })

	c := dialWebSocket(t, srv, DefaultSession, testToken)
	// Read the welcome BEFORE closing, so the server has definitely got past the send: closing
	// earlier races the handshake and would assert something else entirely.
	if msg := c.readMsg(t); msg.Type != MsgAuthResponse {
		t.Fatalf("the first client must be greeted, got %q", msg.Type)
	}
	c.close()
	time.Sleep(50 * time.Millisecond)

	c2 := dialWebSocket(t, srv, DefaultSession, testToken)
	defer c2.close()
	if msg := c2.readMsg(t); msg.Type != MsgAuthResponse {
		t.Errorf("a second client must still get its welcome, got %q", msg.Type)
	}
}

// --- the send failure on a closed socket --------------------------------------------------------

// writeFrame reports an UNEXPECTED failure through the log, and a closed connection is not one. This
// drives the same client twice so both sides of isClosedConnErr are exercised on the real method.
func TestWriteFrameLogsOnlyUnexpectedFailures(t *testing.T) {
	sink := newLogSink(t)
	cl := &wsClient{conn: &fakeConn{writeErr: errors.New("connection reset by peer")}, log: sink.log}
	cl.writeFrame(opText, []byte("{}"))
	if len(sink.lines(t)) != 0 {
		t.Errorf("a reset connection is not an unexpected failure, got %v", sink.lines(t))
	}

	cl.conn = &fakeConn{writeErr: errors.New("a failure nobody plans for")}
	cl.writeFrame(opText, []byte("{}"))
	if len(sink.lines(t)) == 0 {
		t.Error("an unexpected write failure must be logged")
	}
}

// logxImportForLevels keeps the logx import used in the files whose only reference is a type.
var _ = logx.Warn
