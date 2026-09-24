package llm

// --- Claude Code --------------------------------------------------------------
//
// The claude-code provider runs on the user's own Claude subscription by driving
// the official, unmodified `claude` CLI they logged into with `claude auth login`.
// motita never sees or stores those credentials: it starts claude, replays the
// conversation over stream-json, and reads back the next assistant turn. The loop,
// the tools, the sandbox and the compaction stay motita's.
//
// The tools reach claude through an inert MCP server (this same binary, see
// ServeClaudeCodeMCP), so they appear as mcp__motita__<name>. claude answers with
// tool_use and is stopped at the end of that message, before it can act on it.

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/madkoding/motita/internal/config"
)

// ClaudeCodeMCPCommand is the hidden subcommand claude starts as its MCP server.
const ClaudeCodeMCPCommand = "__claude-code-mcp"

// claudeToolPrefix is how claude names a tool served by the "motita" MCP server.
const claudeToolPrefix = "mcp__motita__"

// writeFile and executable are indirected so their failures can be tested: a fresh
// temporary directory does not refuse a write, and os.Executable does not fail, on demand.
var (
	writeFile  = os.WriteFile
	executable = os.Executable
)

// fatalError is a failure that trying again cannot fix: a missing program, a
// login that is not there, a request claude refuses as such.
type fatalError struct{ error }

func (e fatalError) Unwrap() error { return e.error }

// claudeBlock is one content block, both in the frames motita writes and in the
// assistant messages claude prints. A tool_use block always carries its input.
type claudeBlock struct {
	Type      string          `json:"type"`
	Text      string          `json:"text,omitempty"`
	ID        string          `json:"id,omitempty"`
	Name      string          `json:"name,omitempty"`
	Input     json.RawMessage `json:"input,omitempty"`
	ToolUseID string          `json:"tool_use_id,omitempty"`
	Content   string          `json:"content,omitempty"`
}

type claudeMessage struct {
	Role    string        `json:"role"`
	Content []claudeBlock `json:"content"`
}

// claudeFrame is one stream-json input line.
type claudeFrame struct {
	Type        string        `json:"type"`
	Message     claudeMessage `json:"message"`
	ShouldQuery *bool         `json:"shouldQuery,omitempty"`
}

// claudeTool is one entry of the manifest the MCP server lists.
type claudeTool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
}

type claudeMCPConfig struct {
	MCPServers map[string]claudeMCPServer `json:"mcpServers"`
}

type claudeMCPServer struct {
	Command string   `json:"command"`
	Args    []string `json:"args"`
}

// claudeEvent is the part of a stream-json output line that motita reads.
type claudeEvent struct {
	Type       string          `json:"type"`
	Subtype    string          `json:"subtype"`
	IsError    bool            `json:"is_error"`
	NumTurns   int             `json:"num_turns"`
	Result     string          `json:"result"`
	Errors     json.RawMessage `json:"errors"`
	Error      string          `json:"error"`
	MCPServers []struct {
		Name   string `json:"name"`
		Status string `json:"status"`
	} `json:"mcp_servers"`
	Message claudeMessage `json:"message"`
	Event   struct {
		Type  string `json:"type"`
		Delta struct {
			Type       string `json:"type"`
			Text       string `json:"text"`
			StopReason string `json:"stop_reason"`
		} `json:"delta"`
	} `json:"event"`
	Response json.RawMessage `json:"response"`
}

// claudeFrames turns the conversation into the system prompt and the stream-json
// frames claude replays. Consecutive user-side turns (parallel tool results, the
// planner's nudge after them) become one frame, as the Messages API wants.
func claudeFrames(messages []Message) (string, []claudeFrame, error) {
	var system []string
	var frames []claudeFrame
	for _, m := range messages {
		role := "user"
		var blocks []claudeBlock
		switch m.Role {
		case "system":
			system = append(system, m.Content)
			continue
		case "assistant":
			role = "assistant"
			if m.Content != "" {
				blocks = append(blocks, claudeBlock{Type: "text", Text: m.Content})
			}
			for _, tc := range m.ToolCalls {
				var input map[string]any
				_ = tc.Function.DecodeArguments(&input)
				if input == nil {
					input = map[string]any{}
				}
				raw, _ := json.Marshal(input)
				blocks = append(blocks, claudeBlock{Type: "tool_use", ID: tc.ID, Name: claudeToolPrefix + tc.Function.Name, Input: raw})
			}
			if len(blocks) == 0 {
				continue
			}
		case "tool":
			blocks = []claudeBlock{{Type: "tool_result", ToolUseID: m.ToolCallID, Content: m.Content}}
		default:
			text := m.Content
			if strings.HasPrefix(text, "/") {
				// claude -p runs a leading "/name" as its own command even with
				// --disable-slash-commands ("/diff ..." never reached the model, measured);
				// with a leading space it does, and it means the same.
				text = " " + text
			}
			blocks = []claudeBlock{{Type: "text", Text: text}}
		}
		if n := len(frames); n > 0 && role == "user" && frames[n-1].Type == "user" {
			frames[n-1].Message.Content = append(frames[n-1].Message.Content, blocks...)
			continue
		}
		frames = append(frames, claudeFrame{Type: role, Message: claudeMessage{Role: role, Content: blocks}})
	}
	if len(frames) == 0 || frames[len(frames)-1].Type != "user" {
		return "", nil, errors.New("claude-code needs a conversation that ends with a user turn")
	}
	return strings.Join(system, "\n\n"), frames, nil
}

// claudeThinks reports whether the reasoning setting asks for thinking at all.
func claudeThinks(r config.Reasoning) bool { return r.Enabled && r.Level != "off" }

// claudeArgs is claude's command line for one turn, and the files it names, which
// go in dir. self is this program, which claude starts as the tool server.
func claudeArgs(cfg config.LLM, dir, system string, tools []Tool, self string) ([]string, map[string][]byte) {
	files := map[string][]byte{"system.md": []byte(system)}
	args := []string{"-p", "--model", cfg.Model,
		"--input-format", "stream-json", "--output-format", "stream-json", "--verbose", "--include-partial-messages",
		"--tools", "", "--system-prompt-file", filepath.Join(dir, "system.md"),
		"--setting-sources", "", "--strict-mcp-config"}
	if len(tools) > 0 {
		manifest := make([]claudeTool, 0, len(tools))
		for _, t := range tools {
			manifest = append(manifest, claudeTool{Name: t.Function.Name, Description: t.Function.Description, InputSchema: t.Function.Parameters})
		}
		files["tools.json"], _ = json.Marshal(manifest)
		mcp, _ := json.Marshal(claudeMCPConfig{MCPServers: map[string]claudeMCPServer{
			"motita": {Command: self, Args: []string{ClaudeCodeMCPCommand, filepath.Join(dir, "tools.json")}},
		}})
		args = append(args, "--mcp-config", string(mcp))
	}
	if claudeThinks(cfg.Reasoning) {
		args = append(args, "--effort", cfg.Reasoning.Level)
	}
	return append(args, "--disable-slash-commands", "--max-turns", "1", "--permission-mode", "dontAsk", "--no-session-persistence"), files
}

// claudeBaseEnv is this process' environment without anything that would point
// claude at an API key, another backend or another effort than the user's own
// subscription login and motita's settings.
func claudeBaseEnv() []string {
	env := []string{} // never nil: a nil Env would hand claude this whole environment
	for _, kv := range os.Environ() {
		name, _, _ := strings.Cut(kv, "=")
		if strings.HasPrefix(name, "ANTHROPIC_") || strings.HasPrefix(name, "CLAUDE_CODE_USE_") || name == "CLAUDE_EFFORT" {
			continue
		}
		env = append(env, kv)
	}
	return env
}

// claudeEnv is the environment of a turn: the base one plus motita's settings.
func claudeEnv(cfg config.LLM) []string {
	env := claudeBaseEnv()
	// os/exec keeps the last value of a repeated name, so these win over the parent's.
	// Nonessential traffic is off for turns only: it cuts claude's cold start, and it also
	// stops claude fetching its full model picker, which discovery needs.
	env = append(env, "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1")
	if !claudeThinks(cfg.Reasoning) {
		// Measured: without it claude thinks anyway (haiku spent 355 thinking tokens on a yes/no).
		env = append(env, "MAX_THINKING_TOKENS=0")
	}
	if cfg.MaxTokens > 0 {
		env = append(env, fmt.Sprintf("CLAUDE_CODE_MAX_OUTPUT_TOKENS=%d", cfg.MaxTokens))
	}
	return env
}

// claudeWorkdir is where claude runs: one private, empty directory kept across
// calls. claude writes its working directory into the prompt, so a new one per call
// would never reuse the prompt cache. fallback is used when there is no such place.
func claudeWorkdir(fallback string) string {
	if cache, err := os.UserCacheDir(); err == nil {
		dir := filepath.Join(cache, "motita", "claude")
		if os.MkdirAll(dir, 0o700) == nil {
			return dir
		}
	}
	return fallback
}

// claudeProcess is one run of the claude CLI: the frames written to it and the
// events it prints back. A turn and the model discovery are each one.
type claudeProcess struct {
	cmd    *exec.Cmd
	ctx    context.Context
	cancel context.CancelFunc
	stdin  io.WriteCloser
	out    *bufio.Scanner
	stderr bytes.Buffer
	// mcp is set when the turn has tools, and claude must then report the tool
	// server as connected.
	mcp bool
}

// startClaude runs claude in dir for at most timeout.
func startClaude(ctx context.Context, timeout time.Duration, dir string, args, env []string) (*claudeProcess, error) {
	bin := os.Getenv("MOTITA_CLAUDE_BIN")
	if bin == "" {
		bin = "claude"
	}
	// Resolved before cmd.Dir is set: os/exec would read a relative path from there.
	path, err := exec.LookPath(bin)
	if err == nil {
		path, err = filepath.Abs(path)
	}
	if errors.Is(err, exec.ErrNotFound) || errors.Is(err, fs.ErrNotExist) {
		return nil, fatalError{fmt.Errorf("%s was not found (%w): install Claude Code and run `claude auth login`", bin, err)}
	}
	p := &claudeProcess{}
	p.ctx, p.cancel = context.WithTimeout(ctx, timeout)
	p.cmd = exec.CommandContext(p.ctx, path, args...)
	p.cmd.Dir = dir
	p.cmd.Env = env
	p.cmd.Stderr = &p.stderr
	// Asked to stop rather than killed: a killed claude leaves its socket behind in
	// /tmp/cc-socks on every call. WaitDelay kills it if it does not go.
	p.cmd.Cancel = func() error { return terminate(p.cmd.Process) }
	p.cmd.WaitDelay = 5 * time.Second
	var stdout io.ReadCloser
	if err == nil {
		p.stdin, err = p.cmd.StdinPipe()
	}
	if err == nil {
		stdout, err = p.cmd.StdoutPipe()
	}
	if err == nil {
		err = p.cmd.Start()
	}
	if err != nil {
		p.cancel()
		return nil, fmt.Errorf("could not run %s: %w", bin, err)
	}
	p.out = bufio.NewScanner(stdout)
	p.out.Buffer(make([]byte, 0, 64<<10), 64<<20)
	return p, nil
}

// send writes one stream-json line. A claude that died shows up as the end of its
// output, which every reader checks, so the write error adds nothing.
func (p *claudeProcess) send(v any) { _ = json.NewEncoder(p.stdin).Encode(v) }

// event returns the next event claude printed. The end of its output is an error,
// and so is a tool server claude could not reach.
func (p *claudeProcess) event() (claudeEvent, error) {
	for p.out.Scan() {
		var ev claudeEvent
		if json.Unmarshal(p.out.Bytes(), &ev) != nil {
			continue
		}
		if p.mcp && ev.Type == "system" && ev.Subtype == "init" {
			status := "missing"
			for _, s := range ev.MCPServers {
				if s.Name == "motita" {
					status = s.Status
				}
			}
			if status != "connected" {
				// Without it claude answers with no tools at all, which reads as a model that
				// will not look at anything. A managed MCP policy is one way to get here.
				return ev, fatalError{fmt.Errorf("claude could not use motita's tool server (MCP status %q)", status)}
			}
		}
		return ev, nil
	}
	return claudeEvent{}, p.exited()
}

// exited explains an output that ended before the answer did.
func (p *claudeProcess) exited() error {
	_ = p.cmd.Wait() // stderr is only complete, and safe to read, once Wait returns
	if err := p.ctx.Err(); err != nil {
		return fmt.Errorf("claude did not answer: %w", err)
	}
	tail := strings.TrimSpace(p.stderr.String())
	if len(tail) > 1000 {
		tail = "..." + tail[len(tail)-1000:]
	}
	return fmt.Errorf("claude exited before answering: %s", tail)
}

// stop ends the process, whatever state it is in.
func (p *claudeProcess) stop() {
	p.cancel()
	_ = p.cmd.Wait()
}

// replay writes the conversation. Every user frame but the last is recorded
// without a query, and claude acknowledges each one with a zero-turn result.
func (p *claudeProcess) replay(frames []claudeFrame) error {
	for i, f := range frames {
		wait := f.Type == "user" && i < len(frames)-1
		if wait {
			f.ShouldQuery = new(bool)
		}
		p.send(f)
		for wait {
			ev, err := p.event()
			if err != nil {
				return err
			}
			if ev.Type != "result" {
				continue
			}
			if ev.NumTurns != 0 || ev.IsError {
				return fatalError{fmt.Errorf("claude refused the history replay (%s)", ev.Subtype)}
			}
			wait = false
		}
	}
	return p.stdin.Close()
}

// readTurn reads the answer to the last frame. onText, when not nil, gets the text
// as it streams.
//
// A message_stop is not always the end of the turn: claude also stops a message
// that only thought (and asks again), one cut at max_tokens (and continues it), and
// a stream it is about to retry. Only a stop with a final reason and something to
// show ends the turn.
func (p *claudeProcess) readTurn(onText func(string)) (Reply, error) {
	var reply, msg Reply // what the turn has, and what the current message adds
	stop := ""
	keep := func() {
		reply.Content += msg.Content
		reply.Calls = append(reply.Calls, msg.Calls...)
		msg = Reply{}
	}
	finish := func() (Reply, error) {
		keep()
		reply.FinishReason = "stop"
		if len(reply.Calls) > 0 {
			reply.FinishReason = "tool_calls"
		} else if stop == "max_tokens" {
			reply.FinishReason = "length"
		}
		return reply, nil
	}
	for {
		ev, err := p.event()
		if err != nil {
			return Reply{}, err
		}
		switch ev.Type {
		case "stream_event":
			switch ev.Event.Type {
			case "message_start":
				msg, stop = Reply{}, ""
			case "content_block_delta":
				if ev.Event.Delta.Type == "text_delta" && onText != nil {
					onText(ev.Event.Delta.Text)
				}
			case "message_delta":
				stop = ev.Event.Delta.StopReason
			case "message_stop":
				final := stop == "end_turn" || stop == "tool_use" || stop == "stop_sequence"
				if final && (msg.Content != "" || len(msg.Calls) > 0) {
					// ponytail: claude may start another upstream request after message_stop
					// (it tries to run the denied tool); stop() ends it. A loopback admission
					// relay like Hermes' admission.py is the upgrade path if that shows in usage.
					return finish()
				}
				if stop == "max_tokens" {
					keep() // claude continues the answer in the next message
				}
				// ponytail: a retried stream's text already went to onText and is sent again;
				// buffer per message if that shows up in the stream path.
				msg = Reply{}
			}
		case "assistant":
			if ev.Error != "" {
				return Reply{}, assistantError(ev)
			}
			for _, b := range ev.Message.Content {
				switch b.Type {
				case "text":
					msg.Content += b.Text
				case "tool_use":
					msg.Calls = append(msg.Calls, ToolCall{ID: b.ID, Type: "function", Function: FunctionCall{
						Name: strings.TrimPrefix(b.Name, claudeToolPrefix), Arguments: b.Input,
					}})
				}
			}
		case "result":
			keep()
			// error_max_turns after tool calls is claude trying to run a denied tool once
			// the answer was complete: the answer stands.
			if ev.IsError && (ev.Subtype != "error_max_turns" || len(reply.Calls) == 0) {
				return Reply{}, fmt.Errorf("claude failed (%s): %s", ev.Subtype, strings.TrimSpace(ev.Result+" "+string(ev.Errors)))
			}
			return finish()
		}
	}
}

// assistantError is claude's report of a request that failed upstream. It is
// fatal unless its kind is one that trying again can help.
func assistantError(ev claudeEvent) error {
	var text string
	for _, b := range ev.Message.Content {
		text += b.Text
	}
	err := fmt.Errorf("claude: %s: %s", ev.Error, text)
	switch ev.Error {
	case "rate_limit", "overloaded", "server_error", "unknown":
		return err
	}
	return fatalError{err}
}

// callClaudeCode asks the local claude CLI for the next assistant turn. onText,
// when not nil, receives the text as it streams.
func (c *Client) callClaudeCode(ctx context.Context, messages []Message, tools []Tool, onText func(string)) (Reply, error) {
	system, frames, err := claudeFrames(messages)
	if err != nil {
		return Reply{}, fatalError{err}
	}
	var self string
	if len(tools) > 0 {
		if self, err = executable(); err != nil {
			return Reply{}, fatalError{fmt.Errorf("claude needs motita's own path to serve it the tools: %w", err)}
		}
	}
	// Absolute, because claude reads the paths below from its own directory.
	dir, err := os.MkdirTemp("", "motita-claude-")
	if err == nil {
		defer os.RemoveAll(dir)
		dir, err = filepath.Abs(dir)
	}
	if err != nil {
		return Reply{}, err
	}
	args, files := claudeArgs(c.cfg, dir, system, tools, self)
	for name, data := range files {
		if err := writeFile(filepath.Join(dir, name), data, 0o600); err != nil {
			return Reply{}, err
		}
	}
	p, err := startClaude(ctx, c.cfg.Timeout, claudeWorkdir(dir), args, claudeEnv(c.cfg))
	if err != nil {
		return Reply{}, err
	}
	defer p.stop()
	p.mcp = len(tools) > 0
	if err := p.replay(frames); err != nil {
		return Reply{}, err
	}
	return p.readTurn(onText)
}

// callClaudeCodeText is Complete's single call: text only, no tools.
func (c *Client) callClaudeCodeText(ctx context.Context, messages []Message) (string, error) {
	reply, err := c.callClaudeCode(ctx, messages, nil, nil)
	if err != nil {
		return "", err
	}
	if reply.Content == "" {
		return "", errors.New("claude returned a response with no text")
	}
	return reply.Content, nil
}

// callClaudeCodeStream adapts callClaudeCode to the streaming interface: the text
// as it arrives, then one chunk per tool call, then the reply.
func (c *Client) callClaudeCodeStream(ctx context.Context, messages []Message, tools []Tool) (<-chan StreamChunk, error) {
	out := make(chan StreamChunk, 8)
	go func() {
		defer close(out)
		streamed := false
		reply, err := c.callClaudeCode(ctx, messages, tools, func(s string) {
			streamed = true
			out <- StreamChunk{Event: StreamText, Text: s}
		})
		if err != nil {
			out <- StreamChunk{Event: StreamError, Error: err}
			return
		}
		if !streamed && reply.Content != "" {
			// claude's non-streaming fallback prints the answer only whole, and the
			// planner takes its text from the chunks, not from the final reply.
			out <- StreamChunk{Event: StreamText, Text: reply.Content}
		}
		for i := range reply.Calls {
			out <- StreamChunk{Event: StreamToolCall, Call: &reply.Calls[i]}
		}
		out <- StreamChunk{Event: StreamDone, Reply: reply}
	}()
	return out, nil
}

// ClaudeCodeCatalogue is what the logged-in claude account offers.
type ClaudeCodeCatalogue struct {
	// Plan is the subscription, as claude names it ("Claude Max").
	Plan   string
	Models []ClaudeCodeModel
}

// ClaudeCodeModel is one entry of claude's own model picker.
type ClaudeCodeModel struct {
	// Value is what llm.model takes ("sonnet", "opus[1m]").
	Value       string `json:"value"`
	Resolved    string `json:"resolvedModel"`
	DisplayName string `json:"displayName"`
	Description string `json:"description"`
}

// ClaudeCodeModels asks the local claude CLI which models the logged-in account
// offers. It is claude's initialize handshake only: no model is called.
func ClaudeCodeModels(ctx context.Context) (ClaudeCodeCatalogue, error) {
	args := []string{"-p", "--input-format", "stream-json", "--output-format", "stream-json", "--verbose",
		"--tools", "", "--setting-sources", "", "--strict-mcp-config", "--disable-slash-commands", "--no-session-persistence"}
	// The base environment: with nonessential traffic off claude lists 5 models instead of the
	// account's 11 (measured).
	p, err := startClaude(ctx, 30*time.Second, claudeWorkdir(os.TempDir()), args, claudeBaseEnv())
	if err != nil {
		return ClaudeCodeCatalogue{}, err
	}
	defer p.stop()
	_, _ = io.WriteString(p.stdin, `{"type":"control_request","request_id":"models","request":{"subtype":"initialize"}}`+"\n")
	_ = p.stdin.Close()
	for {
		ev, err := p.event()
		if err != nil {
			return ClaudeCodeCatalogue{}, err
		}
		if ev.Type != "control_response" {
			continue
		}
		var r struct {
			Subtype  string `json:"subtype"`
			Error    string `json:"error"`
			Response struct {
				Models  []ClaudeCodeModel `json:"models"`
				Account struct {
					SubscriptionType string `json:"subscriptionType"`
				} `json:"account"`
			} `json:"response"`
		}
		_ = json.Unmarshal(ev.Response, &r)
		if r.Subtype != "success" {
			return ClaudeCodeCatalogue{}, fmt.Errorf("claude did not list its models: %s", r.Error)
		}
		return ClaudeCodeCatalogue{Plan: r.Response.Account.SubscriptionType, Models: r.Response.Models}, nil
	}
}

// ServeClaudeCodeMCP is the MCP server claude is pointed at: newline-delimited
// JSON-RPC that lists the tools in the manifest and refuses to run any of them,
// because motita executes its tools itself.
func ServeClaudeCodeMCP(in io.Reader, out io.Writer, manifestPath string) error {
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		return err
	}
	var tools []claudeTool
	if err := json.Unmarshal(data, &tools); err != nil {
		return fmt.Errorf("invalid tool manifest %s: %w", manifestPath, err)
	}
	sc := bufio.NewScanner(in)
	sc.Buffer(make([]byte, 0, 64<<10), 64<<20)
	enc := json.NewEncoder(out)
	for sc.Scan() {
		var req struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
		}
		if json.Unmarshal(sc.Bytes(), &req) != nil || req.ID == nil {
			continue // notifications get no reply
		}
		var result any = struct{}{}
		switch req.Method {
		case "initialize":
			// serverInfo.version is required: without it claude marks the server failed
			// and the turn runs with no tools (measured).
			result = map[string]any{"protocolVersion": "2024-11-05", "capabilities": map[string]any{"tools": map[string]any{}}, "serverInfo": map[string]any{"name": "motita", "version": "1"}}
		case "tools/list":
			result = map[string]any{"tools": tools}
		case "tools/call":
			result = map[string]any{"isError": true, "content": []any{map[string]any{"type": "text", "text": "denied: motita executes tools itself"}}}
		}
		if err := enc.Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": result}); err != nil {
			return err
		}
	}
	return sc.Err()
}
