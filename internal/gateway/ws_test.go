package gateway

import (
	"bufio"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// wsTestClient is a minimal WebSocket client for driving the server in tests. It
// performs the handshake, sends and receives text frames, and handles masking — the
// minimum needed to exercise the protocol. It is NOT a general-purpose client.
type wsTestClient struct {
	conn   net.Conn
	prefix []byte // bytes read past the handshake headers, belonging to the first frame
}

// dialWebSocket opens a WebSocket connection to the server's /v1/sessions/{id}/ws
// endpoint with the bearer token in the Authorization header (the upgrade request goes
// through requireToken, so the token must be present).
func dialWebSocket(t *testing.T, srv *Server, sessionID, token string) *wsTestClient {
	t.Helper()
	conn, err := net.Dial("tcp", srv.Addr())
	if err != nil {
		t.Fatalf("dialing the server: %v", err)
	}
	key := "dGhlIHNhbXBsZSBub25jZQ==" // RFC 6455 §4.1 sample key.
	req := "GET /v1/sessions/" + sessionID + "/ws HTTP/1.1\r\n" +
		"Host: " + srv.Addr() + "\r\n" +
		"Upgrade: websocket\r\n" +
		"Connection: Upgrade\r\n" +
		"Sec-WebSocket-Key: " + key + "\r\n" +
		"Sec-WebSocket-Version: 13\r\n"
	if token != "" {
		req += "Authorization: Bearer " + token + "\r\n"
	}
	req += "\r\n"
	if _, err := conn.Write([]byte(req)); err != nil {
		conn.Close()
		t.Fatalf("writing the handshake: %v", err)
	}
	// Read the 101 response headers line by line, so we do not accidentally consume
	// the first WebSocket frame (the welcome message) that may arrive right after the
	// handshake. A raw conn.Read could slurp both in one call.
	r := bufio.NewReader(conn)
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			conn.Close()
			t.Fatalf("reading the handshake response: %v", err)
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			break // End of headers.
		}
		if strings.HasPrefix(line, "HTTP/1.1 ") && !strings.HasPrefix(line, "HTTP/1.1 101") {
			conn.Close()
			t.Fatalf("the handshake did not upgrade: %q", line)
		}
		if strings.HasPrefix(line, "Sec-WebSocket-Accept: ") {
			got := strings.TrimPrefix(line, "Sec-WebSocket-Accept: ")
			if got != "s3pPLMBiTxaQ9kYGzzhZRbK+xOo=" {
				conn.Close()
				t.Fatalf("the accept key is wrong: %q", got)
			}
		}
	}
	// Any bytes the bufio.Reader already read past the headers belong to the WebSocket
	// stream. Wrap the conn so the first readMsg sees them. This is the fix for the
	// intermitent hang: a raw conn.Read in the handshake could slurp the welcome frame
	// along with the headers, and the test would never see it.
	var pre []byte
	if r.Buffered() > 0 {
		pre, _ = r.Peek(r.Buffered())
		pre = append([]byte(nil), pre...)
	}
	return &wsTestClient{conn: conn, prefix: pre}
}

// sendText sends a text frame masked (as a client must). It always sends a final frame.
func (c *wsTestClient) sendText(data []byte) error {
	return c.writeFrame(opText, data, true)
}

// sendTextMsg marshals a wsMessage and sends it as a text frame.
func (c *wsTestClient) sendTextMsg(msg wsMessage) error {
	data, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	return c.sendText(data)
}

// writeFrame writes a masked frame (client-to-server frames must be masked).
func (c *wsTestClient) writeFrame(opcode byte, payload []byte, masked bool) error {
	var hdr [14]byte
	hdr[0] = 0x80 | opcode // FIN=1.
	pos := 2
	plen := len(payload)
	switch {
	case plen <= 125:
		hdr[1] = byte(plen)
	case plen <= 65535:
		hdr[1] = 126
		binary.BigEndian.PutUint16(hdr[2:4], uint16(plen))
		pos = 4
	default:
		hdr[1] = 127
		binary.BigEndian.PutUint64(hdr[2:10], uint64(plen))
		pos = 10
	}
	if masked {
		hdr[1] |= 0x80
		// Use a fixed mask for tests — determinism matters more than randomness here.
		mask := [4]byte{0x12, 0x34, 0x56, 0x78}
		copy(hdr[pos:pos+4], mask[:])
		pos += 4
		for i := range payload {
			payload[i] ^= mask[i%4]
		}
	}
	if _, err := c.conn.Write(hdr[:pos]); err != nil {
		return err
	}
	if plen > 0 {
		if _, err := c.conn.Write(payload); err != nil {
			return err
		}
	}
	return nil
}

// prefixedReader wraps a net.Conn and yields any prefix bytes first, then reads from
// the conn. It is what makes the bufio.Reader's buffered bytes reach the frame reader
// instead of being lost.
type prefixedReader struct {
	conn   net.Conn
	prefix []byte
}

func (p *prefixedReader) Read(b []byte) (int, error) {
	if len(p.prefix) > 0 {
		n := copy(b, p.prefix)
		p.prefix = p.prefix[n:]
		return n, nil
	}
	return p.conn.Read(b)
}

// reader returns the prefixed reader for frame reads.
func (c *wsTestClient) reader() *prefixedReader {
	return &prefixedReader{conn: c.conn, prefix: c.prefix}
}

// readText reads one frame and returns its payload as a string. It fails the test on
// any non-text frame. It sets a 5-second read deadline so a test does not hang forever
// when the server does not respond.
func (c *wsTestClient) readText(t *testing.T) (string, byte) {
	t.Helper()
	c.conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	opcode, payload, _, _, err := wsReadFrame(c.reader())
	if err != nil {
		t.Fatalf("reading a frame: %v", err)
	}
	// After the prefix is consumed, clear it so future reads go straight to the conn.
	c.prefix = nil
	return string(payload), opcode
}

// readMsg reads one frame and decodes it as a wsMessage.
func (c *wsTestClient) readMsg(t *testing.T) wsMessage {
	t.Helper()
	data, opcode := c.readText(t)
	if opcode != opText {
		t.Fatalf("expected a text frame, got opcode %x", opcode)
	}
	var msg wsMessage
	if err := json.Unmarshal([]byte(data), &msg); err != nil {
		t.Fatalf("decoding the message: %v\nraw: %s", err, data)
	}
	return msg
}

// close closes the underlying connection.
func (c *wsTestClient) close() {
	c.conn.Close()
}

// newWSTestServer is newTestServer with the WebSocket heartbeat turned off, so tests do
// not race with the 30-second heartbeat goroutine.
func newWSTestServer(t *testing.T, svc Service) *Server {
	t.Helper()
	return newTestServer(t, svc, func(o *Options) { o.WSHeartbeat = -1 })
}

// makeMsg builds a wsMessage with all required fields, for tests that need to send.
func makeMsg(msgType string, payload any) wsMessage {
	data, _ := json.Marshal(payload)
	return wsMessage{
		MsgID:     newUUIDv4(),
		Type:      msgType,
		Flags:     []string{},
		Timestamp: time.Now().UTC().Format(time.RFC3339Nano),
		Payload:   data,
	}
}

// TestWebSocketRejectsNonUpgrade verifies the endpoint refuses a plain GET.
func TestWebSocketRejectsNonUpgrade(t *testing.T) {
	srv := newTestServer(t, &fakeService{})
	req, _ := http.NewRequest(http.MethodGet, srv.BaseURL()+sessionPath(srv, DefaultSession, "/ws"), nil)
	req.Header.Set("Authorization", "Bearer "+testToken)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("non-upgrade GET = %d, want 400", w.Code)
	}
}

// TestWebSocketRequiresToken verifies the WebSocket endpoint is behind the token.
func TestWebSocketRequiresToken(t *testing.T) {
	srv := newTestServer(t, &fakeService{})
	// Dial without a token. The server should refuse the upgrade with 401, not 101.
	conn, err := net.Dial("tcp", srv.Addr())
	if err != nil {
		t.Fatalf("dialing: %v", err)
	}
	defer conn.Close()
	req := "GET /v1/sessions/default/ws HTTP/1.1\r\n" +
		"Host: " + srv.Addr() + "\r\n" +
		"Upgrade: websocket\r\n" +
		"Connection: Upgrade\r\n" +
		"Sec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==\r\n" +
		"Sec-WebSocket-Version: 13\r\n\r\n"
	if _, err := conn.Write([]byte(req)); err != nil {
		t.Fatalf("writing: %v", err)
	}
	buf := make([]byte, 4096)
	n, _ := conn.Read(buf)
	resp := string(buf[:n])
	if !strings.HasPrefix(resp, "HTTP/1.1 401") {
		t.Errorf("websocket without token = %q, want 401", strings.SplitN(resp, "\r\n", 2)[0])
	}
}

// TestWebSocketHandshakeAndWelcome verifies the upgrade succeeds and the server sends a
// welcome message with the IDLE flag.
func TestWebSocketHandshakeAndWelcome(t *testing.T) {
	srv := newWSTestServer(t, &fakeService{})
	cl := dialWebSocket(t, srv, DefaultSession, testToken)
	defer cl.close()

	msg := cl.readMsg(t)
	if msg.Type != MsgAuthResponse {
		t.Errorf("welcome type = %q, want %q", msg.Type, MsgAuthResponse)
	}
	if !msg.hasFlag(FlagIdle) {
		t.Errorf("welcome flags = %v, want to contain %s", msg.Flags, FlagIdle)
	}
}

// TestWebSocketAuth verifies the auth flow: a correct token gets AUTHENTICATED, a wrong
// one gets UNAUTHORIZED.
func TestWebSocketAuth(t *testing.T) {
	srv := newWSTestServer(t, &fakeService{})
	cl := dialWebSocket(t, srv, DefaultSession, testToken)
	defer cl.close()
	_ = cl.readMsg(t) // welcome

	// Correct token.
	cl.sendTextMsg(makeMsg(MsgAuth, map[string]any{"token": testToken}))
	resp := cl.readMsg(t)
	if resp.Type != MsgAuthResponse {
		t.Errorf("auth response type = %q, want %q", resp.Type, MsgAuthResponse)
	}
	if !resp.hasFlag(FlagAuthenticated) {
		t.Errorf("auth response flags = %v, want to contain %s", resp.Flags, FlagAuthenticated)
	}

	// Wrong token (new connection).
	cl2 := dialWebSocket(t, srv, DefaultSession, testToken)
	defer cl2.close()
	_ = cl2.readMsg(t) // welcome
	cl2.sendTextMsg(makeMsg(MsgAuth, map[string]any{"token": "wrong"}))
	resp2 := cl2.readMsg(t)
	if !resp2.hasFlag(FlagUnauthorized) {
		t.Errorf("wrong token flags = %v, want to contain %s", resp2.Flags, FlagUnauthorized)
	}
}

// TestWebSocketQueryBeforeAuth verifies a query without auth is rejected with
// UNAUTHORIZED.
func TestWebSocketQueryBeforeAuth(t *testing.T) {
	srv := newWSTestServer(t, &fakeService{})
	cl := dialWebSocket(t, srv, DefaultSession, testToken)
	defer cl.close()
	_ = cl.readMsg(t) // welcome

	cl.sendTextMsg(makeMsg(MsgQuery, map[string]any{"query": "hello"}))
	resp := cl.readMsg(t)
	if resp.Type != MsgError {
		t.Errorf("query-before-auth type = %q, want %q", resp.Type, MsgError)
	}
	if !resp.hasFlag(FlagUnauthorized) {
		t.Errorf("query-before-auth flags = %v, want %s", resp.Flags, FlagUnauthorized)
	}
}

// TestWebSocketQueryFlow verifies the full query flow: PROCESSING ack, then COMPLETED
// response.
func TestWebSocketQueryFlow(t *testing.T) {
	svc := &fakeService{
		plan: func(_ context.Context, _ string, _ func(string, ...any)) (string, error) {
			return "42", nil
		},
	}
	srv := newWSTestServer(t, svc)
	cl := dialWebSocket(t, srv, DefaultSession, testToken)
	defer cl.close()
	_ = cl.readMsg(t) // welcome
	cl.sendTextMsg(makeMsg(MsgAuth, map[string]any{"token": testToken}))
	_ = cl.readMsg(t) // auth_response

	cl.sendTextMsg(makeMsg(MsgQuery, map[string]any{"query": "what is the answer?"}))

	// First response: PROCESSING.
	processing := cl.readMsg(t)
	if !processing.hasFlag(FlagProcessing) {
		t.Errorf("first response flags = %v, want %s", processing.Flags, FlagProcessing)
	}

	// Second response: COMPLETED with the result.
	completed := cl.readMsg(t)
	if !completed.hasFlag(FlagCompleted) {
		t.Errorf("second response flags = %v, want %s", completed.Flags, FlagCompleted)
	}
	var payload struct {
		Result string `json:"result"`
	}
	if err := completed.decodePayload(&payload); err != nil {
		t.Fatalf("decoding the completed payload: %v", err)
	}
	if payload.Result != "42" {
		t.Errorf("result = %q, want %q", payload.Result, "42")
	}
}

// TestWebSocketQueryError verifies a query that fails returns an error message with
// ERROR_RECOVERABLE.
func TestWebSocketQueryError(t *testing.T) {
	svc := &fakeService{
		plan: func(_ context.Context, _ string, _ func(string, ...any)) (string, error) {
			return "", fmt.Errorf("the model is down")
		},
	}
	srv := newWSTestServer(t, svc)
	cl := dialWebSocket(t, srv, DefaultSession, testToken)
	defer cl.close()
	_ = cl.readMsg(t) // welcome
	cl.sendTextMsg(makeMsg(MsgAuth, map[string]any{"token": testToken}))
	_ = cl.readMsg(t) // auth_response

	cl.sendTextMsg(makeMsg(MsgQuery, map[string]any{"query": "hello"}))
	_ = cl.readMsg(t) // PROCESSING
	errMsg := cl.readMsg(t)
	if errMsg.Type != MsgError {
		t.Errorf("error type = %q, want %q", errMsg.Type, MsgError)
	}
	if !errMsg.hasFlag(FlagErrorRecoverable) {
		t.Errorf("error flags = %v, want %s", errMsg.Flags, FlagErrorRecoverable)
	}
}

// TestWebSocketHeartbeat verifies the server responds to a client heartbeat with an ack.
func TestWebSocketHeartbeat(t *testing.T) {
	srv := newWSTestServer(t, &fakeService{})
	cl := dialWebSocket(t, srv, DefaultSession, testToken)
	defer cl.close()
	_ = cl.readMsg(t) // welcome

	cl.sendTextMsg(makeMsg(MsgHeartbeat, nil))
	ack := cl.readMsg(t)
	if ack.Type != MsgHeartbeatAck {
		t.Errorf("heartbeat ack type = %q, want %q", ack.Type, MsgHeartbeatAck)
	}
}

// TestWebSocketMalformedJSON verifies a non-JSON message gets ERROR_RECOVERABLE.
func TestWebSocketMalformedJSON(t *testing.T) {
	srv := newWSTestServer(t, &fakeService{})
	cl := dialWebSocket(t, srv, DefaultSession, testToken)
	defer cl.close()
	_ = cl.readMsg(t) // welcome

	cl.sendText([]byte("not json at all"))
	errMsg := cl.readMsg(t)
	if errMsg.Type != MsgError {
		t.Errorf("malformed type = %q, want %q", errMsg.Type, MsgError)
	}
	if !errMsg.hasFlag(FlagErrorRecoverable) {
		t.Errorf("malformed flags = %v, want %s", errMsg.Flags, FlagErrorRecoverable)
	}
}

// TestWebSocketUnknownType verifies an unknown message type gets ERROR_RECOVERABLE.
func TestWebSocketUnknownType(t *testing.T) {
	srv := newWSTestServer(t, &fakeService{})
	cl := dialWebSocket(t, srv, DefaultSession, testToken)
	defer cl.close()
	_ = cl.readMsg(t) // welcome

	cl.sendTextMsg(makeMsg("frobnicate", nil))
	errMsg := cl.readMsg(t)
	if errMsg.Type != MsgError {
		t.Errorf("unknown type response = %q, want %q", errMsg.Type, MsgError)
	}
	if !errMsg.hasFlag(FlagErrorRecoverable) {
		t.Errorf("unknown type flags = %v, want %s", errMsg.Flags, FlagErrorRecoverable)
	}
}

// TestWebSocketMultipleClients verifies two clients can connect simultaneously and
// operate independently.
func TestWebSocketMultipleClients(t *testing.T) {
	srv := newWSTestServer(t, &fakeService{
		plan: func(_ context.Context, _ string, _ func(string, ...any)) (string, error) {
			return "ok", nil
		},
	})

	cl1 := dialWebSocket(t, srv, DefaultSession, testToken)
	defer cl1.close()
	cl2 := dialWebSocket(t, srv, DefaultSession, testToken)
	defer cl2.close()

	// Both should get a welcome.
	_ = cl1.readMsg(t)
	_ = cl2.readMsg(t)

	// Both authenticate.
	cl1.sendTextMsg(makeMsg(MsgAuth, map[string]any{"token": testToken}))
	_ = cl1.readMsg(t)
	cl2.sendTextMsg(makeMsg(MsgAuth, map[string]any{"token": testToken}))
	_ = cl2.readMsg(t)

	// Both send heartbeats and get acks.
	cl1.sendTextMsg(makeMsg(MsgHeartbeat, nil))
	ack1 := cl1.readMsg(t)
	if ack1.Type != MsgHeartbeatAck {
		t.Errorf("client 1 heartbeat ack = %q, want %q", ack1.Type, MsgHeartbeatAck)
	}
	cl2.sendTextMsg(makeMsg(MsgHeartbeat, nil))
	ack2 := cl2.readMsg(t)
	if ack2.Type != MsgHeartbeatAck {
		t.Errorf("client 2 heartbeat ack = %q, want %q", ack2.Type, MsgHeartbeatAck)
	}
}

// TestWebSocketAcceptKey verifies the RFC 6455 §4.2.2 sample: the key
// "dGhlIHNhbXBsZSBub25jZQ==" must produce "s3pPLMBiTxaQ9kYGzzhZRbK+xOo=".
func TestWebSocketAcceptKey(t *testing.T) {
	got := wsAcceptKey("dGhlIHNhbXBsZSBub25jZQ==")
	want := "s3pPLMBiTxaQ9kYGzzhZRbK+xOo="
	if got != want {
		t.Errorf("accept key = %q, want %q", got, want)
	}
}

// TestUUIDv4Format verifies the generated UUID matches the v4 format.
func TestUUIDv4Format(t *testing.T) {
	id := newUUIDv4()
	// xxxxxxxx-xxxx-4xxx-yxxx-xxxxxxxxxxxx where y is 8, 9, a, or b.
	if len(id) != 36 {
		t.Fatalf("uuid length = %d, want 36", len(id))
	}
	if id[8] != '-' || id[13] != '-' || id[18] != '-' || id[23] != '-' {
		t.Errorf("uuid %q does not have dashes in the right positions", id)
	}
	if id[14] != '4' {
		t.Errorf("uuid %q version digit = %c, want '4'", id, id[14])
	}
	switch id[19] {
	case '8', '9', 'a', 'b':
	default:
		t.Errorf("uuid %q variant digit = %c, want 8/9/a/b", id, id[19])
	}
}

// TestWebSocketCloseFrame verifies the server handles a close frame from the client.
func TestWebSocketCloseFrame(t *testing.T) {
	srv := newWSTestServer(t, &fakeService{})
	cl := dialWebSocket(t, srv, DefaultSession, testToken)
	_ = cl.readMsg(t) // welcome

	// Send a close frame. The server should respond with a close frame and close the
	// connection.
	cl.writeFrame(opClose, nil, true)

	// The server may send a close frame back before closing. Read it (or an error),
	// then the next read must fail because the connection is closed.
	cl.conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, _, _, _, err := wsReadFrame(cl.conn)
	if err == nil {
		// The first read succeeded (a close frame from the server); the next must fail.
		cl.conn.SetReadDeadline(time.Now().Add(2 * time.Second))
		_, _, _, _, err = wsReadFrame(cl.conn)
	}
	if err == nil {
		t.Error("expected the connection to be closed after a close frame")
	}
}

// TestWebSocketQueryProgress verifies progress lines are sent as PARTIAL messages.
func TestWebSocketQueryProgress(t *testing.T) {
	svc := &fakeService{
		plan: func(_ context.Context, _ string, progress func(string, ...any)) (string, error) {
			progress("thinking...")
			progress("still thinking...")
			return "done", nil
		},
	}
	srv := newWSTestServer(t, svc)
	cl := dialWebSocket(t, srv, DefaultSession, testToken)
	defer cl.close()
	_ = cl.readMsg(t) // welcome
	cl.sendTextMsg(makeMsg(MsgAuth, map[string]any{"token": testToken}))
	_ = cl.readMsg(t) // auth_response

	cl.sendTextMsg(makeMsg(MsgQuery, map[string]any{"query": "hello"}))

	// PROCESSING
	_ = cl.readMsg(t)
	// PARTIAL: thinking...
	p1 := cl.readMsg(t)
	if !p1.hasFlag(FlagPartial) {
		t.Errorf("first partial flags = %v, want %s", p1.Flags, FlagPartial)
	}
	// PARTIAL: still thinking...
	p2 := cl.readMsg(t)
	if !p2.hasFlag(FlagPartial) {
		t.Errorf("second partial flags = %v, want %s", p2.Flags, FlagPartial)
	}
	// COMPLETED
	done := cl.readMsg(t)
	if !done.hasFlag(FlagCompleted) {
		t.Errorf("completed flags = %v, want %s", done.Flags, FlagCompleted)
	}
}

// TestWsReadFrameUnmasked verifies the reader handles unmasked frames (server→client)
// without error — the mask enforcement is the caller's job, not the reader's.
func TestWsReadFrameUnmasked(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()

	go func() {
		// Text frame "hi", NOT masked.
		frame := []byte{0x81, 0x02, 'h', 'i'}
		client.Write(frame)
	}()

	server.SetReadDeadline(time.Now().Add(2 * time.Second))
	opcode, payload, _, masked, err := wsReadFrame(server)
	if err != nil {
		t.Fatalf("wsReadFrame on unmasked frame: %v", err)
	}
	if masked {
		t.Error("unmasked frame should report masked=false")
	}
	if opcode != opText || string(payload) != "hi" {
		t.Errorf("opcode=%x payload=%q, want text/hi", opcode, payload)
	}
}

// TestWsWriteAndReadFrameRoundtrip verifies a frame written by the server can be read
// back by a client (no mask on server→client).
func TestWsWriteAndReadFrameRoundtrip(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()

	payload := []byte(`{"type":"test"}`)
	go func() {
		if err := wsWriteText(server, payload); err != nil {
			t.Errorf("wsWriteText: %v", err)
		}
	}()

	client.SetReadDeadline(time.Now().Add(2 * time.Second))
	// Read the frame manually (unmasked, server→client).
	var hdr [2]byte
	if _, err := io.ReadFull(client, hdr[:]); err != nil {
		t.Fatalf("reading header: %v", err)
	}
	if hdr[0]&0x0f != opText {
		t.Errorf("opcode = %x, want %x", hdr[0]&0x0f, opText)
	}
	if hdr[1]&0x80 != 0 {
		t.Error("server-to-client frame should not be masked")
	}
	length := int(hdr[1] & 0x7f)
	body := make([]byte, length)
	if _, err := io.ReadFull(client, body); err != nil {
		t.Fatalf("reading body: %v", err)
	}
	if string(body) != string(payload) {
		t.Errorf("body = %q, want %q", body, payload)
	}
}
