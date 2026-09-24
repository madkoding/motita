package plan

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/madkoding/motita/internal/agent"
	"github.com/madkoding/motita/internal/config"
	"github.com/madkoding/motita/internal/execx"
	"github.com/madkoding/motita/internal/llm"
	"github.com/madkoding/motita/internal/logx"
	"github.com/madkoding/motita/internal/sandbox"
	"github.com/madkoding/motita/internal/session"
)

// fakeExecutor records every command and returns scripted answers.
type fakeExecutor struct {
	calls  []execx.Request
	script map[string]struct {
		output string
		exit   int
		err    error
	}
	defaultOutput string
	// defaultErr makes every command report this error, which is how the
	// execution-failure branches are exercised.
	defaultErr error
}

func (f *fakeExecutor) run(ctx context.Context, r execx.Request) (string, bool, int, error) {
	f.calls = append(f.calls, r)
	line := r.Command + " " + strings.Join(r.Args, " ")
	if s, ok := f.script[line]; ok {
		return s.output, false, s.exit, s.err
	}
	return f.defaultOutput, false, 0, f.defaultErr
}

func makeAgent(t *testing.T, readOnly bool) (*agent.Agent, *fakeExecutor) {
	t.Helper()
	cfg := config.Default()
	cfg.Agent.ReadOnly = readOnly
	cfg.Agent.WorkspaceDir = t.TempDir()
	log, err := logx.New(logx.Options{Level: logx.Error, Console: false})
	if err != nil {
		t.Fatal(err)
	}
	box, err := sandbox.New(sandbox.Options{
		Dir:         cfg.Agent.WorkspaceDir,
		Limits:      sandbox.Limits{MemoryMB: 64, CPUSeconds: 5},
		Timeout:     10 * time.Second,
		MaxOutputKB: 64,
		Log:         log,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { box.Close() })

	fake := &fakeExecutor{}
	a := agent.New(cfg, log, nil, box, nil)
	a.ExecCommand = fake.run
	return a, fake
}

// llmServer returns an OpenAI-compatible server that drives the conversation.
func llmServer(t *testing.T, replies []replyStep) *httptest.Server {
	t.Helper()
	idx := 0
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req struct {
			Messages []struct {
				Role       string         `json:"role"`
				Content    string         `json:"content"`
				ToolCalls  []llm.ToolCall `json:"tool_calls"`
				ToolCallID string         `json:"tool_call_id"`
			} `json:"messages"`
			Tools []struct {
				Function struct {
					Name string `json:"name"`
				} `json:"function"`
			} `json:"tools"`
		}
		json.Unmarshal(body, &req)

		step := replyStep{finishReason: "stop", content: "(no more steps)"}
		if idx < len(replies) {
			step = replies[idx]
			idx++
		}

		if step.finishReason == "" {
			step.finishReason = "stop"
		}

		// If the request asked for a stream, emit the same reply as SSE chunks.
		var stream bool
		if bytes.Contains(body, []byte(`"stream":true`)) {
			stream = true
		}
		choice := map[string]any{
			"index":         0,
			"finish_reason": step.finishReason,
			"message": map[string]any{
				"role":    "assistant",
				"content": step.content,
			},
		}
		if len(step.calls) > 0 {
			choice["message"].(map[string]any)["tool_calls"] = step.calls
		}
		if stream {
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			if step.content != "" {
				chunk, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"delta": map[string]any{"content": step.content}}}})
				fmt.Fprintf(w, "data: %s\n\n", chunk)
			}
			if len(step.calls) > 0 {
				delta := map[string]any{"tool_calls": step.calls}
				chunk, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"delta": delta, "finish_reason": step.finishReason}}})
				fmt.Fprintf(w, "data: %s\n\n", chunk)
			}
			fmt.Fprint(w, "data: [DONE]\n\n")
			return
		}
		json.NewEncoder(w).Encode(map[string]any{
			"choices": []any{choice},
		})
	}))
}

type replyStep struct {
	finishReason string
	content      string
	calls        []map[string]any
}

func ioMustRead(r io.Reader) []byte {
	b, _ := io.ReadAll(r)
	return b
}

func toolCall(name, id string, args map[string]string) map[string]any {
	raw, _ := json.Marshal(args)
	return map[string]any{
		"id":   id,
		"type": "function",
		"function": map[string]any{
			"name":      name,
			"arguments": string(raw),
		},
	}
}

func toolCallRaw(name, id, raw string) map[string]any {
	return map[string]any{
		"id":   id,
		"type": "function",
		"function": map[string]any{
			"name":      name,
			"arguments": raw,
		},
	}
}

// llmServerStatus answers every request with the given HTTP status, which is what
// the failure paths of the planner need.
func llmServerStatus(t *testing.T, status int) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(status)
		_, _ = w.Write([]byte(`{"error":{"message":"simulated failure"}}`))
	}))
}

func newClient(t *testing.T, srv *httptest.Server) *llm.Client {
	t.Helper()
	c, err := llm.New(config.LLM{
		Provider:    "openai",
		Model:       "mock",
		APIKey:      "test",
		BaseURL:     srv.URL,
		Timeout:     5 * time.Second,
		MaxAttempts: 1,
	}, logx.Global())
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestPlannerNewPanicsWithoutEngine(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("New(nil, nil) must panic")
		}
	}()
	New(nil, nil)
}

func TestRunEmptyInput(t *testing.T) {
	a, _ := makeAgent(t, true)
	srv := llmServer(t, nil)
	defer srv.Close()
	p := New(newClient(t, srv), a)
	_, err := p.Run(context.Background(), "   ")
	if err == nil {
		t.Fatal("expected an error for empty input")
	}
}

func TestListDirectory(t *testing.T) {
	a, _ := makeAgent(t, true)
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("a"), 0o644)
	os.WriteFile(filepath.Join(dir, "b.txt"), []byte("b"), 0o644)
	os.Mkdir(filepath.Join(dir, "sub"), 0o755)

	srv := llmServer(t, []replyStep{
		{finishReason: "tool_calls", calls: []map[string]any{toolCall("list_directory", "c1", map[string]string{"path": dir})}},
		{content: "listed"},
	})
	defer srv.Close()
	p := New(newClient(t, srv), a)
	out, err := p.Run(context.Background(), "list it")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if out != "listed" {
		t.Errorf("output = %q", out)
	}
}

func TestSearchInFiles(t *testing.T) {
	a, fake := makeAgent(t, true)
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "one.txt"), []byte("hello world\n"), 0o644)

	srv := llmServer(t, []replyStep{
		{finishReason: "tool_calls", calls: []map[string]any{toolCall("search_in_files", "c1", map[string]string{"pattern": "hello", "path": dir, "literal": "true"})}},
		{content: "found"},
	})
	defer srv.Close()
	p := New(newClient(t, srv), a)
	out, err := p.Run(context.Background(), "search")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if out != "found" {
		t.Errorf("output = %q", out)
	}
	if len(fake.calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(fake.calls))
	}
	if !strings.Contains(fake.calls[0].Command, "grep") {
		t.Errorf("expected grep command, got %q", fake.calls[0].Command)
	}
}

func TestListDirectoryDefault(t *testing.T) {
	a, _ := makeAgent(t, true)
	dir := t.TempDir()
	os.Chdir(dir)
	defer os.Chdir(t.TempDir()) // prevent leaving a deleted cwd
	os.WriteFile(filepath.Join(dir, "x.txt"), []byte("x"), 0o644)

	srv := llmServer(t, []replyStep{
		{finishReason: "tool_calls", calls: []map[string]any{toolCall("list_directory", "c1", map[string]string{})}},
		{content: "listed"},
	})
	defer srv.Close()
	p := New(newClient(t, srv), a)
	p.Run(context.Background(), "list default")
}

func TestSearchInFilesMissingPattern(t *testing.T) {
	a, _ := makeAgent(t, true)
	srv := llmServer(t, []replyStep{
		{finishReason: "tool_calls", calls: []map[string]any{toolCall("search_in_files", "c1", map[string]string{})}},
		{content: "handled"},
	})
	defer srv.Close()
	p := New(newClient(t, srv), a)
	p.Run(context.Background(), "search nothing")
}

func TestListDirectoryEmpty(t *testing.T) {
	a, _ := makeAgent(t, true)
	dir := t.TempDir()
	srv := llmServer(t, []replyStep{
		{finishReason: "tool_calls", calls: []map[string]any{toolCall("list_directory", "c1", map[string]string{"path": dir})}},
		{content: "empty handled"},
	})
	defer srv.Close()
	p := New(newClient(t, srv), a)
	out, _ := p.Run(context.Background(), "list empty")
	if out != "empty handled" {
		t.Errorf("output = %q", out)
	}
}

func TestListDirectoryError(t *testing.T) {
	a, _ := makeAgent(t, true)
	srv := llmServer(t, []replyStep{
		{finishReason: "tool_calls", calls: []map[string]any{toolCall("list_directory", "c1", map[string]string{"path": "/nonexistent/zzzz"})}},
		{content: "error handled"},
	})
	defer srv.Close()
	p := New(newClient(t, srv), a)
	p.Run(context.Background(), "list bad")
}

func TestRunTextOnly(t *testing.T) {
	a, fake := makeAgent(t, true)
	srv := llmServer(t, []replyStep{{content: "the plan is to read the manual"}})
	defer srv.Close()
	p := New(newClient(t, srv), a)
	out, err := p.Run(context.Background(), "what should I do?")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out != "the plan is to read the manual" {
		t.Errorf("output = %q", out)
	}
	if len(fake.calls) != 0 {
		t.Errorf("no command should run, got %d", len(fake.calls))
	}
}

func TestRunOneToolCallThenAnswer(t *testing.T) {
	a, fake := makeAgent(t, true)
	fake.defaultOutput = "hello\n"
	srv := llmServer(t, []replyStep{
		{finishReason: "tool_calls", calls: []map[string]any{toolCall("execute_command", "c1", map[string]string{"command": "echo hello"})}},
		{content: "say hello back"},
	})
	defer srv.Close()
	p := New(newClient(t, srv), a)
	out, err := p.Run(context.Background(), "check the system")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if out != "say hello back" {
		t.Errorf("output = %q", out)
	}
	if len(fake.calls) != 1 {
		t.Fatalf("expected one command, got %d", len(fake.calls))
	}
	if fake.calls[0].Command != "echo" {
		t.Errorf("command = %q", fake.calls[0].Command)
	}
}

func TestRunMultipleToolCalls(t *testing.T) {
	a, fake := makeAgent(t, true)
	fake.defaultOutput = "ok\n"
	srv := llmServer(t, []replyStep{
		{finishReason: "tool_calls", calls: []map[string]any{
			toolCall("execute_command", "c1", map[string]string{"command": "true"}),
			toolCall("execute_command", "c2", map[string]string{"command": "false"}),
		}},
		{content: "two commands run"},
	})
	defer srv.Close()
	p := New(newClient(t, srv), a)
	out, err := p.Run(context.Background(), "run checks")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if out != "two commands run" {
		t.Errorf("output = %q", out)
	}
	if len(fake.calls) != 2 {
		t.Errorf("expected 2 calls, got %d", len(fake.calls))
	}
}

func TestReadFile(t *testing.T) {
	a, _ := makeAgent(t, true)
	dir := t.TempDir()
	path := filepath.Join(dir, "note.txt")
	os.WriteFile(path, []byte("contents\n"), 0o644)

	srv := llmServer(t, []replyStep{
		{finishReason: "tool_calls", calls: []map[string]any{toolCall("read_file", "c1", map[string]string{"path": path})}},
		{content: "read done"},
	})
	defer srv.Close()
	p := New(newClient(t, srv), a)
	out, err := p.Run(context.Background(), "read it")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if out != "read done" {
		t.Errorf("output = %q", out)
	}
}

func TestReadFileMissingPath(t *testing.T) {
	a, _ := makeAgent(t, true)
	srv := llmServer(t, []replyStep{
		{finishReason: "tool_calls", calls: []map[string]any{toolCall("read_file", "c1", map[string]string{"path": ""})}},
		{content: "handled"},
	})
	defer srv.Close()
	p := New(newClient(t, srv), a)
	p.Run(context.Background(), "read nothing")
}

func TestReadFileDirectory(t *testing.T) {
	a, _ := makeAgent(t, true)
	dir := t.TempDir()
	srv := llmServer(t, []replyStep{
		{finishReason: "tool_calls", calls: []map[string]any{toolCall("read_file", "c1", map[string]string{"path": dir})}},
		{content: "handled"},
	})
	defer srv.Close()
	p := New(newClient(t, srv), a)
	p.Run(context.Background(), "read dir")
}

func TestReadFileTooBig(t *testing.T) {
	a, _ := makeAgent(t, true)
	dir := t.TempDir()
	path := filepath.Join(dir, "big.bin")
	os.WriteFile(path, make([]byte, maxFileBytes+1), 0o644)

	srv := llmServer(t, []replyStep{
		{finishReason: "tool_calls", calls: []map[string]any{toolCall("read_file", "c1", map[string]string{"path": path})}},
		{content: "too big"},
	})
	defer srv.Close()
	p := New(newClient(t, srv), a)
	p.Run(context.Background(), "read big")
}

func TestReadFileEmpty(t *testing.T) {
	a, _ := makeAgent(t, true)
	dir := t.TempDir()
	path := filepath.Join(dir, "empty.txt")
	os.WriteFile(path, nil, 0o644)

	srv := llmServer(t, []replyStep{
		{finishReason: "tool_calls", calls: []map[string]any{toolCall("read_file", "c1", map[string]string{"path": path})}},
		{content: "empty"},
	})
	defer srv.Close()
	p := New(newClient(t, srv), a)
	out, _ := p.Run(context.Background(), "read empty")
	if out != "empty" {
		t.Errorf("output = %q", out)
	}
}

func TestExecuteCommandRefusedInReadOnlyMode(t *testing.T) {
	a, fake := makeAgent(t, true)
	// The real agent.RunCommand applies the read-only policy and refuses destructive commands before the executor.
	srv := llmServer(t, []replyStep{
		{finishReason: "tool_calls", calls: []map[string]any{toolCall("execute_command", "c1", map[string]string{"command": "rm -rf /tmp/x"})}},
		{content: "refused and noted"},
	})
	defer srv.Close()
	p := New(newClient(t, srv), a)
	out, err := p.Run(context.Background(), "destroy")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if out != "refused and noted" {
		t.Errorf("output = %q", out)
	}
	if len(fake.calls) != 0 {
		// The real RunCommand refused before the executor: no call should reach fake.
		t.Errorf("expected no command to reach the executor, got %d", len(fake.calls))
	}
}

// The history used to be trimmed by COUNT: the oldest messages were dropped and nothing
// replaced them, so an agent that had filled its context forgot the task it was given. It
// is now compacted: what is removed is summarised and carried forward, and the tests below
// assert that property rather than the arithmetic of a slice.

// TestTheConversationOutlivesOneRun: two runs against the same planner must see each
// other. Without this the agent cannot be asked a follow-up question, which is the
// difference between a chat and a command line.
func TestTheConversationOutlivesOneRun(t *testing.T) {
	a, _ := makeAgent(t, true)
	srv := llmServer(t, []replyStep{
		{content: "the first answer"},
		{content: "the second answer"},
	})
	defer srv.Close()

	p := New(newClient(t, srv), a)
	if _, err := p.Run(context.Background(), "first question"); err != nil {
		t.Fatal(err)
	}
	before := p.Session().Len()
	if _, err := p.Run(context.Background(), "second question"); err != nil {
		t.Fatal(err)
	}

	// The second run continues the same conversation: the first exchange is still there.
	if got := p.Session().Len(); got <= before {
		t.Errorf("the conversation must grow across runs: %d messages, was %d", got, before)
	}
	history := p.Session().Messages()
	found := false
	for _, m := range history {
		if strings.Contains(m.Content, "first question") {
			found = true
		}
	}
	if !found {
		t.Errorf("the second run must still see the first question, got %+v", history)
	}
}

// TestATurnCompactsBeforeTheWindowOverflows: the compaction happens inside the run, at the
// boundary, so the agent never reaches the provider with a context it will refuse.
func TestATurnCompactsBeforeTheWindowOverflows(t *testing.T) {
	a, _ := makeAgent(t, true)
	srv := llmServer(t, []replyStep{
		{content: "an answer"},
		{content: "the summary of everything before"},
		{content: "another answer"},
	})
	defer srv.Close()

	p := New(newClient(t, srv), a).
		WithSessionPolicy("gpt-4o", 400, 50, 0.5, 2)

	// A conversation big enough to cross the trigger, then one more run.
	sess := p.Session()
	for i := 0; i < 60; i++ {
		sess.Append(llm.Message{Role: "user", Content: strings.Repeat("word ", 30)})
	}
	if !sess.NeedsCompaction() {
		t.Fatalf("the fixture must cross the trigger, at %.0f%%", sess.Used()*100)
	}

	if _, err := p.Run(context.Background(), "and now this"); err != nil {
		t.Fatal(err)
	}
	if sess.Compactions() == 0 {
		t.Error("the run must have compacted the context before calling the model")
	}
	if sess.Summary() == "" {
		t.Error("the compaction must carry a summary forward")
	}
}

// TestTheCompactionIsReportedToTheInterface: a conversation that gets shorter with no
// explanation looks like an agent that lost its place.
func TestTheCompactionIsReportedToTheInterface(t *testing.T) {
	a, _ := makeAgent(t, true)
	srv := llmServer(t, []replyStep{
		{content: "the summary"},
		{content: "an answer"},
	})
	defer srv.Close()

	var trace strings.Builder
	p := New(newClient(t, srv), a).
		WithSessionPolicy("gpt-4o", 400, 50, 0.5, 2).
		WithTrace(func(format string, args ...any) { fmt.Fprintf(&trace, format, args...) })

	sess := p.Session()
	for i := 0; i < 60; i++ {
		sess.Append(llm.Message{Role: "user", Content: strings.Repeat("word ", 30)})
	}
	if _, err := p.Run(context.Background(), "continue"); err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(trace.String(), "context compacted") {
		t.Errorf("the interface must be told, got:\n%s", trace.String())
	}
}

// TestCompactionCarriesTheTaskNotJustTheTail: the whole reason for summarising instead of
// truncating. What the user asked for must survive the fold.
func TestCompactionCarriesTheTaskNotJustTheTail(t *testing.T) {
	a, _ := makeAgent(t, true)
	srv := llmServer(t, []replyStep{
		{content: "the account: the user asked to count .txt files; the answer was 0"},
		{content: "an answer"},
	})
	defer srv.Close()

	p := New(newClient(t, srv), a).
		WithSessionPolicy("gpt-4o", 400, 50, 0.5, 2)
	sess := p.Session()

	// The task is at the START of the conversation, which is what gets folded.
	sess.Append(llm.Message{Role: "user", Content: "count the .txt files in the home directory"})
	for i := 0; i < 60; i++ {
		sess.Append(llm.Message{Role: "user", Content: strings.Repeat("word ", 30)})
	}
	if _, err := p.Run(context.Background(), "and the .md?"); err != nil {
		t.Fatal(err)
	}

	// The summary is in the request the model receives for the NEXT turn.
	msgs := sess.Messages()
	carried := false
	for _, m := range msgs {
		if strings.Contains(m.Content, "count .txt files") {
			carried = true
		}
	}
	if !carried {
		t.Errorf("the task must survive the compaction, got:\n%+v", msgs)
	}
}

// TestAFailedCompactionStopsTheRunRatherThanLosingContext: carrying on with a context that
// is about to be refused turns a recoverable condition into a failed task mid-answer.
func TestAFailedCompactionStopsTheRunRatherThanLosingContext(t *testing.T) {
	a, _ := makeAgent(t, true)
	// The summariser is the engine, and this server refuses every request, so the
	// compaction — which is a model call like any other — cannot be done. The mock used by
	// the other tests always answers something, which would make this test vacuous.
	srv := llmServerStatus(t, http.StatusInternalServerError)
	defer srv.Close()

	p := New(newClient(t, srv), a).
		WithSessionPolicy("gpt-4o", 400, 50, 0.5, 2)
	sess := p.Session()
	for i := 0; i < 60; i++ {
		sess.Append(llm.Message{Role: "user", Content: strings.Repeat("word ", 30)})
	}
	before := sess.Len()

	_, err := p.Run(context.Background(), "continue")
	if err == nil {
		t.Fatal("a context that cannot be compacted must stop the run, not proceed")
	}
	if !strings.Contains(err.Error(), "could not be compacted") {
		t.Errorf("the error must say what happened, got %v", err)
	}
	if sess.Len() < before {
		t.Error("the history must not be shortened by a failed compaction")
	}
}

// TestThePlannerHonoursTheConfiguredWindow: the operator knows a local server configured
// lower than the model's published window, so the explicit value wins.
func TestThePlannerHonoursTheConfiguredWindow(t *testing.T) {
	a, _ := makeAgent(t, true)
	srv := llmServer(t, nil)
	defer srv.Close()
	p := New(newClient(t, srv), a).WithSessionPolicy("gpt-4o", 3000, 100, 0.7, 4)
	sess := p.Session()

	if sess.Window != 3000 {
		t.Errorf("window = %d, want the configured 3000 rather than gpt-4o's published one", sess.Window)
	}
	if sess.Reserve != 100 || sess.CompactAt != 0.7 || sess.KeepRecent != 4 {
		t.Errorf("the policy must come from the configuration: reserve=%d at=%.1f keep=%d",
			sess.Reserve, sess.CompactAt, sess.KeepRecent)
	}
}

// TestWithoutAPolicyTheWindowComesFromTheModel: an unconfigured planner still compacts at a
// sensible place rather than not at all.
func TestWithoutAPolicyTheWindowComesFromTheModel(t *testing.T) {
	a, _ := makeAgent(t, true)
	srv := llmServer(t, nil)
	defer srv.Close()
	p := New(newClient(t, srv), a).WithSessionPolicy("llama3.1:70b", 0, 0, 0, 0)
	sess := p.Session()

	if sess.Window != 128000 {
		t.Errorf("window = %d, want the model's 128000", sess.Window)
	}
	if sess.Reserve <= 0 || sess.CompactAt <= 0 {
		t.Errorf("the defaults must apply: reserve=%d at=%.1f", sess.Reserve, sess.CompactAt)
	}
}

func TestMaxLoopsExhaustion(t *testing.T) {
	a, fake := makeAgent(t, true)
	fake.defaultOutput = "ok\n"
	srv := llmServer(t, []replyStep{
		{finishReason: "tool_calls", calls: []map[string]any{toolCall("execute_command", "c1", map[string]string{"command": "true"})}},
		{finishReason: "tool_calls", calls: []map[string]any{toolCall("execute_command", "c2", map[string]string{"command": "true"})}},
		{content: "final"},
	})
	defer srv.Close()
	p := New(newClient(t, srv), a).WithLoops(0)
	if p.maxLoops != 1 {
		t.Errorf("WithLoops(0) must clamp to 1, got %d", p.maxLoops)
	}
	p = New(newClient(t, srv), a).WithLoops(2)
	out, err := p.Run(context.Background(), "loop")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if out != "final" {
		t.Errorf("output = %q", out)
	}
	if len(fake.calls) != 2 {
		t.Errorf("expected 2 calls, got %d", len(fake.calls))
	}
}

func TestInvalidToolName(t *testing.T) {
	a, _ := makeAgent(t, true)
	srv := llmServer(t, []replyStep{
		{finishReason: "tool_calls", calls: []map[string]any{toolCall("delete_everything", "c1", map[string]string{})}},
		{content: "invalid tool handled"},
	})
	defer srv.Close()
	p := New(newClient(t, srv), a)
	out, err := p.Run(context.Background(), "bad tool")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if out != "invalid tool handled" {
		t.Errorf("output = %q", out)
	}
}

func TestInvalidJSONArguments(t *testing.T) {
	a, _ := makeAgent(t, true)
	srv := llmServer(t, []replyStep{
		{finishReason: "tool_calls", calls: []map[string]any{{
			"id":   "c1",
			"type": "function",
			"function": map[string]any{
				"name":      "read_file",
				"arguments": "not json",
			},
		}}},
		{content: "bad args handled"},
	})
	defer srv.Close()
	p := New(newClient(t, srv), a)
	out, err := p.Run(context.Background(), "bad args")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if out != "bad args handled" {
		t.Errorf("output = %q", out)
	}
}

func TestTraceAndAnswerHooks(t *testing.T) {
	a, _ := makeAgent(t, true)
	var buf bytes.Buffer
	var gotAnswer string
	srv := llmServer(t, []replyStep{{content: "answer"}})
	defer srv.Close()
	p := New(newClient(t, srv), a).
		WithTrace(func(f string, args ...any) { fmt.Fprintf(&buf, f+"\n", args...) }).
		WithAnswer(func(s string) { gotAnswer = s })
	out, err := p.Run(context.Background(), "question")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if out != "answer" {
		t.Errorf("output = %q", out)
	}
	if gotAnswer != "answer" {
		t.Errorf("answer hook = %q", gotAnswer)
	}
	if !strings.Contains(buf.String(), "thinking") {
		t.Errorf("trace = %q", buf.String())
	}
}

func TestLimitString(t *testing.T) {
	if got := limitString("short", 10); got != "short" {
		t.Errorf("got %q", got)
	}
	got := limitString(strings.Repeat("x", maxToolOutputBytes+10), maxToolOutputBytes)
	if !strings.Contains(got, "truncated") {
		t.Errorf("expected truncation notice, got %q", got)
	}
}

func TestDecodeArgs(t *testing.T) {
	var dst readFileArgs
	if err := decodeArgs([]byte(`{"path":"/a"}`), &dst); err != nil || dst.Path != "/a" {
		t.Fatalf("object: %v %q", err, dst.Path)
	}
	dst = readFileArgs{}
	if err := decodeArgs([]byte(`"{\"path\":\"/b\"}"`), &dst); err != nil || dst.Path != "/b" {
		t.Fatalf("string: %v %q", err, dst.Path)
	}
	dst = readFileArgs{Path: "x"}
	if err := decodeArgs([]byte(``), &dst); err != nil || dst.Path != "x" {
		t.Fatalf("empty: %v %q", err, dst.Path)
	}
}

func TestWithTimeout(t *testing.T) {
	a, _ := makeAgent(t, true)
	srv := llmServer(t, nil)
	defer srv.Close()
	c := newClient(t, srv)
	p := New(c, a).WithTimeout(5 * time.Second)
	if p.commandTimeout != 5*time.Second {
		t.Errorf("timeout = %v", p.commandTimeout)
	}
	p2 := New(c, a).WithTimeout(0)
	if p2.commandTimeout != defaultCommandTimeout {
		t.Errorf("timeout = %v", p2.commandTimeout)
	}
}

func TestFinalizeEmpty(t *testing.T) {
	a, _ := makeAgent(t, true)
	srv := llmServer(t, []replyStep{{content: "   "}})
	defer srv.Close()
	p := New(newClient(t, srv), a)
	out, err := p.Run(context.Background(), "ask")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if out != "(the model did not deliver a final answer)" {
		t.Errorf("output = %q", out)
	}
}

func TestEngineError(t *testing.T) {
	a, _ := makeAgent(t, true)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprint(w, `{"error":{"message":"down"}}`)
	}))
	defer srv.Close()
	p := New(newClient(t, srv), a)
	_, err := p.Run(context.Background(), "ask")
	if err == nil {
		t.Fatal("expected an engine error")
	}
}

func TestReadFileOpenError(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root can open most files")
	}
	a, _ := makeAgent(t, true)
	dir := t.TempDir()
	path := filepath.Join(dir, "secret.txt")
	os.WriteFile(path, []byte("x"), 0o000)
	srv := llmServer(t, []replyStep{
		{finishReason: "tool_calls", calls: []map[string]any{toolCall("read_file", "c1", map[string]string{"path": path})}},
		{content: "handled"},
	})
	defer srv.Close()
	p := New(newClient(t, srv), a)
	p.Run(context.Background(), "read secret")
}

func TestReadFileBrokenSymlink(t *testing.T) {
	dir := t.TempDir()
	link := filepath.Join(dir, "dangling")
	if err := os.Symlink(filepath.Join(dir, "nowhere"), link); err != nil {
		t.Skip("symlinks not available")
	}
	a, _ := makeAgent(t, true)
	srv := llmServer(t, []replyStep{
		{finishReason: "tool_calls", calls: []map[string]any{toolCall("read_file", "c1", map[string]string{"path": link})}},
		{content: "handled"},
	})
	defer srv.Close()
	p := New(newClient(t, srv), a)
	p.Run(context.Background(), "read symlink")
}

func TestExecuteCommandTimeoutDefault(t *testing.T) {
	a, fake := makeAgent(t, true)
	fake.defaultOutput = "ok\n"
	srv := llmServer(t, []replyStep{
		{finishReason: "tool_calls", calls: []map[string]any{toolCall("execute_command", "c1", map[string]string{"command": "echo hi"})}},
		{content: "done"},
	})
	defer srv.Close()
	p := New(newClient(t, srv), a).WithTimeout(0)
	out, err := p.Run(context.Background(), "run")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if out != "done" {
		t.Errorf("output = %q", out)
	}
	if len(fake.calls) != 1 {
		t.Errorf("calls = %d", len(fake.calls))
	}
}

func TestExecuteCommandRunCommandError(t *testing.T) {
	a, fake := makeAgent(t, true)
	a.ExecCommand = func(ctx context.Context, r execx.Request) (string, bool, int, error) {
		fake.calls = append(fake.calls, r)
		return "boom", false, 1, fmt.Errorf("refused by policy")
	}
	srv := llmServer(t, []replyStep{
		{finishReason: "tool_calls", calls: []map[string]any{toolCall("execute_command", "c1", map[string]string{"command": "ls"})}},
		{content: "error handled"},
	})
	defer srv.Close()
	p := New(newClient(t, srv), a)
	out, err := p.Run(context.Background(), "run")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if out != "error handled" {
		t.Errorf("output = %q", out)
	}
}

func TestExecuteCommandEmpty(t *testing.T) {
	a, _ := makeAgent(t, true)
	srv := llmServer(t, []replyStep{
		{finishReason: "tool_calls", calls: []map[string]any{toolCall("execute_command", "c1", map[string]string{"command": ""})}},
		{content: "handled"},
	})
	defer srv.Close()
	p := New(newClient(t, srv), a)
	p.Run(context.Background(), "run empty")
}

func TestDecodeArgsInvalidQuoted(t *testing.T) {
	var dst readFileArgs
	if err := decodeArgs([]byte(`"not json`), &dst); err == nil {
		t.Fatal("expected error")
	}
}

func TestForcedFinalEngineError(t *testing.T) {
	a, _ := makeAgent(t, true)
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		stream := bytes.Contains(ioMustRead(r.Body), []byte(`"stream":true`))
		if calls == 1 {
			if stream {
				w.Header().Set("Content-Type", "text/event-stream")
				calls := []map[string]any{toolCall("execute_command", "c1", map[string]string{"command": "true"})}
				delta := map[string]any{"tool_calls": calls}
				chunk, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"delta": delta, "finish_reason": "tool_calls"}}})
				fmt.Fprintf(w, "data: %s\n\n", chunk)
				fmt.Fprint(w, "data: [DONE]\n\n")
				return
			}
			fmt.Fprint(w, `{"choices":[{"finish_reason":"tool_calls","message":{"role":"assistant","tool_calls":[{"id":"c1","type":"function","function":{"name":"execute_command","arguments":"{\"command\":\"true\"}"}}]}}]}`)
			return
		}
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprint(w, `{"error":{"message":"down"}}`)
	}))
	defer srv.Close()
	p := New(newClient(t, srv), a).WithLoops(1)
	_, err := p.Run(context.Background(), "ask")
	if err == nil {
		t.Fatal("expected error when forcing final answer fails")
	}
}

func TestReadFileReadError(t *testing.T) {
	// /proc/self/mem exists and fails on read.
	path := "/proc/self/mem"
	if _, err := os.Stat(path); err != nil {
		t.Skip("no /proc")
	}
	a, _ := makeAgent(t, true)
	srv := llmServer(t, []replyStep{
		{finishReason: "tool_calls", calls: []map[string]any{toolCall("read_file", "c1", map[string]string{"path": path})}},
		{content: "handled"},
	})
	defer srv.Close()
	p := New(newClient(t, srv), a)
	p.Run(context.Background(), "read error")
}

func TestExecuteCommandArgsTimeout(t *testing.T) {
	a, fake := makeAgent(t, true)
	fake.defaultOutput = "ok\n"
	srv := llmServer(t, []replyStep{
		{finishReason: "tool_calls", calls: []map[string]any{toolCallRaw("execute_command", "c1", `{"command":"sleep 1","timeout_seconds":1}`)}},
		{finishReason: "tool_calls", calls: []map[string]any{toolCall("execute_command", "c2", map[string]string{"command": "true"})}},
		{content: "done"},
	})
	defer srv.Close()
	p := New(newClient(t, srv), a).WithTimeout(300 * time.Second)
	out, err := p.Run(context.Background(), "run")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if out != "done" {
		t.Errorf("output = %q", out)
	}
}

func TestDecodeArgsEmptyAfterQuote(t *testing.T) {
	dst := readFileArgs{Path: "x"}
	if err := decodeArgs([]byte(`""`), &dst); err != nil || dst.Path != "x" {
		t.Fatalf("empty quoted string must keep dst: %v %q", err, dst.Path)
	}
}

func TestEmptyAnswerAfterRun(t *testing.T) {
	a, fake := makeAgent(t, true)
	fake.defaultOutput = "ok\n"
	srv := llmServer(t, []replyStep{
		{finishReason: "tool_calls", calls: []map[string]any{toolCall("execute_command", "c1", map[string]string{"command": "true"})}},
		{finishReason: "stop", content: "   "},
	})
	defer srv.Close()
	p := New(newClient(t, srv), a)
	out, err := p.Run(context.Background(), "ask")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if out != "(the model did not deliver a final answer)" {
		t.Errorf("output = %q", out)
	}
}

func TestExecuteCommandInvalidArgs(t *testing.T) {
	a, _ := makeAgent(t, true)
	srv := llmServer(t, []replyStep{
		{finishReason: "tool_calls", calls: []map[string]any{toolCallRaw("execute_command", "c1", "{broken")}},
		{content: "handled"},
	})
	defer srv.Close()
	p := New(newClient(t, srv), a)
	p.Run(context.Background(), "ask")
}

// TestTheContextIsCompactedBetweenToolRounds: the tool results are what makes a plan run
// grow fastest, so the check has to happen again after each round. Checking only at the
// start of a run means a single task with many tools can overflow in the middle of it —
// which is the moment the agent is least able to recover.
func TestTheContextIsCompactedBetweenToolRounds(t *testing.T) {
	a, fake := makeAgent(t, true)
	// The output is large enough that three tool rounds cross the 45% trigger
	// even after the system prompt (which includes the embedded soul, ~1900 tokens)
	// is counted. The window is sized so the prompt alone stays below the trigger
	// and the tool output pushes it over — the same relationship the original
	// fixture had with its smaller prompt.
	fake.defaultOutput = strings.Repeat("the quick brown fox jumps over the lazy dog. ", 300)

	// The steps alternate: a tool round, then an answer, then a tool round again. The
	// summariser is the same engine, so its calls are interleaved with the loop's — and the
	// server answers every request from the same queue, in order.
	srv := llmServer(t, []replyStep{
		{finishReason: "tool_calls", calls: []map[string]any{toolCall("execute_command", "c1", map[string]string{"command": "true"})}},
		{content: "account of the first round"},
		{finishReason: "tool_calls", calls: []map[string]any{toolCall("execute_command", "c2", map[string]string{"command": "true"})}},
		{content: "account of the second round"},
		{finishReason: "tool_calls", calls: []map[string]any{toolCall("execute_command", "c3", map[string]string{"command": "true"})}},
		{content: "account of the third round"},
		{content: "the final answer"},
	})
	defer srv.Close()

	// The window is MEASURED, not chosen. Two conditions have to hold at once for this branch
	// to run at all, and they pull in opposite directions:
	//
	//   - large enough to hold the system prompt plus the two messages the policy keeps
	//     verbatim. Below that floor compaction cannot run — there is nothing to fold — and
	//     the run is correctly reported as unable to compact. That is a real answer, and it
	//     is what a window that is simply too small produces, but it is not this branch.
	//   - small enough that the tool rounds alone cross the trigger, or the check is never
	//     reached.
	//
	// Measured on this test's own fixture: the original 3200 window with a ~500-token
	// prompt satisfied both conditions. The soul (embedded personality) enlarged the
	// prompt from ~500 to ~1900 tokens, so the window was raised to 8000 and the tool
	// output scaled to ~3400 tokens per round, keeping the same proportional relationship
	// that let the tool rounds cross the 45% trigger while the prompt alone stayed below it.
	p := New(newClient(t, srv), a).
		WithLoops(8).
		WithSessionPolicy("gpt-4o", 8000, 100, 0.45, 2)

	out, err := p.Run(context.Background(), "a task that uses several tools")
	if err != nil {
		t.Fatalf("the run must survive the compaction: %v", err)
	}
	if out == "" {
		t.Error("the run must still produce an answer")
	}
	if p.Session().Compactions() == 0 {
		t.Error("the conversation must have been compacted during the tool rounds")
	}
}

// TestWithSessionContinuesInAnExistingConversation: the caller owns the session, so a
// conversation can be carried across planners — which is what the interface does, since it
// builds a planner per turn.
func TestWithSessionContinuesInAnExistingConversation(t *testing.T) {
	a, _ := makeAgent(t, true)
	srv := llmServer(t, []replyStep{{content: "an answer"}})
	defer srv.Close()

	// A conversation that already has history, as a second turn would find it.
	existing := session.New("gpt-4o", SystemPrompt, 128000)
	existing.Append(llm.Message{Role: "user", Content: "the first question"})
	existing.Append(llm.Message{Role: "assistant", Content: "the first answer"})

	p := New(newClient(t, srv), a).WithSession(existing)
	if p.Session() != existing {
		t.Fatal("WithSession must adopt the session it was given")
	}

	if _, err := p.Run(context.Background(), "the second question"); err != nil {
		t.Fatal(err)
	}

	// The first exchange is still there, and the second was added to it.
	msgs := p.Session().Messages()
	found := 0
	for _, m := range msgs {
		if strings.Contains(m.Content, "first question") || strings.Contains(m.Content, "second question") {
			found++
		}
	}
	if found != 2 {
		t.Errorf("both questions must be in the conversation, found %d in %+v", found, msgs)
	}
}

// TestAFailedCompactionBetweenRoundsStopsTheRun: the same rule as at the start of a run. A
// context that cannot be compacted must stop the agent rather than be carried into a request
// the provider will refuse — and mid-task is exactly where that would be least recoverable.
func TestAFailedCompactionBetweenRoundsStopsTheRun(t *testing.T) {
	a, fake := makeAgent(t, true)
	// The tool output is what fills the window, so the compaction is needed after one round.
	fake.defaultOutput = strings.Repeat("output that fills the window ", 60)

	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		body := ioMustRead(r.Body)
		// The summariser is the engine, so its call arrives on the same endpoint. It is the
		// one that fails: failing the tool round instead would abort the run before the
		// compaction was ever attempted, and the branch under test would never execute.
		isSummary := bytes.Contains(body, []byte("working memory of an autonomous agent"))
		if isSummary {
			w.WriteHeader(http.StatusInternalServerError)
			fmt.Fprint(w, `{"error":{"message":"the summariser is down"}}`)
			return
		}
		stream := bytes.Contains(body, []byte(`"stream":true`))
		_ = stream
		call := toolCall("execute_command", "c1", map[string]string{"command": "true"})
		if stream {
			w.Header().Set("Content-Type", "text/event-stream")
			chunk, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{
				"delta":         map[string]any{"tool_calls": []map[string]any{call}},
				"finish_reason": "tool_calls",
			}}})
			fmt.Fprintf(w, "data: %s\n\n", chunk)
			fmt.Fprint(w, "data: [DONE]\n\n")
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{
			"finish_reason": "tool_calls",
			"message":       map[string]any{"role": "assistant", "tool_calls": []map[string]any{call}},
		}}})
	}))
	defer srv.Close()

	// The window is sized so the seeded history sits BELOW the trigger and the tool output
	// pushes it over: the check at the start of the run then passes, and the one after the
	// tool round is the first to fire. A window too small for the seed makes the opening
	// check fail first, and the branch under test then never executes. The window must also
	// accommodate the system prompt, which includes the soul (the embedded personality is
	// larger than the old fixed prompt, so the window is sized to leave room for it).
	p := New(newClient(t, srv), a).
		WithLoops(6).
		WithSessionPolicy("gpt-4o", 8000, 100, 0.5, 2)

	sess := p.Session()
	for i := 0; i < 4; i++ {
		sess.Append(llm.Message{Role: "user", Content: "earlier turn"})
		sess.Append(llm.Message{Role: "assistant", Content: "earlier answer"})
	}
	if sess.NeedsCompaction() {
		t.Fatalf("the fixture must start below the trigger, it is at %.0f%%", sess.Used()*100)
	}

	_, err := p.Run(context.Background(), "a task whose tool output fills the window")
	if err == nil {
		t.Fatal("a compaction that fails between rounds must stop the run")
	}
	if !strings.Contains(err.Error(), "could not be compacted") {
		t.Errorf("the error must say what happened, got %v", err)
	}
	if sess.Compactions() != 0 {
		t.Error("a failed compaction must not be counted")
	}
}
