package plan

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/madkoding/motita/internal/execx"
)

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
		{finishReason: "tool_calls",
			calls: []map[string]any{toolCall("execute_command", "c1", map[string]string{"command": "echo hi"})}},
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
		{finishReason: "tool_calls",
			calls: []map[string]any{toolCall("execute_command", "c1", map[string]string{"command": "ls"})}},
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
		{finishReason: "tool_calls",
			calls: []map[string]any{toolCall("execute_command", "c1", map[string]string{"command": ""})}},
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
				chunk, _ := json.Marshal(
					map[string]any{"choices": []any{map[string]any{"delta": delta, "finish_reason": "tool_calls"}}})
				fmt.Fprintf(w, "data: %s\n\n", chunk)
				fmt.Fprint(w, "data: [DONE]\n\n")
				return
			}
			fmt.Fprint(w,
				`{"choices":[{"finish_reason":"tool_calls","message":{"role":"assistant","tool_calls":[{"id":"c1",`+
					`"type":"function","function":{"name":"execute_command","arguments":"{\"command\":\"true\"}"}}]}}]}`)
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
	makeDumpable(t)
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
		{finishReason: "tool_calls",
			calls: []map[string]any{toolCallRaw("execute_command", "c1", `{"command":"sleep 1","timeout_seconds":1}`)}},
		{finishReason: "tool_calls",
			calls: []map[string]any{toolCall("execute_command", "c2", map[string]string{"command": "true"})}},
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
		{finishReason: "tool_calls",
			calls: []map[string]any{toolCall("execute_command", "c1", map[string]string{"command": "true"})}},
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
