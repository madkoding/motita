package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// fakeServer is the other end of a pipe: it reads what the client writes and lets a test
// script the answers.
type fakeServer struct {
	t   *testing.T
	in  *bufio.Scanner
	out io.WriteCloser
	// r and w are the client's ends.
	r io.Reader
	w io.Writer
	c io.Closer
}

func newFake(t *testing.T) *fakeServer {
	t.Helper()
	cr, sw := io.Pipe() // server writes, client reads
	sr, cw := io.Pipe() // client writes, server reads
	f := &fakeServer{t: t, in: bufio.NewScanner(sr), out: sw, r: cr, w: cw, c: cw}
	t.Cleanup(func() { _ = sw.Close(); _ = cw.Close() })
	return f
}

func (f *fakeServer) read() message {
	f.t.Helper()
	if !f.in.Scan() {
		f.t.Fatalf("the client wrote nothing: %v", f.in.Err())
	}
	var m message
	if err := json.Unmarshal(f.in.Bytes(), &m); err != nil {
		f.t.Fatal(err)
	}
	return m
}

func (f *fakeServer) send(v any) {
	f.t.Helper()
	raw, _ := json.Marshal(v)
	if _, err := f.out.Write(append(raw, '\n')); err != nil {
		f.t.Fatal(err)
	}
}

func (f *fakeServer) answer(m message, result any) {
	f.send(map[string]any{"jsonrpc": "2.0", "id": json.RawMessage(m.ID), "result": result})
}

// handshake answers initialize and swallows the initialized notice.
func (f *fakeServer) handshake() {
	m := f.read()
	if m.Method != "initialize" {
		f.t.Fatalf("first message %q", m.Method)
	}
	f.answer(m, map[string]any{
		"protocolVersion": "2025-06-18", "capabilities": map[string]any{"tools": map[string]any{}},
		"serverInfo": map[string]any{"name": "fake", "version": "9"}, "instructions": "be nice",
	})
	if n := f.read(); n.Method != "notifications/initialized" {
		f.t.Fatalf("second message %q", n.Method)
	}
}

func connect(t *testing.T, h Handler) (*Client, *fakeServer) {
	t.Helper()
	f := newFake(t)
	done := make(chan struct{})
	go func() { f.handshake(); close(done) }()
	c, err := NewClient(context.Background(), "fake", f.r, f.w, f.c, h)
	if err != nil {
		t.Fatal(err)
	}
	<-done
	t.Cleanup(func() { _ = c.Close() })
	return c, f
}

func TestHandshake(t *testing.T) {
	c, _ := connect(t, Handler{})
	if c.Name() != "fake" || c.Server.Name != "fake" || c.Server.Protocol != "2025-06-18" ||
		c.Server.Instructions != "be nice" || len(c.Server.Capabilities) == 0 {
		t.Fatalf("server = %+v", c.Server)
	}
}

func TestHandshakeAdvertisesElicitationOnlyWithAHandler(t *testing.T) {
	for _, withHandler := range []bool{false, true} {
		f := newFake(t)
		got := make(chan message, 1)
		go func() {
			m := f.read()
			got <- m
			f.answer(m, map[string]any{})
			f.read()
		}()
		h := Handler{}
		if withHandler {
			h.Elicit = func(context.Context, ElicitRequest) ElicitResult { return ElicitResult{} }
		}
		c, err := NewClient(context.Background(), "x", f.r, f.w, f.c, h)
		if err != nil {
			t.Fatal(err)
		}
		m := <-got
		if has := strings.Contains(string(m.Params), "elicitation"); has != withHandler {
			t.Fatalf("handler=%v params=%s", withHandler, m.Params)
		}
		_ = c.Close()
	}
}

func TestHandshakeFails(t *testing.T) {
	f := newFake(t)
	go func() {
		m := f.read()
		f.send(map[string]any{"jsonrpc": "2.0", "id": json.RawMessage(m.ID), "error": map[string]any{"code": -1, "message": "no"}})
	}()
	if _, err := NewClient(context.Background(), "x", f.r, f.w, nil, Handler{}); err == nil ||
		!strings.Contains(err.Error(), "handshake") || !strings.Contains(err.Error(), "mcp error -1: no") {
		t.Fatalf("err = %v", err)
	}
}

func TestToolsPagesAndCall(t *testing.T) {
	c, f := connect(t, Handler{})
	go func() {
		m := f.read()
		f.answer(m, map[string]any{"tools": []map[string]any{{"name": "a"}}, "nextCursor": "p2"})
		m = f.read()
		if !strings.Contains(string(m.Params), "p2") {
			t.Errorf("cursor missing: %s", m.Params)
		}
		f.answer(m, map[string]any{"tools": []map[string]any{{"name": "b", "description": "d"}}})
		m = f.read()
		f.answer(m, map[string]any{"content": []map[string]any{{"type": "text", "text": "hi"}}})
	}()
	tools, err := c.Tools(context.Background())
	if err != nil || len(tools) != 2 || tools[1].Description != "d" {
		t.Fatalf("tools = %v, %v", tools, err)
	}
	res, err := c.Call(context.Background(), "a", nil)
	if err != nil || res.Text(100) != "hi" {
		t.Fatalf("call = %+v, %v", res, err)
	}
}

func TestToolsError(t *testing.T) {
	c, f := connect(t, Handler{})
	go func() {
		m := f.read()
		f.send(map[string]any{"jsonrpc": "2.0", "id": json.RawMessage(m.ID), "error": map[string]any{"code": -32601, "message": "nope"}})
	}()
	if _, err := c.Tools(context.Background()); err == nil || !strings.Contains(err.Error(), "nope") {
		t.Fatalf("err = %v", err)
	}
}

func TestResultText(t *testing.T) {
	r := CallResult{Content: []Content{
		{Type: "text", Text: "one"},
		{Type: "image", MIMEType: "image/png"},
		{Type: "resource", Resource: &struct {
			URI  string `json:"uri"`
			Text string `json:"text"`
		}{URI: "file:///x", Text: "body"}},
	}}
	got := r.Text(1000)
	for _, want := range []string{"one", "[image image/png  not shown]", "file:///x\nbody"} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in %q", want, got)
		}
	}
	if got := (CallResult{StructuredContent: json.RawMessage(`{"a":1}`)}).Text(100); got != `{"a":1}` {
		t.Fatalf("structured = %q", got)
	}
	if got := r.Text(3); !strings.HasPrefix(got, "one\n[cut: the answer is longer than 3 bytes]") {
		t.Fatalf("cut = %q", got)
	}
}

func TestRequestCancelled(t *testing.T) {
	c, f := connect(t, Handler{})
	ctx, cancel := context.WithCancel(context.Background())
	got := make(chan message, 2)
	go func() {
		got <- f.read()
		got <- f.read()
	}()
	go func() { <-time.After(20 * time.Millisecond); cancel() }()
	if err := c.Request(ctx, "slow", map[string]any{}, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v", err)
	}
	<-got
	if m := <-got; m.Method != "notifications/cancelled" {
		t.Fatalf("second = %q", m.Method)
	}
}

func TestRequestNoResultTarget(t *testing.T) {
	c, f := connect(t, Handler{})
	go func() { f.answer(f.read(), map[string]any{}) }()
	if err := c.Request(context.Background(), "x", map[string]any{}, nil); err != nil {
		t.Fatal(err)
	}
}

func TestRequestUnmarshalableParams(t *testing.T) {
	c, _ := connect(t, Handler{})
	if err := c.Request(context.Background(), "x", make(chan int), nil); err == nil {
		t.Fatal("want a marshal error")
	}
}

func TestRequestBadResult(t *testing.T) {
	c, f := connect(t, Handler{})
	go func() { f.answer(f.read(), "text") }()
	var out struct{ A int }
	if err := c.Request(context.Background(), "x", map[string]any{}, &out); err == nil {
		t.Fatal("want a decode error")
	}
}

func TestServerGone(t *testing.T) {
	c, f := connect(t, Handler{})
	go func() { f.read(); _ = f.out.Close() }()
	if err := c.Request(context.Background(), "x", map[string]any{}, nil); err == nil ||
		!strings.Contains(err.Error(), "closed") {
		t.Fatalf("err = %v", err)
	}
	if err := c.Request(context.Background(), "y", map[string]any{}, nil); err == nil ||
		!strings.Contains(err.Error(), "not connected") {
		t.Fatalf("after = %v", err)
	}
}

func TestWriteFails(t *testing.T) {
	c, f := connect(t, Handler{})
	_ = f.c.Close()
	if err := c.Request(context.Background(), "x", map[string]any{}, nil); err == nil {
		t.Fatal("want a write error")
	}
}

func TestClosedClient(t *testing.T) {
	c, _ := connect(t, Handler{})
	_ = c.Close()
	if err := c.Request(context.Background(), "x", map[string]any{}, nil); err == nil {
		t.Fatal("want an error after Close")
	}
}

func TestCloseWithoutCloser(t *testing.T) {
	c := newClient("x", io.Discard, strings.NewReader(""), nil, Handler{})
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestNoiseIsIgnored(t *testing.T) {
	c, f := connect(t, Handler{})
	go func() {
		m := f.read()
		_, _ = f.out.Write([]byte("not json\n"))
		f.send(map[string]any{"jsonrpc": "2.0", "id": "text-id", "result": 1})
		f.send(map[string]any{"jsonrpc": "2.0", "id": 9999, "result": 1})
		f.send(map[string]any{"jsonrpc": "2.0", "method": "notifications/message", "params": map[string]any{}})
		f.answer(m, map[string]any{})
	}()
	if err := c.Request(context.Background(), "x", map[string]any{}, nil); err != nil {
		t.Fatal(err)
	}
}

func TestServerRequests(t *testing.T) {
	_, f := connect(t, Handler{})
	f.send(map[string]any{"jsonrpc": "2.0", "id": "p1", "method": "ping"})
	m := f.read()
	if string(m.ID) != `"p1"` || m.Error != nil {
		t.Fatalf("ping = %+v", m)
	}
	f.send(map[string]any{"jsonrpc": "2.0", "id": 5, "method": "sampling/createMessage"})
	m = f.read()
	if m.Error == nil || m.Error.Code != -32601 || !strings.Contains(m.Error.Error(), "sampling") {
		t.Fatalf("unsupported = %+v", m)
	}
}

func TestElicitation(t *testing.T) {
	var seen ElicitRequest
	actions := []string{Accept, Decline, "weird"}
	n := 0
	c, f := connect(t, Handler{Elicit: func(ctx context.Context, r ElicitRequest) ElicitResult {
		if ctx == nil {
			t.Error("no context")
		}
		seen = r
		a := actions[n%len(actions)]
		n++
		return ElicitResult{Action: a, Content: map[string]any{"ok": true}}
	}})
	_ = c
	want := []struct {
		action  string
		content bool
	}{{Accept, true}, {Decline, false}, {Cancel, false}}
	for i, w := range want {
		f.send(map[string]any{"jsonrpc": "2.0", "id": i + 1, "method": "elicitation/create",
			"params": map[string]any{"message": "sure?", "requestedSchema": map[string]any{"type": "object"}}})
		m := f.read()
		var res ElicitResult
		_ = json.Unmarshal(m.Result, &res)
		if res.Action != w.action || (res.Content != nil) != w.content {
			t.Fatalf("answer %d = %+v", i, res)
		}
	}
	if seen.Server != "fake" || seen.Mode != "form" || seen.Message != "sure?" {
		t.Fatalf("seen = %+v", seen)
	}
}

func TestElicitationUsesTheCallContext(t *testing.T) {
	type key struct{}
	var got any
	c, f := connect(t, Handler{Elicit: func(ctx context.Context, r ElicitRequest) ElicitResult {
		got = ctx.Value(key{})
		return ElicitResult{Action: Accept}
	}})
	ctx := context.WithValue(context.Background(), key{}, "call")
	go func() {
		m := f.read()
		f.send(map[string]any{"jsonrpc": "2.0", "id": 77, "method": "elicitation/create",
			"params": map[string]any{"mode": "url", "message": "pay", "url": "https://x"}})
		f.read()
		f.answer(m, map[string]any{})
	}()
	if err := c.Request(ctx, "tools/call", map[string]any{}, nil); err != nil {
		t.Fatal(err)
	}
	if got != "call" {
		t.Fatalf("ctx value = %v", got)
	}
}

func TestElicitationRefusals(t *testing.T) {
	_, f := connect(t, Handler{})
	f.send(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "elicitation/create", "params": map[string]any{"message": "q"}})
	if m := f.read(); m.Error == nil || !strings.Contains(m.Error.Message, "not supported") {
		t.Fatalf("no handler = %+v", m)
	}
	_, f = connect(t, Handler{Elicit: func(context.Context, ElicitRequest) ElicitResult { return ElicitResult{} }})
	f.send(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "elicitation/create", "params": "text"})
	if m := f.read(); m.Error == nil || m.Error.Code != -32602 {
		t.Fatalf("bad params = %+v", m)
	}
}

func TestDefaults(t *testing.T) {
	schema := json.RawMessage(`{"type":"object","properties":{"ok":{"type":"boolean","default":true},"name":{"type":"string"},"n":{"default":3}},"required":["ok","name"]}`)
	values, missing := Defaults(schema)
	if values["ok"] != true || values["n"] != float64(3) || len(missing) != 1 || missing[0] != "name" {
		t.Fatalf("values = %v, missing = %v", values, missing)
	}
	if v, m := Defaults(nil); len(v) != 0 || len(m) != 0 {
		t.Fatalf("empty schema = %v %v", v, m)
	}
}

// helperServer is the process Start runs in the tests below: a server that answers the
// handshake, a ping-like tool call, and exits when its input ends.
func TestMain(m *testing.M) {
	if os.Getenv("MCP_TEST_SERVER") == "1" {
		runHelperServer()
		return
	}
	os.Exit(m.Run())
}

func runHelperServer() {
	sc := bufio.NewScanner(os.Stdin)
	for sc.Scan() {
		var m message
		_ = json.Unmarshal(sc.Bytes(), &m)
		if len(m.ID) == 0 {
			continue
		}
		res := map[string]any{"protocolVersion": ProtocolVersion, "serverInfo": map[string]any{"name": "proc"}, "env": os.Getenv("MCP_TEST_EXTRA")}
		raw, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": json.RawMessage(m.ID), "result": res})
		_, _ = os.Stdout.Write(append(raw, '\n'))
	}
}

func TestStartRunsAProcess(t *testing.T) {
	t.Setenv("MCP_TEST_SERVER", "1")
	t.Setenv("SECRET_KEY", "must-not-leak")
	c, err := Start(context.Background(), Config{
		Name: "proc", Command: os.Args[0], Env: map[string]string{"MCP_TEST_SERVER": "1", "MCP_TEST_EXTRA": "x"},
	}, Handler{})
	if err != nil {
		t.Fatal(err)
	}
	if c.Server.Name != "proc" {
		t.Fatalf("server = %+v", c.Server)
	}
	for _, e := range environment(map[string]string{"A": "b"}) {
		if strings.HasPrefix(e, "SECRET_KEY") {
			t.Fatal("the environment leaked a variable")
		}
	}
	if err := c.Close(); err != nil && !errors.Is(err, os.ErrClosed) {
		t.Fatal(err)
	}
}

func TestStartFailures(t *testing.T) {
	if _, err := Start(context.Background(), Config{Name: "x", Command: "/nonexistent/program"}, Handler{}); err == nil ||
		!strings.Contains(err.Error(), "start x") {
		t.Fatalf("missing program = %v", err)
	}
	// A program that exits at once never answers the handshake.
	if _, err := Start(context.Background(), Config{Name: "x", Command: "true"}, Handler{}); err == nil ||
		!strings.Contains(err.Error(), "handshake") {
		t.Fatalf("silent program = %v", err)
	}
	oldIn, oldOut := stdinPipe, stdoutPipe
	defer func() { stdinPipe, stdoutPipe = oldIn, oldOut }()
	stdinPipe = func(*exec.Cmd) (io.WriteCloser, error) { return nil, errors.New("no descriptors") }
	if _, err := Start(context.Background(), Config{Command: "true"}, Handler{}); err == nil {
		t.Fatal("want the stdin error")
	}
	stdinPipe = oldIn
	stdoutPipe = func(*exec.Cmd) (io.ReadCloser, error) { return nil, errors.New("no descriptors") }
	if _, err := Start(context.Background(), Config{Command: "true"}, Handler{}); err == nil {
		t.Fatal("want the stdout error")
	}
}
