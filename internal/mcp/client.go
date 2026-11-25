// Package mcp is a client for servers that speak the Model Context Protocol.
//
// A server is a program the person chose to connect (agent.mcp in the configuration). The
// client starts it, talks JSON-RPC 2.0 to it over its standard input and output, and offers what
// the agent needs: the server's tools, calling one, and answering the questions a server asks
// in the middle of a call (elicitation, see elicit.go).
//
// Everything a server says is third-party data. The package never acts on it by itself: a
// handler the caller supplies decides what an elicitation gets as an answer.
package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
)

// ProtocolVersion is the revision of the specification this client asks for. A server that
// knows an older one answers with it, and the client then speaks that one; the features of a
// newer revision are only used when the server's answer lists them.
const ProtocolVersion = "2025-11-25"

// maxLine bounds one message a server sends, so a broken server cannot exhaust memory.
const maxLine = 16 << 20

// Handler answers what a server asks of the client. A nil field means the client does not
// support that request and says so to the server.
type Handler struct {
	// Elicit answers an elicitation: the server paused a call to ask the person something.
	Elicit func(ctx context.Context, req ElicitRequest) ElicitResult
}

// rpcError is the error object of a JSON-RPC response.
type rpcError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

func (e *rpcError) Error() string { return fmt.Sprintf("mcp error %d: %s", e.Code, e.Message) }

// message is any JSON-RPC message: a request, a notification or a response.
type message struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

// Client is one connected server.
type Client struct {
	name    string
	handler Handler
	closer  io.Closer
	cmd     *exec.Cmd

	wmu sync.Mutex
	w   io.Writer

	mu      sync.Mutex
	next    int64
	pending map[int64]chan message
	active  context.Context
	closed  bool
	err     error

	// Server is what the server said about itself in its initialize answer.
	Server ServerInfo
}

// ServerInfo is the server's own description, kept for the person to read.
type ServerInfo struct {
	Name         string          `json:"name"`
	Version      string          `json:"version"`
	Protocol     string          `json:"-"`
	Instructions string          `json:"-"`
	Capabilities json.RawMessage `json:"-"`
}

// Config is how one server is started.
type Config struct {
	Name    string
	Command string
	Args    []string
	// Env is added to a minimal environment (PATH, HOME): the person's own variables, API keys
	// included, are NOT handed to a program they only connected.
	Env map[string]string
	Dir string
}

// Start runs the server and completes the protocol handshake.
func Start(ctx context.Context, cfg Config, h Handler) (*Client, error) {
	cmd := exec.Command(cfg.Command, cfg.Args...)
	cmd.Dir = cfg.Dir
	cmd.Env = environment(cfg.Env)
	cmd.Stderr = nil
	in, err := stdinPipe(cmd)
	if err != nil {
		return nil, err
	}
	out, err := stdoutPipe(cmd)
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start %s: %w", cfg.Name, err)
	}
	c := newClient(cfg.Name, in, out, in, h)
	c.cmd = cmd
	if err := c.initialize(ctx); err != nil {
		_ = c.Close()
		return nil, err
	}
	return c, nil
}

// The pipes of a process fail only when the system is out of descriptors; the seams let a test
// reach the handling.
var (
	stdinPipe  = (*exec.Cmd).StdinPipe
	stdoutPipe = (*exec.Cmd).StdoutPipe
)

// environment is the environment of a server: PATH and HOME, then what the person configured.
func environment(extra map[string]string) []string {
	env := []string{"PATH=" + os.Getenv("PATH"), "HOME=" + os.Getenv("HOME")}
	for k, v := range extra {
		env = append(env, k+"="+v)
	}
	return env
}

// NewClient talks to a server already connected through r and w (a test, or a transport the
// caller opened) and completes the handshake.
func NewClient(
	ctx context.Context, name string, r io.Reader, w io.Writer, closer io.Closer, h Handler,
) (*Client, error) {
	c := newClient(name, w, r, closer, h)
	if err := c.initialize(ctx); err != nil {
		_ = c.Close()
		return nil, err
	}
	return c, nil
}

func newClient(name string, w io.Writer, r io.Reader, closer io.Closer, h Handler) *Client {
	c := &Client{name: name, w: w, closer: closer, handler: h, pending: map[int64]chan message{}}
	go c.read(r)
	return c
}

// Name is the name the person gave the server.
func (c *Client) Name() string { return c.name }

// read delivers every message the server sends until its output ends.
func (c *Client) read(r io.Reader) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64<<10), maxLine)
	for sc.Scan() {
		var m message
		if json.Unmarshal(sc.Bytes(), &m) != nil {
			continue
		}
		c.dispatch(m)
	}
	err := sc.Err()
	if err == nil {
		err = io.EOF
	}
	c.fail(err)
}

// fail ends every call in flight: the server is gone.
func (c *Client) fail(err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.err == nil {
		c.err = err
	}
	for id, ch := range c.pending {
		close(ch)
		delete(c.pending, id)
	}
}

// dispatch routes one message: a response to the call waiting for it, a request to the handler.
func (c *Client) dispatch(m message) {
	if m.Method == "" {
		var id int64
		if json.Unmarshal(m.ID, &id) != nil {
			return
		}
		c.mu.Lock()
		ch, ok := c.pending[id]
		delete(c.pending, id)
		c.mu.Unlock()
		if ok {
			ch <- m
		}
		return
	}
	if len(m.ID) == 0 {
		return // a notification: nothing here needs one
	}
	go c.serve(m)
}

// serve answers a request of the server.
func (c *Client) serve(m message) {
	switch m.Method {
	case "ping":
		c.reply(m.ID, map[string]any{}, nil)
	case "elicitation/create":
		c.serveElicit(m)
	default:
		c.reply(m.ID, nil, &rpcError{Code: -32601, Message: "method not supported: " + m.Method})
	}
}

// reply writes the answer to a server's request.
func (c *Client) reply(id json.RawMessage, result any, e *rpcError) {
	m := message{JSONRPC: "2.0", ID: id, Error: e}
	if e == nil {
		raw, _ := json.Marshal(result)
		m.Result = raw
	}
	_ = c.write(m)
}

// write sends one message, a line of JSON.
func (c *Client) write(m message) error {
	raw, _ := json.Marshal(m) // every part of a message is already valid JSON
	c.wmu.Lock()
	defer c.wmu.Unlock()
	_, err := c.w.Write(append(raw, '\n'))
	return err
}

// Request sends a call and waits for its answer, which is decoded into out (nil to drop it).
func (c *Client) Request(ctx context.Context, method string, params, out any) error {
	raw, err := json.Marshal(params)
	if err != nil {
		return err
	}
	ch := make(chan message, 1)
	c.mu.Lock()
	if c.closed || c.err != nil {
		err := c.err
		c.mu.Unlock()
		return errors.Join(errors.New("the server is not connected"), err)
	}
	c.next++
	id := c.next
	c.pending[id] = ch
	prev := c.active
	c.active = ctx
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		delete(c.pending, id)
		c.active = prev
		c.mu.Unlock()
	}()
	idRaw, _ := json.Marshal(id)
	if err := c.write(message{JSONRPC: "2.0", ID: idRaw, Method: method, Params: raw}); err != nil {
		return err
	}
	select {
	case <-ctx.Done():
		_ = c.notify("notifications/cancelled", map[string]any{"requestId": id, "reason": "the client gave up"})
		return ctx.Err()
	case m, ok := <-ch:
		if !ok {
			return errors.New("the server closed the connection")
		}
		if m.Error != nil {
			return m.Error
		}
		if out == nil {
			return nil
		}
		return json.Unmarshal(m.Result, out)
	}
}

// notify sends a notification, which has no answer.
func (c *Client) notify(method string, params map[string]any) error {
	raw, _ := json.Marshal(params)
	return c.write(message{JSONRPC: "2.0", Method: method, Params: raw})
}

// activeContext is the context of the call in flight, which is what a question from the server
// belongs to; without one the question has no one to ask.
func (c *Client) activeContext() context.Context {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.active == nil {
		return context.Background()
	}
	return c.active
}

// initialize is the handshake: what each side supports, then the "initialized" notice.
func (c *Client) initialize(ctx context.Context) error {
	caps := map[string]any{}
	if c.handler.Elicit != nil {
		caps["elicitation"] = map[string]any{"form": map[string]any{}, "url": map[string]any{}}
	}
	var res struct {
		ProtocolVersion string          `json:"protocolVersion"`
		Capabilities    json.RawMessage `json:"capabilities"`
		ServerInfo      ServerInfo      `json:"serverInfo"`
		Instructions    string          `json:"instructions"`
	}
	err := c.Request(ctx, "initialize", map[string]any{
		"protocolVersion": ProtocolVersion,
		"capabilities":    caps,
		"clientInfo":      map[string]any{"name": "motita", "version": "1"},
	}, &res)
	if err != nil {
		return fmt.Errorf("%s: handshake: %w", c.name, err)
	}
	c.Server = res.ServerInfo
	c.Server.Protocol = res.ProtocolVersion
	c.Server.Instructions = res.Instructions
	c.Server.Capabilities = res.Capabilities
	return c.notify("notifications/initialized", map[string]any{})
}

// Close disconnects: the server's input is closed, which is how a stdio server is told to stop,
// and a server that does not stop is killed.
func (c *Client) Close() error {
	c.mu.Lock()
	c.closed = true
	c.mu.Unlock()
	var err error
	if c.closer != nil {
		err = c.closer.Close()
	}
	if c.cmd != nil && c.cmd.Process != nil {
		_ = c.cmd.Process.Kill()
		_ = c.cmd.Wait()
	}
	return err
}
