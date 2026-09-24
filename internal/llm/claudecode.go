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
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// ClaudeCodeMCPCommand is the hidden subcommand claude starts as its MCP server.
const ClaudeCodeMCPCommand = "__claude-code-mcp"

// claudeToolPrefix is how claude names a tool served by the "motita" MCP server.
const claudeToolPrefix = "mcp__motita__"

// writeFile is indirected so the failure to write the per-call files can be
// tested: a freshly created temporary directory does not refuse a write on its own.
var writeFile = os.WriteFile

// fatalError is a failure that trying again cannot fix: a missing program, a
// login that is not there, a request claude refuses as such.
type fatalError struct{ error }

// claudeFrame is one stream-json input line.
type claudeFrame struct {
	Type        string        `json:"type"`
	Message     claudeMessage `json:"message"`
	ShouldQuery *bool         `json:"shouldQuery,omitempty"`
}

type claudeMessage struct {
	Role    string           `json:"role"`
	Content []map[string]any `json:"content"`
}

// claudeEvent is the part of a stream-json output line that motita reads.
type claudeEvent struct {
	Type     string `json:"type"`
	Subtype  string `json:"subtype"`
	IsError  bool   `json:"is_error"`
	NumTurns int    `json:"num_turns"`
	Result   string `json:"result"`
	Error    string `json:"error"`
	Message  struct {
		Content []struct {
			Type  string          `json:"type"`
			Text  string          `json:"text"`
			ID    string          `json:"id"`
			Name  string          `json:"name"`
			Input json.RawMessage `json:"input"`
		} `json:"content"`
	} `json:"message"`
	Event struct {
		Type  string `json:"type"`
		Delta struct {
			Type       string `json:"type"`
			Text       string `json:"text"`
			StopReason string `json:"stop_reason"`
		} `json:"delta"`
	} `json:"event"`
}

// claudeFrames turns the conversation into the system prompt and the stream-json
// frames claude replays. Consecutive user-side turns (parallel tool results, the
// planner's nudge after them) become one frame, as the Messages API wants.
func claudeFrames(messages []Message) (string, []claudeFrame, error) {
	var system []string
	var frames []claudeFrame
	for _, m := range messages {
		role := "user"
		var blocks []map[string]any
		switch m.Role {
		case "system":
			system = append(system, m.Content)
			continue
		case "assistant":
			role = "assistant"
			if m.Content != "" {
				blocks = append(blocks, map[string]any{"type": "text", "text": m.Content})
			}
			for _, tc := range m.ToolCalls {
				var input map[string]any
				_ = tc.Function.DecodeArguments(&input)
				if input == nil {
					input = map[string]any{}
				}
				blocks = append(blocks, map[string]any{
					"type": "tool_use", "id": tc.ID, "name": claudeToolPrefix + tc.Function.Name, "input": input,
				})
			}
			if len(blocks) == 0 {
				continue
			}
		case "tool":
			blocks = []map[string]any{{"type": "tool_result", "tool_use_id": m.ToolCallID, "content": m.Content}}
		default:
			blocks = []map[string]any{{"type": "text", "text": m.Content}}
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

// claudeEnv is this process' environment without what would point claude at an
// API key or another backend instead of the user's own subscription login.
func claudeEnv() []string {
	env := []string{} // never nil: a nil Env would hand claude this whole environment
	for _, kv := range os.Environ() {
		name, _, _ := strings.Cut(kv, "=")
		switch name {
		case "ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN", "ANTHROPIC_BASE_URL",
			"CLAUDE_CODE_USE_BEDROCK", "CLAUDE_CODE_USE_VERTEX", "CLAUDE_CODE_USE_FOUNDRY":
			continue
		}
		env = append(env, kv)
	}
	return env
}

// callClaudeCode asks the local claude CLI for the next assistant turn. onText,
// when not nil, receives the text as it streams.
func (c *Client) callClaudeCode(ctx context.Context, messages []Message, tools []Tool, onText func(string)) (Reply, error) {
	system, frames, err := claudeFrames(messages)
	if err != nil {
		return Reply{}, fatalError{err}
	}
	dir, err := os.MkdirTemp("", "motita-claude-")
	if err != nil {
		return Reply{}, err
	}
	defer os.RemoveAll(dir)

	files := map[string][]byte{"system.md": []byte(system)}
	args := []string{"-p", "--model", c.cfg.Model,
		"--input-format", "stream-json", "--output-format", "stream-json", "--verbose", "--include-partial-messages",
		"--tools", "", "--system-prompt-file", filepath.Join(dir, "system.md"),
		"--setting-sources", "", "--strict-mcp-config"}
	if len(tools) > 0 {
		manifest := make([]map[string]any, 0, len(tools))
		for _, t := range tools {
			schema := t.Function.Parameters
			if schema == nil {
				schema = map[string]any{"type": "object"}
			}
			manifest = append(manifest, map[string]any{"name": t.Function.Name, "description": t.Function.Description, "inputSchema": schema})
		}
		data, err := json.Marshal(manifest)
		if err != nil {
			return Reply{}, fatalError{fmt.Errorf("could not serialise the tools: %w", err)}
		}
		files["tools.json"] = data
		self, _ := os.Executable()
		mcp, _ := json.Marshal(map[string]any{"mcpServers": map[string]any{"motita": map[string]any{
			"command": self, "args": []string{ClaudeCodeMCPCommand, filepath.Join(dir, "tools.json")},
		}}})
		args = append(args, "--mcp-config", string(mcp))
	}
	args = append(args, "--disable-slash-commands", "--max-turns", "1", "--permission-mode", "dontAsk", "--no-session-persistence")
	for name, data := range files {
		if err := writeFile(filepath.Join(dir, name), data, 0o600); err != nil {
			return Reply{}, err
		}
	}

	bin := os.Getenv("MOTITA_CLAUDE_BIN")
	if bin == "" {
		bin = "claude"
	}
	ctx, cancel := context.WithTimeout(ctx, c.cfg.Timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Dir = dir
	cmd.Env = claudeEnv()
	cmd.WaitDelay = 5 * time.Second
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	var stdout io.ReadCloser
	stdin, err := cmd.StdinPipe()
	if err == nil {
		stdout, err = cmd.StdoutPipe()
	}
	if err == nil {
		err = cmd.Start()
	}
	if err != nil {
		return Reply{}, fatalError{fmt.Errorf("could not run %s (%v): install Claude Code and run `claude auth login`", bin, err)}
	}
	defer func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }()

	sc := bufio.NewScanner(stdout)
	sc.Buffer(make([]byte, 0, 64<<10), 64<<20)
	next := func() (claudeEvent, bool) {
		for sc.Scan() {
			var ev claudeEvent
			if json.Unmarshal(sc.Bytes(), &ev) == nil {
				return ev, true
			}
		}
		return claudeEvent{}, false
	}
	// exited explains an output that ended before the answer did.
	exited := func() error {
		_ = cmd.Wait() // stderr is only complete, and safe to read, once Wait returns
		if ctx.Err() != nil {
			return fmt.Errorf("claude did not answer: %w", ctx.Err())
		}
		tail := strings.TrimSpace(stderr.String())
		if len(tail) > 1000 {
			tail = "..." + tail[len(tail)-1000:]
		}
		return fmt.Errorf("claude exited before answering: %s", tail)
	}

	// History replay: every user frame but the last is recorded without a query,
	// and claude acknowledges each one with a zero-turn result.
	enc := json.NewEncoder(stdin)
	for i, f := range frames {
		replay := f.Type == "user" && i < len(frames)-1
		if replay {
			f.ShouldQuery = new(bool)
		}
		_ = enc.Encode(f) // a claude that died shows up as the end of its output below
		for replay {
			ev, ok := next()
			if !ok {
				return Reply{}, exited()
			}
			if ev.Type != "result" {
				continue
			}
			if ev.NumTurns != 0 || ev.IsError {
				return Reply{}, fatalError{fmt.Errorf("claude refused the history replay (%s)", ev.Subtype)}
			}
			replay = false
		}
	}
	_ = stdin.Close()

	var reply Reply
	stop := ""
	done := func() (Reply, error) {
		reply.FinishReason = "stop"
		if len(reply.Calls) > 0 {
			reply.FinishReason = "tool_calls"
		} else if stop == "max_tokens" {
			reply.FinishReason = "length"
		}
		return reply, nil
	}
	for {
		ev, ok := next()
		if !ok {
			return Reply{}, exited()
		}
		switch ev.Type {
		case "stream_event":
			switch ev.Event.Type {
			case "content_block_delta":
				if ev.Event.Delta.Type == "text_delta" && onText != nil {
					onText(ev.Event.Delta.Text)
				}
			case "message_delta":
				stop = ev.Event.Delta.StopReason
			case "message_stop":
				// ponytail: claude may start another upstream request after message_stop
				// (it tries to run the denied tool); the deferred kill stops it. A loopback
				// admission relay like Hermes' admission.py is the upgrade path if that
				// shows in usage.
				return done()
			}
		case "assistant":
			var text string
			for _, b := range ev.Message.Content {
				switch b.Type {
				case "text":
					text += b.Text
				case "tool_use":
					reply.Calls = append(reply.Calls, ToolCall{ID: b.ID, Type: "function", Function: FunctionCall{
						Name: strings.TrimPrefix(b.Name, claudeToolPrefix), Arguments: b.Input,
					}})
				}
			}
			if ev.Error != "" {
				err := fmt.Errorf("claude: %s: %s", ev.Error, text)
				switch ev.Error {
				case "authentication_failed", "billing_error", "invalid_request":
					return Reply{}, fatalError{err}
				}
				return Reply{}, err
			}
			reply.Content += text
		case "result":
			if ev.IsError {
				return Reply{}, fmt.Errorf("claude failed (%s): %s", ev.Subtype, ev.Result)
			}
			return done()
		}
	}
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
		reply, err := c.callClaudeCode(ctx, messages, tools, func(s string) {
			out <- StreamChunk{Event: StreamText, Text: s}
		})
		if err != nil {
			out <- StreamChunk{Event: StreamError, Error: err}
			return
		}
		for i := range reply.Calls {
			out <- StreamChunk{Event: StreamToolCall, Call: &reply.Calls[i]}
		}
		out <- StreamChunk{Event: StreamDone, Reply: reply}
	}()
	return out, nil
}

// ServeClaudeCodeMCP is the MCP server claude is pointed at: newline-delimited
// JSON-RPC that lists the tools in the manifest and refuses to run any of them,
// because motita executes its tools itself.
func ServeClaudeCodeMCP(in io.Reader, out io.Writer, manifestPath string) error {
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		return err
	}
	var tools []any
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
