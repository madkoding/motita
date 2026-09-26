package gateway

// The last reachable branches: the two stores whose LISTING fails, the read loop's unexpected read
// error, and the handshake's response-buffer failure.
//
// These are the leftovers of a package that is now covered end to end, so each one is here to make
// the failure EXPLICIT rather than to add a line to a report.

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
)

// --- the store listings that fail ---------------------------------------------------------------

// A session store whose LISTING fails reports it, and the gateway still starts: one unreadable
// directory must not cost the user the whole server.
//
// The directory is replaced by a FILE, so ReadDir fails with ENOTDIR rather than returning an empty
// list - which is the difference between "nothing saved" and "I could not look".
func TestASessionStoreWhoseListingFailsIsReportedAtStartup(t *testing.T) {
	// A FILE where the store's directory belongs.
	dir := filepath.Join(t.TempDir(), "sessions")
	if err := os.WriteFile(dir, []byte("a file where a directory belongs"), 0o600); err != nil {
		t.Fatal(err)
	}

	// newSessionStore itself refuses to create it, which is the earlier failure. What this test
	// needs is a store that OPENED and then stopped being readable, so the store is built first
	// over a real directory and the directory is replaced afterwards.
	real := filepath.Join(t.TempDir(), "real-sessions")
	st, err := newSessionStore(real)
	if err != nil {
		t.Fatalf("newSessionStore: %v", err)
	}
	if err := os.RemoveAll(real); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(real, []byte("now a file"), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := st.loadAll(); err == nil {
		t.Fatal("a listing that cannot read the directory must be reported")
	} else if !strings.Contains(err.Error(), "could not read the session directory") {
		t.Errorf("err = %q, want it to name the directory", err)
	}

	// And the server reports it and still serves, rather than refusing to start.
	srv, sink := startServer(t, Options{
		Token:        testToken,
		WorkspaceDir: t.TempDir(),
		SessionDir:   real,
		ProjectDir:   filepath.Join(t.TempDir(), "projects"),
	})
	if srv == nil {
		t.Fatal("a gateway whose session store cannot be listed must still start")
	}
	srv.loadPersistedSessions()
	if len(sink.lines(t)) == 0 {
		t.Error("the failure to load persisted sessions must reach the operator")
	}
}

// The same for the PROJECT store: a listing that cannot read answers an error the handler turns into
// a 500, rather than an empty list a client would delete against.
func TestAProjectStoreWhoseListingFailsIsReported(t *testing.T) {
	real := filepath.Join(t.TempDir(), "real-projects")
	ps, err := newProjectStore(real)
	if err != nil {
		t.Fatalf("newProjectStore: %v", err)
	}
	if err := os.RemoveAll(real); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(real, []byte("now a file"), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := ps.loadAll(); err == nil {
		t.Fatal("a project listing that cannot read the directory must be reported")
	}
}

// --- the read loop's unexpected read error ------------------------------------------------------

// A read that fails for a reason OTHER than the peer going away is logged: silent EOFs are expected,
// an unexpected error is not, and a log that cannot tell them apart is a log nobody reads.
func TestAnUnexpectedReadErrorIsLogged(t *testing.T) {
	sink := newLogSink(t)
	cl := &wsClient{conn: &fakeConn{readErr: errors.New("the network interface went away")}, log: sink.log}

	// The loop returns on the error, so it is run and then asserted.
	cl.readLoop(context.Background(), make(chan struct{}, 1))

	lines := strings.Join(sink.lines(t), "\n")
	if !strings.Contains(lines, "read") {
		t.Errorf("an unexpected read error must be logged, got %v", sink.lines(t))
	}
}

// And a CLEAN EOF is not logged: every disconnect would otherwise be a warning, and the real
// failures would drown in them.
func TestACleanReadEOFIsNotLogged(t *testing.T) {
	sink := newLogSink(t)
	cl := &wsClient{conn: &fakeConn{readErr: io.EOF}, log: sink.log}

	cl.readLoop(context.Background(), make(chan struct{}, 1))
	if len(sink.lines(t)) != 0 {
		t.Errorf("a client hanging up is not an error, got %v", sink.lines(t))
	}
}

// --- the handshake's buffered write -------------------------------------------------------------

// A handshake whose BUFFERED write fails is refused: the response is written through a bufio.Writer
// that wraps the hijacked connection, so the failure surfaces there rather than at the socket.
func TestAHandshakeWhoseBufferedWriteFailsIsRefused(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/ws", nil)
	req.Header.Set("Sec-WebSocket-Key", "dGhlIHNhbXBsZSBub25jZQ==")

	// A writer whose buffer is SMALLER than the handshake response: the write fills it, fails on
	// the flush, and never reaches the socket.
	h := &hijackingWriter{w: bufio.NewWriterSize(failWriter{}, 4)}

	if _, _, err := wsHandshake(h, req); err == nil {
		t.Error("a handshake that cannot be buffered out must be refused")
	}
}

// hijackingWriterWithConn is a ResponseWriter whose hijack hands back a connection that works for
// writing but fails on the buffer, which is the seam the buffer-failure case needs.
type hijackingWriterWithConn struct {
	conn net.Conn
}

func (h *hijackingWriterWithConn) Header() http.Header         { return make(http.Header) }
func (h *hijackingWriterWithConn) Write(p []byte) (int, error) { return h.conn.Write(p) }
func (h *hijackingWriterWithConn) WriteHeader(int)             {}
func (h *hijackingWriterWithConn) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	// The buffer wraps a writer that fails only once the handshake's bytes are pushed through it.
	return h.conn, bufio.NewReadWriter(bufio.NewReader(strings.NewReader("")), bufio.NewWriterSize(failWriter{}, 4)), nil
}

// The same case through the writer that hijacks a connection instead of a bare buffer, so the
// failure is attributed to the buffer and not to the hijack.
func TestAHandshakeFailsWhenOnlyTheResponseBufferDoes(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/ws", nil)
	req.Header.Set("Sec-WebSocket-Key", "dGhlIHNhbXBsZSBub25jZQ==")
	h := &hijackingWriterWithConn{conn: &fakeConn{}}

	if _, _, err := wsHandshake(h, req); err == nil {
		t.Error("a handshake whose buffered response cannot be written must be refused")
	}
	_ = time.Now
}

// --- the heartbeat ack channel ------------------------------------------------------------------

// A message that resets the server's heartbeat timer ALWAYS acks when the channel has room.
//
// Both the client's heartbeat and its heartbeat_ack take this path, and the coverage of it used to
// depend on the ack channel happening to be empty at that instant - which is a race with the
// heartbeat goroutine, and it showed up as a flaky 99.9% under load. Dispatching with an EMPTY
// buffer of capacity one makes the send unconditional and the branch deterministic.
func TestAMessageThatResetsTheHeartbeatAlwaysAcks(t *testing.T) {
	for _, msgType := range []string{MsgHeartbeat, MsgHeartbeatAck} {
		t.Run(msgType, func(t *testing.T) {
			// Write succeeds and does not block: net.Pipe would, which is its own trap.
			cl := &wsClient{conn: &fakeConn{}}
			ackCh := make(chan struct{}, 1) // empty, with room

			cl.dispatch(wsMessage{MsgID: newUUIDv4(), Type: msgType}, ackCh)

			select {
			case <-ackCh:
				// The heartbeat timer was reset, which is what keeps a quiet connection alive.
			default:
				t.Errorf("%s must reset the server's heartbeat timer", msgType)
			}
		})
	}
}

// And with a FULL channel the send is dropped rather than blocking the read loop: a slow ack must
// never stall the connection that is waiting for it.
func TestAResetWithAFullAckChannelDoesNotBlock(t *testing.T) {
	cl := &wsClient{conn: &fakeConn{}}
	ackCh := make(chan struct{}, 1)
	ackCh <- struct{}{} // full

	done := make(chan struct{})
	go func() {
		cl.dispatch(wsMessage{MsgID: newUUIDv4(), Type: MsgHeartbeatAck}, ackCh)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("a full ack channel must not block the read loop: the default case is what prevents it")
	}
}

// --- the heartbeat loop's two exits -------------------------------------------------------------

// The heartbeat loop returns when the context it was started with is cancelled, WHILE WAITING for an
// ack. That exit is what stops the goroutine when the gateway closes, and it was only covered when
// the cancellation happened to win a race against the timeout - hence the flaky report.
//
// Here the cancellation is the ONLY event that can end the wait: the ack channel is empty and the
// timeout is hours away, so the branch is taken deterministically.
func TestTheHeartbeatLoopStopsWhileWaitingForAnAck(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	// A write that succeeds without a peer, so the loop reaches the wait.
	cl := &wsClient{conn: &fakeConn{}, heartbeat: time.Millisecond}

	done := make(chan struct{})
	go func() {
		cl.heartbeatLoop(ctx, make(chan struct{})) // never acked
		close(done)
	}()

	// Let the loop send at least one probe and enter its wait, then cancel.
	time.Sleep(30 * time.Millisecond)
	cancel()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("a cancelled context must end the wait for an ack, not leave the goroutine running")
	}
}

// And a write that fails ends the loop immediately: there is no point waiting for an ack from a peer
// that can no longer be written to.
func TestTheHeartbeatLoopStopsWhenItsWriteFails(t *testing.T) {
	cl := &wsClient{
		conn:      &fakeConn{writeErr: errors.New("the peer is gone")},
		heartbeat: time.Millisecond,
	}
	done := make(chan struct{})
	go func() {
		cl.heartbeatLoop(context.Background(), make(chan struct{}))
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("a failed probe must end the heartbeat loop")
	}
}
