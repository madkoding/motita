// Package tui implements the interactive text-based user interface that is
// launched when starlight is run without arguments and without a configured
// task. It is deliberately built with the Go standard library only.
package tui

import (
	"context"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/madkoding/starlight/internal/agent"
	"github.com/madkoding/starlight/internal/config"
	"github.com/madkoding/starlight/internal/llm"
	"github.com/madkoding/starlight/internal/logx"
	"github.com/madkoding/starlight/internal/onboard"
	"github.com/madkoding/starlight/internal/plan"
	"github.com/madkoding/starlight/internal/sandbox"
	taskpkg "github.com/madkoding/starlight/internal/task"
)

// Runner is the callback that executes the selected mode. The TUI package uses
// this interface so tests can inject fake implementations.
type Runner interface {
	// RunPlan runs the read-only plan mode with the given user prompt.
	RunPlan(ctx context.Context, prompt string, trace func(string, ...any)) (string, error)
	// RunTask runs the agent in task mode with the given task description.
	RunTask(ctx context.Context, task string) error
	// RunConfig runs the onboarding wizard.
	RunConfig(ctx context.Context) error
}

// AgentRunner is the subset of *agent.Agent that the TUI needs.
type AgentRunner interface {
	Run(ctx context.Context) error
	RunCommand(ctx context.Context, command string) (string, int, error)
}

// agentFactory builds an agent from the current configuration. It is injectable
// so tests can avoid running a real agent.
type agentFactory func(cfg config.Config, log *logx.Logger, engine *llm.Client, box *sandbox.Sandbox, source taskpkg.Source) AgentRunner

// defaultAgentFactory uses the real agent package.
func defaultAgentFactory(cfg config.Config, log *logx.Logger, engine *llm.Client, box *sandbox.Sandbox, source taskpkg.Source) AgentRunner {
	return agent.New(cfg, log, engine, box, source)
}

// AppRunner is the production implementation that calls the real layers.
type AppRunner struct {
	Out      io.Writer
	Err      io.Writer
	Cfg      config.Config
	Engine   *llm.Client
	Box      *sandbox.Sandbox
	Log      *logx.Logger
	newAgent agentFactory
}

// NewAppRunner creates the production runner.
func NewAppRunner(out, errs io.Writer, cfg config.Config, engine *llm.Client, box *sandbox.Sandbox, log *logx.Logger) *AppRunner {
	return &AppRunner{Out: out, Err: errs, Cfg: cfg, Engine: engine, Box: box, Log: log, newAgent: defaultAgentFactory}
}

// RunPlan executes the read-only planner and writes the final answer to Out.
func (r *AppRunner) RunPlan(ctx context.Context, prompt string, trace func(string, ...any)) (string, error) {
	cfg := r.Cfg
	cfg.Agent.ReadOnly = true
	ag := r.newAgent(cfg, r.Log, r.Engine, r.Box, nil)
	planner := plan.New(r.Engine, ag).
		WithTimeout(planDefaultTimeout(r.Cfg)).
		WithLoops(planDefaultLoops(r.Cfg)).
		WithTrace(trace)
	answer, err := planner.Run(ctx, prompt)
	if err != nil {
		return "", err
	}
	fmt.Fprintln(r.Out, answer)
	return answer, nil
}

func planDefaultTimeout(cfg config.Config) time.Duration {
	if cfg.Sandbox.Timeout > 0 {
		return cfg.Sandbox.Timeout
	}
	return 120 * time.Second
}

func planDefaultLoops(cfg config.Config) int {
	if cfg.Agent.MaxRetries > 0 && cfg.Agent.MaxRetries <= 10 {
		return cfg.Agent.MaxRetries
	}
	return 5
}

// RunTask runs the agent with a single text task.
func (r *AppRunner) RunTask(ctx context.Context, task string) error {
	source, err := taskpkg.NewText(task, "tui")
	if err != nil {
		return err
	}
	ag := r.newAgent(r.Cfg, r.Log, r.Engine, r.Box, source)
	return ag.Run(ctx)
}

// RunConfig runs the first-run configuration wizard.
func (r *AppRunner) RunConfig(ctx context.Context) error {
	path := "./starlight.yaml"
	_, err := onboard.Run(os.Stdin, r.Out, path, onboard.Answers{}, time.Now())
	return err
}
