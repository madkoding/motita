package llm

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/madkoding/motita/internal/config"
)

// TestMain lets the test binary stand in for the claude CLI. With
// MOTITA_FAKE_CLAUDE set it never runs the tests: it records what the client gave
// it and prints the script in that variable instead of talking to a model.
func TestMain(m *testing.M) {
	if script, ok := os.LookupEnv("MOTITA_FAKE_CLAUDE"); ok {
		fakeClaude(script)
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// claudeRecord is what the fake saw.
type claudeRecord struct {
	Args     []string          `json:"args"`
	Env      []string          `json:"env"`
	Dir      string            `json:"dir"`
	System   string            `json:"system"`
	MCP      string            `json:"mcp"`
	Manifest string            `json:"manifest"`
	Frames   []json.RawMessage `json:"frames"`
}

// fakeClaude acknowledges every replayed frame (or does what
// MOTITA_FAKE_CLAUDE_ACK says), records everything once its input ends, then
// prints the script line by line. "sleep" hangs, "stderr:<text>" writes to stderr.
// A SIGTERM leaves <record>.term behind, so a test can tell it from a kill.
func fakeClaude(script string) {
	record := os.Getenv("MOTITA_FAKE_CLAUDE_RECORD")
	term := make(chan os.Signal, 1)
	signal.Notify(term, syscall.SIGTERM)
	go func() {
		<-term
		_ = os.WriteFile(record+".term", nil, 0o600)
		os.Exit(0)
	}()

	rec := claudeRecord{Args: os.Args[1:], Env: os.Environ()}
	rec.Dir, _ = os.Getwd()
	init := `{"type":"system","subtype":"init","apiKeySource":"none"}`
	for i, a := range rec.Args {
		switch a {
		case "--system-prompt-file":
			b, _ := os.ReadFile(rec.Args[i+1])
			rec.System = string(b)
		case "--mcp-config":
			rec.MCP = rec.Args[i+1]
			var cfg claudeMCPConfig
			_ = json.Unmarshal([]byte(rec.MCP), &cfg)
			b, _ := os.ReadFile(cfg.MCPServers["motita"].Args[1])
			rec.Manifest = string(b)
			status := os.Getenv("MOTITA_FAKE_CLAUDE_MCP")
			if status == "" {
				status = "connected"
			}
			init = fmt.Sprintf(`{"type":"system","subtype":"init","mcp_servers":[{"name":"other","status":"failed"},{"name":"motita","status":%q}]}`, status)
		}
	}
	// What claude prints before any answer, plus a line that is not JSON at all.
	fmt.Println(init)
	fmt.Println("not json")

	sc := bufio.NewScanner(os.Stdin)
	sc.Buffer(make([]byte, 0, 1<<16), 1<<24)
	for sc.Scan() {
		rec.Frames = append(rec.Frames, json.RawMessage(slices.Clone(sc.Bytes())))
		if bytes.Contains(sc.Bytes(), []byte(`"shouldQuery":false`)) {
			switch ack := os.Getenv("MOTITA_FAKE_CLAUDE_ACK"); ack {
			case "exit":
				os.Exit(3)
			case "":
				fmt.Println(`{"type":"result","subtype":"success","num_turns":0}`)
			default:
				fmt.Println(ack)
			}
		}
	}
	data, _ := json.Marshal(rec)
	_ = os.WriteFile(record, data, 0o600)

	for _, line := range strings.Split(script, "\n") {
		switch {
		case line == "sleep":
			time.Sleep(time.Minute)
		case strings.HasPrefix(line, "stderr:"):
			fmt.Fprint(os.Stderr, strings.TrimPrefix(line, "stderr:"))
		default:
			fmt.Println(line)
		}
	}
}

// claudeTestClient points the claude-code provider at the fake with a script,
// and returns where the fake's record will be.
func claudeTestClient(t *testing.T, script string, tweak func(*config.LLM)) (*Client, string) {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	record := filepath.Join(t.TempDir(), "record.json")
	t.Setenv("MOTITA_CLAUDE_BIN", exe)
	t.Setenv("MOTITA_FAKE_CLAUDE", script)
	t.Setenv("MOTITA_FAKE_CLAUDE_RECORD", record)
	// Under -race a process sleeps a second on exit to report late races; the fake has
	// none worth that on every call.
	t.Setenv("GORACE", "atexit_sleep_ms=0")
	cfg := config.LLM{Provider: "claude-code", Model: "sonnet", MaxAttempts: 1}
	if tweak != nil {
		tweak(&cfg)
	}
	c, err := New(cfg, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return c, record
}

func readRecord(t *testing.T, path string) claudeRecord {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("the fake left no record: %v", err)
	}
	var rec claudeRecord
	if err := json.Unmarshal(data, &rec); err != nil {
		t.Fatal(err)
	}
	return rec
}

func lines(l ...string) string { return strings.Join(l, "\n") }

func textDelta(s string) string {
	return fmt.Sprintf(`{"type":"stream_event","event":{"type":"content_block_delta","delta":{"type":"text_delta","text":%q}}}`, s)
}

func assistantText(s string) string {
	return fmt.Sprintf(`{"type":"assistant","message":{"content":[{"type":"text","text":%q}]}}`, s)
}

func stopReason(s string) string {
	return fmt.Sprintf(`{"type":"stream_event","event":{"type":"message_delta","delta":{"stop_reason":%q}}}`, s)
}

const (
	evStart = `{"type":"stream_event","event":{"type":"message_start"}}`
	evStop  = `{"type":"stream_event","event":{"type":"message_stop"}}`
	evTool  = `{"type":"assistant","message":{"content":[{"type":"tool_use","id":"toolu_1","name":"mcp__motita__read_file","input":{"path":"go.mod"}}]}}`
)

// textScript is a streamed text answer; nothing after its message_stop may be needed.
var textScript = lines(evStart, textDelta("Reading "), textDelta("go.mod."), assistantText("Reading go.mod."),
	stopReason("end_turn"), evStop, "sleep")

// toolScript is what claude answered live with a tool call: the tool_use, then
// message_stop, then the result of the turn it could not take. The result must
// never be read: the reply is complete at message_stop.
var toolScript = lines(
	evStart,
	`{"type":"stream_event","event":{"type":"content_block_delta","delta":{"type":"thinking_delta","thinking":"hmm"}}}`,
	`{"type":"assistant","message":{"content":[{"type":"thinking","thinking":"hmm"}]}}`,
	textDelta("Reading "), textDelta("go.mod."), assistantText("Reading go.mod."),
	`{"type":"stream_event","event":{"type":"content_block_delta","delta":{"type":"input_json_delta","partial_json":"{}"}}}`,
	evTool,
	`{"type":"rate_limit_event"}`,
	stopReason("tool_use"), evStop,
	`{"type":"result","subtype":"error_max_turns","is_error":true,"num_turns":2}`,
	"sleep",
)

var hi = []Message{{Role: "user", Content: "hi"}}

func TestClaudeFramesReplaysTheConversation(t *testing.T) {
	system, frames, err := claudeFrames([]Message{
		{Role: "system", Content: "S1"},
		{Role: "user", Content: "hi"},
		{Role: "assistant", Content: "looking", ToolCalls: []ToolCall{
			{ID: "t1", Function: FunctionCall{Name: "read_file", Arguments: json.RawMessage(`"{\"path\":\"a\"}"`)}},
			{ID: "t2", Function: FunctionCall{Name: "list_directory", Arguments: json.RawMessage(`null`)}},
			{ID: "t3", Function: FunctionCall{Name: "list_skills", Arguments: json.RawMessage(`not json`)}},
		}},
		{Role: "tool", ToolCallID: "t1", Content: "A"},
		{Role: "tool", ToolCallID: "t2", Content: ""},
		{Role: "tool", ToolCallID: "t3", Content: "C"},
		{Role: "user", Content: "You have reached the tool-call limit."},
		{Role: "assistant"}, // nothing to replay
		{Role: "system", Content: "S2"},
		{Content: "/diff what changed since v1?"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if system != "S1\n\nS2" {
		t.Errorf("system = %q", system)
	}
	got, _ := json.Marshal(frames)
	want := `[{"type":"user","message":{"role":"user","content":[{"type":"text","text":"hi"}]}},` +
		`{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"looking"},` +
		`{"type":"tool_use","id":"t1","name":"mcp__motita__read_file","input":{"path":"a"}},` +
		`{"type":"tool_use","id":"t2","name":"mcp__motita__list_directory","input":{}},` +
		`{"type":"tool_use","id":"t3","name":"mcp__motita__list_skills","input":{}}]}},` +
		`{"type":"user","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","content":"A"},` +
		`{"type":"tool_result","tool_use_id":"t2"},{"type":"tool_result","tool_use_id":"t3","content":"C"},` +
		`{"type":"text","text":"You have reached the tool-call limit."},` +
		// A leading "/" would be run by claude as its own command, not sent to the model.
		`{"type":"text","text":" /diff what changed since v1?"}]}}]`
	if string(got) != want {
		t.Errorf("frames:\n got %s\nwant %s", got, want)
	}
}

func TestClaudeFramesRefusesAnAssistantPrefill(t *testing.T) {
	for _, msgs := range [][]Message{
		nil,
		{{Role: "system", Content: "S"}},
		{{Role: "user", Content: "hi"}, {Role: "assistant", Content: "prefill"}},
	} {
		if _, _, err := claudeFrames(msgs); err == nil {
			t.Errorf("%+v: a history that does not end with the user must be refused", msgs)
		}
	}
}

// TestClaudeCodeToolCallRoundTrip drives the whole call: what goes to claude (argv,
// environment, system prompt, manifest, replayed frames) and what comes back.
func TestClaudeCodeToolCallRoundTrip(t *testing.T) {
	c, record := claudeTestClient(t, toolScript, func(c *config.LLM) { c.MaxTokens = 2048 })
	for _, v := range []string{"ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN", "ANTHROPIC_BASE_URL", "ANTHROPIC_CUSTOM_HEADERS",
		"CLAUDE_CODE_USE_BEDROCK", "CLAUDE_CODE_USE_ANTHROPIC_AWS", "CLAUDE_CODE_USE_MANTLE", "CLAUDE_EFFORT"} {
		t.Setenv(v, "leak")
	}
	t.Setenv("CLAUDE_CODE_OAUTH_TOKEN", "the-users-own")
	t.Setenv("MAX_THINKING_TOKENS", "9000")
	tools := []Tool{
		NewTool("read_file", "Reads a file.", ObjectSchema(map[string]any{"path": StringProperty("p")}, "path")),
		NewTool("list_skills", "Lists skills.", ObjectSchema(map[string]any{})),
	}
	var streamed strings.Builder
	start := time.Now()
	reply, err := c.callClaudeCode(context.Background(), []Message{
		{Role: "system", Content: "You are motita."},
		{Role: "user", Content: "what module is this?"},
		{Role: "assistant", ToolCalls: []ToolCall{{ID: "toolu_0", Function: FunctionCall{Name: "list_directory", Arguments: json.RawMessage(`{}`)}}}},
		{Role: "tool", ToolCallID: "toolu_0", Content: "go.mod"},
	}, tools, func(s string) { streamed.WriteString(s) })
	if err != nil {
		t.Fatal(err)
	}
	if time.Since(start) > 10*time.Second {
		t.Errorf("the call must end at message_stop, not wait for claude to exit (%v)", time.Since(start))
	}

	if reply.Content != "Reading go.mod." || streamed.String() != "Reading go.mod." || reply.FinishReason != "tool_calls" {
		t.Errorf("reply = %+v, streamed %q", reply, streamed.String())
	}
	if len(reply.Calls) != 1 || reply.Calls[0].ID != "toolu_1" || reply.Calls[0].Type != "function" ||
		reply.Calls[0].Function.Name != "read_file" || string(reply.Calls[0].Function.Arguments) != `{"path":"go.mod"}` {
		t.Errorf("calls = %+v", reply.Calls)
	}
	if _, err := os.Stat(record + ".term"); err != nil && runtime.GOOS != "windows" {
		t.Errorf("claude must be asked to stop with SIGTERM, not killed: %v", err)
	}

	rec := readRecord(t, record)
	want := []string{"-p", "--model", "sonnet", "--input-format", "stream-json", "--output-format", "stream-json",
		"--verbose", "--include-partial-messages", "--tools", "", "--system-prompt-file"}
	if !slices.Equal(rec.Args[:len(want)], want) {
		t.Errorf("argv starts %q", rec.Args)
	}
	// Reasoning is off here, so there is no --effort.
	tail := []string{"--setting-sources", "", "--strict-mcp-config", "--mcp-config", rec.MCP,
		"--disable-slash-commands", "--max-turns", "1", "--permission-mode", "dontAsk", "--no-session-persistence"}
	if !slices.Equal(rec.Args[len(want)+1:], tail) {
		t.Errorf("argv ends %q", rec.Args[len(want)+1:])
	}
	if !filepath.IsAbs(rec.Args[len(want)]) {
		t.Errorf("the system prompt path must be absolute: %s", rec.Args[len(want)])
	}
	if rec.System != "You are motita." {
		t.Errorf("system prompt = %q", rec.System)
	}
	for _, kv := range rec.Env {
		if strings.HasSuffix(kv, "=leak") {
			t.Errorf("%s reached claude: it must use the user's own login", kv)
		}
	}
	env := map[string]string{}
	for _, kv := range rec.Env {
		k, v, _ := strings.Cut(kv, "=")
		env[k] = v // the last one wins, as in os/exec
	}
	for k, v := range map[string]string{
		"CLAUDE_CODE_OAUTH_TOKEN": "the-users-own", "MOTITA_FAKE_CLAUDE_RECORD": record,
		"CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC": "1", "MAX_THINKING_TOKENS": "0", "CLAUDE_CODE_MAX_OUTPUT_TOKENS": "2048",
	} {
		if env[k] != v {
			t.Errorf("claude's %s = %q, want %q", k, env[k], v)
		}
	}

	var mcp claudeMCPConfig
	if err := json.Unmarshal([]byte(rec.MCP), &mcp); err != nil {
		t.Fatal(err)
	}
	self, _ := os.Executable()
	if s := mcp.MCPServers["motita"]; s.Command != self || len(s.Args) != 2 || s.Args[0] != ClaudeCodeMCPCommand {
		t.Errorf("mcp config = %s", rec.MCP)
	}
	if _, err := os.Stat(filepath.Dir(mcp.MCPServers["motita"].Args[1])); !os.IsNotExist(err) {
		t.Errorf("the per-call directory must be removed: %v", err)
	}
	wantManifest := `[{"name":"read_file","description":"Reads a file.","inputSchema":{"properties":{"path":{"description":"p","type":"string"}},"required":["path"],"type":"object"}},` +
		`{"name":"list_skills","description":"Lists skills.","inputSchema":{"properties":{},"type":"object"}}]`
	if rec.Manifest != wantManifest {
		t.Errorf("manifest = %s", rec.Manifest)
	}

	wantFrames := []string{
		`{"type":"user","message":{"role":"user","content":[{"type":"text","text":"what module is this?"}]},"shouldQuery":false}`,
		`{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","id":"toolu_0","name":"mcp__motita__list_directory","input":{}}]}}`,
		`{"type":"user","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_0","content":"go.mod"}]}}`,
	}
	if len(rec.Frames) != len(wantFrames) {
		t.Fatalf("frames = %s", rec.Frames)
	}
	for i, f := range rec.Frames {
		if string(f) != wantFrames[i] {
			t.Errorf("frame %d:\n got %s\nwant %s", i, f, wantFrames[i])
		}
	}
}

// TestClaudeCodeRunsInMotitasDirectory: claude tells the model where it is working and
// whether that is a git repository, and the tools resolve paths against motita's directory,
// so that is where claude must run. Being the same on every call is what the prompt cache
// needs.
func TestClaudeCodeRunsInMotitasDirectory(t *testing.T) {
	c, record := claudeTestClient(t, textScript, nil)
	cwd := t.TempDir()
	t.Chdir(cwd)
	want, _ := filepath.EvalSymlinks(cwd)
	for range 2 {
		if _, err := c.CompleteTools(context.Background(), hi, nil); err != nil {
			t.Fatal(err)
		}
		if got, _ := filepath.EvalSymlinks(readRecord(t, record).Dir); got != want {
			t.Errorf("claude ran in %q, want motita's %q", got, want)
		}
	}
}

func TestClaudeCodeReasoningIsEffort(t *testing.T) {
	c, record := claudeTestClient(t, textScript, func(c *config.LLM) { c.Reasoning = config.Reasoning{Enabled: true, Level: "high"} })
	if _, err := c.CompleteTools(context.Background(), hi, nil); err != nil {
		t.Fatal(err)
	}
	rec := readRecord(t, record)
	if i := slices.Index(rec.Args, "--effort"); i < 0 || rec.Args[i+1] != "high" {
		t.Errorf("argv = %q, want --effort high", rec.Args)
	}
	if slices.Contains(rec.Env, "MAX_THINKING_TOKENS=0") || slices.ContainsFunc(rec.Env, func(kv string) bool { return strings.HasPrefix(kv, "CLAUDE_CODE_MAX_OUTPUT_TOKENS=") }) {
		t.Errorf("thinking must not be switched off, and no max_tokens was set: %q", rec.Env)
	}
}

// TestClaudeCodeResolvesRelativePaths: os/exec reads a relative program path from
// cmd.Dir, and claude reads the file paths from its own directory.
func TestClaudeCodeResolvesRelativePaths(t *testing.T) {
	c, record := claudeTestClient(t, textScript, nil)
	exe, _ := os.Executable()
	cwd := t.TempDir()
	t.Chdir(cwd)
	rel, err := filepath.Rel(cwd, exe)
	if err != nil {
		t.Skip("the test binary is not reachable by a relative path from here")
	}
	t.Setenv("MOTITA_CLAUDE_BIN", rel)
	if err := os.Mkdir("tmp", 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TMPDIR", "tmp")
	reply, err := c.callClaudeCode(context.Background(), []Message{{Role: "system", Content: "S"}, {Role: "user", Content: "hi"}}, nil, nil)
	if err != nil || reply.Content != "Reading go.mod." {
		t.Fatalf("reply = %+v, %v", reply, err)
	}
	if rec := readRecord(t, record); rec.System != "S" {
		t.Errorf("claude could not read its system prompt: %q", rec.System)
	}
}

func TestClaudeCodeWithoutToolsHasNoMCPServer(t *testing.T) {
	c, record := claudeTestClient(t, textScript, nil)
	text, err := c.Complete(context.Background(), hi)
	if err != nil || text != "Reading go.mod." {
		t.Fatalf("Complete = %q, %v", text, err)
	}
	if rec := readRecord(t, record); slices.Contains(rec.Args, "--mcp-config") {
		t.Errorf("no tools means no MCP server: %q", rec.Args)
	}
}

func TestClaudeCodeCompleteWantsText(t *testing.T) {
	c, _ := claudeTestClient(t, `{"type":"result","subtype":"success","num_turns":1}`, nil)
	if _, err := c.Complete(context.Background(), hi); err == nil || !strings.Contains(err.Error(), "no text") {
		t.Errorf("an answer with no text must be an error, got %v", err)
	}
	t.Setenv("MOTITA_CLAUDE_BIN", filepath.Join(t.TempDir(), "claude"))
	if _, err := c.Complete(context.Background(), hi); err == nil || !strings.Contains(err.Error(), "install Claude Code") {
		t.Errorf("a failed call must reach Complete's caller, got %v", err)
	}
}

// TestClaudeCodeMessageStopsThatAreNotTheEnd: claude stops a message that only thought
// (and asks again), one cut at max_tokens (and continues it) and a stream it is about
// to retry. None of those is the answer.
func TestClaudeCodeMessageStopsThatAreNotTheEnd(t *testing.T) {
	c, _ := claudeTestClient(t, lines(
		evStart, `{"type":"assistant","message":{"content":[{"type":"thinking","thinking":"hmm"}]}}`, stopReason("end_turn"), evStop,
		evStart, textDelta("Part one, "), assistantText("Part one, "), stopReason("max_tokens"), evStop,
		evStart, textDelta("lost"), assistantText("lost"), evStop,
		evStart, textDelta("part two."), assistantText("part two."), stopReason("end_turn"), evStop,
		"sleep"), nil)
	reply, err := c.CompleteTools(context.Background(), hi, nil)
	if err != nil || reply.Content != "Part one, part two." || reply.FinishReason != "stop" {
		t.Errorf("reply = %+v, %v", reply, err)
	}
}

// TestClaudeCodeWithoutStreamEvents: claude's non-streaming fallback prints only the
// assistant message and the result.
func TestClaudeCodeWithoutStreamEvents(t *testing.T) {
	c, _ := claudeTestClient(t, lines(assistantText("Whole answer."),
		`{"type":"result","subtype":"success","num_turns":1,"result":"Whole answer."}`), nil)
	var got []string
	for chunk := range c.CompleteToolsStream(context.Background(), hi, nil) {
		got = append(got, fmt.Sprintf("%d:%s", chunk.Event, chunk))
	}
	if want := []string{"0:Whole answer.", "3:"}; !slices.Equal(got, want) {
		t.Errorf("the text must reach the planner as a chunk: %q, want %q", got, want)
	}

	// A tool call ends in error_max_turns: claude tried to run the denied tool.
	c, _ = claudeTestClient(t, lines(evTool,
		`{"type":"result","subtype":"error_max_turns","is_error":true,"num_turns":2,"errors":["Reached maximum number of turns (1)"]}`), nil)
	reply, err := c.CompleteTools(context.Background(), hi, nil)
	if err != nil || len(reply.Calls) != 1 || reply.FinishReason != "tool_calls" {
		t.Errorf("reply = %+v, %v", reply, err)
	}

	// The same result with nothing collected is a failure, and says why.
	c, _ = claudeTestClient(t, `{"type":"result","subtype":"error_max_turns","is_error":true,"num_turns":2,"errors":["Reached maximum number of turns (1)"]}`, nil)
	if _, err := c.CompleteTools(context.Background(), hi, nil); err == nil || !strings.Contains(err.Error(), "Reached maximum number of turns") {
		t.Errorf("err = %v", err)
	}
}

// TestClaudeCodeEndsAtAResultToo: an answer with no message_stop is complete at its
// result, and max_tokens is reported as a truncated answer.
func TestClaudeCodeEndsAtAResultToo(t *testing.T) {
	c, _ := claudeTestClient(t, lines(assistantText("Reading go.mod."), stopReason("max_tokens"),
		`{"type":"result","subtype":"success","num_turns":1,"result":"Reading go.mod."}`), nil)
	reply, err := c.CompleteTools(context.Background(), hi, nil)
	if err != nil || reply.Content != "Reading go.mod." || reply.FinishReason != "length" {
		t.Errorf("reply = %+v, %v", reply, err)
	}
}

func TestClaudeCodeStreams(t *testing.T) {
	c, _ := claudeTestClient(t, toolScript, nil)
	var got []string
	for chunk := range c.CompleteToolsStream(context.Background(), hi, nil) {
		got = append(got, fmt.Sprintf("%d:%s", chunk.Event, chunk))
		if chunk.Event == StreamDone && chunk.Reply.FinishReason != "tool_calls" {
			t.Errorf("done reply = %+v", chunk.Reply)
		}
	}
	want := []string{"0:Reading ", "0:go.mod.", "1:[tool call: read_file]", "3:"}
	if !slices.Equal(got, want) {
		t.Errorf("chunks = %q, want %q", got, want)
	}
}

func TestClaudeCodeStreamReportsAnError(t *testing.T) {
	c, _ := claudeTestClient(t, `{"type":"result","subtype":"error_during_execution","is_error":true,"result":"boom"}`, nil)
	var last StreamChunk
	for chunk := range c.CompleteToolsStream(context.Background(), hi, nil) {
		last = chunk
	}
	if last.Event != StreamError || !strings.Contains(last.Error.Error(), "error_during_execution") || !strings.Contains(last.Error.Error(), "boom") {
		t.Errorf("last chunk = %+v", last)
	}
}

// TestClaudeCodeFailures: every way the call can fail, and whether retrying it
// could help.
func TestClaudeCodeFailures(t *testing.T) {
	history := []Message{{Role: "user", Content: "hi"}, {Role: "assistant", Content: "yes"}, {Role: "user", Content: "go on"}}
	tools := []Tool{NewTool("read_file", "Reads a file.", ObjectSchema(map[string]any{}))}
	long := strings.Repeat("x", 2000) + "the end"
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	assistantError := func(kind, text string) string {
		return fmt.Sprintf(`{"type":"assistant","error":%q,"message":{"content":[{"type":"text","text":%q}]}}`, kind, text)
	}
	cases := []struct {
		name     string
		script   string
		setup    func(t *testing.T)
		tweak    func(*config.LLM)
		ctx      context.Context
		messages []Message
		tools    []Tool
		want     string
		is       error
		retry    bool
	}{
		{name: "logged out", script: assistantError("authentication_failed", "Please run /login"),
			want: "authentication_failed: Please run /login"},
		{name: "no such model", script: assistantError("model_not_found", "There's an issue with the selected model (sonet)"),
			want: "model_not_found"},
		{name: "rate limited", script: assistantError("rate_limit", "slow down"), want: "rate_limit: slow down", retry: true},
		{name: "overloaded", script: assistantError("overloaded", "busy"), want: "overloaded: busy", retry: true},
		{name: "exits early", script: "stderr:" + long,
			want: "exited before answering: ..." + strings.Repeat("x", 993) + "the end", retry: true},
		{name: "does not answer in time", script: "sleep", tweak: func(c *config.LLM) { c.Timeout = 300 * time.Millisecond },
			want: "did not answer: context deadline exceeded", is: context.DeadlineExceeded, retry: true},
		{name: "cancelled", ctx: cancelled, want: "context canceled", is: context.Canceled, retry: true},
		{name: "replay refused", messages: history,
			setup: func(t *testing.T) {
				t.Setenv("MOTITA_FAKE_CLAUDE_ACK", `{"type":"result","subtype":"success","num_turns":1}`)
			},
			want: "refused the history replay (success)"},
		{name: "replay crashes", messages: history,
			setup: func(t *testing.T) { t.Setenv("MOTITA_FAKE_CLAUDE_ACK", "exit") },
			want:  "exited before answering", retry: true},
		{name: "tool server not connected", tools: tools,
			setup: func(t *testing.T) { t.Setenv("MOTITA_FAKE_CLAUDE_MCP", "failed") },
			want:  `claude could not use motita's tool server (MCP status "failed")`},
		{name: "no claude", setup: func(t *testing.T) { t.Setenv("MOTITA_CLAUDE_BIN", filepath.Join(t.TempDir(), "claude")) },
			want: "install Claude Code and run `claude auth login`", is: fs.ErrNotExist},
		{name: "no claude on PATH", setup: func(t *testing.T) { t.Setenv("MOTITA_CLAUDE_BIN", ""); t.Setenv("PATH", t.TempDir()) },
			want: "claude was not found", is: exec.ErrNotFound},
		{name: "claude not executable", setup: func(t *testing.T) {
			bin := filepath.Join(t.TempDir(), "claude")
			_ = os.WriteFile(bin, []byte("#!/bin/sh\n"), 0o600)
			t.Setenv("MOTITA_CLAUDE_BIN", bin)
		}, want: "could not run", retry: true},
		{name: "claude not a program", setup: func(t *testing.T) {
			bin := filepath.Join(t.TempDir(), "claude")
			_ = os.WriteFile(bin, []byte("not a program"), 0o700)
			t.Setenv("MOTITA_CLAUDE_BIN", bin)
		}, want: "could not run", retry: true},
		{name: "prefill", messages: []Message{{Role: "user", Content: "hi"}, {Role: "assistant", Content: "x"}},
			want: "ends with a user turn"},
		{name: "no temporary directory", setup: func(t *testing.T) { t.Setenv("TMPDIR", filepath.Join(t.TempDir(), "missing")) },
			want: "missing", retry: true},
		{name: "no own path", tools: tools, setup: func(t *testing.T) {
			executable = func() (string, error) { return "", errors.New("no /proc") }
			t.Cleanup(func() { executable = os.Executable })
		}, want: "claude needs motita's own path to serve it the tools: no /proc"},
		{name: "unwritable file", setup: func(t *testing.T) {
			writeFile = func(string, []byte, os.FileMode) error { return errors.New("disk full") }
			t.Cleanup(func() { writeFile = os.WriteFile })
		}, want: "disk full", retry: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, _ := claudeTestClient(t, tc.script, tc.tweak)
			if tc.setup != nil {
				tc.setup(t)
			}
			if tc.messages == nil {
				tc.messages = hi
			}
			if tc.ctx == nil {
				tc.ctx = context.Background()
			}
			_, err := c.callClaudeCode(tc.ctx, tc.messages, tc.tools, nil)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want it to contain %q", err, tc.want)
			}
			if tc.is != nil && !errors.Is(err, tc.is) {
				t.Errorf("err = %v, want it to wrap %v", err, tc.is)
			}
			if retryable(err) != tc.retry {
				t.Errorf("retryable = %v, want %v", retryable(err), tc.retry)
			}
		})
	}
}

// TestClaudeCodeFatalErrorsAreNotRetried: the retry loop must give up at once on a
// failure that retrying cannot fix.
func TestClaudeCodeFatalErrorsAreNotRetried(t *testing.T) {
	c, _ := claudeTestClient(t, "", func(c *config.LLM) { c.MaxAttempts = 3; c.BackoffInitial = time.Millisecond })
	t.Setenv("MOTITA_CLAUDE_BIN", filepath.Join(t.TempDir(), "claude"))
	if _, err := c.CompleteTools(context.Background(), hi, nil); err == nil || strings.Contains(err.Error(), "attempts were exhausted") {
		t.Errorf("err = %v: a missing claude must fail without retrying", err)
	}
}

// TestCompleteToolsStreamRetriesAFailureBeforeAnyChunk: a stream that fails before it
// delivered anything is a failed attempt, and the retry policy applies to it.
func TestCompleteToolsStreamRetriesAFailureBeforeAnyChunk(t *testing.T) {
	stream := func(chunks ...StreamChunk) <-chan StreamChunk {
		ch := make(chan StreamChunk, len(chunks))
		for _, c := range chunks {
			ch <- c
		}
		close(ch)
		return ch
	}
	collectAll := func(c *Client) []StreamChunk {
		var got []StreamChunk
		for chunk := range c.CompleteToolsStream(context.Background(), hi, nil) {
			got = append(got, chunk)
		}
		return got
	}
	c := testClient(t, "openai", "http://unused", func(l *config.LLM) { l.MaxAttempts = 3 })
	attempts := 0
	c.openStream = func(context.Context, []Message, []Tool) (<-chan StreamChunk, error) {
		attempts++
		if attempts == 1 {
			return stream(StreamChunk{Event: StreamError, Error: errors.New("busy")}), nil
		}
		return stream(StreamChunk{Event: StreamText, Text: "ok"}, StreamChunk{Event: StreamDone}), nil
	}
	if got := collectAll(c); attempts != 2 || len(got) != 2 || got[0].Text != "ok" {
		t.Errorf("attempts = %d, chunks = %+v", attempts, got)
	}

	// Not retryable: reported at once.
	attempts = 0
	c.openStream = func(context.Context, []Message, []Tool) (<-chan StreamChunk, error) {
		attempts++
		return stream(StreamChunk{Event: StreamError, Error: fatalError{errors.New("logged out")}}), nil
	}
	if got := collectAll(c); attempts != 1 || len(got) != 1 || got[0].Event != StreamError {
		t.Errorf("attempts = %d, chunks = %+v", attempts, got)
	}

	// After text reached the caller, a failure is the answer's: not retried.
	attempts = 0
	c.openStream = func(context.Context, []Message, []Tool) (<-chan StreamChunk, error) {
		attempts++
		return stream(StreamChunk{Event: StreamText, Text: "half"}, StreamChunk{Event: StreamError, Error: errors.New("cut")}), nil
	}
	if got := collectAll(c); attempts != 1 || len(got) != 2 || got[1].Event != StreamError {
		t.Errorf("attempts = %d, chunks = %+v", attempts, got)
	}
}

func TestNewAcceptsClaudeCodeWithoutAKey(t *testing.T) {
	if _, err := New(config.LLM{Provider: "Claude-Code", Model: "sonnet"}, nil); err != nil {
		t.Errorf("claude-code uses the user's own login and needs no key: %v", err)
	}
}

func TestClaudeCodeModels(t *testing.T) {
	// Neither variable may come from the shell running the tests: the assertion below is about
	// what motita adds.
	for _, k := range []string{"CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC", "MAX_THINKING_TOKENS"} {
		t.Setenv(k, "")
		_ = os.Unsetenv(k)
	}
	_, record := claudeTestClient(t, lines(
		`{"type":"control_response","response":{"subtype":"success","request_id":"models","response":{`+
			`"models":[{"value":"sonnet","resolvedModel":"claude-sonnet-5","displayName":"Sonnet","description":"Sonnet 5 · Efficient for routine tasks"},`+
			`{"value":"haiku","resolvedModel":"claude-haiku-4-5-20251001","displayName":"Haiku","description":"Haiku 4.5 · Fastest"}],`+
			`"account":{"subscriptionType":"Claude Max"}}}}`), nil)
	got, err := ClaudeCodeModels(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got.Plan != "Claude Max" || len(got.Models) != 2 || got.Models[0] != (ClaudeCodeModel{
		Value: "sonnet", Resolved: "claude-sonnet-5", DisplayName: "Sonnet", Description: "Sonnet 5 · Efficient for routine tasks"}) {
		t.Errorf("catalogue = %+v", got)
	}
	// The handshake only: no user frame, so no model is called.
	rec := readRecord(t, record)
	if len(rec.Frames) != 1 || !strings.Contains(string(rec.Frames[0]), `"control_request"`) || slices.Contains(rec.Args, "--model") {
		t.Errorf("frames = %s, argv = %q", rec.Frames, rec.Args)
	}
	// Nonessential traffic off makes claude list 5 models instead of the account's 11: it is a
	// setting for turns, and discovery must not carry it (nor the turn's thinking switch).
	for _, kv := range rec.Env {
		if strings.HasPrefix(kv, "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=") || strings.HasPrefix(kv, "MAX_THINKING_TOKENS=") {
			t.Errorf("discovery ran with %s", kv)
		}
	}
	if !slices.Contains(claudeEnv(config.LLM{}), "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1") {
		t.Error("a turn must still run with nonessential traffic off")
	}
}

func TestClaudeCodeModelsFailures(t *testing.T) {
	claudeTestClient(t, `{"type":"control_response","response":{"subtype":"error","request_id":"models","error":"not now"}}`, nil)
	if _, err := ClaudeCodeModels(context.Background()); err == nil || !strings.Contains(err.Error(), "not now") {
		t.Errorf("err = %v", err)
	}
	t.Setenv("MOTITA_FAKE_CLAUDE", "")
	if _, err := ClaudeCodeModels(context.Background()); err == nil || !strings.Contains(err.Error(), "exited before answering") {
		t.Errorf("err = %v", err)
	}
	t.Setenv("MOTITA_CLAUDE_BIN", filepath.Join(t.TempDir(), "claude"))
	if _, err := ClaudeCodeModels(context.Background()); err == nil || !strings.Contains(err.Error(), "install Claude Code") {
		t.Errorf("err = %v", err)
	}
}

func TestServeClaudeCodeMCP(t *testing.T) {
	manifest := filepath.Join(t.TempDir(), "tools.json")
	if err := os.WriteFile(manifest, []byte(`[{"name":"read_file","inputSchema":{"type":"object"}}]`), 0o600); err != nil {
		t.Fatal(err)
	}
	in := strings.NewReader(lines(
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`garbage`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`,
		`{"jsonrpc":"2.0","id":"c","method":"tools/call","params":{"name":"read_file"}}`,
		`{"jsonrpc":"2.0","id":4,"method":"ping"}`,
	))
	var out bytes.Buffer
	if err := ServeClaudeCodeMCP(in, &out, manifest); err != nil {
		t.Fatal(err)
	}
	want := lines(
		`{"id":1,"jsonrpc":"2.0","result":{"capabilities":{"tools":{}},"protocolVersion":"2024-11-05","serverInfo":{"name":"motita","version":"1"}}}`,
		`{"id":2,"jsonrpc":"2.0","result":{"tools":[{"name":"read_file","description":"","inputSchema":{"type":"object"}}]}}`,
		`{"id":"c","jsonrpc":"2.0","result":{"content":[{"text":"denied: motita executes tools itself","type":"text"}],"isError":true}}`,
		`{"id":4,"jsonrpc":"2.0","result":{}}`,
	) + "\n"
	if out.String() != want {
		t.Errorf("replies:\n got %s\nwant %s", out.String(), want)
	}
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("closed") }

func TestServeClaudeCodeMCPFailures(t *testing.T) {
	dir := t.TempDir()
	bad := filepath.Join(dir, "bad.json")
	good := filepath.Join(dir, "good.json")
	_ = os.WriteFile(bad, []byte(`{`), 0o600)
	_ = os.WriteFile(good, []byte(`[]`), 0o600)
	ping := `{"jsonrpc":"2.0","id":1,"method":"ping"}`
	if err := ServeClaudeCodeMCP(strings.NewReader(ping), &bytes.Buffer{}, filepath.Join(dir, "missing.json")); err == nil {
		t.Error("a missing manifest must be an error")
	}
	if err := ServeClaudeCodeMCP(strings.NewReader(ping), &bytes.Buffer{}, bad); err == nil || !strings.Contains(err.Error(), "invalid tool manifest") {
		t.Errorf("an invalid manifest must be an error, got %v", err)
	}
	if err := ServeClaudeCodeMCP(strings.NewReader(ping), failingWriter{}, good); err == nil {
		t.Error("a reply that cannot be written must end the server")
	}
}
