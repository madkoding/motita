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
	"strings"
	"time"

	"github.com/madkoding/starlight/internal/llm"
)

// Resource limits tuned for low-memory systems (i386) and for safety.
const (
	maxFileBytes          = 1 << 20
	maxToolOutputBytes    = 64 << 10
	maxHistory            = 41
	defaultCommandTimeout = 120 * time.Second
	defaultMaxLoops       = 5
)

// CommandRunner executes a single command line and is the only thing the planner
// needs from the agent layer. The real *agent.Agent implements it.
type CommandRunner interface {
	RunCommand(ctx context.Context, command string) (string, int, error)
}

// Planner runs the read-only plan/chat loop.
type Planner struct {
	engine *llm.Client
	runner CommandRunner

	// maxLoops caps the number of tool turns per user message.
	maxLoops int
	// commandTimeout is the default timeout for execute_command.
	commandTimeout time.Duration
	// trace receives progress lines; nil means silent.
	trace func(string, ...any)
	// answer writes the final answer; nil means it is returned.
	answer func(string)
}

// New builds a Planner with the given engine and command runner.
// The runner must enforce read-only semantics; the planner assumes every command
// it submits is safe to run.
func New(engine *llm.Client, runner CommandRunner) *Planner {
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

const systemPrompt = `You are Starlight in read-only plan mode.
Your job is to explore the local system, read files, run read-only commands, and then deliver a plain-text plan of action.

Rules:
- Use read_file when you need the contents of a text file (max 1 MiB).
- Use execute_command when you need command output. Keep commands read-only; any command that would change the system will be refused.
- Do not invent file contents or command output.
- Once you have enough information, deliver the final answer as plain text without calling any more tools.
- Answer in English, directly and concisely.`

// tools returns the tool definitions exposed to the model.
func (p *Planner) tools() []llm.Tool {
	return []llm.Tool{
		llm.NewTool("read_file", "Reads the contents of a text file from the local system (maximum 1 MiB).",
			llm.ObjectSchema(map[string]any{
				"path": llm.StringProperty("Absolute or relative path of the file to read."),
			}, "path")),
		llm.NewTool("execute_command", "Runs a read-only command on the local system and returns stdout and stderr combined. Destructive commands are refused.",
			llm.ObjectSchema(map[string]any{
				"command":         llm.StringProperty("The command to run. Pipes, redirections and shell metacharacters are not allowed in read-only mode."),
				"timeout_seconds": llm.IntegerProperty("Maximum run time in seconds (defaults to 120)."),
			}, "command")),
	}
}

// Run starts with the system prompt, adds the user input, and loops until the
// model delivers a final answer or the loop limit is reached.
func (p *Planner) Run(ctx context.Context, input string) (string, error) {
	if strings.TrimSpace(input) == "" {
		return "", errors.New("the instruction is empty")
	}

	messages := []llm.Message{{Role: "system", Content: systemPrompt}}
	messages = append(messages, llm.Message{Role: "user", Content: input})

	for i := 1; i <= p.maxLoops; i++ {
		p.tracef("[thinking...]")
		reply, err := p.engine.CompleteTools(ctx, messages, p.tools())
		if err != nil {
			return "", fmt.Errorf("could not reach the reasoning engine: %w", err)
		}

		// Assistant message carries text and/or tool calls.
		messages = append(messages, llm.Message{
			Role:      "assistant",
			Content:   reply.Content,
			ToolCalls: reply.Calls,
		})
		messages = trimHistory(messages)

		if !reply.WantsTools() {
			return p.finalize(reply.Content), nil
		}

		for _, tc := range reply.Calls {
			result := p.runTool(ctx, tc)
			messages = append(messages, llm.Message{
				Role:       "tool",
				Content:    result,
				ToolCallID: tc.ID,
			})
			messages = trimHistory(messages)
		}
	}

	// Loop limit reached: force a final text answer with no tools.
	p.tracef("[limit of %d iterations reached; forcing final answer]", p.maxLoops)
	reply, err := p.engine.Complete(ctx, messages)
	if err != nil {
		return "", fmt.Errorf("could not reach the reasoning engine: %w", err)
	}
	return p.finalize(reply), nil
}

func (p *Planner) tracef(format string, args ...any) {
	if p.trace != nil {
		p.trace(format, args...)
	}
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

// runTool executes one tool call and returns a model-readable result.
func (p *Planner) runTool(ctx context.Context, tc llm.ToolCall) string {
	p.tracef("[running tool: %s]", tc.Function.Name)
	switch tc.Function.Name {
	case "read_file":
		return p.toolReadFile(tc.Function.Arguments)
	case "execute_command":
		return p.toolExecuteCommand(ctx, tc.Function.Arguments)
	default:
		return fmt.Sprintf("Error: unknown tool %q", tc.Function.Name)
	}
}

// readFileArgs are the arguments for read_file.
type readFileArgs struct {
	Path string `json:"path"`
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

// trimHistory keeps the system prompt plus the most recent messages, dropping
// the oldest non-system messages when the history grows too large.
func trimHistory(messages []llm.Message) []llm.Message {
	if len(messages) <= maxHistory {
		return messages
	}
	// Keep the system prompt and the most recent maxHistory-1 messages.
	cut := messages[len(messages)-(maxHistory-1):]
	// Drop leading tool results that would be orphaned (no matching assistant call).
	for len(cut) > 0 && cut[0].Role == "tool" {
		cut = cut[1:]
	}
	fresh := make([]llm.Message, 0, len(cut)+1)
	fresh = append(fresh, messages[0])
	fresh = append(fresh, cut...)
	return fresh
}
