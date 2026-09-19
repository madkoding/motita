// Package plan implements the read-only plan/chat mode.
//
// In this mode the agent explores the local system (reads files and runs read-only
// commands) and delivers a plain-text plan of action. It does not change the system:
// every command is routed through agent.RunCommand, which applies the read-only
// policy before execution.
package plan

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/madkoding/starlight/internal/llm"
	"github.com/madkoding/starlight/internal/session"
)

// Resource limits tuned for low-memory systems (i386) and for safety.
const (
	maxFileBytes          = 1 << 20
	maxToolOutputBytes    = 64 << 10
	defaultCommandTimeout = 120 * time.Second
	defaultMaxLoops       = 5
)

// CommandRunner executes a single command line and is the only thing the planner
// needs from the agent layer. The real *agent.Agent implements it.
type CommandRunner interface {
	RunCommand(ctx context.Context, command string) (string, int, error)
}

// Engine is the reasoning client the planner talks to. The real *llm.Client
// implements it; the interface exists so the loop can be driven by a fake in tests.
type Engine interface {
	CompleteToolsStream(ctx context.Context, messages []llm.Message, tools []llm.Tool) <-chan llm.StreamChunk
	Complete(ctx context.Context, messages []llm.Message) (string, error)
}

// Planner runs the read-only plan/chat loop.
type Planner struct {
	engine Engine
	runner CommandRunner

	// maxLoops caps the number of tool turns per user message.
	maxLoops int
	// commandTimeout is the default timeout for execute_command.
	commandTimeout time.Duration
	// trace receives progress lines; nil means silent.
	trace func(string, ...any)
	// answer writes the final answer; nil means it is returned.
	answer func(string)
	// stream receives live model output (thinking fragments, tool call names).
	stream func(string)

	// session is the conversation this planner continues. Nil means "create one on
	// first use": a planner built directly still works, and one that is handed a session
	// keeps the same conversation across runs.
	session *session.Session
	// The window and the compaction policy, so a session created here honours the
	// configuration rather than the package defaults.
	model      string
	window     int
	reserve    int
	compactAt  float64
	keepRecent int
}

// New builds a Planner with the given engine and command runner.
// The runner must enforce read-only semantics; the planner assumes every command
// it submits is safe to run.
func New(engine Engine, runner CommandRunner) *Planner {
	if engine == nil {
		panic("plan.New: engine is nil")
	}
	return &Planner{
		engine:         engine,
		runner:         runner,
		maxLoops:       defaultMaxLoops,
		commandTimeout: defaultCommandTimeout,
	}
}

// WithLoops sets the maximum tool iterations per turn.
func (p *Planner) WithLoops(n int) *Planner {
	if n < 1 {
		n = 1
	}
	p.maxLoops = n
	return p
}

// WithTimeout sets the default command timeout.
func (p *Planner) WithTimeout(d time.Duration) *Planner {
	if d <= 0 {
		d = defaultCommandTimeout
	}
	p.commandTimeout = d
	return p
}

// WithTrace sets the trace function.
func (p *Planner) WithTrace(fn func(string, ...any)) *Planner {
	p.trace = fn
	return p
}

// WithAnswer sets the answer writer.
func (p *Planner) WithAnswer(fn func(string)) *Planner {
	p.answer = fn
	return p
}

// WithStream sets the stream writer used for live model output (thinking, tool
// call announcements and streamed text fragments).
func (p *Planner) WithStream(fn func(string)) *Planner {
	p.stream = fn
	return p
}

// SystemPrompt is the instruction that opens every plan conversation. It is exported
// because the session that carries the conversation between turns must be opened with
// exactly this text: a second copy would drift from the one the requests actually use.
const SystemPrompt = `You are Starlight, an autonomous systems agent running in read-only plan mode.

Your identity and purpose:
- You are not a chatbot. You are an agent that investigates the local machine, reads files, runs safe commands, and produces a concrete, actionable plan.
- You have access to the local filesystem and shell, but you MUST NOT change anything in this mode.
- Every claim you make must be based on a tool result. Do not invent paths, file contents or command output.

Workflow:
1. Understand the user's objective.
2. Explore the system with list_directory, read_file, execute_command and search_in_files until you have the facts.
3. If the request is unclear or impossible, say so explicitly.
4. When you have enough information, deliver the final answer as plain text with a concise plan and the evidence you gathered.

Rules:
- Prefer reading files and directories before running broad commands.
- All commands are read-only. Any destructive command (rm, cp, mv, write, >, |, ;, &&, $(...), etc.) will be refused by the guardrails.
- If a command is refused, do not insist; try a different, read-only approach.
- Keep commands simple and single-purpose.
- Answer in English, directly and concisely.`

// tools returns the tool definitions exposed to the model.
func (p *Planner) tools() []llm.Tool {
	return []llm.Tool{
		llm.NewTool("list_directory", "Lists the contents of a directory (files and subdirectories).",
			llm.ObjectSchema(map[string]any{
				"path": llm.StringProperty("Absolute or relative path of the directory to list. Defaults to the current directory."),
			})),
		llm.NewTool("read_file", "Reads the contents of a text file from the local system (maximum 1 MiB).",
			llm.ObjectSchema(map[string]any{
				"path": llm.StringProperty("Absolute or relative path of the file to read."),
			}, "path")),
		llm.NewTool("execute_command", "Runs a read-only command on the local system and returns stdout and stderr combined. Destructive commands are refused by guardrails.",
			llm.ObjectSchema(map[string]any{
				"command":         llm.StringProperty("The command to run. Pipes, redirections, shell metacharacters and multi-command chains are not allowed in read-only mode."),
				"timeout_seconds": llm.IntegerProperty("Maximum run time in seconds (defaults to 120)."),
			}, "command")),
		llm.NewTool("search_in_files", "Searches for a pattern inside text files under a directory using grep/ripgrep.",
			llm.ObjectSchema(map[string]any{
				"pattern": llm.StringProperty("The regular expression or literal string to search for."),
				"path":    llm.StringProperty("Directory or file to search in. Defaults to the current directory."),
				"literal": llm.StringProperty("Set to true to search for a literal string instead of a regex (default: false)."),
			}, "pattern")),
	}
}

// Run continues the session with one instruction, looping until the model delivers a
// final answer or the loop limit is reached.
//
// The conversation lives in p.session, not in a list built here. That is the difference
// between an agent that can be asked a follow-up question and one that starts from
// nothing every time: the earlier turns are still there, and the summary of whatever had
// to be compacted travels with them.
//
// Before each request the session is given the chance to compact. Doing it HERE, at the
// boundary, rather than after a provider error, is what keeps the agent on task: it
// compacts while it still has room to think, instead of discovering the overflow halfway
// through an answer and losing the thread.
func (p *Planner) Run(ctx context.Context, input string) (string, error) {
	if strings.TrimSpace(input) == "" {
		return "", errors.New("the instruction is empty")
	}

	sess := p.sessionFor()
	sess.Append(llm.Message{Role: "user", Content: input})

	if err := p.compactIfNeeded(ctx, sess); err != nil {
		return "", err
	}

	// WithLoops floors the limit at one, so a planner built through New always has
	// a usable value. A zero-value Planner skips the loop entirely and falls
	// through to the forced answer below, which is the same graceful outcome
	// without a second, unreachable clamp here.
	loops := p.maxLoops
	for i := 1; i <= loops; i++ {
		p.tracef("[thinking...]")
		reply, err := p.streamTools(ctx, sess.Messages())
		if err != nil {
			return "", fmt.Errorf("could not reach the reasoning engine: %w", err)
		}

		// Assistant message carries text and/or tool calls.
		sess.Append(llm.Message{
			Role:      "assistant",
			Content:   reply.Content,
			ToolCalls: reply.Calls,
		})

		if !reply.WantsTools() {
			return p.finalize(reply.Content), nil
		}

		for _, tc := range reply.Calls {
			result := p.runTool(ctx, tc)
			sess.Append(llm.Message{
				Role:       "tool",
				Content:    result,
				ToolCallID: tc.ID,
			})
		}

		// If this was the last allowed loop, add a strong instruction to produce text.
		if i == loops {
			sess.Append(llm.Message{
				Role:    "user",
				Content: "You have reached the tool-call limit. Now deliver the final answer as plain text. Do not call any more tools.",
			})
		}

		// The tool results are what makes a plan run grow fastest, so the check is
		// made after each round rather than only at the start.
		if err := p.compactIfNeeded(ctx, sess); err != nil {
			return "", err
		}
	}

	// Should not be reached because the last loop adds the force message, but keep as
	// a safety net in case the model still calls tools.
	p.tracef("[limit of %d iterations reached; forcing final answer]", p.maxLoops)
	reply, err := p.engine.Complete(ctx, sess.Messages())
	if err != nil {
		return "", fmt.Errorf("could not reach the reasoning engine: %w", err)
	}
	return p.finalize(reply), nil
}

// sessionFor returns the conversation this planner is working in, creating one on first
// use. A planner built without a session still works — it simply gets one per instance,
// which for a single run is the same as owning it.
func (p *Planner) sessionFor() *session.Session {
	if p.session == nil {
		// New already applied the package defaults; the policy overrides only where the
		// configuration actually said something. Assigning zeroes here would disable the
		// reserve and the trigger, which is the failure that lets a context overflow in
		// silence.
		s := session.New(p.model, SystemPrompt, p.window)
		if p.reserve > 0 {
			s.Reserve = p.reserve
		}
		if p.compactAt > 0 {
			s.CompactAt = p.compactAt
		}
		if p.keepRecent > 0 {
			s.KeepRecent = p.keepRecent
		}
		s.Summariser = p.engine
		p.session = s
	}
	return p.session
}

// compactIfNeeded compacts before the context overflows, and reports what it did.
//
// Failure is returned rather than swallowed. The alternative — carrying on with a
// context that is about to be rejected — turns a recoverable condition into a failed task
// in the middle of an answer, which is exactly the behaviour this is meant to prevent.
func (p *Planner) compactIfNeeded(ctx context.Context, sess *session.Session) error {
	if !sess.NeedsCompaction() {
		return nil
	}
	before := sess.Len()
	if err := sess.Compact(ctx); err != nil {
		return fmt.Errorf("the context is full and could not be compacted: %w", err)
	}
	// The interface says so: a user who sees the conversation get shorter without an
	// explanation assumes the agent has lost its place.
	p.tracef("[context compacted: %d messages folded into a summary, %d kept]", before-sess.Len(), sess.Len())
	return nil
}

// Session exposes the conversation, so the caller can report on it and so a later run
// continues in the same one.
func (p *Planner) Session() *session.Session { return p.sessionFor() }

// WithSession continues in an existing conversation instead of starting a new one.
func (p *Planner) WithSession(s *session.Session) *Planner {
	p.session = s
	return p
}

func (p *Planner) tracef(format string, args ...any) {
	if p.trace != nil {
		p.trace(format, args...)
	}
}

func emitToolCall(trace func(string, ...any), name string, args json.RawMessage) {
	if trace == nil {
		return
	}
	var kvMap map[string]string
	_ = json.Unmarshal(args, &kvMap)
	var kv []string
	for k, v := range kvMap {
		kv = append(kv, fmt.Sprintf("%s=%s", k, v))
	}
	sort.Strings(kv)
	trace("[using tool: %s %s]", name, strings.Join(kv, " "))
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func (p *Planner) finalize(content string) string {
	content = strings.TrimSpace(content)
	if content == "" {
		content = "(the model did not deliver a final answer)"
	}
	if p.answer != nil {
		p.answer(content)
	}
	return content
}

// streamTools calls the engine in streaming mode, forwarding live text and tool-call
// announcements through the stream callback. It returns the accumulated reply.
func (p *Planner) streamTools(ctx context.Context, messages []llm.Message) (llm.Reply, error) {
	streamCh := p.engine.CompleteToolsStream(ctx, messages, p.tools())
	var acc llm.StreamResult
	var pendingTool string
	var textBuf strings.Builder
	for chunk := range streamCh {
		switch chunk.Event {
		case llm.StreamError:
			return llm.Reply{}, chunk.Error
		case llm.StreamDone:
			return acc.FinalReply(), nil
		case llm.StreamText:
			textBuf.WriteString(chunk.Text)
			p.writeStream(chunk.Text)
			acc.Handle(chunk)
		case llm.StreamToolCall:
			// Announce each distinct tool once: the stream repeats the call while
			// its arguments are still arriving, and forwarding every repetition
			// would fill the screen with the same line.
			if chunk.Call != nil && chunk.Call.Function.Name != "" && chunk.Call.Function.Name != pendingTool {
				pendingTool = chunk.Call.Function.Name
				p.writeStream(fmt.Sprintf("[using tool: %s]", chunk.Call.Function.Name))
			}
			acc.Handle(chunk)
		}
	}
	// Channel closed without a done event: use what we accumulated.
	return acc.FinalReply(), nil
}

func (p *Planner) writeStream(s string) {
	if p.stream != nil {
		p.stream(s)
	}
}

// runTool executes one tool call and returns a model-readable result.
func (p *Planner) runTool(ctx context.Context, tc llm.ToolCall) string {
	emitToolCall(p.trace, tc.Function.Name, tc.Function.Arguments)
	switch tc.Function.Name {
	case "list_directory":
		return p.toolListDirectory(tc.Function.Arguments)
	case "read_file":
		return p.toolReadFile(tc.Function.Arguments)
	case "execute_command":
		return p.toolExecuteCommand(ctx, tc.Function.Arguments)
	case "search_in_files":
		return p.toolSearchInFiles(ctx, tc.Function.Arguments)
	default:
		return fmt.Sprintf("Error: unknown tool %q", tc.Function.Name)
	}
}

// readFileArgs are the arguments for read_file.
type readFileArgs struct {
	Path string `json:"path"`
}

// listDirectoryArgs are the arguments for list_directory.
type listDirectoryArgs struct {
	Path string `json:"path"`
}

// toolListDirectory lists a directory's entries.
func (p *Planner) toolListDirectory(raw []byte) string {
	var args listDirectoryArgs
	if err := decodeArgs(raw, &args); err != nil {
		return "Error parsing the arguments: " + err.Error()
	}
	path := strings.TrimSpace(args.Path)
	if path == "" {
		path = "."
	}

	entries, err := os.ReadDir(path)
	if err != nil {
		return "Error listing the directory: " + err.Error()
	}
	if len(entries) == 0 {
		return "(empty directory)"
	}
	var sb strings.Builder
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() {
			name += "/"
		}
		fmt.Fprintln(&sb, name)
	}
	return strings.TrimSpace(sb.String())
}

// toolReadFile reads a text file with the 1 MiB limit.
func (p *Planner) toolReadFile(raw []byte) string {
	var args readFileArgs
	if err := decodeArgs(raw, &args); err != nil {
		return "Error parsing the arguments: " + err.Error()
	}
	path := strings.TrimSpace(args.Path)
	if path == "" {
		return "Error: the 'path' parameter is missing."
	}

	fi, err := os.Stat(path)
	if err != nil {
		return "Error accessing the file: " + err.Error()
	}
	if fi.IsDir() {
		return fmt.Sprintf("Error: %q is a directory; list its contents with execute_command.", path)
	}
	if fi.Size() > maxFileBytes {
		return fmt.Sprintf("Error: the file is %d bytes and exceeds the %d-byte limit (1 MiB).", fi.Size(), maxFileBytes)
	}

	f, err := os.Open(path)
	if err != nil {
		return "Error opening the file: " + err.Error()
	}
	defer f.Close()

	data, err := io.ReadAll(io.LimitReader(f, maxFileBytes))
	if err != nil {
		return "Error reading the file: " + err.Error()
	}
	if len(data) == 0 {
		return "(the file is empty)"
	}
	return string(data)
}

// executeCommandArgs are the arguments for execute_command.
type executeCommandArgs struct {
	Command        string `json:"command"`
	TimeoutSeconds int    `json:"timeout_seconds,omitempty"`
}

// toolExecuteCommand runs a command through the agent's read-only executor.
func (p *Planner) toolExecuteCommand(ctx context.Context, raw []byte) string {
	var args executeCommandArgs
	if err := decodeArgs(raw, &args); err != nil {
		return "Error parsing the arguments: " + err.Error()
	}
	command := strings.TrimSpace(args.Command)
	if command == "" {
		return "Error: the 'command' parameter is missing."
	}

	timeout := p.commandTimeout
	if args.TimeoutSeconds > 0 {
		timeout = time.Duration(args.TimeoutSeconds) * time.Second
	}

	toolCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	output, exit, err := p.runner.RunCommand(toolCtx, command)
	output = limitString(output, maxToolOutputBytes)

	var sb strings.Builder
	fmt.Fprintf(&sb, "$ %s\n", command)
	if err != nil {
		fmt.Fprintf(&sb, "Execution error: %v\n", err)
	}
	if output != "" {
		sb.WriteString(output)
		if !strings.HasSuffix(output, "\n") {
			sb.WriteString("\n")
		}
	}
	fmt.Fprintf(&sb, "[exit=%d]", exit)
	return sb.String()
}

// searchInFilesArgs are the arguments for search_in_files.
type searchInFilesArgs struct {
	Pattern string `json:"pattern"`
	Path    string `json:"path"`
	Literal string `json:"literal"`
}

// toolSearchInFiles searches text files for a pattern.
func (p *Planner) toolSearchInFiles(ctx context.Context, raw []byte) string {
	var args searchInFilesArgs
	if err := decodeArgs(raw, &args); err != nil {
		return "Error parsing the arguments: " + err.Error()
	}
	pattern := strings.TrimSpace(args.Pattern)
	if pattern == "" {
		return "Error: the 'pattern' parameter is missing."
	}
	path := strings.TrimSpace(args.Path)
	if path == "" {
		path = "."
	}

	toolCtx, cancel := context.WithTimeout(ctx, p.commandTimeout)
	defer cancel()

	var command string
	if strings.EqualFold(args.Literal, "true") {
		command = fmt.Sprintf("grep -R -n -F %s %s", shellQuote(pattern), shellQuote(path))
	} else {
		command = fmt.Sprintf("grep -R -n -E %s %s", shellQuote(pattern), shellQuote(path))
	}

	output, exit, err := p.runner.RunCommand(toolCtx, command)
	output = limitString(output, maxToolOutputBytes)

	var sb strings.Builder
	fmt.Fprintf(&sb, "$ %s\n", command)
	if err != nil {
		fmt.Fprintf(&sb, "Execution error: %v\n", err)
	}
	if output != "" {
		sb.WriteString(output)
		if !strings.HasSuffix(output, "\n") {
			sb.WriteString("\n")
		}
	}
	fmt.Fprintf(&sb, "[exit=%d]", exit)
	return sb.String()
}

// decodeArgs normalizes provider differences (object, stringified object, empty).
func decodeArgs(raw []byte, dst any) error {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || string(trimmed) == "null" {
		return nil
	}
	if trimmed[0] == '"' {
		var text string
		if err := json.Unmarshal(trimmed, &text); err != nil {
			return err
		}
		trimmed = []byte(strings.TrimSpace(text))
	}
	if len(trimmed) == 0 {
		return nil
	}
	return json.Unmarshal(trimmed, dst)
}

// limitString truncates text to the given byte length and appends a notice.
func limitString(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + fmt.Sprintf("\n[... output truncated to %d KiB ...]", max/1024)
}

// WithSessionPolicy configures the conversation a planner keeps: the model whose window
// it must respect, and the compaction policy from the configuration.
//
// The values are applied to the session when it is created, so a session shared between
// planners keeps the policy it was built with — which is what a conversation spanning many
// turns needs.
func (p *Planner) WithSessionPolicy(model string, window, reserve int, compactAt float64, keepRecent int) *Planner {
	p.model = model
	p.window = window
	p.reserve = reserve
	p.compactAt = compactAt
	p.keepRecent = keepRecent
	return p
}
