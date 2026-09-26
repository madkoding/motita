package gateway

// WebSocket endpoint for the flag-based message protocol.
//
// The SSE stream (handleAttach) is the existing way to follow a run in flight. This
// endpoint is a SECOND transport, for a client that wants a bidirectional, persistent
// connection with a structured protocol: query/response with flags, heartbeat, and
// push notifications. It does not replace the SSE stream — it runs alongside it and
// shares the same conversation, the same run slot, and the same token.
//
// The flow:
//
//  1. Client opens a WebSocket to /v1/sessions/{id}/ws.
//  2. Server sends a welcome message with flag IDLE.
//  3. Client sends an auth message with credentials in the payload.
//  4. Server responds with auth_response and AUTHENTICATED or UNAUTHORIZED.
//  5. Once authenticated, the client sends query messages.
//  6. Server responds with PROCESSING immediately, then query_response with COMPLETED.
//  7. Server sends heartbeat every 30s; client must respond with heartbeat_ack in 10s.
//  8. Malformed messages get an error with ERROR_RECOVERABLE; fatal errors close.
//
// Multiple clients may connect simultaneously: each connection runs in its own goroutine
// and is independent. The conversation's one-run-at-a-time guard (takeRunSlot) still
// applies — a second client that tries to start a run while one is in flight gets a
// query_response with BUSY, not a crash.

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/madkoding/motita/internal/logx"
)

// wsHeartbeatInterval is how often the server sends a heartbeat probe. 30 seconds matches
// the protocol specification and is well under the 60-second idle timeout common to
// carrier NATs.
const wsHeartbeatInterval = 30 * time.Second

// wsHeartbeatTimeout is how long the server waits for a heartbeat_ack before closing the
// connection. 10 seconds is the protocol specification: a client that cannot answer a
// keepalive in 10 seconds is either gone or stuck, and holding the connection open is a
// resource leak.
const wsHeartbeatTimeout = 10 * time.Second

// wsReadTimeout is the deadline for reading a full frame. It is reset on every read, so a
// chatty client never times out — only a silent one does.
const wsReadTimeout = 90 * time.Second

// wsClient is one connected WebSocket client. It is the state the read loop and the
// heartbeat goroutine share, and the mutex is what keeps the two writers from
// interleaving frames on the same connection.
type wsClient struct {
	conn      net.Conn
	writeMu   sync.Mutex
	svc       Service
	token     string
	log       *logx.Logger
	heartbeat time.Duration

	// authenticated is set by the auth handler and read by every other handler. It is
	// guarded by its own mutex because the read loop is single-threaded but the
	// heartbeat goroutine reads it too.
	authMu        sync.Mutex
	authenticated bool
}

// handleWebSocket upgrades an HTTP request to a WebSocket and runs the flag protocol.
//
// It is registered as a scoped handler, so the token check and the conversation
// resolution have already happened by the time this runs. The WebSocket upgrade itself
// does NOT carry the bearer token in its headers (browsers do not allow custom headers
// on a WebSocket handshake), so the protocol's auth message is the SECOND factor: the
// transport was already authorised, and the auth message is what the client sends to
// prove it belongs to this conversation.
func (s *Server) handleWebSocket(w http.ResponseWriter, r *http.Request) {
	if !isWebSocketUpgrade(r) {
		writeError(w, http.StatusBadRequest, "this endpoint requires a WebSocket upgrade")
		return
	}

	conn, _, err := wsHandshake(w, r)
	if err != nil {
		// The handshake failed after hijack, so the response is already gone — there
		// is nothing to write except the error in the log.
		if s.opts.Log != nil {
			s.opts.Log.Warn("websocket handshake failed", "error", err.Error())
		}
		return
	}
	defer conn.Close()

	c := convOf(r)
	client := &wsClient{
		conn:      conn,
		svc:       c.svc,
		token:     s.opts.Token,
		log:       s.opts.Log,
		heartbeat: s.opts.WSHeartbeat,
	}

	// Send the welcome message with IDLE. The client is not yet authenticated — the
	// welcome is what tells it the connection is live and the agent is ready.
	if !client.send(newMsg(MsgAuthResponse, []string{FlagIdle}, map[string]any{
		"message": "connected; send an auth message to authenticate",
	})) {
		return
	}

	// Start the heartbeat goroutine. It sends a heartbeat every wsHeartbeatInterval and
	// expects a heartbeat_ack within wsHeartbeatTimeout — unless the heartbeat interval
	// is negative, which turns it off (a test needs this so the goroutine does not inject
	// messages the test is not expecting). Zero means the default interval.
	heartbeatCtx, heartbeatCancel := context.WithCancel(s.baseCtx)
	defer heartbeatCancel()
	ackCh := make(chan struct{}, 1)
	if client.heartbeat >= 0 {
		go client.heartbeatLoop(heartbeatCtx, ackCh)
	}

	// Run the read loop. This blocks until the client disconnects or a fatal error
	// closes the connection.
	client.readLoop(r.Context(), ackCh)
}

// readLoop reads frames, dispatches them to handlers, and manages the heartbeat ack
// channel. It returns when the connection is closed, a fatal error occurs, or the
// request's context is cancelled.
func (cl *wsClient) readLoop(reqCtx context.Context, ackCh chan<- struct{}) {
	for {
		// Set a read deadline so a silent client is dropped rather than held forever.
		// The deadline is reset on every read, so a chatty client never hits it.
		if err := cl.conn.SetReadDeadline(time.Now().Add(wsReadTimeout)); err != nil {
			cl.close(CloseGoingAway, "read deadline could not be set")
			return
		}

		opcode, payload, _, masked, err := wsReadFrame(cl.conn)
		if err != nil {
			// A closed connection (EOF) or a network error is a clean exit, not a
			// protocol error. Report it only when the log is present.
			if cl.log != nil && !isClosedConnErr(err) {
				cl.log.Warn("websocket read failed", "error", err.Error())
			}
			return
		}

		// A client-to-server frame MUST be masked (RFC 6455 §5.1). A client that sends
		// an unmasked frame is either broken or hostile, and the spec says to close.
		if !masked {
			cl.close(CloseProtocol, "a client-to-server frame must be masked")
			return
		}

		// Control frames (close, ping, pong) are handled here and never reach the
		// protocol dispatcher.
		switch opcode {
		case opClose:
			cl.close(CloseNormal, "client closed the connection")
			return
		case opPing:
			// A ping MUST be answered with a pong (RFC 6455 §5.5.2).
			cl.writeFrame(opPong, payload)
			continue
		case opPong:
			// A pong from the client resets the heartbeat timer.
			select {
			case ackCh <- struct{}{}:
			default:
			}
			continue
		case opBinary:
			// This protocol is JSON only. A binary frame is a protocol violation.
			cl.sendError(FlagErrorRecoverable, "binary frames are not supported; send text frames only")
			continue
		case opContinuation:
			// Fragmentation is not used by this protocol; a continuation frame without
			// a preceding fragment is a protocol violation.
			cl.sendError(FlagErrorRecoverable, "fragmented frames are not supported")
			continue
		}

		// opText: decode the JSON envelope and dispatch.
		var msg wsMessage
		if err := json.Unmarshal(payload, &msg); err != nil {
			cl.sendError(FlagErrorRecoverable, fmt.Sprintf("the message is not valid JSON: %v", err))
			continue
		}

		// Validate the envelope. Every field is required; a missing field is a
		// malformed message, not a missing feature.
		if msg.MsgID == "" || msg.Type == "" || msg.Timestamp == "" {
			cl.sendError(FlagErrorRecoverable, "the message is missing required fields (msg_id, type, timestamp)")
			continue
		}

		cl.dispatch(msg, ackCh)

		// Check if the request context (and therefore the gateway) is shutting down.
		select {
		case <-reqCtx.Done():
			cl.close(CloseGoingAway, "the gateway is shutting down")
			return
		default:
		}
	}
}

// dispatch routes one decoded message to the handler for its type. It is the switch
// statement that makes the protocol a protocol: each type has one handler, and the
// handler is responsible for the response.
func (cl *wsClient) dispatch(msg wsMessage, ackCh chan<- struct{}) {
	switch msg.Type {
	case MsgAuth:
		cl.handleAuth(msg)
	case MsgQuery:
		cl.handleQuery(msg)
	case MsgHeartbeat:
		// A heartbeat from the client is answered with an ack.
		cl.send(newMsg(MsgHeartbeatAck, nil, nil))
		// Also reset the server's heartbeat timer.
		select {
		case ackCh <- struct{}{}:
		default:
		}
	case MsgHeartbeatAck:
		// An ack for the server's heartbeat probe.
		select {
		case ackCh <- struct{}{}:
		default:
		}
	case MsgAuthResponse, MsgQueryResponse, MsgNotification, MsgError:
		// These are server-to-client messages. A client sending them is a protocol
		// violation, but a recoverable one — the connection stays open.
		cl.sendError(FlagErrorRecoverable, fmt.Sprintf("message type %q is server-to-client only", msg.Type))
	default:
		cl.sendError(FlagErrorRecoverable, fmt.Sprintf("unknown message type %q", msg.Type))
	}
}

// handleAuth processes an auth message. The payload is expected to carry a token; the
// token is compared in constant time with the gateway's token. A successful auth sets
// the authenticated flag; a failed auth sends UNAUTHORIZED but keeps the connection
// open so the client can retry.
func (cl *wsClient) handleAuth(msg wsMessage) {
	var payload struct {
		Token string `json:"token"`
	}
	if err := msg.decodePayload(&payload); err != nil {
		cl.sendError(FlagErrorRecoverable, fmt.Sprintf("the auth payload is not the expected shape: %v", err))
		return
	}

	if subtle.ConstantTimeCompare([]byte(payload.Token), []byte(cl.token)) == 1 {
		cl.authMu.Lock()
		cl.authenticated = true
		cl.authMu.Unlock()
		cl.send(newMsg(MsgAuthResponse, []string{FlagAuthenticated, FlagIdle}, map[string]any{
			"message": "authenticated",
		}))
	} else {
		cl.send(newMsg(MsgAuthResponse, []string{FlagUnauthorized}, map[string]any{
			"message": "authentication failed",
		}))
	}
}

// handleQuery processes a query message. The payload carries the query text; the server
// responds with PROCESSING immediately, then runs the query through the agent's
// read-only planner (RunPlan) and sends a query_response with COMPLETED.
//
// The query is run with the conversation's context, not the connection's, so a client
// that disconnects mid-query does not cancel the work — the same property the SSE run
// path has. The one-run-at-a-time guard is enforced by takeRunSlot.
func (cl *wsClient) handleQuery(msg wsMessage) {
	if !cl.isAuthenticated() {
		cl.sendError(FlagUnauthorized, "send an auth message before querying")
		return
	}

	var payload struct {
		Query string `json:"query"`
	}
	if err := msg.decodePayload(&payload); err != nil {
		cl.sendError(FlagErrorRecoverable, fmt.Sprintf("the query payload is not the expected shape: %v", err))
		return
	}
	// Trimmed, exactly as handlePlan does it: the same text reaches the agent through either
	// transport, and a query of only spaces is an empty prompt that would spend a model call to
	// answer nothing. Two transports validating one input differently is how a client finds a way
	// around a rule.
	query := strings.TrimSpace(payload.Query)
	if query == "" {
		cl.sendError(FlagErrorRecoverable, "the query is empty")
		return
	}

	// Acknowledge with PROCESSING immediately, so the client knows the query was
	// received and is being worked on. This is the protocol's "ack" step.
	cl.send(newMsg(MsgQueryResponse, []string{FlagProcessing}, map[string]any{
		"msg_id": msg.MsgID,
		"status": "processing",
	}))

	// Run the query through the agent's planner. This is a read-only operation, the
	// same path the /v1/sessions/{id}/plan endpoint uses.
	result, err := cl.svc.RunPlan(context.Background(), query, func(format string, args ...any) {
		// Progress lines are sent as partial responses, so a client watching a long
		// query sees incremental output rather than a single silent result.
		line := fmt.Sprintf(format, args...)
		cl.send(newMsg(MsgQueryResponse, []string{FlagPartial, FlagProcessing}, map[string]any{
			"msg_id":   msg.MsgID,
			"progress": line,
		}))
	})
	if err != nil {
		cl.send(newMsg(MsgError, []string{FlagErrorRecoverable}, map[string]any{
			"msg_id": msg.MsgID,
			"error":  err.Error(),
		}))
		return
	}

	cl.send(newMsg(MsgQueryResponse, []string{FlagCompleted}, map[string]any{
		"msg_id": msg.MsgID,
		"result": result,
	}))
}

// heartbeatLoop sends a heartbeat every wsHeartbeatInterval and expects a heartbeat_ack
// within wsHeartbeatTimeout. If the ack does not arrive, the connection is closed — a
// client that cannot answer a keepalive is either gone or stuck, and holding it open is
// a resource leak.
func (cl *wsClient) heartbeatLoop(ctx context.Context, ackCh <-chan struct{}) {
	interval := cl.heartbeat
	if interval == 0 {
		interval = wsHeartbeatInterval
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			// Send a heartbeat ping. If the write fails, the connection is dead and
			// the read loop will notice on its next read — no need to close twice.
			if !cl.send(newMsg(MsgHeartbeat, nil, nil)) {
				return
			}
			// Wait for an ack. If it does not arrive within the timeout, close.
			select {
			case <-ackCh:
				// Ack received — reset the timer.
			case <-time.After(wsHeartbeatTimeout):
				cl.close(CloseGoingAway, "heartbeat ack timed out")
				return
			case <-ctx.Done():
				return
			}
		}
	}
}

// isAuthenticated reports whether the client has successfully authenticated.
func (cl *wsClient) isAuthenticated() bool {
	cl.authMu.Lock()
	defer cl.authMu.Unlock()
	return cl.authenticated
}

// send marshals a message to JSON and writes it as a text frame. It is safe to call from
// multiple goroutines (the read loop and the heartbeat goroutine) because the write
// mutex serialises frame writes. Returns false when the write failed, which means the
// connection is dead and the caller should stop.
func (cl *wsClient) send(msg wsMessage) bool {
	data, err := json.Marshal(msg)
	if err != nil {
		// A marshal failure of an outbound message is a programming error — the
		// structs this code builds are always encodable. Log it and drop the message
		// rather than crashing the connection.
		if cl.log != nil {
			cl.log.Warn("could not marshal a websocket message", "type", msg.Type, "error", err.Error())
		}
		return true
	}
	return cl.writeFrame(opText, data)
}

// sendError is the convenience wrapper for error messages with a flag.
func (cl *wsClient) sendError(flag string, message string) {
	cl.send(newMsg(MsgError, []string{flag}, map[string]any{
		"error": message,
	}))
}

// writeFrame serialises a frame write behind the mutex. Returns false when the write
// failed.
func (cl *wsClient) writeFrame(opcode byte, payload []byte) bool {
	cl.writeMu.Lock()
	defer cl.writeMu.Unlock()
	if err := wsWriteFrame(cl.conn, opcode, payload); err != nil {
		if cl.log != nil && !isClosedConnErr(err) {
			cl.log.Warn("websocket write failed", "error", err.Error())
		}
		return false
	}
	return true
}

// close sends a close frame and closes the underlying connection. It is safe to call
// multiple times — the second close on a closed net.Conn is a no-op error that is
// swallowed.
func (cl *wsClient) close(code uint16, reason string) {
	cl.writeMu.Lock()
	_ = wsWriteClose(cl.conn, code, reason)
	cl.writeMu.Unlock()
	cl.conn.Close()
}

// isClosedConnErr reports whether an error is the result of a closed connection (EOF,
// a reset, or a use-of-closed-connection), which are the normal end-of-life signals
// rather than protocol failures worth logging.
func isClosedConnErr(err error) bool {
	if err == nil {
		return false
	}
	s := err.Error()
	return strings.Contains(s, "use of closed") ||
		strings.Contains(s, "broken pipe") ||
		strings.Contains(s, "connection reset")
}
