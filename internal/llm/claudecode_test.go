package llm

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
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
func fakeClaude(script string) {
	rec := claudeRecord{Args: os.Args[1:], Env: os.Environ()}
	rec.Dir, _ = os.Getwd()
	for i, a := range rec.Args {
		switch a {
		case "--system-prompt-file":
			b, _ := os.ReadFile(rec.Args[i+1])
			rec.System = string(b)
		case "--mcp-config":
			rec.MCP = rec.Args[i+1]
			var cfg struct {
				Servers map[string]struct{ Args []string } `json:"mcpServers"`
			}
			_ = json.Unmarshal([]byte(rec.MCP), &cfg)
			b, _ := os.ReadFile(cfg.Servers["motita"].Args[1])
			rec.Manifest = string(b)
		}
	}
	// What claude prints before any answer, plus a line that is not JSON at all.
	fmt.Println(`{"type":"system","subtype":"init","apiKeySource":"none"}`)
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
	_ = os.WriteFile(os.Getenv("MOTITA_FAKE_CLAUDE_RECORD"), data, 0o600)

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

const (
	evStop = `{"type":"stream_event","event":{"type":"message_stop"}}`
	evText = `{"type":"assistant","message":{"content":[{"type":"text","text":"Reading go.mod."}]}}`
	evTool = `{"type":"assistant","message":{"content":[{"type":"tool_use","id":"toolu_1","name":"mcp__motita__read_file","input":{"path":"go.mod"}}]}}`
)

// toolScript is what claude answered live with a tool call: the tool_use, then
// message_stop, then the result of the turn it could not take. The result must
// never be read: the reply is complete at message_stop.
var toolScript = lines(
	`{"type":"stream_event","event":{"type":"message_start"}}`,
	`{"type":"stream_event","event":{"type":"content_block_delta","delta":{"type":"thinking_delta","thinking":"hmm"}}}`,
	`{"type":"assistant","message":{"content":[{"type":"thinking","thinking":"hmm"}]}}`,
	`{"type":"stream_event","event":{"type":"content_block_delta","delta":{"type":"text_delta","text":"Reading "}}}`,
	`{"type":"stream_event","event":{"type":"content_block_delta","delta":{"type":"text_delta","text":"go.mod."}}}`,
	evText,
	`{"type":"stream_event","event":{"type":"content_block_delta","delta":{"type":"input_json_delta","partial_json":"{}"}}}`,
	evTool,
	`{"type":"rate_limit_event"}`,
	`{"type":"stream_event","event":{"type":"message_delta","delta":{"stop_reason":"tool_use"}}}`,
	evStop,
	`{"type":"result","subtype":"error_max_turns","is_error":true,"num_turns":2}`,
	"sleep",
)

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
		{Role: "tool", ToolCallID: "t2", Content: "B"},
		{Role: "tool", ToolCallID: "t3", Content: "C"},
		{Role: "user", Content: "You have reached the tool-call limit."},
		{Role: "assistant"}, // nothing to replay
		{Role: "system", Content: "S2"},
		{Content: "again"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if system != "S1\n\nS2" {
		t.Errorf("system = %q", system)
	}
	got, _ := json.Marshal(frames)
	want := `[{"type":"user","message":{"role":"user","content":[{"text":"hi","type":"text"}]}},` +
		`{"type":"assistant","message":{"role":"assistant","content":[{"text":"looking","type":"text"},` +
		`{"id":"t1","input":{"path":"a"},"name":"mcp__motita__read_file","type":"tool_use"},` +
		`{"id":"t2","input":{},"name":"mcp__motita__list_directory","type":"tool_use"},` +
		`{"id":"t3","input":{},"name":"mcp__motita__list_skills","type":"tool_use"}]}},` +
		`{"type":"user","message":{"role":"user","content":[{"content":"A","tool_use_id":"t1","type":"tool_result"},` +
		`{"content":"B","tool_use_id":"t2","type":"tool_result"},{"content":"C","tool_use_id":"t3","type":"tool_result"},` +
		`{"text":"You have reached the tool-call limit.","type":"text"},{"text":"again","type":"text"}]}}]`
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
	c, record := claudeTestClient(t, toolScript, nil)
	for _, v := range []string{"ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN", "ANTHROPIC_BASE_URL",
		"CLAUDE_CODE_USE_BEDROCK", "CLAUDE_CODE_USE_VERTEX", "CLAUDE_CODE_USE_FOUNDRY"} {
		t.Setenv(v, "leak")
	}
	tools := []Tool{
		NewTool("read_file", "Reads a file.", ObjectSchema(map[string]any{"path": StringProperty("p")}, "path")),
		NewTool("list_skills", "Lists skills.", nil),
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

	rec := readRecord(t, record)
	want := []string{"-p", "--model", "sonnet", "--input-format", "stream-json", "--output-format", "stream-json",
		"--verbose", "--include-partial-messages", "--tools", "", "--system-prompt-file"}
	if !slices.Equal(rec.Args[:len(want)], want) {
		t.Errorf("argv starts %q", rec.Args)
	}
	tail := []string{"--setting-sources", "", "--strict-mcp-config", "--mcp-config", rec.MCP,
		"--disable-slash-commands", "--max-turns", "1", "--permission-mode", "dontAsk", "--no-session-persistence"}
	if !slices.Equal(rec.Args[len(want)+1:], tail) {
		t.Errorf("argv ends %q", rec.Args[len(want)+1:])
	}
	if rec.System != "You are motita." {
		t.Errorf("system prompt = %q", rec.System)
	}
	for _, kv := range rec.Env {
		if strings.HasSuffix(kv, "=leak") {
			t.Errorf("%s reached claude: it must use the user's own login", kv)
		}
	}
	if !slices.Contains(rec.Env, "MOTITA_FAKE_CLAUDE_RECORD="+record) {
		t.Error("the rest of the environment must reach claude")
	}
	if !strings.HasPrefix(filepath.Base(rec.Dir), "motita-claude-") {
		t.Errorf("claude ran in %s, not in the per-call directory", rec.Dir)
	}
	if _, err := os.Stat(rec.Dir); !os.IsNotExist(err) {
		t.Errorf("the per-call directory must be removed: %v", err)
	}

	var mcp struct {
		Servers map[string]struct {
			Command string
			Args    []string
		} `json:"mcpServers"`
	}
	if err := json.Unmarshal([]byte(rec.MCP), &mcp); err != nil {
		t.Fatal(err)
	}
	self, _ := os.Executable()
	if s := mcp.Servers["motita"]; s.Command != self || len(s.Args) != 2 || s.Args[0] != ClaudeCodeMCPCommand {
		t.Errorf("mcp config = %s", rec.MCP)
	}
	wantManifest := `[{"description":"Reads a file.","inputSchema":{"properties":{"path":{"description":"p","type":"string"}},"required":["path"],"type":"object"},"name":"read_file"},` +
		`{"description":"Lists skills.","inputSchema":{"type":"object"},"name":"list_skills"}]`
	if rec.Manifest != wantManifest {
		t.Errorf("manifest = %s", rec.Manifest)
	}

	wantFrames := []string{
		`{"type":"user","message":{"role":"user","content":[{"text":"what module is this?","type":"text"}]},"shouldQuery":false}`,
		`{"type":"assistant","message":{"role":"assistant","content":[{"id":"toolu_0","input":{},"name":"mcp__motita__list_directory","type":"tool_use"}]}}`,
		`{"type":"user","message":{"role":"user","content":[{"content":"go.mod","tool_use_id":"toolu_0","type":"tool_result"}]}}`,
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

func TestClaudeCodeWithoutToolsHasNoMCPServer(t *testing.T) {
	c, record := claudeTestClient(t, lines(evText, evStop), nil)
	text, err := c.Complete(context.Background(), []Message{{Role: "user", Content: "hi"}})
	if err != nil || text != "Reading go.mod." {
		t.Fatalf("Complete = %q, %v", text, err)
	}
	if rec := readRecord(t, record); slices.Contains(rec.Args, "--mcp-config") {
		t.Errorf("no tools means no MCP server: %q", rec.Args)
	}
}

func TestClaudeCodeCompleteWantsText(t *testing.T) {
	c, _ := claudeTestClient(t, evStop, nil)
	if _, err := c.Complete(context.Background(), []Message{{Role: "user", Content: "hi"}}); err == nil || !strings.Contains(err.Error(), "no text") {
		t.Errorf("an answer with no text must be an error, got %v", err)
	}
	t.Setenv("MOTITA_CLAUDE_BIN", filepath.Join(t.TempDir(), "claude"))
	if _, err := c.Complete(context.Background(), []Message{{Role: "user", Content: "hi"}}); err == nil || !strings.Contains(err.Error(), "install Claude Code") {
		t.Errorf("a failed call must reach Complete's caller, got %v", err)
	}
}

// TestClaudeCodeEndsAtAResultToo: an answer with no message_stop is complete at
// its result, and max_tokens is reported as a truncated answer.
func TestClaudeCodeEndsAtAResultToo(t *testing.T) {
	c, _ := claudeTestClient(t, lines(evText,
		`{"type":"stream_event","event":{"type":"message_delta","delta":{"stop_reason":"max_tokens"}}}`,
		`{"type":"result","subtype":"success","num_turns":1,"result":"Reading go.mod."}`), nil)
	reply, err := c.CompleteTools(context.Background(), []Message{{Role: "user", Content: "hi"}}, nil)
	if err != nil || reply.Content != "Reading go.mod." || reply.FinishReason != "length" {
		t.Errorf("reply = %+v, %v", reply, err)
	}
}

func TestClaudeCodeStopReasonEndTurnIsStop(t *testing.T) {
	c, _ := claudeTestClient(t, lines(evText,
		`{"type":"stream_event","event":{"type":"message_delta","delta":{"stop_reason":"end_turn"}}}`, evStop), nil)
	reply, err := c.CompleteTools(context.Background(), []Message{{Role: "user", Content: "hi"}}, nil)
	if err != nil || reply.FinishReason != "stop" {
		t.Errorf("reply = %+v, %v", reply, err)
	}
}

func TestClaudeCodeStreams(t *testing.T) {
	c, _ := claudeTestClient(t, toolScript, nil)
	var got []string
	for chunk := range c.CompleteToolsStream(context.Background(), []Message{{Role: "user", Content: "hi"}}, nil) {
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
	for chunk := range c.CompleteToolsStream(context.Background(), []Message{{Role: "user", Content: "hi"}}, nil) {
		last = chunk
	}
	if last.Event != StreamError || !strings.Contains(last.Error.Error(), "error_during_execution") || !strings.Contains(last.Error.Error(), "boom") {
		t.Errorf("last chunk = %+v", last)
	}
}

// TestClaudeCodeFailures: every way the call can fail, and whether retrying it
// could help.
func TestClaudeCodeFailures(t *testing.T) {
	user := []Message{{Role: "user", Content: "hi"}}
	history := []Message{{Role: "user", Content: "hi"}, {Role: "assistant", Content: "yes"}, {Role: "user", Content: "go on"}}
	long := strings.Repeat("x", 2000) + "the end"
	cases := []struct {
		name     string
		script   string
		setup    func(t *testing.T)
		tweak    func(*config.LLM)
		messages []Message
		tools    []Tool
		want     string
		retry    bool
	}{
		{name: "logged out", script: `{"type":"assistant","error":"authentication_failed","message":{"content":[{"type":"text","text":"Please run /login"}]}}`,
			want: "authentication_failed: Please run /login"},
		{name: "rate limited", script: `{"type":"assistant","error":"rate_limit","message":{"content":[{"type":"text","text":"slow down"}]}}`,
			want: "rate_limit: slow down", retry: true},
		{name: "exits early", script: "stderr:" + long,
			want: "exited before answering: ..." + strings.Repeat("x", 993) + "the end", retry: true},
		{name: "does not answer in time", script: "sleep", tweak: func(c *config.LLM) { c.Timeout = 300 * time.Millisecond },
			want: "did not answer: context deadline exceeded", retry: true},
		{name: "replay refused", messages: history,
			setup: func(t *testing.T) {
				t.Setenv("MOTITA_FAKE_CLAUDE_ACK", `{"type":"result","subtype":"success","num_turns":1}`)
			},
			want: "refused the history replay (success)"},
		{name: "replay crashes", messages: history,
			setup: func(t *testing.T) { t.Setenv("MOTITA_FAKE_CLAUDE_ACK", "exit") },
			want:  "exited before answering", retry: true},
		{name: "no claude", setup: func(t *testing.T) { t.Setenv("MOTITA_CLAUDE_BIN", filepath.Join(t.TempDir(), "claude")) },
			want: "install Claude Code and run `claude auth login`"},
		{name: "no claude on PATH", setup: func(t *testing.T) { t.Setenv("MOTITA_CLAUDE_BIN", ""); t.Setenv("PATH", t.TempDir()) },
			want: "could not run claude"},
		{name: "prefill", messages: []Message{{Role: "user", Content: "hi"}, {Role: "assistant", Content: "x"}},
			want: "ends with a user turn"},
		{name: "no temporary directory", setup: func(t *testing.T) { t.Setenv("TMPDIR", filepath.Join(t.TempDir(), "missing")) },
			want: "missing", retry: true},
		{name: "unserialisable tool", tools: []Tool{NewTool("bad", "", map[string]any{"f": func() {}})},
			want: "could not serialise the tools"},
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
				tc.messages = user
			}
			_, err := c.callClaudeCode(context.Background(), tc.messages, tc.tools, nil)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want it to contain %q", err, tc.want)
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
	if _, err := c.CompleteTools(context.Background(), []Message{{Role: "user", Content: "hi"}}, nil); err == nil || strings.Contains(err.Error(), "attempts were exhausted") {
		t.Errorf("err = %v: a missing claude must fail without retrying", err)
	}
}

func TestNewAcceptsClaudeCodeWithoutAKey(t *testing.T) {
	if _, err := New(config.LLM{Provider: "Claude-Code", Model: "sonnet"}, nil); err != nil {
		t.Errorf("claude-code uses the user's own login and needs no key: %v", err)
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
		`{"id":2,"jsonrpc":"2.0","result":{"tools":[{"inputSchema":{"type":"object"},"name":"read_file"}]}}`,
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
