package tui

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/madkoding/starlight/internal/agent"
	"github.com/madkoding/starlight/internal/config"
	"github.com/madkoding/starlight/internal/llm"
	"github.com/madkoding/starlight/internal/logx"
	"github.com/madkoding/starlight/internal/onboard"
	"github.com/madkoding/starlight/internal/sandbox"
	taskpkg "github.com/madkoding/starlight/internal/task"
)

func TestAppRunnerRunPlan(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
	if !strings.Contains(out.String(), "the plan") {
		t.Errorf("answer not written to Out: %q", out.String())
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
	err := r.RunTask(context.Background(), "   ")
	if err == nil {
		t.Error("empty task must error")
	}
}

func TestAppRunnerRunConfig(t *testing.T) {
	inTempDir(t, func() {
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
		if _, err := os.Stat("starlight.yaml"); err != nil {
			t.Errorf("configuration not written: %v", err)
		}
	})
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
	r.newAgent = func(config.Config, *logx.Logger, *llm.Client, *sandbox.Sandbox, taskpkg.Source) AgentRunner {
		return &fakeAgent{}
	}
	if err := r.RunTask(context.Background(), "valid task"); err != nil {
		t.Fatalf("unexpected error: %v", err)
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
	r.newAgent = func(config.Config, *logx.Logger, *llm.Client, *sandbox.Sandbox, taskpkg.Source) AgentRunner {
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
