package tui

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/madkoding/starlight/internal/agent"
	"github.com/madkoding/starlight/internal/config"
	"github.com/madkoding/starlight/internal/llm"
	"github.com/madkoding/starlight/internal/logx"
	"github.com/madkoding/starlight/internal/sandbox"
	taskpkg "github.com/madkoding/starlight/internal/task"
)

// These tests exercise the production runner through the paths the interactive
// session uses: the configuration the status line reads, the reasoning switch, and
// the mapping from a task result to the sentence shown in the chat.

// TestAppRunnerConfigReflectsTheReasoningSwitch: the status bar reads Config(), so
// the switch has to be visible there and not only inside the engine.
func TestAppRunnerConfigReflectsTheReasoningSwitch(t *testing.T) {
	r := NewAppRunner(&bytes.Buffer{}, &bytes.Buffer{}, config.Default(), nil, nil, logx.Global())
	if got := r.Config().LLM.Provider; got != config.Default().LLM.Provider {
		t.Errorf("Config() = %q, want the runner's own configuration", got)
	}

	r.SetReasoning("high")
	cfg := r.Config()
	if cfg.LLM.Reasoning.Level != "high" || !cfg.LLM.Reasoning.Enabled {
		t.Errorf("SetReasoning(high) left %+v", cfg.LLM.Reasoning)
	}
	// Turning it off also clears the enabled flag, so the request builder does not
	// keep sending a level the user asked to stop using.
	r.SetReasoning("off")
	cfg = r.Config()
	if cfg.LLM.Reasoning.Level != "off" || cfg.LLM.Reasoning.Enabled {
		t.Errorf("SetReasoning(off) left %+v", cfg.LLM.Reasoning)
	}
}

// TestAppRunnerEngineIsLazy: the TUI can start with no configuration file, which
// means no engine. Choosing plan or task then has to build one from the current
// configuration instead of dereferencing a nil client.
func TestAppRunnerEngineIsLazy(t *testing.T) {
	cfg := config.Default()
	cfg.LLM.Provider = "openai"
	cfg.LLM.APIKey = ""
	r := NewAppRunner(&bytes.Buffer{}, &bytes.Buffer{}, cfg, nil, nil, logx.Global())

	if _, err := r.engine(); err == nil {
		t.Error("an engine cannot be built without a key, and that must be an error")
	}

	// With a key it is built on demand, and an injected one is preferred.
	r.Cfg.LLM.APIKey = "k"
	built, err := r.engine()
	if err != nil {
		t.Fatalf("engine: %v", err)
	}
	if built == nil {
		t.Fatal("engine returned nil without an error")
	}
	injected := &llm.Client{}
	r.Engine = injected
	got, err := r.engine()
	if err != nil {
		t.Fatalf("engine: %v", err)
	}
	if got != injected {
		t.Error("an injected engine must be reused instead of building a second one")
	}
}

// TestAppRunnerRunTaskMapsEveryOutcome: the summary shown in the chat is built
// from the task result, and each shape of result has to produce a sentence a user
// can read.
func TestAppRunnerRunTaskMapsEveryOutcome(t *testing.T) {
	cases := []struct {
		name string
		tr   agent.TaskResult
		want string
	}{
		{
			name: "a synthesised summary wins",
			tr:   agent.TaskResult{Pass: true, Summary: "hay 20 archivos .txt"},
			want: "hay 20 archivos .txt",
		},
		{
			name: "a pass without a summary names its reason",
			tr:   agent.TaskResult{Pass: true, Reason: "2 checks passed"},
			want: "completed: 2 checks passed",
		},
		{
			name: "a pass also reports the final action",
			tr:   agent.TaskResult{Pass: true, Reason: "1 check passed", FinalAction: "echo done"},
			want: "final action: echo done",
		},
		{
			name: "the word none is not an action",
			tr:   agent.TaskResult{Pass: true, Reason: "1 check passed", FinalAction: "none"},
			want: "completed: 1 check passed",
		},
		{
			name: "a failure is reported as such",
			tr:   agent.TaskResult{Pass: false, Reason: "the check failed"},
			want: "failed: the check failed",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := NewAppRunner(&bytes.Buffer{}, &bytes.Buffer{}, config.Default(), &llm.Client{}, &sandbox.Sandbox{}, logx.Global())
			r.newAgent = func(config.Config, *logx.Logger, *llm.Client, *sandbox.Sandbox, taskpkg.Source) AgentRunner {
				return &resultAgent{tr: tc.tr}
			}
			got, err := r.RunTask(context.Background(), "a task", func(string, ...any) {})
			if err != nil {
				t.Fatalf("RunTask: %v", err)
			}
			if !strings.Contains(got, tc.want) {
				t.Errorf("result = %q, want it to contain %q", got, tc.want)
			}
			if tc.name == "the word none is not an action" && strings.Contains(got, "none") {
				t.Errorf("the placeholder action must not be shown: %q", got)
			}
		})
	}
}

// TestAppRunnerRunTaskWithoutAResult: an agent that never reports anything still
// has to produce a sentence, because an empty chat message looks like a bug.
func TestAppRunnerRunTaskWithoutAResult(t *testing.T) {
	r := NewAppRunner(&bytes.Buffer{}, &bytes.Buffer{}, config.Default(), &llm.Client{}, &sandbox.Sandbox{}, logx.Global())
	r.newAgent = func(config.Config, *logx.Logger, *llm.Client, *sandbox.Sandbox, taskpkg.Source) AgentRunner {
		return &resultAgent{silent: true}
	}
	got, err := r.RunTask(context.Background(), "a task", func(string, ...any) {})
	if err != nil {
		t.Fatalf("RunTask: %v", err)
	}
	if got == "" {
		t.Error("a task that reports nothing must still yield an explanation")
	}
}

// TestAppRunnerRunTaskPropagatesAnAgentFailure: an error from the agent has to
// reach the chat, not be swallowed.
func TestAppRunnerRunTaskPropagatesAnAgentFailure(t *testing.T) {
	r := NewAppRunner(&bytes.Buffer{}, &bytes.Buffer{}, config.Default(), &llm.Client{}, &sandbox.Sandbox{}, logx.Global())
	r.newAgent = func(config.Config, *logx.Logger, *llm.Client, *sandbox.Sandbox, taskpkg.Source) AgentRunner {
		return &resultAgent{err: errors.New("the agent could not start")}
	}
	_, err := r.RunTask(context.Background(), "a task", func(string, ...any) {})
	if err == nil || !strings.Contains(err.Error(), "could not start") {
		t.Errorf("err = %v, want the agent's failure", err)
	}
}

// TestAppRunnerRunTaskRejectsAnEmptyTask: the text source refuses an empty task,
// and the runner has to surface that instead of running the agent on nothing.
func TestAppRunnerRunTaskRejectsAnEmptyTask(t *testing.T) {
	r := NewAppRunner(&bytes.Buffer{}, &bytes.Buffer{}, config.Default(), &llm.Client{}, &sandbox.Sandbox{}, logx.Global())
	r.newAgent = func(config.Config, *logx.Logger, *llm.Client, *sandbox.Sandbox, taskpkg.Source) AgentRunner {
		t.Error("the agent must not be built for an empty task")
		return &resultAgent{}
	}
	if _, err := r.RunTask(context.Background(), "   ", func(string, ...any) {}); err == nil {
		t.Error("an empty task must be rejected")
	}
}

// TestAppRunnerRunTaskForwardsProgress: the phase lines the agent emits are what
// the chat shows while a task runs, so they must reach the caller's callback.
func TestAppRunnerRunTaskForwardsProgress(t *testing.T) {
	r := NewAppRunner(&bytes.Buffer{}, &bytes.Buffer{}, config.Default(), &llm.Client{}, &sandbox.Sandbox{}, logx.Global())
	r.newAgent = func(config.Config, *logx.Logger, *llm.Client, *sandbox.Sandbox, taskpkg.Source) AgentRunner {
		return &resultAgent{tr: agent.TaskResult{Pass: true, Reason: "ok"}, phases: []string{"analysing…", "running: ls"}}
	}
	var seen []string
	if _, err := r.RunTask(context.Background(), "a task", func(format string, args ...any) {
		seen = append(seen, format)
	}); err != nil {
		t.Fatalf("RunTask: %v", err)
	}
	if len(seen) != 2 {
		t.Errorf("the progress lines must be forwarded, got %v", seen)
	}
}

// TestSummariseIsTheOnlyMapping: the sentence the chat shows is produced in one
// place, so a change to the wording cannot drift between callers.
func TestSummariseIsTheOnlyMapping(t *testing.T) {
	if got := summarise(agent.TaskResult{Pass: true, Summary: "s", Reason: "r"}); got != "s" {
		t.Errorf("the summary must win over the reason, got %q", got)
	}
	if got := summarise(agent.TaskResult{Pass: false, Reason: "why"}); got != "failed: why" {
		t.Errorf("got %q", got)
	}
}

// resultAgent is an AgentRunner that reports a fixed outcome, so the runner's
// mapping can be tested without a real agent.
type resultAgent struct {
	tr        agent.TaskResult
	err       error
	silent    bool
	phases    []string
	observer  func(agent.TaskResult)
	progress  func(string, ...any)
	runCalled bool
}

func (a *resultAgent) Run(context.Context) error {
	a.runCalled = true
	if a.silent {
		return a.err
	}
	for _, p := range a.phases {
		if a.progress != nil {
			a.progress("%s", p)
		}
	}
	if a.observer != nil {
		a.observer(a.tr)
	}
	return a.err
}

func (a *resultAgent) RunCommand(context.Context, string) (string, int, error) { return "", 0, nil }

func (a *resultAgent) SetObserver(fn func(agent.TaskResult)) { a.observer = fn }

func (a *resultAgent) SetProgress(fn func(format string, args ...any)) { a.progress = fn }

// TestRealAgentSatisfiesTheObserverInterface: the runner installs its hooks
// through an interface, so the production agent must keep satisfying it. A change
// that removes either method would otherwise silently stop the chat from ever
// showing a result.
func TestRealAgentSatisfiesTheObserverInterface(t *testing.T) {
	var _ taskObserver = (*agent.Agent)(nil)
}
