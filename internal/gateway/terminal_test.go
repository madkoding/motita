package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/madkoding/motita/internal/sandbox"
)

// fakeTerm is a terminal the test drives: what the gateway writes lands in typed, what the test
// prints reaches the gateway's reads, and the shell ends when exit is sent or the context is done.
type fakeTerm struct {
	ctx      context.Context
	outR     *io.PipeReader
	outW     *io.PipeWriter
	mu       sync.Mutex
	typed    strings.Builder
	sizes    [][2]int
	writeErr error
	exit     chan int
	closed   chan struct{}
	once     sync.Once
}

func newFakeTerm(ctx context.Context) *fakeTerm {
	r, w := io.Pipe()
	return &fakeTerm{ctx: ctx, outR: r, outW: w, exit: make(chan int, 1), closed: make(chan struct{})}
}

func (f *fakeTerm) Read(p []byte) (int, error) { return f.outR.Read(p) }
func (f *fakeTerm) Write(p []byte) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.writeErr != nil {
		return 0, f.writeErr
	}
	f.typed.Write(p)
	return len(p), nil
}
func (f *fakeTerm) Close() error {
	f.once.Do(func() { close(f.closed) })
	return nil
}
func (f *fakeTerm) Resize(cols, rows int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sizes = append(f.sizes, [2]int{cols, rows})
	return nil
}
func (f *fakeTerm) Wait() (int, error) {
	select {
	case code := <-f.exit:
		f.outW.Close()
		return code, nil
	case <-f.ctx.Done():
		return -1, nil
	}
}

func (f *fakeTerm) typedText() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.typed.String()
}

// termService is a session whose terminal is a fakeTerm, or the error it is given.
type termService struct {
	fakeService
	mu    sync.Mutex
	term  *fakeTerm
	size  [2]int
	err   error
	setup func(*fakeTerm)
	ready chan struct{}
}

func (s *termService) OpenTerminal(ctx context.Context, cols, rows int) (sandbox.TerminalSession, error) {
	if s.err != nil {
		return nil, s.err
	}
	t := newFakeTerm(ctx)
	if s.setup != nil {
		s.setup(t)
	}
	s.mu.Lock()
	s.term, s.size = t, [2]int{cols, rows}
	s.mu.Unlock()
	if s.ready != nil {
		close(s.ready)
	}
	return t, nil
}

func (s *termService) current() *fakeTerm {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.term
}

func terminalServer(t *testing.T, svc Service) *Server {
	t.Helper()
	return newTestServer(t, svc, func(o *Options) { o.WSHeartbeat = -1 })
}

// readJSON reads frames until a text one comes, and decodes it.
func (c *wsTestClient) readJSON(t *testing.T) map[string]any {
	t.Helper()
	for {
		data, op := c.readText(t)
		if op != opText {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(data), &m); err != nil {
			t.Fatalf("not JSON: %q", data)
		}
		return m
	}
}

func TestATerminalCarriesKeysOutputResizesAndItsEnd(t *testing.T) {
	svc := &termService{ready: make(chan struct{})}
	srv := terminalServer(t, svc)
	c := dialWSPath(t, srv, "/v1/sessions/"+DefaultSession+"/terminal?cols=100&rows=30", testToken)
	defer c.close()
	<-svc.ready
	term := svc.current()
	if svc.size != [2]int{100, 30} {
		t.Errorf("size %v", svc.size)
	}

	conv := srv.conversationOf(DefaultSession)
	st := conv.status()
	if !st.Terminal || st.Running {
		t.Errorf("an open terminal is not the agent working: %+v", st)
	}
	if conv.takeRunSlot() || conv.refusalOfRun() != msgTerminalOpen {
		t.Errorf("a run must wait for the terminal: %q", conv.refusalOfRun())
	}

	c.writeFrame(opBinary, []byte("ls "), true)
	c.writeFrame(opContinuation, []byte("-la\n"), true)
	c.writeFrame(opText, []byte(`{"type":"resize","cols":120,"rows":40}`), true)
	c.writeFrame(opText, []byte(`not json`), true)
	// A continuation after a text message is not input.
	c.writeFrame(opContinuation, []byte("ignored"), true)
	c.writeFrame(opPong, nil, true)
	c.writeFrame(opPing, []byte("hb"), true)
	if data, op := c.readText(t); op != opPong || data != "hb" {
		t.Errorf("a ping is answered: %x %q", op, data)
	}
	waitFor(t, func() bool { return term.typedText() == "ls -la\n" })
	term.mu.Lock()
	sizes := term.sizes
	term.mu.Unlock()
	if len(sizes) != 1 || sizes[0] != [2]int{120, 40} {
		t.Errorf("resizes %v", sizes)
	}

	go term.outW.Write([]byte("total 0\n"))
	if data, op := c.readText(t); op != opBinary || data != "total 0\n" {
		t.Errorf("output %x %q", op, data)
	}
	term.exit <- 3
	if m := c.readJSON(t); m["type"] != "exit" || m["code"] != float64(3) {
		t.Errorf("end %v", m)
	}
	if _, op := c.readText(t); op != opClose {
		t.Errorf("then the close, got %x", op)
	}
	waitFor(t, func() bool { return !conv.isRunning() && !conv.hasTerminal() })
}

func TestATerminalIsRefusedWithItsReason(t *testing.T) {
	cases := []struct {
		name   string
		svc    *termService
		before func(*conversation)
		want   string
	}{
		{"integrated", &termService{}, func(c *conversation) { c.merged = true }, msgSessionIntegrated},
		{"busy", &termService{}, func(c *conversation) { c.takeRunSlot() }, "the agent is working"},
		{"closing", &termService{}, func(c *conversation) {
			c.takeRunSlot()
			c.beginClosing()
		}, msgSessionClosing},
		{"cannot open", &termService{err: errors.New("no pty here")}, func(*conversation) {}, "no pty here"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := terminalServer(t, tc.svc)
			tc.before(srv.conversationOf(DefaultSession))
			c := dialWSPath(t, srv, "/v1/sessions/"+DefaultSession+"/terminal", testToken)
			defer c.close()
			m := c.readJSON(t)
			if m["type"] != "error" || !strings.Contains(m["message"].(string), tc.want) {
				t.Errorf("got %v", m)
			}
		})
	}
}

func TestTheTerminalEndpointNeedsAnUpgradeAndAService(t *testing.T) {
	srv := terminalServer(t, &termService{})
	req, _ := http.NewRequest(http.MethodGet, srv.BaseURL()+"/v1/sessions/"+DefaultSession+"/terminal", nil)
	req.Header.Set("Authorization", "Bearer "+testToken)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusBadRequest {
		t.Errorf("a plain GET: %d", res.StatusCode)
	}

	plain := terminalServer(t, &fakeService{})
	if code := upgradeCode(t, plain); code != http.StatusNotImplemented {
		t.Errorf("a service without a terminal: %d", code)
	}
}

// upgradeCode is the status the terminal endpoint answers an upgrade with before it hijacks.
func upgradeCode(t *testing.T, srv *Server) int {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/terminal", nil)
	req.Header.Set("Upgrade", "websocket")
	req.Header.Set("Connection", "Upgrade")
	req.Header.Set("Sec-WebSocket-Key", "dGhlIHNhbXBsZSBub25jZQ==")
	req.Header.Set("Sec-WebSocket-Version", "13")
	c, _ := srv.lookup(DefaultSession)
	req = req.WithContext(context.WithValue(req.Context(), conversationKey, c))
	w := httptest.NewRecorder()
	srv.handleTerminal(w, req)
	return w.Code
}

func TestATerminalHandshakeThatFailsIsLogged(t *testing.T) {
	sink := newLogSink(t)
	srv := newTestServer(t, &termService{}, func(o *Options) { o.Log = sink.log })
	c, _ := srv.lookup(DefaultSession)
	req := httptest.NewRequest(http.MethodGet, "/terminal", nil)
	req = req.WithContext(context.WithValue(req.Context(), conversationKey, c))
	req.Header.Set("Upgrade", "websocket")
	req.Header.Set("Connection", "Upgrade")
	req.Header.Set("Sec-WebSocket-Key", "dGhlIHNhbXBsZSBub25jZQ==")
	req.Header.Set("Sec-WebSocket-Version", "13")
	srv.handleTerminal(&plainWriter{header: make(http.Header)}, req)
	if !strings.Contains(strings.Join(sink.lines(t), "\n"), "terminal handshake failed") {
		t.Errorf("log %v", sink.lines(t))
	}
	// Without a log the failure is only the return.
	quiet := newTestServer(t, &termService{}, func(o *Options) { o.Log = nil })
	qc, _ := quiet.lookup(DefaultSession)
	quiet.handleTerminal(&plainWriter{header: make(http.Header)},
		req.WithContext(context.WithValue(context.Background(), conversationKey, qc)))
}

// A client that goes away ends the shell; output that arrives after it is dropped.
func TestATerminalWhoseClientLeavesEndsItsShell(t *testing.T) {
	svc := &termService{ready: make(chan struct{})}
	srv := terminalServer(t, svc)
	c := dialWSPath(t, srv, "/v1/sessions/"+DefaultSession+"/terminal", testToken)
	<-svc.ready
	term := svc.current()
	c.close()
	waitFor(t, func() bool { return term.ctx.Err() != nil })
	conv := srv.conversationOf(DefaultSession)
	waitFor(t, func() bool { return !conv.isRunning() })
	// The output pump is still reading: what it reads now has no connection to go to.
	term.outW.Write([]byte("late"))
	time.Sleep(50 * time.Millisecond)
}

// The protocol's faults end the terminal: an unmasked frame, a key the shell cannot take, a close.
func TestATerminalEndsOnTheProtocolsFaults(t *testing.T) {
	prev := terminalDrain
	terminalDrain = 10 * time.Millisecond
	t.Cleanup(func() { terminalDrain = prev })
	for name, send := range map[string]func(*wsTestClient){
		"unmasked": func(c *wsTestClient) { c.writeFrame(opBinary, []byte("x"), false) },
		"close":    func(c *wsTestClient) { c.writeFrame(opClose, nil, true) },
		"key":      func(c *wsTestClient) { c.writeFrame(opBinary, []byte("x"), true) },
	} {
		t.Run(name, func(t *testing.T) {
			svc := &termService{ready: make(chan struct{}),
				setup: func(f *fakeTerm) { f.writeErr = errors.New("the shell is gone") }}
			srv := terminalServer(t, svc)
			c := dialWSPath(t, srv, "/v1/sessions/"+DefaultSession+"/terminal", testToken)
			defer c.close()
			<-svc.ready
			send(c)
			conv := srv.conversationOf(DefaultSession)
			waitFor(t, func() bool { return !conv.isRunning() })
		})
	}
}

func TestATerminalPingsItsClient(t *testing.T) {
	prev := terminalPing
	terminalPing = 10 * time.Millisecond
	t.Cleanup(func() { terminalPing = prev })
	svc := &termService{ready: make(chan struct{})}
	srv := terminalServer(t, svc)
	c := dialWSPath(t, srv, "/v1/sessions/"+DefaultSession+"/terminal", testToken)
	defer c.close()
	<-svc.ready
	if _, op := c.readText(t); op != opPing {
		t.Errorf("got %x", op)
	}
	svc.current().exit <- 0
}

// Deleting a session stops its terminal and waits for it, within the deletion's bound.
func TestADeletionStopsTheTerminal(t *testing.T) {
	svc := &termService{ready: make(chan struct{})}
	srv := terminalServer(t, svc)
	c := dialWSPath(t, srv, "/v1/sessions/"+DefaultSession+"/terminal", testToken)
	defer c.close()
	<-svc.ready
	conv := srv.conversationOf(DefaultSession)
	if stopped, settled := conv.stopRunForDeletion(5 * time.Second); !stopped || !settled {
		t.Errorf("stopped %v settled %v", stopped, settled)
	}
	if stopped, settled := conv.stopTerminal(time.Second); stopped || !settled {
		t.Errorf("nothing left to stop: %v %v", stopped, settled)
	}

	stuck := &conversation{}
	stuck.holdTerminal(func() {})
	if stopped, settled := stuck.stopTerminal(10 * time.Millisecond); !stopped || settled {
		t.Errorf("a terminal that does not end: %v %v", stopped, settled)
	}
}

func TestTerminalSizeFallsBackOnWhatItCannotRead(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/?cols=x&rows=12", nil)
	if terminalSize(r, "cols", 80) != 80 || terminalSize(r, "rows", 24) != 12 {
		t.Error("sizes")
	}
}
