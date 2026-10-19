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

// Literals used more than once in this file.
const (
	streamJSON = "stream-json"
)

// ClaudeCodeMCPCommand is the hidden subcommand claude starts as its MCP server.
const ClaudeCodeMCPCommand = "__claude-code-mcp"

// claudeToolPrefix is how claude names a tool served by the "motita" MCP server.
const claudeToolPrefix = "mcp__motita__"

// claudeStructuredTool is the tool claude answers a --json-schema turn with.
const claudeStructuredTool = "StructuredOutput"

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
	Source    *claudeSource   `json:"source,omitempty"`
}

// claudeSource is an image block's picture.
type claudeSource struct {
	Type      string `json:"type"`
	MediaType string `json:"media_type"`
	Data      string `json:"data"`
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
		// Usage is a message's whole consumption, on its message_delta.
		Usage *anthropicUsage `json:"usage"`
	} `json:"event"`
	// Usage is the whole turn's consumption, on the result event.
	Usage    *anthropicUsage `json:"usage"`
	Response json.RawMessage `json:"response"`
	// RateLimitInfo is what claude says of the subscription's limits on a rate_limit_event.
	RateLimitInfo *claudeRateLimit `json:"rate_limit_info"`
	// StructuredOutput is the object a --json-schema turn produced, repeated on its result.
	StructuredOutput json.RawMessage `json:"structured_output"`
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
			if blocks = assistantBlocks(m); len(blocks) == 0 {
				continue
			}
		case "tool":
			blocks = []claudeBlock{{Type: "tool_result", ToolUseID: m.ToolCallID, Content: m.Content}}
		default:
			blocks = append([]claudeBlock{{Type: "text", Text: claudeUserText(m.Content)}}, claudeImages(m.Images)...)
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

// assistantBlocks are the content blocks of an assistant turn: its text, then one tool_use per call.
func assistantBlocks(m Message) []claudeBlock {
	var blocks []claudeBlock
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
		blocks = append(blocks, claudeBlock{
			Type: "tool_use", ID: tc.ID, Name: claudeToolPrefix + tc.Function.Name, Input: raw,
		})
	}
	return blocks
}

// claudeUserText is a user turn's text as claude is given it. claude -p runs a leading "/name" as
// its own command even with --disable-slash-commands ("/diff ..." never reached the model,
// measured); with a leading space it does, and it means the same.
func claudeUserText(text string) string {
	if strings.HasPrefix(text, "/") {
		return " " + text
	}
	return text
}

// claudeThinks reports whether the reasoning setting asks for thinking at all.
func claudeThinks(r config.Reasoning) bool { return r.Enabled && r.Level != "off" }

// claudeArgs is claude's command line for one turn, and the files it names, which
// go in dir. self is this program, which claude starts as the tool server.
func claudeArgs(
	cfg config.LLM, dir, system string, tools []Tool, self string, schema json.RawMessage,
) ([]string, map[string][]byte) {
	files := map[string][]byte{"system.md": []byte(system)}
	args := []string{"-p", "--model", cfg.Model,
		"--input-format", streamJSON, "--output-format", streamJSON, "--verbose", "--include-partial-messages",
		"--tools", "", "--system-prompt-file", filepath.Join(dir, "system.md"),
		"--setting-sources", "", "--strict-mcp-config"}
	if len(tools) > 0 {
		manifest := make([]claudeTool, 0, len(tools))
		for _, t := range tools {
			manifest = append(manifest, claudeTool{
				Name: t.Function.Name, Description: t.Function.Description, InputSchema: t.Function.Parameters,
			})
		}
		files["tools.json"], _ = json.Marshal(manifest)
		mcp, _ := json.Marshal(claudeMCPConfig{MCPServers: map[string]claudeMCPServer{
			"motita": {Command: self, Args: []string{ClaudeCodeMCPCommand, filepath.Join(dir, "tools.json")}},
		}})
		args = append(args, "--mcp-config", string(mcp))
	} else if len(schema) > 0 {
		// A phase that wants one JSON object gets it enforced instead of hoped for: claude
		// answers through a schema-checked tool of its own, and none of the replies that
		// cost real runs whole rounds (prose, a pseudo tool call, a tab inside a string)
		// can come back. It only applies without tools: a tool turn answers with calls.
		args = append(args, "--json-schema", string(schema))
	} else {
		// A plain text turn: nothing to serve and nothing to enforce.
	}
	if claudeThinks(cfg.Reasoning) {
		args = append(args, "--effort", cfg.Reasoning.Level)
	}
	return append(args, "--disable-slash-commands", "--max-turns", "1", "--permission-mode", "dontAsk",
		"--no-session-persistence"), files
}

// claudeBaseEnv is this process' environment without anything that would point
// claude at an API key, another backend or another effort than the user's own
// subscription login and motita's settings.
func claudeBaseEnv() []string {
	env := []string{} // never nil: a nil Env would hand claude this whole environment
	for _, kv := range os.Environ() {
		name, _, _ := strings.Cut(kv, "=")
		if strings.HasPrefix(name, "ANTHROPIC_") || strings.HasPrefix(name, "CLAUDE_CODE_USE_") ||
			name == "CLAUDE_EFFORT" {
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

// startClaude runs claude for at most timeout, in motita's own working directory.
//
// That directory, and not one of claude's own: claude tells the model where it is working and
// whether that is a git repository, and the tools resolve paths against motita's directory. It is
// also the same on every call, which the prompt cache needs. With the flags claude is given it
// reports the directory but loads nothing from it (a CLAUDE.md there is ignored, measured).
func startClaude(ctx context.Context, timeout time.Duration, args, env []string) (*claudeProcess, error) {
	bin := os.Getenv("MOTITA_CLAUDE_BIN")
	if bin == "" {
		bin = "claude"
	}
	// Absolute, so what runs does not depend on the directory it runs in.
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
			if status := motitaMCPStatus(ev); status != "connected" {
				// Without it claude answers with no tools at all, which reads as a model that
				// will not look at anything. A managed MCP policy is one way to get here.
				return ev, fatalError{fmt.Errorf("claude could not use motita's tool server (MCP status %q)", status)}
			}
		}
		if ev.Type == "rate_limit_event" {
			for _, q := range ev.RateLimitInfo.quotas() {
				totals.quota(usageSource{provider: "claude-code"}, q)
			}
		}
		return ev, nil
	}
	return claudeEvent{}, p.exited()
}

// claudeRateLimit is one limit of a Claude subscription as claude reports it: which window, how much
// of it is used (a fraction of 1, sent only once the usage nears a threshold) and when it starts over.
type claudeRateLimit struct {
	Status        string   `json:"status"`
	ResetsAt      float64  `json:"resetsAt"`
	RateLimitType string   `json:"rateLimitType"`
	Utilization   *float64 `json:"utilization"`
	// UnifiedWindows is every window's share and reset, read from the subscription's response
	// headers after an answer that moved a window, below any threshold too. Newer claude versions send it; older ones
	// leave it out.
	UnifiedWindows map[string]claudeWindow `json:"unifiedWindows"`
}

// claudeWindow is one window of a subscription: the fraction of it used and when it starts over.
type claudeWindow struct {
	Utilization float64 `json:"utilization"`
	ResetsAt    float64 `json:"resetsAt"`
}

// claudeWindows names the windows of a subscription that are quotas: the five-hour session and the week.
var claudeWindows = map[string]Quota{
	"five_hour": {Name: "session", Minutes: 300},
	"seven_day": {Name: "week", Minutes: weekMinutes},
}

// quotas is every quota the event states: each window's share when claude sends them all, else the
// one limit the event is about.
func (r *claudeRateLimit) quotas() []Quota {
	if r == nil {
		return nil
	}
	var out []Quota
	for kind, w := range r.UnifiedWindows {
		q, ok := claudeWindows[kind]
		if !ok {
			continue
		}
		q.Used = min(100, max(0, w.Utilization*100))
		q.ResetsAt = epoch(w.ResetsAt)
		out = append(out, q)
	}
	if len(out) > 0 {
		return out
	}
	if q, ok := r.quota(); ok {
		return []Quota{q}
	}
	return nil
}

// epoch is a reset time claude sends, in seconds or in milliseconds, or nil when it sends none.
func epoch(t float64) *time.Time {
	if t <= 0 {
		return nil
	}
	if t > 1e12 { // milliseconds
		t /= 1000
	}
	at := time.Unix(int64(t), 0).UTC()
	return &at
}

// quota is the limit as a quota: the five-hour window is the session and the seven-day one the week.
// It is not one when the event is about another window. claude sends the utilization only near a
// threshold: before that the quota says only whether the usage is below its warning level or past
// it, never a share. A limit that is rejected is fully used.
func (r *claudeRateLimit) quota() (Quota, bool) {
	q, ok := claudeWindows[r.RateLimitType]
	if !ok {
		return Quota{}, false
	}
	switch {
	case r.Utilization != nil:
		q.Used = min(100, max(0, *r.Utilization*100))
	case r.Status == "rejected":
		q.Used = 100
	case r.Status == "allowed":
		q.Status = "ok"
	case r.Status == "allowed_warning":
		q.Status = "warning"
	default:
		return Quota{}, false
	}
	q.ResetsAt = epoch(r.ResetsAt)
	return q, true
}

// motitaMCPStatus is the status claude's init event reports for motita's tool server, "missing"
// when it does not name it.
func motitaMCPStatus(ev claudeEvent) string {
	status := "missing"
	for _, s := range ev.MCPServers {
		if s.Name == "motita" {
			status = s.Status
		}
	}
	return status
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
	t := &claudeTurn{onText: onText}
	for {
		ev, err := p.event()
		if err != nil {
			return Reply{}, err
		}
		switch ev.Type {
		case "stream_event":
			if t.streamEvent(ev) {
				return t.finish(), nil
			}
		case "assistant":
			if ev.Error != "" {
				return Reply{}, assistantError(ev)
			}
			t.assistant(ev)
		case "result":
			return t.result(ev)
		default:
			// system and other events carry nothing of the answer.
		}
	}
}

// claudeTurn is the answer readTurn is putting together.
type claudeTurn struct {
	onText func(string)
	// reply is what the turn has, and msg what the current message adds.
	reply, msg Reply
	stop       string
	// A turn usually ends at a message_stop, before claude prints its result, so what it
	// consumed is added up message by message; the result's totals replace the sum when they
	// do arrive, because they also count what no message_delta reported.
	msgUsage Usage
}

// keep adds the current message to the turn.
func (t *claudeTurn) keep() {
	t.reply.Content += t.msg.Content
	t.reply.Calls = append(t.reply.Calls, t.msg.Calls...)
	t.msg = Reply{}
}

// finish ends the turn: the reply, with the reason it stopped.
func (t *claudeTurn) finish() Reply {
	t.keep()
	t.reply.FinishReason = "stop"
	if len(t.reply.Calls) > 0 {
		t.reply.FinishReason = "tool_calls"
	} else if t.stop == "max_tokens" {
		t.reply.FinishReason = "length"
	} else {
		// A plain stop.
	}
	return t.reply
}

// streamEvent reads one stream event, and reports whether it ended the turn.
func (t *claudeTurn) streamEvent(ev claudeEvent) bool {
	switch ev.Event.Type {
	case "message_start":
		t.msg, t.stop, t.msgUsage = Reply{}, "", Usage{}
	case "content_block_delta":
		if ev.Event.Delta.Type == "text_delta" && t.onText != nil {
			t.onText(ev.Event.Delta.Text)
		}
	case "message_delta":
		t.stop = ev.Event.Delta.StopReason
		t.msgUsage = ev.Event.Usage.usage()
	case "message_stop":
		return t.messageStop()
	default:
		// Other stream events carry nothing the turn keeps.
	}
	return false
}

// messageStop ends the current message, and reports whether it also ended the turn.
func (t *claudeTurn) messageStop() bool {
	t.reply.Usage, t.msgUsage = t.reply.Usage.Add(t.msgUsage), Usage{}
	if isFinalStop(t.stop) && replyHasAnswer(t.msg) {
		// ponytail: claude may start another upstream request after message_stop
		// (it tries to run the denied tool); stop() ends it. A loopback admission
		// relay like Hermes' admission.py is the upgrade path if that shows in usage.
		return true
	}
	if t.stop == "max_tokens" {
		t.keep() // claude continues the answer in the next message
	}
	// ponytail: a retried stream's text already went to onText and is sent again;
	// buffer per message if that shows up in the stream path.
	t.msg = Reply{}
	return false
}

// isFinalStop reports whether a message's stop reason can end the turn.
func isFinalStop(stop string) bool {
	return stop == "end_turn" || stop == "tool_use" || stop == "stop_sequence"
}

// replyHasAnswer reports whether r has anything to show: text or a tool call.
func replyHasAnswer(r Reply) bool {
	return r.Content != "" || len(r.Calls) > 0
}

// assistant adds an assistant message's blocks to the current message.
func (t *claudeTurn) assistant(ev claudeEvent) {
	for _, b := range ev.Message.Content {
		switch b.Type {
		case "text":
			t.msg.Content += b.Text
		case "tool_use":
			t.toolUse(b)
		default:
			// Thinking and other blocks are not part of the reply.
		}
	}
}

// toolUse adds a tool_use block to the current message. With --json-schema claude delivers the
// answer as a call to its own StructuredOutput tool: it is the reply, not a tool motita should run.
func (t *claudeTurn) toolUse(b claudeBlock) {
	if b.Name == claudeStructuredTool {
		t.msg.Content += string(b.Input)
		return
	}
	t.msg.Calls = append(t.msg.Calls, ToolCall{ID: b.ID, Type: "function", Function: FunctionCall{
		Name: strings.TrimPrefix(b.Name, claudeToolPrefix), Arguments: b.Input,
	}})
}

// result reads claude's result event, which ends the turn.
func (t *claudeTurn) result(ev claudeEvent) (Reply, error) {
	t.keep()
	if u := ev.Usage.usage(); u.Total() > 0 {
		t.reply.Usage = u
	}
	if !replyHasAnswer(t.reply) && hasStructuredOutput(ev) {
		t.reply.Content = string(ev.StructuredOutput)
	}
	// error_max_turns after tool calls is claude trying to run a denied tool once
	// the answer was complete: the answer stands.
	if ev.IsError && (ev.Subtype != "error_max_turns" || len(t.reply.Calls) == 0) {
		return Reply{}, fmt.Errorf("claude failed (%s): %s",
			ev.Subtype, strings.TrimSpace(ev.Result+" "+string(ev.Errors)))
	}
	return t.finish(), nil
}

// hasStructuredOutput reports whether a result event repeats a --json-schema answer.
func hasStructuredOutput(ev claudeEvent) bool {
	return len(ev.StructuredOutput) > 0 && string(ev.StructuredOutput) != "null"
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
	default:
		return fatalError{err}
	}
}

// callClaudeCode asks the local claude CLI for the next assistant turn. onText,
// when not nil, receives the text as it streams.
func (c *Client) callClaudeCode(
	ctx context.Context, messages []Message, tools []Tool, onText func(string),
) (Reply, error) {
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
	// Absolute, so the paths claude is given do not depend on where it runs.
	dir, err := os.MkdirTemp("", "motita-claude-")
	if err == nil {
		defer os.RemoveAll(dir)
		dir, err = filepath.Abs(dir)
	}
	if err != nil {
		return Reply{}, err
	}
	args, files := claudeArgs(c.cfg, dir, system, tools, self, jsonSchemaFrom(ctx))
	for name, data := range files {
		if err := writeFile(filepath.Join(dir, name), data, 0o600); err != nil {
			return Reply{}, err
		}
	}
	p, err := startClaude(ctx, c.cfg.Timeout, args, claudeEnv(c.cfg))
	if err != nil {
		return Reply{}, err
	}
	p.mcp = len(tools) > 0
	reply, err := p.turn(frames, onText)
	if err != nil {
		p.stop()
		return Reply{}, err
	}
	// The answer is out; the subscription's limits come after it.
	go p.drain(claudeDrainWait)
	recordUsage(ctx, c.source(), reply.Usage)
	return reply, nil
}

// turn replays the conversation and reads the answer to it.
func (p *claudeProcess) turn(frames []claudeFrame, onText func(string)) (Reply, error) {
	if err := p.replay(frames); err != nil {
		return Reply{}, err
	}
	return p.readTurn(onText)
}

// claudeDrainWait is how long claude is read after its answer before it is stopped.
var claudeDrainWait = 3 * time.Second

// drain reads what claude prints after an answer, for at most wait, then stops it, and reports
// whether claude stated the subscription's limits. claude prints its rate_limit_event after the
// message_stop that ends a turn (measured with claude 2.1.303), so a process stopped at the answer
// never told them. The first event of the limits, or the turn's result, is as far as it is read.
func (p *claudeProcess) drain(wait time.Duration) bool {
	defer p.stop()
	timer := time.AfterFunc(wait, p.cancel)
	defer timer.Stop()
	for {
		ev, err := p.event()
		if err != nil || ev.Type == "result" {
			return false
		}
		if ev.Type == "rate_limit_event" {
			return true
		}
	}
}

// claudeProbeTimeout bounds one ProbeClaudeQuota.
const claudeProbeTimeout = time.Minute

// claudeProbeArgs is claude's command line for ProbeClaudeQuota: the smallest model, no tools, no
// settings, no session left behind, and a system prompt of one line instead of claude's own.
var claudeProbeArgs = append(claudeBareArgs(), "--model", "haiku",
	"--system-prompt", "Answer with the single word: ok", "--max-turns", "1", "--permission-mode", "dontAsk")

// claudeBareArgs is the command line of a claude that only talks stream-json: no tools, no settings,
// no MCP server, no slash command and no session left behind.
func claudeBareArgs() []string {
	return []string{"-p", "--input-format", streamJSON, "--output-format", streamJSON, "--verbose",
		"--tools", "", "--setting-sources", "", "--strict-mcp-config", "--disable-slash-commands",
		"--no-session-persistence"}
}

// ProbeClaudeQuota asks claude the smallest question there is so that it states the subscription's
// limits, which it only does with an answer; they land in the ledger like those of any turn. The
// question is not counted as a call. It fails when claude cannot run or states no limits.
func ProbeClaudeQuota(ctx context.Context) error {
	p, err := startClaude(ctx, claudeProbeTimeout, claudeProbeArgs, claudeEnv(config.LLM{MaxTokens: 16}))
	if err != nil {
		return err
	}
	_, frames, _ := claudeFrames([]Message{{Role: "user", Content: "ok"}})
	if err := p.replay(frames); err != nil {
		p.stop()
		return err
	}
	if !p.drain(claudeProbeTimeout) {
		return errors.New("claude stated no subscription limits")
	}
	return nil
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
func (c *Client) callClaudeCodeStream(
	ctx context.Context, messages []Message, tools []Tool,
) (<-chan StreamChunk, error) {
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
	args := claudeBareArgs()
	// The base environment: with nonessential traffic off claude lists 5 models instead of the
	// account's 11 (measured).
	p, err := startClaude(ctx, 30*time.Second, args, claudeBaseEnv())
	if err != nil {
		return ClaudeCodeCatalogue{}, err
	}
	defer p.stop()
	_, _ = io.WriteString(p.stdin,
		`{"type":"control_request","request_id":"models","request":{"subtype":"initialize"}}`+"\n")
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
			result = map[string]any{
				"protocolVersion": "2024-11-05",
				"capabilities":    map[string]any{"tools": map[string]any{}},
				"serverInfo":      map[string]any{"name": "motita", "version": "1"},
			}
		case "tools/list":
			result = map[string]any{"tools": tools}
		case "tools/call":
			result = map[string]any{"isError": true, "content": []any{
				map[string]any{"type": "text", "text": "denied: motita executes tools itself"},
			}}
		default:
			// Any other method is answered with an empty result.
		}
		if err := enc.Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": result}); err != nil {
			return err
		}
	}
	return sc.Err()
}
