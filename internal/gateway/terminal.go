package gateway

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/madkoding/motita/internal/sandbox"
)

// The interactive terminal: a person's shell on a pseudo-terminal, carried over a WebSocket.
//
// It is for what a typed line cannot do - a command that asks for input, an editor, a REPL - and so
// it speaks no message protocol, only the terminal's own bytes:
//
//	client → server  binary frames: what was typed, as the terminal's input
//	                 text frames:   {"type":"resize","cols":N,"rows":M}
//	server → client  binary frames: what the terminal printed
//	                 text frames:   {"type":"exit","code":N} when the shell ends,
//	                                {"type":"error","message":"..."} when it could not open
//
// The terminal HOLDS the conversation's run slot while it is open, as a typed command does: the
// agent and the person would otherwise be working in the same folder at once. What is sent to
// the agent meanwhile waits in the queue, and starts when the terminal closes.

// terminalOpener is what a session's service offers to open a terminal. Like shellRunner it is
// optional: a service that cannot (a client of another gateway) is answered "not implemented".
type terminalOpener interface {
	OpenTerminal(ctx context.Context, cols, rows int) (sandbox.TerminalSession, error)
}

// terminalPing is how often the server pings an open terminal's client. A browser answers on its
// own, and the answer is a frame that renews the read deadline: a phone that went away without a
// close is noticed within wsReadTimeout, and the run slot it held is given back.
var terminalPing = 30 * time.Second

// terminalDrain is how long the end of a terminal waits for the last of its output (a variable so
// a test can shorten it).
var terminalDrain = 2 * time.Second

// msgTerminalOpen is the refusal of a run while a person's terminal holds the slot.
const msgTerminalOpen = "a terminal is open in this session: the agent works once it is closed (type exit)"

// terminalHold is an open terminal, as the conversation knows it: how to end it, and when it ended.
type terminalHold struct {
	stop context.CancelFunc
	done chan struct{}
}

// holdTerminal records the terminal that took the run slot.
func (c *conversation) holdTerminal(stop context.CancelFunc) *terminalHold {
	h := &terminalHold{stop: stop, done: make(chan struct{})}
	c.stateMu.Lock()
	c.terminal = h
	c.stateMu.Unlock()
	return h
}

// dropTerminal forgets h, which has ended.
func (c *conversation) dropTerminal(h *terminalHold) {
	c.stateMu.Lock()
	c.terminal = nil
	c.stateMu.Unlock()
	close(h.done)
}

// hasTerminal reports whether a terminal is open in the conversation.
func (c *conversation) hasTerminal() bool {
	c.stateMu.Lock()
	defer c.stateMu.Unlock()
	return c.terminal != nil
}

// stopTerminal ends the open terminal for a deletion and waits, as stopRunForDeletion does for a
// run: whether there was one, and whether it ended in time.
func (c *conversation) stopTerminal(timeout time.Duration) (bool, bool) {
	c.stateMu.Lock()
	h := c.terminal
	c.stateMu.Unlock()
	if h == nil {
		return false, true
	}
	h.stop()
	select {
	case <-h.done:
		return true, true
	case <-time.After(timeout):
		return true, false
	}
}

// terminalSize reads one dimension the client asked for, def when it asked for none.
func terminalSize(r *http.Request, name string, def int) int {
	n, err := strconv.Atoi(r.URL.Query().Get(name))
	if err != nil {
		return def
	}
	return n
}

// handleTerminal opens the session's terminal for the person on the other end of the WebSocket.
func (s *Server) handleTerminal(w http.ResponseWriter, r *http.Request) {
	if !isWebSocketUpgrade(r) {
		writeError(w, http.StatusBadRequest, "this endpoint requires a WebSocket upgrade")
		return
	}
	c := convOf(r)
	op, ok := c.svc.(terminalOpener)
	if !ok {
		writeError(w, http.StatusNotImplemented, "this session cannot open a terminal")
		return
	}
	conn, _, err := wsHandshake(w, r)
	if err != nil {
		if s.opts.Log != nil {
			s.opts.Log.Warn("terminal handshake failed", "error", err.Error())
		}
		return
	}
	tc := &termConn{conn: conn}
	defer conn.Close()

	// Refusals come after the upgrade, as a message: a browser is not shown the status of a
	// refused upgrade, and the person must read why the terminal did not open.
	if c.isMerged() {
		tc.fail(msgSessionIntegrated)
		return
	}
	if !c.takeRunSlot() {
		reason := c.refusalOfRun()
		if reason == msgRunBusy {
			reason = "the agent is working in this session: the terminal opens once it has finished"
		}
		tc.fail(reason)
		return
	}
	defer s.releaseAndDrain(c)

	ctx, cancel := context.WithCancel(s.baseCtx)
	defer cancel()
	hold := c.holdTerminal(cancel)
	defer c.dropTerminal(hold)

	term, err := op.OpenTerminal(ctx, terminalSize(r, "cols", 80), terminalSize(r, "rows", 24))
	if err != nil {
		tc.fail(err.Error())
		return
	}
	defer term.Close()
	// The input read is blocked on the connection: ending the terminal for any reason (the
	// shell's exit, a deletion, the gateway's shutdown) closes it so the read returns.
	stopClosing := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stopClosing()

	output := make(chan struct{})
	go func() {
		defer close(output)
		tc.pumpOutput(term)
	}()
	go func() {
		tc.pumpInput(term)
		cancel()
	}()
	go tc.ping(ctx)

	code, _ := term.Wait()
	select {
	case <-output:
	case <-time.After(terminalDrain):
	}
	tc.sendJSON(map[string]any{"type": "exit", "code": code})
	tc.closeWith(CloseNormal, "")
}

// termConn is the WebSocket of one terminal, with the lock that keeps its writers apart.
type termConn struct {
	conn    net.Conn
	writeMu sync.Mutex
}

// write sends one frame, and reports whether it went.
func (tc *termConn) write(opcode byte, payload []byte) bool {
	tc.writeMu.Lock()
	defer tc.writeMu.Unlock()
	_ = tc.conn.SetWriteDeadline(time.Now().Add(wsWriteTimeout))
	return wsWriteFrame(tc.conn, opcode, payload) == nil
}

func (tc *termConn) sendJSON(v map[string]any) {
	data, _ := json.Marshal(v)
	tc.write(opText, data)
}

func (tc *termConn) closeWith(code uint16, reason string) {
	tc.writeMu.Lock()
	defer tc.writeMu.Unlock()
	_ = tc.conn.SetWriteDeadline(time.Now().Add(wsWriteTimeout))
	_ = wsWriteClose(tc.conn, code, reason)
}

// fail tells the client why there is no terminal, and closes.
func (tc *termConn) fail(message string) {
	tc.sendJSON(map[string]any{"type": "error", "message": message})
	tc.closeWith(CloseNormal, "")
}

// pumpOutput sends what the terminal prints until it has nothing more (the shell is gone) or the
// client stopped taking it.
func (tc *termConn) pumpOutput(term sandbox.TerminalSession) {
	buf := make([]byte, 32<<10)
	for {
		n, err := term.Read(buf)
		if n > 0 && !tc.write(opBinary, buf[:n]) {
			return
		}
		if err != nil {
			return
		}
	}
}

// shellRoutes are the two ways a person types into a session's folder: one command at a time, and
// the interactive terminal.
func (s *Server) shellRoutes(mux *http.ServeMux, scoped func(http.HandlerFunc) http.Handler) {
	mux.Handle("POST /v1/sessions/{id}/shell", scoped(s.handleShell))
	mux.Handle("GET /v1/sessions/{id}/terminal", scoped(s.handleTerminal))
}

// terminalControl is a text frame from the client.
type terminalControl struct {
	Type string `json:"type"`
	Cols int    `json:"cols"`
	Rows int    `json:"rows"`
}

// pumpInput hands what the client types to the terminal and applies its resizes, until the client
// closes, goes silent past the read deadline, or breaks the protocol.
func (tc *termConn) pumpInput(term sandbox.TerminalSession) {
	// A binary message may come in fragments; its continuations are input too, a text one's are
	// not (a control message is a few bytes and never fragmented by a browser).
	binaryMessage := false
	for {
		_ = tc.conn.SetReadDeadline(time.Now().Add(wsReadTimeout))
		opcode, payload, _, masked, err := wsReadFrame(tc.conn)
		if err != nil {
			return
		}
		if !masked {
			tc.closeWith(CloseProtocol, "client frames must be masked")
			return
		}
		if !tc.handleFrame(term, opcode, payload, &binaryMessage) {
			return
		}
	}
}

// handleFrame acts on one frame from the client; it says false when the connection is over.
func (tc *termConn) handleFrame(term sandbox.TerminalSession, opcode byte, payload []byte, binaryMessage *bool) bool {
	switch opcode {
	case opBinary, opContinuation:
		*binaryMessage = opcode == opBinary || *binaryMessage
		return !*binaryMessage || writeInput(term, payload)
	case opText:
		*binaryMessage = false
		resizeFrom(term, payload)
	case opPing:
		tc.write(opPong, payload)
	case opClose:
		return false
	default:
		// A pong, or an opcode nobody sends: any frame renews the deadline, nothing more.
	}
	return true
}

// writeInput types payload into the terminal, and says whether it could.
func writeInput(term sandbox.TerminalSession, payload []byte) bool {
	_, err := term.Write(payload)
	return err == nil
}

// resizeFrom applies a resize control message; any other text is ignored.
func resizeFrom(term sandbox.TerminalSession, payload []byte) {
	var ctl terminalControl
	if json.Unmarshal(payload, &ctl) == nil && ctl.Type == "resize" {
		_ = term.Resize(ctl.Cols, ctl.Rows)
	}
}

// ping keeps the connection proven alive while the terminal is open.
func (tc *termConn) ping(ctx context.Context) {
	ticker := time.NewTicker(terminalPing)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			tc.write(opPing, nil)
		}
	}
}
