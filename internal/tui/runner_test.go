package tui

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
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
	"github.com/madkoding/motita/internal/llm"
	"github.com/madkoding/motita/internal/logx"
	"github.com/madkoding/motita/internal/onboard"
	"github.com/madkoding/motita/internal/sandbox"
	taskpkg "github.com/madkoding/motita/internal/task"
)

func TestAppRunnerRunPlan(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if bytes.Contains(body, []byte(`"stream":true`)) {
			w.Header().Set("Content-Type", "text/event-stream")
			chunk, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"delta": map[string]any{"content": "the plan"}}}})
			fmt.Fprintf(w, "data: %s\n\n", chunk)
			fmt.Fprint(w, "data: [DONE]\n\n")
			return
		}
		fmt.Fprint(w, `{"choices":[{"message":{"content":"the plan"}}]}`)
	}))
	defer srv.Close()

	cfg := config.Default()
	cfg.LLM.Provider = "openai"
	cfg.LLM.APIKey = "key"
	cfg.LLM.BaseURL = srv.URL
	cfg.LLM.MaxAttempts = 1
	cfg.LLM.BackoffInitial = time.Millisecond
	cfg.LLM.BackoffMax = time.Millisecond
	cfg.Sandbox.Kind = "none"

	out := &bytes.Buffer{}
	r := NewAppRunner(out, &bytes.Buffer{}, cfg, nil, nil, logx.Global())
	engine, err := llm.New(cfg.LLM, r.Log)
	if err != nil {
		t.Fatal(err)
	}
	r.Engine = engine
	answer, err := r.RunPlan(context.Background(), "prompt", func(string, ...any) {})
	if err != nil {
		t.Fatalf("RunPlan error: %v", err)
	}
	if answer != "the plan" {
		t.Errorf("answer = %q", answer)
	}
	if !strings.Contains(out.String(), "the plan") && answer != "the plan" {
		t.Errorf("answer not returned or written to Out: out=%q answer=%q", out.String(), answer)
	}
}

func TestAppRunnerRunPlanError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprint(w, "boom")
	}))
	defer srv.Close()

	cfg := config.Default()
	cfg.LLM.Provider = "openai"
	cfg.LLM.APIKey = "key"
	cfg.LLM.BaseURL = srv.URL
	cfg.LLM.MaxAttempts = 1
	cfg.Sandbox.Kind = "none"

	r := NewAppRunner(&bytes.Buffer{}, &bytes.Buffer{}, cfg, nil, nil, logx.Global())
	engine, err := llm.New(cfg.LLM, r.Log)
	if err != nil {
		t.Fatal(err)
	}
	r.Engine = engine
	_, err = r.RunPlan(context.Background(), "prompt", func(string, ...any) {})
	if err == nil {
		t.Fatal("expected an error")
	}
}

func TestAppRunnerRunTask(t *testing.T) {
	r := NewAppRunner(&bytes.Buffer{}, &bytes.Buffer{}, config.Default(), &llm.Client{}, &sandbox.Sandbox{}, logx.Global())
	_, err := r.RunTask(context.Background(), "   ", func(string, ...any) {})
	if err == nil {
		t.Error("empty task must error")
	}
}

func TestAppRunnerRunConfig(t *testing.T) {
	inTempDir(t, func() {
		// The wizard writes into the motita home, so the test owns one. Without this it would
		// write into the HOME of whoever runs the suite.
		home := t.TempDir()
		t.Setenv("HOME", home)
		r := NewAppRunner(&bytes.Buffer{}, &bytes.Buffer{}, config.Default(), &llm.Client{}, &sandbox.Sandbox{}, logx.Global())
		oldStdin := os.Stdin
		r2, w2, _ := os.Pipe()
		os.Stdin = r2
		defer func() { os.Stdin = oldStdin; r2.Close() }()
		go func() {
			defer w2.Close()
			w2.WriteString("openai\n1\n2\n\n\n")
		}()
		err := r.RunConfig(context.Background())
		if err != nil {
			t.Fatalf("RunConfig error: %v", err)
		}
		want := filepath.Join(home, ".motita", "motita.yaml")
		if _, err := os.Stat(want); err != nil {
			t.Errorf("configuration not written to %s: %v", want, err)
		}
	})
}

// TestAppRunnerRunModelsListsAndMarksTheConfiguredOne: the menu must show the
// catalogue and point at the model in use.
func TestAppRunnerRunModelsListsAndMarksTheConfiguredOne(t *testing.T) {
	var out bytes.Buffer
	cfg := config.Default()
	cfg.LLM.Provider = "ollama"
	cfg.LLM.BaseURL = "https://ollama.com/v1"
	cfg.LLM.Model = "glm-5.3"
	cfg.LLM.APIKey = "secret-key-value"

	r := NewAppRunner(&out, &bytes.Buffer{}, cfg, &llm.Client{}, &sandbox.Sandbox{}, logx.Global())
	r.listModels = func(_ context.Context, baseURL, apiKey string) ([]string, error) {
		if baseURL != "https://ollama.com/v1" {
			t.Errorf("baseURL = %q", baseURL)
		}
		if apiKey != "secret-key-value" {
			t.Errorf("the key must be passed to the lister")
		}
		return []string{"glm-5.3", "gpt-oss:120b"}, nil
	}
	report, err := r.RunModels(context.Background())
	if err != nil {
		t.Fatalf("RunModels: %v", err)
	}
	got := report
	if !strings.Contains(got, "glm-5.3") || !strings.Contains(got, "gpt-oss:120b") {
		t.Errorf("the catalogue must be printed:\n%s", got)
	}
	if !strings.Contains(got, "* glm-5.3") {
		t.Errorf("the configured model must be marked:\n%s", got)
	}
}

// TestAppRunnerRunModelsNeverPrintsTheKey: the screen has to be safe to share.
func TestAppRunnerRunModelsNeverPrintsTheKey(t *testing.T) {
	var out bytes.Buffer
	cfg := config.Default()
	cfg.LLM.APIKey = "super-secret-value"

	r := NewAppRunner(&out, &bytes.Buffer{}, cfg, &llm.Client{}, &sandbox.Sandbox{}, logx.Global())
	r.listModels = func(context.Context, string, string) ([]string, error) { return []string{"m"}, nil }
	report, err := r.RunModels(context.Background())
	if err != nil {
		t.Fatalf("RunModels: %v", err)
	}
	if strings.Contains(report, "super-secret-value") {
		t.Error("the API key must never be printed")
	}
	if !strings.Contains(report, "api key  : present") {
		t.Errorf("the presence of the key must be reported:\n%s", report)
	}
}

// TestAppRunnerRunModelsReportsAMissingKeyWithTheRightVariable: the message has
// to name the variable that actually works for the provider.
func TestAppRunnerRunModelsReportsAMissingKeyWithTheRightVariable(t *testing.T) {
	var out bytes.Buffer
	cfg := config.Default()
	cfg.LLM.Provider = "ollama"
	cfg.LLM.APIKey = ""

	r := NewAppRunner(&out, &bytes.Buffer{}, cfg, &llm.Client{}, &sandbox.Sandbox{}, logx.Global())
	r.listModels = func(context.Context, string, string) ([]string, error) { return nil, nil }
	report, err := r.RunModels(context.Background())
	if err != nil {
		t.Fatalf("RunModels: %v", err)
	}
	if !strings.Contains(report, "OLLAMA_API_KEY") {
		t.Errorf("the missing-key message must name OLLAMA_API_KEY:\n%s", report)
	}
}

// TestAppRunnerRunModelsSurvivesACatalogueFailure: a failed listing must not look
// like a broken agent; the configured model is still reported.
func TestAppRunnerRunModelsSurvivesACatalogueFailure(t *testing.T) {
	var out bytes.Buffer
	cfg := config.Default()
	cfg.LLM.Provider = "ollama"
	cfg.LLM.Model = "glm-5.3"
	cfg.LLM.BaseURL = ""

	r := NewAppRunner(&out, &bytes.Buffer{}, cfg, &llm.Client{}, &sandbox.Sandbox{}, logx.Global())
	r.listModels = func(_ context.Context, baseURL, _ string) ([]string, error) {
		if baseURL != "https://ollama.com/v1" {
			t.Errorf("an empty base URL must fall back to the provider default, got %q", baseURL)
		}
		return nil, errors.New("connection refused")
	}
	report, err := r.RunModels(context.Background())
	if err != nil {
		t.Fatalf("a catalogue failure must not be an error: %v", err)
	}
	got := report
	if !strings.Contains(got, "connection refused") {
		t.Errorf("the failure must be reported:\n%s", got)
	}
	if !strings.Contains(got, "glm-5.3") {
		t.Errorf("the configured model must still be named:\n%s", got)
	}
}

// TestAppRunnerRunModelsWithNoBaseURLAndNoDefault: a provider nobody knows has no
// default endpoint, and the screen must say so instead of printing an empty URL.
func TestAppRunnerRunModelsWithNoBaseURLAndNoDefault(t *testing.T) {
	var out bytes.Buffer
	cfg := config.Default()
	cfg.LLM.Provider = "custom"
	cfg.LLM.BaseURL = ""
	cfg.LLM.Model = "x"

	r := NewAppRunner(&out, &bytes.Buffer{}, cfg, &llm.Client{}, &sandbox.Sandbox{}, logx.Global())
	r.listModels = func(_ context.Context, baseURL, _ string) ([]string, error) {
		if baseURL != "" {
			t.Errorf("baseURL = %q, want empty for an unknown provider", baseURL)
		}
		return []string{"x"}, nil
	}
	report, err := r.RunModels(context.Background())
	if err != nil {
		t.Fatalf("RunModels: %v", err)
	}
	if !strings.Contains(report, "x") {
		t.Errorf("the catalogue must be printed:\n%s", report)
	}
}

// TestAppRunnerRunModelsUsesTheRealListerByDefault: an AppRunner built by hand has
// no injected lister, and the method must fall back to the real one instead of
// panicking on a nil function.
func TestAppRunnerRunModelsUsesTheRealListerByDefault(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":"real-model"}]}`))
	}))
	defer srv.Close()

	var out bytes.Buffer
	cfg := config.Default()
	cfg.LLM.Provider = "openai"
	cfg.LLM.BaseURL = srv.URL + "/v1"
	cfg.LLM.APIKey = "k"

	// Built by hand, so listModels is nil.
	r := &AppRunner{Out: &out, Err: &bytes.Buffer{}, Cfg: cfg, Log: logx.Global()}
	report, err := r.RunModels(context.Background())
	if err != nil {
		t.Fatalf("RunModels: %v", err)
	}
	if !strings.Contains(report, "real-model") {
		t.Errorf("the real lister must be used when none is injected:\n%s", report)
	}
}

func TestPlanDefaultTimeout(t *testing.T) {
	cfg := config.Default()
	cfg.Sandbox.Timeout = 0
	if planDefaultTimeout(cfg) != 120*time.Second {
		t.Error("default timeout wrong")
	}
	cfg.Sandbox.Timeout = 30 * time.Second
	if planDefaultTimeout(cfg) != 30*time.Second {
		t.Error("configured timeout ignored")
	}
}

func TestPlanDefaultLoops(t *testing.T) {
	cfg := config.Default()
	cfg.Agent.MaxRetries = 0
	if planDefaultLoops(cfg) != 5 {
		t.Error("default loops wrong")
	}
	cfg.Agent.MaxRetries = 3
	if planDefaultLoops(cfg) != 3 {
		t.Error("configured loops ignored")
	}
	cfg.Agent.MaxRetries = 20
	if planDefaultLoops(cfg) != 5 {
		t.Error("out-of-range loops not clamped")
	}
}

func inTempDir(t *testing.T, body func()) {
	t.Helper()
	previous, err := os.Getwd()
	if err != nil {
		t.Fatalf("could not read the working directory: %v", err)
	}
	dir := t.TempDir()
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("could not enter %s: %v", dir, err)
	}
	defer func() {
		if err := os.Chdir(previous); err != nil {
			t.Fatalf("could not go back to %s: %v", previous, err)
		}
	}()
	body()
}

var _ *agent.Agent
var _ *llm.Client
var _ *sandbox.Sandbox
var _ *onboard.Provider
var _ taskpkg.Source

func TestAppRunnerRunTaskSuccess(t *testing.T) {
	r := NewAppRunner(&bytes.Buffer{}, &bytes.Buffer{}, config.Default(), &llm.Client{}, &sandbox.Sandbox{}, logx.Global())
	r.newAgent = func(config.Config, *logx.Logger, *llm.Client, *sandbox.Sandbox, taskpkg.Source, bool) AgentRunner {
		return &fakeAgent{}
	}
	result, err := r.RunTask(context.Background(), "valid task", func(string, ...any) {})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == "" {
		t.Fatal("expected a result string from a successful task")
	}
}

func TestAppRunnerRunPlanWithFakeAgent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"choices":[{"message":{"content" :"plan"}}]}`)
	}))
	defer srv.Close()
	cfg := config.Default()
	cfg.LLM.Provider = "openai"
	cfg.LLM.APIKey = "key"
	cfg.LLM.BaseURL = srv.URL
	cfg.LLM.MaxAttempts = 1
	cfg.Sandbox.Kind = "none"
	r := NewAppRunner(&bytes.Buffer{}, &bytes.Buffer{}, cfg, nil, nil, logx.Global())
	engine, _ := llm.New(cfg.LLM, r.Log)
	r.Engine = engine
	r.newAgent = func(config.Config, *logx.Logger, *llm.Client, *sandbox.Sandbox, taskpkg.Source, bool) AgentRunner {
		return &fakeAgent{}
	}
	_, err := r.RunPlan(context.Background(), "prompt", func(string, ...any) {})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

type fakeAgent struct{}

func (fakeAgent) Run(context.Context) error                               { return nil }
func (fakeAgent) RunCommand(context.Context, string) (string, int, error) { return "", 0, nil }
func (fakeAgent) SetTranscript([]agent.DialogueTurn)                      {}
func (fakeAgent) Transcript() []agent.DialogueTurn                        { return nil }

// With no HOME the wizard falls back to the working directory, so a stripped environment can
// still configure itself instead of refusing for want of a variable.
func TestRunConfigWithoutHomeUsesTheWorkingDirectory(t *testing.T) {
	inTempDir(t, func() {
		t.Setenv("HOME", "")
		r := NewAppRunner(&bytes.Buffer{}, &bytes.Buffer{}, config.Default(), &llm.Client{}, &sandbox.Sandbox{}, logx.Global())
		oldStdin := os.Stdin
		r2, w2, _ := os.Pipe()
		os.Stdin = r2
		defer func() { os.Stdin = oldStdin; r2.Close() }()
		go func() {
			defer w2.Close()
			w2.WriteString("openai\n1\n2\n\n\n")
		}()
		if err := r.RunConfig(context.Background()); err != nil {
			t.Fatalf("RunConfig error: %v", err)
		}
		if _, err := os.Stat("motita.yaml"); err != nil {
			t.Errorf("with no HOME the working directory is used: %v", err)
		}
	})
}
