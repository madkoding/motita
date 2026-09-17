// Command starlight is a command-line AI agent written in pure Go
// (standard library only, zero external dependencies) that talks to any
// OpenAI-compatible API.
//
// It is aimed at i386 machines (linux/386): the binary is static, does not use
// cgo and applies explicit memory limits (file reads, command output, history
// size) plus network timeouts.
//
// Environment variables:
//
//	OPENAI_API_KEY   (required) API key.
//	OPENAI_BASE_URL  (optional) base URL; defaults to https://api.openai.com/v1
//	OPENAI_MODEL     (optional) model; defaults to gpt-4o-mini
//
// Usage:
//
//	export OPENAI_API_KEY=sk-...
//	starlight                                            # interactive REPL
//	starlight -p "list the .go files in the current directory"  # one instruction and exit
//
// Trace lines ([Thinking...], [Running tool: ...]) are written to stderr; the
// final answer goes to stdout, so it can be piped.
package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"runtime"
	"strings"
	"time"
)

// ---------------------------------------------------------------------------
// Presentation and resource limits
// ---------------------------------------------------------------------------

const (
	// ANSI colours by hand: no external library is allowed.
	colorReset  = "\033[0m"
	colorBold   = "\033[1m"
	colorDim    = "\033[2m"
	colorRed    = "\033[31m"
	colorGreen  = "\033[32m"
	colorYellow = "\033[33m"
	colorCyan   = "\033[36m"

	// maxFileBytes limits read_file to 1 MiB: RAM is scarce on an i386 and we do
	// not want the agent trying to load a video or a huge log.
	maxFileBytes = 1 << 20

	// maxToolOutputBytes trims the accumulated output of a command (64 KiB).
	maxToolOutputBytes = 64 << 10

	// maxResponseBytes limits how much of the HTTP response is read.
	maxResponseBytes = 8 << 20

	// maxHistory is how many messages are kept in memory (the system prompt
	// always stays). It stops a long REPL from growing without control in 32-bit.
	maxHistory = 41

	// defaultCommandTimeout stops a hung command from blocking the agent.
	defaultCommandTimeout = 120

	defaultMaxLoops = 5
	defaultBaseURL  = "https://api.openai.com/v1"
	defaultModel    = "gpt-4o-mini"
	defaultTimeout  = 60 * time.Second

	programName = "starlight"
)

// version is injected at build time: -ldflags "-X main.version=v1.0.0".
var version = "dev"

// exitProcess ends the program. It is a variable so the command-line decisions
// can be tested in-process: os.Exit cannot be observed from inside a test.
var exitProcess = os.Exit

// colorEnabled is turned off with --no-color or with NO_COLOR=1.
var colorEnabled = true

// paint wraps s in an ANSI colour, honouring the global setting.
func paint(color, s string) string {
	if !colorEnabled || color == "" {
		return s
	}
	return color + s + colorReset
}

// trace writes progress messages to stderr (they never contaminate stdout).
func trace(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format, args...)
}

const systemPrompt = `You are Starlight, a terminal agent that helps work on the local system.
You answer in English, directly and concisely.

Rules:
- Use the tools when you need real information from the system (file contents or
  command output). Never invent a file's contents or a command's result.
- Prefer read-only commands. Before running anything destructive (deleting,
  overwriting, installing, publishing), explain what you are going to do and wait
  for confirmation.
- If a tool returns an error, read it and correct the plan before retrying.
- Once you have the answer, deliver it as final text without calling any more
  tools.`

// ---------------------------------------------------------------------------
// OpenAI-compatible API types
// ---------------------------------------------------------------------------

type chatRequest struct {
	Model      string    `json:"model"`
	Messages   []message `json:"messages"`
	Tools      []tool    `json:"tools,omitempty"`
	ToolChoice string    `json:"tool_choice,omitempty"`
}

type message struct {
	Role       string     `json:"role"`
	Content    string     `json:"content,omitempty"`
	ToolCalls  []toolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
	Name       string     `json:"name,omitempty"`
}

type tool struct {
	Type     string      `json:"type"`
	Function functionDef `json:"function"`
}

type functionDef struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"`
}

type toolCall struct {
	ID       string       `json:"id"`
	Type     string       `json:"type"`
	Function functionCall `json:"function"`
}

type functionCall struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

type chatResponse struct {
	Choices []struct {
		Message      message `json:"message"`
		FinishReason string  `json:"finish_reason"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

// ---------------------------------------------------------------------------
// Configuration
// ---------------------------------------------------------------------------

type config struct {
	apiKey   string
	baseURL  string
	model    string
	maxLoops int
	timeout  time.Duration
}

func getEnv(key, fallback string) string {
	if v, ok := os.LookupEnv(key); ok && strings.TrimSpace(v) != "" {
		return strings.TrimSpace(v)
	}
	return fallback
}

// ---------------------------------------------------------------------------
// Agent tools
// ---------------------------------------------------------------------------

// readFileArgs are read_file's arguments.
type readFileArgs struct {
	Path string `json:"path"`
}

// runCommandArgs are run_command's arguments.
type runCommandArgs struct {
	Cmd            string `json:"cmd"`
	TimeoutSeconds int    `json:"timeout_seconds,omitempty"`
}

// decodeArgs deserialises a tool's arguments.
//
// It covers three formats seen in production (not every
// "OpenAI-compatible" provider honours the specification):
//
//	{"path":"x"}          -> JSON object (what the spec mandates)
//	"{\"path\":\"x\"}"    -> a string containing JSON (seen on several gateways)
//	(empty)               -> no arguments
//
// Without this normalisation the agent would fail with "cannot unmarshal string"
// even when the user wrote the request correctly.
func decodeArgs(raw json.RawMessage, dst any) error {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || string(trimmed) == "null" {
		return nil
	}
	if trimmed[0] == '"' {
		var text string
		if err := json.Unmarshal(trimmed, &text); err != nil {
			return err
		}
		if strings.TrimSpace(text) == "" {
			return nil
		}
		trimmed = []byte(text)
	}
	return json.Unmarshal(trimmed, dst)
}

// toolReadFile implements read_file (text, max 1 MiB).
func toolReadFile(raw json.RawMessage) string {
	var args readFileArgs
	if err := decodeArgs(raw, &args); err != nil {
		return "Error parsing the arguments: " + err.Error()
	}
	if strings.TrimSpace(args.Path) == "" {
		return "Error: the 'path' parameter is missing."
	}

	fi, err := os.Stat(args.Path)
	if err != nil {
		return "Error accessing the file: " + err.Error()
	}
	if fi.IsDir() {
		return "Error: '" + args.Path + "' is a directory; list its contents with run_command (for example: ls -la " + args.Path + ")."
	}
	if fi.Size() > maxFileBytes {
		return fmt.Sprintf("Error: the file is %d bytes and exceeds the %d-byte limit (1 MiB).", fi.Size(), maxFileBytes)
	}

	f, err := os.Open(args.Path)
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

// limitedBuffer accumulates at most max bytes and records whether anything was
// cut, so the whole output of a command is not loaded into RAM.
type limitedBuffer struct {
	buf       bytes.Buffer
	max       int
	truncated bool
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	room := b.max - b.buf.Len()
	if room <= 0 {
		b.truncated = true
		return len(p), nil // the surplus is dropped, but the command does not fail
	}
	if room < len(p) {
		b.buf.Write(p[:room])
		b.truncated = true
		return len(p), nil
	}
	b.buf.Write(p)
	return len(p), nil
}

// toolRunCommand implements run_command through `sh -c`, so pipes, redirections
// and quoting work the way a person would expect.
func toolRunCommand(raw json.RawMessage) string {
	var args runCommandArgs
	if err := decodeArgs(raw, &args); err != nil {
		return "Error parsing the arguments: " + err.Error()
	}
	command := strings.TrimSpace(args.Cmd)
	if command == "" {
		return "Error: the 'cmd' parameter is missing."
	}

	seconds := args.TimeoutSeconds
	if seconds <= 0 {
		seconds = defaultCommandTimeout
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(seconds)*time.Second)
	defer cancel()

	cmd := shellCommand(ctx, command)
	// Commands go into their own process group and are killed as a group when
	// the deadline expires: if only `sh` died, its children would keep the pipe
	// open and Wait would hang (see process_unix.go).
	configureGroup(cmd)
	cmd.Cancel = func() error { return killGroup(cmd) }
	cmd.WaitDelay = 2 * time.Second

	output := &limitedBuffer{max: maxToolOutputBytes}
	cmd.Stdout = output
	cmd.Stderr = output

	err := cmd.Run()
	text := output.buf.String()
	if output.truncated {
		text += fmt.Sprintf("\n[... output truncated to %d KiB ...]", maxToolOutputBytes/1024)
	}

	if ctx.Err() == context.DeadlineExceeded {
		return fmt.Sprintf("Error: the command exceeded the %d s limit and was terminated.\nPartial output:\n%s", seconds, text)
	}
	if err != nil {
		return fmt.Sprintf("Execution error: %v\nOutput:\n%s", err, text)
	}
	if strings.TrimSpace(text) == "" {
		return "(the command finished without producing output)"
	}
	return text
}

// toolDefinitions describes the functions exposed to the model.
func toolDefinitions() []tool {
	return []tool{
		{
			Type: "function",
			Function: functionDef{
				Name:        "read_file",
				Description: "Reads the contents of a text file from the local system (maximum 1 MiB).",
				Parameters: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"path": map[string]any{
							"type":        "string",
							"description": "Absolute or relative path of the file to read.",
						},
					},
					"required": []string{"path"},
				},
			},
		},
		{
			Type: "function",
			Function: functionDef{
				Name:        "run_command",
				Description: "Runs a shell command (sh -c) on the local system and returns stdout and stderr combined.",
				Parameters: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"cmd": map[string]any{
							"type":        "string",
							"description": "Shell command to run (pipes and redirections allowed).",
						},
						"timeout_seconds": map[string]any{
							"type":        "integer",
							"description": fmt.Sprintf("Maximum run time in seconds (defaults to %d).", defaultCommandTimeout),
						},
					},
					"required": []string{"cmd"},
				},
			},
		},
	}
}

// ---------------------------------------------------------------------------
// Agent
// ---------------------------------------------------------------------------

type agent struct {
	cfg    config
	client *http.Client
	hist   []message
}

func newAgent(cfg config) *agent {
	a := &agent{
		cfg:    cfg,
		client: &http.Client{Timeout: cfg.timeout},
	}
	a.reset()
	a.hist = append(a.hist, message{Role: "system", Content: systemPrompt})
	return a
}

// reset empties the history, leaving only the system prompt.
func (a *agent) reset() {
	a.hist = []message{{Role: "system", Content: systemPrompt}}
}

// complete sends the history to the API and returns the assistant's message.
// An empty toolChoice means "decide freely"; "none" forbids tools.
func (a *agent) complete(toolChoice string) (message, error) {
	request := chatRequest{
		Model:      a.cfg.model,
		Messages:   a.hist,
		Tools:      toolDefinitions(),
		ToolChoice: toolChoice,
	}
	// The request only carries strings and slices of structs of strings, so the
	// marshalling cannot fail; a failure here would be a programming error.
	body, _ := json.Marshal(request)

	url := strings.TrimRight(a.cfg.baseURL, "/") + "/chat/completions"
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return message{}, fmt.Errorf("invalid request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+a.cfg.apiKey)

	resp, err := a.client.Do(req)
	if err != nil {
		return message{}, fmt.Errorf("network error: %w", err)
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return message{}, fmt.Errorf("error reading the response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		var apiErr struct {
			Error struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if json.Unmarshal(data, &apiErr) == nil && apiErr.Error.Message != "" {
			return message{}, fmt.Errorf("HTTP %d: %s", resp.StatusCode, apiErr.Error.Message)
		}
		return message{}, fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(data)))
	}

	var out chatResponse
	if err := json.Unmarshal(data, &out); err != nil {
		return message{}, fmt.Errorf("unreadable response: %w", err)
	}
	if out.Error != nil && out.Error.Message != "" {
		return message{}, errors.New("the API returned an error: " + out.Error.Message)
	}
	// Some providers return 200 with empty choices: do not index blindly.
	if len(out.Choices) == 0 {
		return message{}, errors.New("the API returned no option (empty choices)")
	}
	return out.Choices[0].Message, nil
}

// trimHistory keeps the system prompt and the most recent messages, without
// leaving orphaned tool results at the front.
func (a *agent) trimHistory() {
	if len(a.hist) <= maxHistory {
		return
	}
	cut := a.hist[len(a.hist)-(maxHistory-1):]
	for len(cut) > 0 && cut[0].Role == "tool" {
		cut = cut[1:]
	}
	fresh := make([]message, 0, len(cut)+1)
	fresh = append(fresh, a.hist[0])
	fresh = append(fresh, cut...)
	a.hist = fresh
}

// runTool runs one call from the model and returns its result.
func (a *agent) runTool(tc toolCall) string {
	trace("  %s\n", paint(colorYellow, "[⚙️  Running tool: "+tc.Function.Name+"]"))
	switch tc.Function.Name {
	case "read_file":
		return toolReadFile(tc.Function.Arguments)
	case "run_command":
		return toolRunCommand(tc.Function.Arguments)
	default:
		return "Error: unknown tool '" + tc.Function.Name + "'"
	}
}

// turn processes one user instruction: an agent loop with a maximum number of
// tool iterations and a forced final answer if it runs out.
func (a *agent) turn(input string) error {
	a.hist = append(a.hist, message{Role: "user", Content: input})
	a.trimHistory()

	previousMessages := len(a.hist)

	for i := 1; i <= a.cfg.maxLoops; i++ {
		trace("  %s\n", paint(colorCyan, "[🧠 Thinking...]"))

		msg, err := a.complete("")
		if err != nil {
			// The failed turn is rolled back so the history is not left
			// inconsistent.
			a.hist = a.hist[:previousMessages-1]
			return err
		}
		a.hist = append(a.hist, msg)
		a.trimHistory()

		if len(msg.ToolCalls) == 0 {
			content := strings.TrimSpace(msg.Content)
			if content == "" {
				content = "(the model returned an empty response)"
			}
			fmt.Fprintln(os.Stdout, paint(colorBold, content))
			return nil
		}

		for _, tc := range msg.ToolCalls {
			result := a.runTool(tc)
			a.hist = append(a.hist, message{
				Role:       "tool",
				Content:    result,
				ToolCallID: tc.ID,
				Name:       tc.Function.Name,
			})
			a.trimHistory()
		}
	}

	// The iterations ran out: the final answer is requested with no tools.
	trace("  %s\n", paint(colorYellow, fmt.Sprintf("[⏳ Limit of %d iterations reached: forcing the final answer]", a.cfg.maxLoops)))
	msg, err := a.complete("none")
	if err != nil {
		a.hist = a.hist[:previousMessages-1]
		return err
	}
	a.hist = append(a.hist, msg)
	a.trimHistory()

	content := strings.TrimSpace(msg.Content)
	if content == "" {
		content = "(the model did not deliver a final answer)"
	}
	fmt.Fprintln(os.Stdout, paint(colorBold, content))
	return nil
}

// ---------------------------------------------------------------------------
// User interface
// ---------------------------------------------------------------------------

func help() {
	fmt.Fprint(os.Stderr, strings.Join([]string{
		"",
		paint(colorBold, "starlight — terminal AI agent (pure Go, OpenAI-compatible)"),
		"",
		"REPL commands:",
		"  /help       this help",
		"  /reset      forget the conversation",
		"  exit        finish (also Ctrl+D)",
		"",
		"Environment variables:",
		"  OPENAI_API_KEY   API key (required)",
		"  OPENAI_BASE_URL  defaults to https://api.openai.com/v1",
		"  OPENAI_MODEL     defaults to gpt-4o-mini",
		"",
	}, "\n"))
}

func main() {
	var (
		flagPrompt  = flag.String("p", "", "run one instruction and exit (non-interactive mode)")
		flagModel   = flag.String("model", "", "model to use (defaults to $OPENAI_MODEL or "+defaultModel+")")
		flagURL     = flag.String("url", "", "API base URL (defaults to $OPENAI_BASE_URL)")
		flagLoops   = flag.Int("max-loops", defaultMaxLoops, "maximum tool iterations per turn")
		flagTimeout = flag.Duration("timeout", defaultTimeout, "HTTP timeout per request")
		flagNoColor = flag.Bool("no-color", false, "disable ANSI colours")
		flagVersion = flag.Bool("version", false, "print the version and exit")
	)
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: %s [options] [instruction]\n\nOptions:\n", programName)
		flag.PrintDefaults()
	}
	flag.Parse()

	if *flagVersion {
		fmt.Printf("%s %s (%s/%s)\n", programName, version, runtime.GOOS, runtime.GOARCH)
		return
	}

	if *flagNoColor || os.Getenv("NO_COLOR") != "" {
		colorEnabled = false
	}

	cfg := config{
		apiKey:   getEnv("OPENAI_API_KEY", ""),
		baseURL:  getEnv("OPENAI_BASE_URL", defaultBaseURL),
		model:    getEnv("OPENAI_MODEL", defaultModel),
		maxLoops: *flagLoops,
		timeout:  *flagTimeout,
	}
	if *flagModel != "" {
		cfg.model = *flagModel
	}
	if *flagURL != "" {
		cfg.baseURL = *flagURL
	}
	if cfg.maxLoops < 1 {
		cfg.maxLoops = 1
	}

	if cfg.apiKey == "" {
		fmt.Fprintf(os.Stderr, "%s\n", paint(colorRed, "❌ Error: you must set the OPENAI_API_KEY environment variable."))
		fmt.Fprintf(os.Stderr, "   Example: export OPENAI_API_KEY=\"your_key\"\n")
		exitProcess(1)
		return
	}

	ag := newAgent(cfg)

	// Non-interactive mode: instruction from a flag or from loose arguments.
	input := *flagPrompt
	if input == "" && flag.NArg() > 0 {
		input = strings.Join(flag.Args(), " ")
	}
	if input != "" {
		// One-shot mode: one instruction, then out. A failure exits non-zero so a
		// script can tell it apart from a success (there is nothing to return to).
		if err := ag.turn(input); err != nil {
			fmt.Fprintf(os.Stderr, "%s\n", paint(colorRed, "❌ "+err.Error()))
			exitProcess(1)
			return
		}
		exitProcess(0)
		return
	}

	trace("%s\n", paint(colorGreen, "🤖 Starlight ready. Type your instruction ('/help' for help, 'exit' to finish)."))
	trace("%s\n", paint(colorDim, fmt.Sprintf("   model=%s  endpoint=%s  max-loops=%d", cfg.model, cfg.baseURL, cfg.maxLoops)))

	reader := bufio.NewReader(os.Stdin)
	for {
		trace("\n%s", paint(colorBold, "> "))

		line, err := reader.ReadString('\n')
		line = strings.TrimSpace(line)

		if line == "" {
			if err != nil { // EOF (Ctrl+D) or read error
				trace("\n%s\n", paint(colorDim, "👋 Agent finished."))
				return
			}
			continue
		}

		switch strings.ToLower(line) {
		case "exit", "quit", "/exit", "/quit":
			trace("%s\n", paint(colorDim, "👋 Agent finished."))
			return
		case "/help", "help":
			help()
			continue
		case "/reset":
			ag.reset()
			trace("%s\n", paint(colorDim, "🧹 Conversation forgotten."))
			continue
		}

		if err := ag.turn(line); err != nil {
			fmt.Fprintf(os.Stderr, "%s\n", paint(colorRed, "❌ "+err.Error()))
		}

		if err != nil { // the input stream was closed
			trace("%s\n", paint(colorDim, "👋 Agent finished."))
			return
		}
	}
}
