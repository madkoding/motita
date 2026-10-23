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
	// The workspace is the test's whole temporary root, not one directory inside it: t.TempDir
	// hands out siblings, and a confined command may only read what the sandbox was given by
	// name. With the workspace one level down, a test that searches a tree it just made fails
	// for a reason that is not the code, wherever TMPDIR points.
	cfg.Agent.WorkspaceDir = filepath.Dir(t.TempDir())
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
			writeStreamReply(w, step)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{
			"choices": []any{choice},
		})
	}))
}

// writeStreamReply emits one scripted reply as SSE chunks: its text, then its tool calls.
func writeStreamReply(w http.ResponseWriter, step replyStep) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.WriteHeader(http.StatusOK)
	if step.content != "" {
		chunk, _ := json.Marshal(
			map[string]any{"choices": []any{map[string]any{"delta": map[string]any{"content": step.content}}}})
		fmt.Fprintf(w, "data: %s\n\n", chunk)
	}
	if len(step.calls) > 0 {
		delta := map[string]any{"tool_calls": step.calls}
		chunk, _ := json.Marshal(
			map[string]any{"choices": []any{map[string]any{"delta": delta, "finish_reason": step.finishReason}}})
		fmt.Fprintf(w, "data: %s\n\n", chunk)
	}
	fmt.Fprint(w, "data: [DONE]\n\n")
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
		{finishReason: "tool_calls",
			calls: []map[string]any{toolCall("list_directory", "c1", map[string]string{"path": dir})}},
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
		{finishReason: "tool_calls",
			calls: []map[string]any{
				toolCall("search_in_files", "c1", map[string]string{"pattern": "hello", "path": dir, "literal": "true"})}},
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
		{finishReason: "tool_calls",
			calls: []map[string]any{toolCall("list_directory", "c1", map[string]string{"path": dir})}},
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
		{finishReason: "tool_calls",
			calls: []map[string]any{toolCall("list_directory", "c1", map[string]string{"path": "/nonexistent/zzzz"})}},
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
		{finishReason: "tool_calls",
			calls: []map[string]any{toolCall("execute_command", "c1", map[string]string{"command": "echo hello"})}},
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
		{finishReason: "tool_calls",
			calls: []map[string]any{toolCall("execute_command", "c1", map[string]string{"command": "rm -rf /tmp/x"})}},
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

func TestMaxLoopsExhaustion(t *testing.T) {
	a, fake := makeAgent(t, true)
	fake.defaultOutput = "ok\n"
	srv := llmServer(t, []replyStep{
		{finishReason: "tool_calls",
			calls: []map[string]any{toolCall("execute_command", "c1", map[string]string{"command": "true"})}},
		{finishReason: "tool_calls",
			calls: []map[string]any{toolCall("execute_command", "c2", map[string]string{"command": "true"})}},
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
