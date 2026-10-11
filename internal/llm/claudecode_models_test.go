package llm

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/madkoding/motita/internal/config"
)

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
			`"models":[{"value":"sonnet","resolvedModel":"claude-sonnet-5","displayName":"Sonnet","description":"Sonnet 5 · `+
			`Efficient for routine tasks"},`+
			`{"value":"haiku","resolvedModel":"claude-haiku-4-5-20251001","displayName":"Haiku","description":"Haiku 4.5 · `+
			`Fastest"}],`+
			`"account":{"subscriptionType":"Claude Max"}}}}`), nil)
	got, err := ClaudeCodeModels(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got.Plan != "Claude Max" || len(got.Models) != 2 || got.Models[0] != (ClaudeCodeModel{
		Value: "sonnet", Resolved: "claude-sonnet-5", DisplayName: "Sonnet",
		Description: "Sonnet 5 · Efficient for routine tasks"}) {
		t.Errorf("catalogue = %+v", got)
	}
	// The handshake only: no user frame, so no model is called.
	rec := readRecord(t, record)
	if len(rec.Frames) != 1 || !strings.Contains(string(rec.Frames[0]), `"control_request"`) ||
		slices.Contains(rec.Args, "--model") {
		t.Errorf("frames = %s, argv = %q", rec.Frames, rec.Args)
	}
	// Nonessential traffic off makes claude list 5 models instead of the account's 11: it is a
	// setting for turns, and discovery must not carry it (nor the turn's thinking switch).
	for _, kv := range rec.Env {
		if strings.HasPrefix(kv, "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=") ||
			strings.HasPrefix(kv, "MAX_THINKING_TOKENS=") {
			t.Errorf("discovery ran with %s", kv)
		}
	}
	if !slices.Contains(claudeEnv(config.LLM{}), "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1") {
		t.Error("a turn must still run with nonessential traffic off")
	}
}

func TestClaudeCodeModelsFailures(t *testing.T) {
	claudeTestClient(
		t,
		`{"type":"control_response","response":{"subtype":"error","request_id":"models","error":"not now"}}`,
		nil,
	)
	if _, err := ClaudeCodeModels(context.Background()); err == nil || !strings.Contains(err.Error(), "not now") {
		t.Errorf("err = %v", err)
	}
	t.Setenv("MOTITA_FAKE_CLAUDE", "")
	if _, err := ClaudeCodeModels(context.Background()); err == nil ||
		!strings.Contains(err.Error(), "exited before answering") {
		t.Errorf("err = %v", err)
	}
	t.Setenv("MOTITA_CLAUDE_BIN", filepath.Join(t.TempDir(), "claude"))
	if _, err := ClaudeCodeModels(context.Background()); err == nil ||
		!strings.Contains(err.Error(), "install Claude Code") {
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
		`{"id":1,"jsonrpc":"2.0","result":{"capabilities":{"tools":{}},"protocolVersion":"2024-11-05",`+
			`"serverInfo":{"name":"motita","version":"1"}}}`,
		`{"id":2,"jsonrpc":"2.0","result":{"tools":[{"name":"read_file","description":"","inputSchema":{"type":"object"}}]}}`,
		`{"id":"c","jsonrpc":"2.0","result":{"content":[{"text":"denied: motita executes tools `+
			`itself","type":"text"}],"isError":true}}`,
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
	if err := ServeClaudeCodeMCP(
		strings.NewReader(ping), &bytes.Buffer{}, filepath.Join(dir, "missing.json"),
	); err == nil {
		t.Error("a missing manifest must be an error")
	}
	if err := ServeClaudeCodeMCP(strings.NewReader(ping), &bytes.Buffer{}, bad); err == nil ||
		!strings.Contains(err.Error(), "invalid tool manifest") {
		t.Errorf("an invalid manifest must be an error, got %v", err)
	}
	if err := ServeClaudeCodeMCP(strings.NewReader(ping), failingWriter{}, good); err == nil {
		t.Error("a reply that cannot be written must end the server")
	}
}
