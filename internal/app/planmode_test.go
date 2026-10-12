package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/madkoding/motita/internal/agent"
	"github.com/madkoding/motita/internal/config"
	"github.com/madkoding/motita/internal/llm"
	"github.com/madkoding/motita/internal/logx"
	"github.com/madkoding/motita/internal/sandbox"
)

// --- Plan mode tests --------------------------------------------------------

func planServer(t *testing.T, answers []string) *httptest.Server {
	t.Helper()
	idx := 0
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Messages []struct {
				Content string `json:"content"`
			} `json:"messages"`
			Stream bool `json:"stream"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		text := ""
		for _, m := range req.Messages {
			text += m.Content
		}
		var content string
		if idx < len(answers) {
			content = answers[idx]
			idx++
		} else {
			content = "done"
		}
		if req.Stream {
			w.Header().Set("Content-Type", "text/event-stream")
			chunk, _ := json.Marshal(
				map[string]any{"choices": []any{map[string]any{"delta": map[string]any{"content": content}}}},
			)
			fmt.Fprintf(w, "data: %s\n\n", chunk)
			fmt.Fprint(w, "data: [DONE]\n\n")
			return
		}
		json.NewEncoder(w).Encode(map[string]any{
			"choices": []any{map[string]any{"message": map[string]any{"content": content}}},
		})
	}))
}

func planConfig(t *testing.T, srv *httptest.Server) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	mustWrite(t, path, fmt.Sprintf(`sandbox:
  kind: none
  cgroups: off
llm:
  api_key: x
  base_url: %s
  model: mock
  max_attempts: 1
  timeout: 10s
agent:
  workspace_dir: %s
  log_level: error
  log_console: false
gateway:
  listen: "127.0.0.1:0"
`, srv.URL, dir))
	return path
}

func TestParsePlanAndPromptFlags(t *testing.T) {
	cases := []struct {
		name     string
		args     []string
		expected flags
		fails    bool
	}{
		{"plan flag", []string{"-plan"}, flags{plan: true}, false},
		{"plan=true", []string{"-plan=true"}, flags{plan: true}, false},
		{"plan=false", []string{"-plan=false"}, flags{plan: false}, false},
		{"prompt", []string{"-p", "ask"}, flags{plan: false, prompt: "ask"}, false},
		{"prompt with equals", []string{"-p=ask"}, flags{plan: false, prompt: "ask"}, false},
		{"prompt missing value", []string{"-p"}, flags{}, true},
		{"serve flag", []string{"-serve"}, flags{serve: true}, false},
		{"serve long form", []string{"--serve"}, flags{serve: true}, false},
		{"gateway address", []string{"-gateway", "127.0.0.1:8787"}, flags{gateway: "127.0.0.1:8787"}, false},
		{"gateway address with equals", []string{"-gateway=127.0.0.1:8787"}, flags{gateway: "127.0.0.1:8787"}, false},
		{"gateway off", []string{"-gateway", "off"}, flags{gateway: "off"}, false},
		{"gateway missing value", []string{"-gateway"}, flags{}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parse(tc.args)
			if tc.fails {
				if err == nil {
					t.Fatal("expected error")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got.plan != tc.expected.plan || got.prompt != tc.expected.prompt {
				t.Errorf("got %+v, expected %+v", got, tc.expected)
			}
		})
	}
}

func TestPlanOneShotPrompt(t *testing.T) {
	silence(t)
	srv := planServer(t, []string{"the plan"})
	defer srv.Close()
	path := planConfig(t, srv)
	var out, errs bytes.Buffer
	code := Run(Options{
		Args: []string{"-config", path, "-plan", "-p", "list files"},
		Out:  &out,
		Err:  &errs,
	})
	if code != Success {
		t.Fatalf("code = %d, errs = %q", code, errs.String())
	}
	if !strings.Contains(out.String(), "the plan") {
		t.Errorf("out = %q", out.String())
	}
}

func TestPlanOneShotError(t *testing.T) {
	silence(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprint(w, `{"error":{"message":"down"}}`)
	}))
	defer srv.Close()
	path := planConfig(t, srv)
	var out, errs bytes.Buffer
	code := Run(Options{
		Args: []string{"-config", path, "-plan", "-p", "list files"},
		Out:  &out,
		Err:  &errs,
	})
	if code != RunError {
		t.Fatalf("code = %d, errs = %q", code, errs.String())
	}
	if !strings.Contains(errs.String(), "❌") {
		t.Errorf("errs = %q", errs.String())
	}
}

func TestPlanFlagWithoutPromptEntersTUI(t *testing.T) {
	silence(t)
	srv := planServer(t, []string{"hello"})
	defer srv.Close()
	path := planConfig(t, srv)
	var out, errs bytes.Buffer
	tuiCalled := false
	code := Run(Options{
		Args: []string{"-config", path, "-plan"},
		Out:  &out,
		Err:  &errs,
		RunTUI: func(context.Context, config.Config, *llm.Client, *sandbox.Sandbox, *logx.Logger) int {
			tuiCalled = true
			return Success
		},
	})
	if code != Success {
		t.Fatalf("code = %d", code)
	}
	if !tuiCalled {
		t.Fatal("-plan without -prompt should enter the TUI")
	}
}

func TestTUIFlag(t *testing.T) {
	silence(t)
	srv := planServer(t, []string{"hello"})
	defer srv.Close()
	path := planConfig(t, srv)
	var out, errs bytes.Buffer
	tuiCalled := false
	code := Run(Options{
		Args: []string{"-config", path, "-tui"},
		Out:  &out,
		Err:  &errs,
		RunTUI: func(context.Context, config.Config, *llm.Client, *sandbox.Sandbox, *logx.Logger) int {
			tuiCalled = true
			return Success
		},
	})
	if code != Success {
		t.Fatalf("code = %d", code)
	}
	if !tuiCalled {
		t.Fatal("-tui should launch the TUI")
	}
}

func TestDefaultNoConfigGoesToTUI(t *testing.T) {
	silence(t)
	t.Setenv("MOTITA_LLM_API_KEY", "test")
	srv := planServer(t, []string{"default plan"})
	defer srv.Close()
	var out, errs bytes.Buffer
	tuiCalled := false
	code := Run(Options{
		Args: nil,
		Out:  &out,
		Err:  &errs,
		NewEngine: func(c config.LLM, l *logx.Logger) (*llm.Client, error) {
			c.BaseURL = srv.URL
			c.Model = "mock"
			return llm.New(c, l)
		},
		NewSandbox: func(o sandbox.Options) (*sandbox.Sandbox, error) {
			o.Dir = t.TempDir()
			return sandbox.New(o)
		},
		RunTUI: func(context.Context, config.Config, *llm.Client, *sandbox.Sandbox, *logx.Logger) int {
			tuiCalled = true
			return Success
		},
	})
	if code != Success {
		t.Fatalf("code = %d, errs = %q", code, errs.String())
	}
	if !tuiCalled {
		t.Fatal("no task should launch the TUI by default")
	}
}

func TestPlanDefaultTimeoutAndLoops(t *testing.T) {
	cfg := config.Default()
	cfg.Sandbox.Timeout = 0
	if planDefaultTimeout(cfg) != 120*time.Second {
		t.Error("default timeout wrong")
	}
	cfg.Agent.MaxRetries = 0
	if planDefaultLoops(cfg) != 5 {
		t.Errorf("default loops = %d", planDefaultLoops(cfg))
	}
	cfg.Agent.MaxRetries = 3
	if planDefaultLoops(cfg) != 3 {
		t.Errorf("loops = %d", planDefaultLoops(cfg))
	}
	cfg.Agent.MaxRetries = 20
	if planDefaultLoops(cfg) != 5 {
		t.Errorf("loops = %d", planDefaultLoops(cfg))
	}
}

func TestRunTUINilHook(t *testing.T) {
	silence(t)
	t.Setenv("MOTITA_LLM_API_KEY", "test")
	srv := planServer(t, []string{"hello"})
	defer srv.Close()
	var out bytes.Buffer
	code := Run(Options{
		// planConfig asks for an ephemeral gateway port: this walks the real interface, so it
		// binds a real listener, and the shipped default is a fixed port two tests cannot share.
		Args:  []string{"-config", planConfig(t, srv), "-tui"},
		Out:   &out,
		Err:   &out,
		Stdin: strings.NewReader("q\n"),
	})
	if code != 0 {
		t.Fatalf("code = %d", code)
	}
}

// TestTUIRealRunnerPlanModeDoesNotPanic: the TUI is handed the production runner
// built inside runTUI. A struct literal there left the agent factory nil, so
// choosing Plan mode from the real menu crashed with a nil pointer dereference
// that no test caught, because every test injected the RunTUI hook instead of
// exercising the real one. This walks the real path: menu -> plan -> answer.
func TestTUIRealRunnerPlanModeDoesNotPanic(t *testing.T) {
	silence(t)
	t.Setenv("MOTITA_LLM_API_KEY", "test")
	srv := planServer(t, []string{"the answer"})
	defer srv.Close()

	var out bytes.Buffer
	code := Run(Options{
		// p selects plan mode, then the prompt, then Enter to leave the answer
		// screen, then q to quit the menu. planConfig supplies an ephemeral gateway port.
		Args:  []string{"-config", planConfig(t, srv), "-tui"},
		Out:   &out,
		Err:   &out,
		Stdin: strings.NewReader("/p\nwhat is running?\n\nq\n"),
	})
	if code != 0 {
		t.Fatalf("code = %d, out = %q", code, out.String())
	}
	if !strings.Contains(out.String(), "the answer") {
		t.Errorf("the plan answer must be shown, out = %q", out.String())
	}
	if got := strings.Count(out.String(), "the answer"); got < 1 {
		t.Errorf("the answer must appear at least once, found %d times in %q", got, out.String())
	}
	if strings.Contains(out.String(), "panic") {
		t.Errorf("the real runner must not panic, out = %q", out.String())
	}
}

// TestTUIRealRunnerModelsModeLists: choosing Models from the real menu must walk
// the production runner too, without panicking on a nil dependency.
func TestTUIRealRunnerModelsModeLists(t *testing.T) {
	silence(t)
	t.Setenv("MOTITA_LLM_API_KEY", "test")
	// A catalogue that answers for any path, so the real lister succeeds.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":"model-from-catalogue"}]}`))
	}))
	defer srv.Close()

	path := planConfig(t, srv)
	var out bytes.Buffer
	code := Run(Options{
		Args:  []string{"-config", path, "-tui"},
		Out:   &out,
		Err:   &out,
		Stdin: strings.NewReader("/m\n\nq\n"),
	})
	if code != 0 {
		t.Fatalf("code = %d, out = %q", code, out.String())
	}
	if !strings.Contains(out.String(), "base URL") {
		t.Errorf("the models screen must show the active setup, out = %q", out.String())
	}
}

func TestDefaultNoConfigButTaskUsesTaskMode(t *testing.T) {
	silence(t)
	t.Setenv("MOTITA_LLM_API_KEY", "test")
	var out, errs bytes.Buffer
	code := Run(Options{
		Args:     []string{"-task", "TASK-FROM-CMD"},
		Out:      &out,
		Err:      &errs,
		RunAgent: func(context.Context, *agent.Agent) error { return nil },
	})
	if code != Success {
		t.Fatalf("code = %d, errs = %q", code, errs.String())
	}
}
