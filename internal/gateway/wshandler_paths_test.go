package gateway

// The protocol handler's paths that ws_test.go does not reach: the frame types it refuses, the
// messages that arrive before auth, the payloads of the wrong shape, the heartbeat loop, and the
// write paths that only run when the connection is already dead.
//
// These drive a wsClient over a net.Pipe pair rather than through a real socket. The read loop is
// a loop over frames, so the pipe is enough to be exact: every byte the test writes is a byte the
// server reads, in the order it reads it.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"
)

// wsPair builds a client whose connection is one end of a pipe, plus the wsClient the handler
// code under test will run on. The read loop is NOT started: each test drives one call to make the
// branch it is about unambiguous.
func wsPair(t *testing.T, svc Service, token string) (*wsClient, net.Conn, chan struct{}) {
	t.Helper()
	server, client := net.Pipe()
	t.Cleanup(func() { _ = server.Close(); _ = client.Close() })
	cl := &wsClient{conn: server, svc: svc, token: token, heartbeat: -1}
	return cl, client, make(chan struct{}, 1)
}

// readOneFrame reads exactly one frame as a client, so a test can assert on what the server sent.
func readOneFrame(t *testing.T, c net.Conn) (byte, wsMessage) {
	t.Helper()
	_ = c.SetReadDeadline(time.Now().Add(2 * time.Second))
	opcode, payload, _, _, err := wsReadFrame(c)
	if err != nil {
		t.Fatalf("reading a frame: %v", err)
	}
	if opcode != opText {
		return opcode, wsMessage{}
	}
	var msg wsMessage
	if err := json.Unmarshal(payload, &msg); err != nil {
		t.Fatalf("decoding %q: %v", payload, err)
	}
	return opcode, msg
}

// clientFrame builds a masked client frame for the wire.
func clientFrame(opcode byte, payload []byte) []byte {
	return wsFrameBytes(opcode, payload, true, true, [4]byte{0x12, 0x34, 0x56, 0x78})
}

// A masked text frame that is not JSON is answered with a recoverable error and the connection
// stays open: a client that sends one bad message is told, not disconnected.
func TestWSAReadLoopReportsMalformedJSON(t *testing.T) {
	cl, client, ackCh := wsPair(t, &fakeService{}, "the-token")
	go cl.readLoop(context.Background(), ackCh)
	go func() { _, _ = client.Write(clientFrame(opText, []byte("{ not json"))) }()

	_, msg := readOneFrame(t, client)
	if msg.Type != MsgError {
		t.Fatalf("type = %q, want an error message", msg.Type)
	}
	if !msg.hasFlag(FlagErrorRecoverable) {
		t.Errorf("flags = %v, want %s: the connection must survive a bad message", msg.Flags, FlagErrorRecoverable)
	}
	if !strings.Contains(string(msg.Payload), "JSON") {
		t.Errorf("payload = %s, want it to name the problem", msg.Payload)
	}
}

// An envelope missing any of its three required fields is malformed rather than a message with a
// default: the id is what a response is matched to, and the timestamp is what orders them.
func TestWSAReadLoopRefusesAnIncompleteEnvelope(t *testing.T) {
	cases := map[string]string{
		"no msg_id":    `{"type":"query","timestamp":"2026-01-01T00:00:00Z"}`,
		"no type":      `{"msg_id":"m1","timestamp":"2026-01-01T00:00:00Z"}`,
		"no timestamp": `{"msg_id":"m1","type":"query"}`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			cl, client, ackCh := wsPair(t, &fakeService{}, "the-token")
			go cl.readLoop(context.Background(), ackCh)
			go func() { _, _ = client.Write(clientFrame(opText, []byte(body))) }()

			_, msg := readOneFrame(t, client)
			if msg.Type != MsgError || !msg.hasFlag(FlagErrorRecoverable) {
				t.Fatalf("got %q with flags %v, want a recoverable error", msg.Type, msg.Flags)
			}
			if !strings.Contains(string(msg.Payload), "required fields") {
				t.Errorf("payload = %s, want it to name the missing fields", msg.Payload)
			}
		})
	}
}

// A client-to-server frame MUST be masked (RFC 6455 §5.1), and an unmasked one closes the
// connection with a protocol error. This is the rule that stops a proxy's bytes from being
// mistaken for a client's.
func TestWSAReadLoopClosesOnAnUnmaskedFrame(t *testing.T) {
	cl, client, ackCh := wsPair(t, &fakeService{}, "the-token")
	done := make(chan struct{})
	go func() { cl.readLoop(context.Background(), ackCh); close(done) }()
	// The frame goes out on a goroutine and the read below starts immediately: a pipe with no
	// buffer blocks the writer until a reader appears, so writing first and reading after would
	// deadlock. The read is what unblocks it.
	go func() { _, _ = client.Write(wsFrameBytes(opText, []byte("{}"), true, false, [4]byte{})) }()

	// The server answers with a close frame and ends the loop.
	_ = client.SetReadDeadline(time.Now().Add(5 * time.Second))
	opcode, payload, _, _, err := wsReadFrame(client)
	if err != nil {
		t.Fatalf("reading the close frame: %v", err)
	}
	if opcode != opClose {
		t.Fatalf("opcode = %x, want a close frame", opcode)
	}
	if code := uint16(payload[0])<<8 | uint16(payload[1]); code != CloseProtocol {
		t.Errorf("close code = %d, want %d (protocol error)", code, CloseProtocol)
	}
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("the read loop did not end after a protocol violation")
	}
}

// Binary frames are refused: this protocol is JSON, and silently reading a binary payload as a
// string is how a client gets an answer about the wrong bytes.
func TestWSAReadLoopRefusesBinaryAndContinuationFrames(t *testing.T) {
	cases := map[string]byte{
		"a binary frame":       opBinary,
		"a continuation frame": opContinuation,
	}
	for name, opcode := range cases {
		t.Run(name, func(t *testing.T) {
			cl, client, ackCh := wsPair(t, &fakeService{}, "the-token")
			go cl.readLoop(context.Background(), ackCh)
			go func() { _, _ = client.Write(clientFrame(opcode, []byte("payload"))) }()

			_, msg := readOneFrame(t, client)
			if msg.Type != MsgError || !msg.hasFlag(FlagErrorRecoverable) {
				t.Fatalf("got %q with flags %v, want a recoverable error", msg.Type, msg.Flags)
			}
			if !strings.Contains(string(msg.Payload), "not supported") {
				t.Errorf("payload = %s, want it to say the frame type is unsupported", msg.Payload)
			}
		})
	}
}

// A ping MUST be answered with a pong carrying the same payload (RFC 6455 §5.5.2): a client that
// uses ping to measure the link needs its own bytes back.
func TestWSAReadLoopAnswersAPing(t *testing.T) {
	cl, client, ackCh := wsPair(t, &fakeService{}, "the-token")
	go cl.readLoop(context.Background(), ackCh)
	go func() { _, _ = client.Write(clientFrame(opPing, []byte("probe"))) }()

	_ = client.SetReadDeadline(time.Now().Add(2 * time.Second))
	opcode, payload, _, _, err := wsReadFrame(client)
	if err != nil {
		t.Fatalf("reading the pong: %v", err)
	}
	if opcode != opPong {
		t.Fatalf("opcode = %x, want a pong", opcode)
	}
	if string(payload) != "probe" {
		t.Errorf("payload = %q, want the ping's own bytes back", payload)
	}
}

// A client's close frame ends the loop with a normal close, not a protocol error.
func TestWSAReadLoopEndsOnAClientClose(t *testing.T) {
	cl, client, ackCh := wsPair(t, &fakeService{}, "the-token")
	done := make(chan struct{})
	go func() { cl.readLoop(context.Background(), ackCh); close(done) }()
	// The server's close frame must be READ or the pipe blocks: a pipe has no buffer, so the write
	// on the other side cannot return until someone takes the bytes.
	go func() {
		_ = client.SetReadDeadline(time.Now().Add(5 * time.Second))
		_, _, _, _, _ = wsReadFrame(client)
	}()
	go func() { _, _ = client.Write(clientFrame(opClose, []byte{0x03, 0xe8})) }()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("a client close must end the read loop")
	}
}

// A pong from the client resets the heartbeat timer through the ack channel.
func TestWSAReadLoopFeedsAPongToTheHeartbeat(t *testing.T) {
	cl, client, ackCh := wsPair(t, &fakeService{}, "the-token")
	go cl.readLoop(context.Background(), ackCh)
	go func() { _, _ = client.Write(clientFrame(opPong, nil)) }()

	select {
	case <-ackCh:
	case <-time.After(2 * time.Second):
		t.Fatal("a pong must reach the heartbeat's ack channel")
	}
}

// Before authenticating, a query is refused with UNAUTHORIZED: the transport token authorises the
// connection, and the auth message is the second factor the protocol requires.
func TestWSAQueryBeforeAuthIsRefused(t *testing.T) {
	for name, msgType := range map[string]string{"a query": MsgQuery} {
		t.Run(name, func(t *testing.T) {
			cl, client, ackCh := wsPair(t, &fakeService{}, "the-token")
			go cl.readLoop(context.Background(), ackCh)
			body, _ := json.Marshal(wsMessage{
				MsgID: "m1", Type: msgType, Timestamp: "2026-01-01T00:00:00Z",
				Payload: json.RawMessage(`{"query":"hello"}`),
			})
			go func() { _, _ = client.Write(clientFrame(opText, body)) }()

			_, got := readOneFrame(t, client)
			if got.Type != MsgError {
				t.Fatalf("type = %q, want an error", got.Type)
			}
			if !got.hasFlag(FlagUnauthorized) {
				t.Errorf("flags = %v, want %s before an auth message", got.Flags, FlagUnauthorized)
			}
		})
	}
}

// An auth payload that is not the expected shape is reported rather than treated as an empty
// token, which would turn a client bug into a silent authentication failure.
func TestWSAuthRefusesAPayloadOfTheWrongShape(t *testing.T) {
	cl, client, _ := wsPair(t, &fakeService{}, "the-token")
	go func() {
		cl.handleAuth(wsMessage{
			MsgID: "m1", Type: MsgAuth, Timestamp: "2026-01-01T00:00:00Z",
			Payload: json.RawMessage(`{"token":12345}`),
		})
	}()

	_, msg := readOneFrame(t, client)
	if msg.Type != MsgError || !msg.hasFlag(FlagErrorRecoverable) {
		t.Fatalf("got %q with flags %v, want a recoverable error", msg.Type, msg.Flags)
	}
	if !strings.Contains(string(msg.Payload), "expected shape") {
		t.Errorf("payload = %s, want it to name the shape", msg.Payload)
	}
	if cl.isAuthenticated() {
		t.Error("a malformed auth payload must not authenticate the client")
	}
}

// The wrong token is refused with UNAUTHORIZED and leaves the connection open, so the client can
// correct itself without reconnecting.
func TestWSAuthRefusesTheWrongToken(t *testing.T) {
	cl, client, _ := wsPair(t, &fakeService{}, "the-token")
	go func() {
		cl.handleAuth(wsMessage{
			MsgID: "m1", Type: MsgAuth, Timestamp: "2026-01-01T00:00:00Z",
			Payload: json.RawMessage(`{"token":"not-the-token"}`),
		})
	}()

	_, msg := readOneFrame(t, client)
	if msg.Type != MsgAuthResponse {
		t.Fatalf("type = %q, want an auth_response", msg.Type)
	}
	if !msg.hasFlag(FlagUnauthorized) || msg.hasFlag(FlagAuthenticated) {
		t.Errorf("flags = %v, want %s and not %s", msg.Flags, FlagUnauthorized, FlagAuthenticated)
	}
	if cl.isAuthenticated() {
		t.Error("the wrong token must not authenticate")
	}
}

// An authenticated query is acknowledged with PROCESSING first, so the client knows it was
// received before the work starts, and then answered with COMPLETED.
func TestWSAQueryIsAcknowledgedThenAnswered(t *testing.T) {
	svc := &fakeService{plan: func(_ context.Context, prompt string, progress func(string, ...any)) (string, error) {
		progress("working on %s", prompt)
		return "the answer", nil
	}}
	cl, client, _ := wsPair(t, svc, "the-token")
	cl.authenticated = true

	go func() {
		cl.handleQuery(wsMessage{
			MsgID: "m1", Type: MsgQuery, Timestamp: "2026-01-01T00:00:00Z",
			Payload: json.RawMessage(`{"query":"hello"}`),
		})
	}()

	_, first := readOneFrame(t, client)
	if first.Type != MsgQueryResponse || !first.hasFlag(FlagProcessing) {
		t.Fatalf("first message = %q with flags %v, want an ack with %s", first.Type, first.Flags, FlagProcessing)
	}
	_, second := readOneFrame(t, client)
	if second.Type != MsgQueryResponse || !second.hasFlag(FlagPartial) {
		t.Fatalf("second message = %q with flags %v, want progress with %s", second.Type, second.Flags, FlagPartial)
	}
	if !strings.Contains(string(second.Payload), "working on hello") {
		t.Errorf("progress payload = %s, want the line the service reported", second.Payload)
	}
	_, third := readOneFrame(t, client)
	if third.Type != MsgQueryResponse || !third.hasFlag(FlagCompleted) {
		t.Fatalf("third message = %q with flags %v, want the answer with %s", third.Type, third.Flags, FlagCompleted)
	}
	if !strings.Contains(string(third.Payload), "the answer") {
		t.Errorf("answer payload = %s, want the result", third.Payload)
	}
}

// A query payload of the wrong shape and an empty query are both refusals, not runs: an agent turn
// on an empty prompt spends a model call to answer nothing. The blank case is the interesting one -
// the WebSocket path must trim exactly as the HTTP plan endpoint does, or the same text reaches the
// agent through one transport and is refused through the other.
func TestWSAQueryRefusesABadOrEmptyPayload(t *testing.T) {
	// The fake returns an error so that a query which WRONGLY gets through is answered with a
	// distinguishable message rather than with the ack that any accepted query gets.
	svc := &fakeService{plan: func(_ context.Context, prompt string, _ func(string, ...any)) (string, error) {
		return "", fmt.Errorf("the service was called with %q, which should have been refused", prompt)
	}}
	cases := map[string]struct {
		payload string
		want    string
	}{
		"a payload of the wrong shape": {`{"query":42}`, "expected shape"},
		"no query field":               {`{}`, "empty"},
		"a blank query":                {`{"query":"   "}`, "empty"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			cl, client, _ := wsPair(t, svc, "the-token")
			cl.authenticated = true
			go func() {
				cl.handleQuery(wsMessage{
					MsgID: "m1", Type: MsgQuery, Timestamp: "2026-01-01T00:00:00Z",
					Payload: json.RawMessage(tc.payload),
				})
			}()

			_, msg := readOneFrame(t, client)
			// A refusal comes BEFORE the PROCESSING ack: the ack means "accepted", and a refused
			// query must not have told the client it was accepted.
			if msg.Type != MsgError || !msg.hasFlag(FlagErrorRecoverable) {
				t.Fatalf("got %q with flags %v, want a recoverable error", msg.Type, msg.Flags)
			}
			if !strings.Contains(string(msg.Payload), tc.want) {
				t.Errorf("payload = %s, want it to mention %q", msg.Payload, tc.want)
			}
		})
	}
}

// A failed query is reported as an error carrying the reason rather than as an empty answer: a
// client that cannot tell "no result" from "it broke" will retry forever.
func TestWSAQueryReportsAFailure(t *testing.T) {
	svc := &fakeService{plan: func(context.Context, string, func(string, ...any)) (string, error) {
		return "", errors.New("there is no model configured")
	}}
	cl, client, _ := wsPair(t, svc, "the-token")
	cl.authenticated = true
	go func() {
		cl.handleQuery(wsMessage{
			MsgID: "m1", Type: MsgQuery, Timestamp: "2026-01-01T00:00:00Z",
			Payload: json.RawMessage(`{"query":"hello"}`),
		})
	}()

	_, ack := readOneFrame(t, client)
	if !ack.hasFlag(FlagProcessing) {
		t.Fatalf("the ack must come first, got flags %v", ack.Flags)
	}
	_, msg := readOneFrame(t, client)
	if msg.Type != MsgError {
		t.Fatalf("type = %q, want an error", msg.Type)
	}
	if !strings.Contains(string(msg.Payload), "no model") {
		t.Errorf("payload = %s, want the failure reason", msg.Payload)
	}
}

// A server-to-client type sent by a client is a recoverable protocol violation: the connection
// stays open.
func TestWSDispatchRefusesServerOnlyTypes(t *testing.T) {
	for _, msgType := range []string{MsgAuthResponse, MsgQueryResponse, MsgNotification, MsgError} {
		t.Run(msgType, func(t *testing.T) {
			cl, client, ackCh := wsPair(t, &fakeService{}, "the-token")
			go func() { cl.dispatch(wsMessage{MsgID: "m", Type: msgType}, ackCh) }()

			_, msg := readOneFrame(t, client)
			if msg.Type != MsgError || !msg.hasFlag(FlagErrorRecoverable) {
				t.Fatalf("got %q with flags %v, want a recoverable error", msg.Type, msg.Flags)
			}
			if !strings.Contains(string(msg.Payload), "server-to-client only") {
				t.Errorf("payload = %s, want it to say the direction is wrong", msg.Payload)
			}
		})
	}
}

// An unknown type is refused by name, so a client can see which of its messages was not
// understood.
func TestWSDispatchRefusesAnUnknownType(t *testing.T) {
	cl, client, ackCh := wsPair(t, &fakeService{}, "the-token")
	go func() { cl.dispatch(wsMessage{MsgID: "m", Type: "telepathy"}, ackCh) }()

	_, msg := readOneFrame(t, client)
	if msg.Type != MsgError {
		t.Fatalf("type = %q, want an error", msg.Type)
	}
	if !strings.Contains(string(msg.Payload), "telepathy") {
		t.Errorf("payload = %s, want it to name the unknown type", msg.Payload)
	}
}

// A client's own heartbeat is answered with an ack, and the same message resets the server's timer.
func TestWSDispatchAnswersAClientHeartbeat(t *testing.T) {
	cl, client, ackCh := wsPair(t, &fakeService{}, "the-token")
	go func() { cl.dispatch(wsMessage{MsgID: "m", Type: MsgHeartbeat}, ackCh) }()

	_, msg := readOneFrame(t, client)
	if msg.Type != MsgHeartbeatAck {
		t.Fatalf("type = %q, want %s", msg.Type, MsgHeartbeatAck)
	}
	select {
	case <-ackCh:
	case <-time.After(2 * time.Second):
		t.Error("a client heartbeat must also reset the server's timer")
	}
}

// The heartbeat loop gives up when no ack arrives: a client that cannot answer a keepalive is
// either gone or stuck, and holding the connection is a resource leak.
func TestWSHeartbeatClosesWhenTheAckNeverComes(t *testing.T) {
	cl, client, ackCh := wsPair(t, &fakeService{}, "the-token")
	cl.heartbeat = 5 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan struct{})
	go func() { cl.heartbeatLoop(ctx, ackCh); close(done) }()

	// The heartbeat frame arrives, and then - because nothing acks - the close frame.
	_ = client.SetReadDeadline(time.Now().Add(15 * time.Second))
	opcode, _, _, _, err := wsReadFrame(client)
	if err != nil {
		t.Fatalf("reading the heartbeat: %v", err)
	}
	if opcode != opText {
		t.Fatalf("opcode = %x, want a heartbeat text frame", opcode)
	}
	opcode, _, _, _, err = wsReadFrame(client)
	if err != nil {
		t.Fatalf("reading the close frame: %v", err)
	}
	if opcode != opClose {
		t.Fatalf("opcode = %x, want a close frame after the ack timed out", opcode)
	}
	select {
	case <-done:
	case <-time.After(15 * time.Second):
		t.Fatal("the heartbeat loop did not give up")
	}
}

// An acked heartbeat keeps the loop running rather than closing: the ack is what the loop exists to
// wait for.
func TestWSHeartbeatKeepsGoingWhenAcked(t *testing.T) {
	cl, client, ackCh := wsPair(t, &fakeService{}, "the-token")
	cl.heartbeat = 5 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { cl.heartbeatLoop(ctx, ackCh); close(done) }()

	// Ack every heartbeat for a while, then stop the loop from here.
	for i := 0; i < 3; i++ {
		_ = client.SetReadDeadline(time.Now().Add(5 * time.Second))
		opcode, _, _, _, err := wsReadFrame(client)
		if err != nil {
			t.Fatalf("reading heartbeat %d: %v", i, err)
		}
		if opcode != opText {
			t.Fatalf("heartbeat %d: opcode = %x, want text", i, opcode)
		}
		ackCh <- struct{}{}
	}
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the heartbeat loop must stop when its context is cancelled")
	}
}

// A heartbeat write that fails ends the loop rather than spinning: the connection is dead, and the
// read loop will notice on its own.
func TestWSHeartbeatStopsWhenTheWriteFails(t *testing.T) {
	server, client := net.Pipe()
	cl := &wsClient{conn: server, svc: &fakeService{}, heartbeat: 5 * time.Millisecond}
	_ = client.Close() // no reader: every write fails
	t.Cleanup(func() { _ = server.Close() })

	done := make(chan struct{})
	go func() { cl.heartbeatLoop(context.Background(), make(chan struct{}, 1)); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("a failed heartbeat write must end the loop")
	}
}

// send reports false when the connection is gone, which is what callers use to decide the client
// has left.
func TestWSSendReportsADeadConnection(t *testing.T) {
	server, client := net.Pipe()
	_ = client.Close()
	cl := &wsClient{conn: server, svc: &fakeService{}}
	t.Cleanup(func() { _ = server.Close() })

	if cl.send(newMsg(MsgNotification, nil, nil)) {
		t.Error("send must report false when the connection is gone")
	}
	if cl.writeFrame(opText, bytes.Repeat([]byte{'x'}, 4096)) {
		t.Error("writeFrame must report false when the connection is gone")
	}
	// And a second close is swallowed rather than panicking.
	cl.close(CloseNormal, "already closed")
}

// The welcome message is what tells a client the connection is live, and it carries IDLE so a
// client knows the agent is ready rather than busy.
func TestWSWelcomeCarriesIDLE(t *testing.T) {
	msg := newMsg(MsgAuthResponse, []string{FlagIdle}, map[string]any{"message": "connected"})
	if !msg.hasFlag(FlagIdle) {
		t.Errorf("flags = %v, want %s", msg.Flags, FlagIdle)
	}
	if msg.MsgID == "" || msg.Timestamp == "" {
		t.Error("the welcome needs an id and a timestamp like every other message")
	}
}

// newMsg never emits a null payload: a client that has to tell "empty" from "missing" is a client
// with a bug waiting to happen.
func TestWSNewMsgNeverSendsANullPayload(t *testing.T) {
	for name, payload := range map[string]any{"a nil payload": nil} {
		t.Run(name, func(t *testing.T) {
			msg := newMsg(MsgNotification, nil, payload)
			if string(msg.Payload) != "{}" {
				t.Errorf("payload = %s, want {} rather than null", msg.Payload)
			}
		})
	}
}

// isClosedConnErr tells the normal end-of-life errors apart from real protocol failures, which is
// what keeps a disconnected client out of the logs.
func TestWSIsClosedConnErr(t *testing.T) {
	cases := map[string]struct {
		err  error
		want bool
	}{
		"nil":                        {nil, false},
		"a protocol error":           {errors.New("the model is not configured"), false},
		"use of a closed connection": {errors.New("use of closed network connection"), true},
		"a broken pipe":              {errors.New("write tcp: broken pipe"), true},
		"a reset connection":         {errors.New("read tcp: connection reset by peer"), true},
	}
	for name, tc := range cases {
		if got := isClosedConnErr(tc.err); got != tc.want {
			t.Errorf("%s: isClosedConnErr = %v, want %v", name, got, tc.want)
		}
	}
}

// A client whose conn cannot even take a read deadline is closed rather than left in the loop.
func TestWSAReadLoopClosesWhenTheDeadlineCannotBeSet(t *testing.T) {
	// net.Pipe's SetReadDeadline always succeeds, so the branch is reached with a conn that is
	// already closed: the deadline call fails and the loop takes the close path.
	server, client := net.Pipe()
	_ = server.Close()
	cl := &wsClient{conn: server, svc: &fakeService{}}
	t.Cleanup(func() { _ = client.Close() })

	done := make(chan struct{})
	go func() { cl.readLoop(context.Background(), make(chan struct{}, 1)); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("the read loop must end when its connection is gone")
	}
}
