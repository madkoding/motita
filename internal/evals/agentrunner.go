package evals

import (
	"context"
	"errors"
	"os/exec"
	"strings"
	"time"

	"github.com/madkoding/motita/internal/agent"
	"github.com/madkoding/motita/internal/config"
	"github.com/madkoding/motita/internal/llm"
	"github.com/madkoding/motita/internal/logx"
	"github.com/madkoding/motita/internal/onboard"
	"github.com/madkoding/motita/internal/sandbox"
	"github.com/madkoding/motita/internal/task"
)

// caseTimeout bounds one task: a model that wanders must not hold the quota for ever.
const caseTimeout = 4 * time.Minute

// shell is the program the checks run with.
const shell = "sh"

// newBox builds the sandbox of a task, a variable so a test can make it fail.
var newBox = sandbox.New

// Supported says whether this computer can run the checks, which are shell commands.
var Supported = func() bool {
	_, err := exec.LookPath(shell)
	return err == nil
}

// AgentRunner runs each task through the real agent, with the model in Cfg, in a sandbox over the
// task's own folder. The agent gets no notes, skills, projects or history: only the task.
type AgentRunner struct {
	Cfg config.Config
	Log *logx.Logger
}

// Run implements Runner.
func (a AgentRunner) Run(ctx context.Context, c Case, dir string) (Outcome, error) {
	cfg := a.Cfg
	cfg.Anchor = config.Anchor{
		Kind: "command", Command: shell, Args: []string{"-c", c.Check},
		Timeout: 20 * time.Second, ExpectOutput: "OK",
	}
	cfg.Agent.WorkspaceDir = dir
	cfg.Agent.SubtaskDepth = 0
	engine, err := llm.New(cfg.LLM, a.Log)
	if err != nil {
		return Outcome{}, err
	}
	box, err := newBox(sandbox.Options{
		Dir: dir, Limits: sandbox.Limits{MemoryMB: 512, CPUSeconds: 30},
		Timeout: 30 * time.Second, MaxOutputKB: 64, ConfineWrites: true, Log: a.Log,
	})
	if err != nil {
		return Outcome{}, err
	}
	defer box.Close()
	source, err := task.NewText(c.Prompt, "evaluation")
	if err != nil {
		return Outcome{}, err
	}
	ag := agent.New(cfg, a.Log, engine, box, source)
	var result *agent.TaskResult
	ag.Observer = func(r agent.TaskResult) { result = &r }
	meter := &llm.Meter{}
	ctx, cancel := context.WithTimeout(llm.WithMeter(ctx, meter), caseTimeout)
	defer cancel()
	_ = ag.Run(ctx)
	if result == nil {
		return Outcome{}, errors.New("the model produced no result")
	}
	if !result.Pass && meter.Calls() == 0 {
		// Nothing was spent: the model never answered, which says nothing about the model.
		return Outcome{}, errors.New("the model did not answer: " + strings.TrimSpace(result.Reason))
	}
	return outcomeOf(*result, meter.Usage(), cfg.LLM), nil
}

// outcomeOf turns the agent's verdict and the tokens it spent into an Outcome.
func outcomeOf(r agent.TaskResult, u llm.Usage, l config.LLM) Outcome {
	in, out, known := PriceOf(l.Provider, l.Model)
	// A provider that reported no usage leaves nothing to price: zero dollars would be a wrong answer.
	known = known && u.Total() > 0
	o := Outcome{
		Pass: r.Pass, Steps: r.Attempts, Tokens: u.Total(), DurationMS: r.DurationMS,
		Reason: strings.TrimSpace(r.Reason), CostKnown: known,
	}
	if known {
		o.CostUSD = (float64(u.Input+u.CacheWrite)*in + float64(u.Output)*out + float64(u.CacheRead)*in) / 1e6
	}
	return o
}

// PriceOf is the approximate price, in USD per million input and output tokens, of a model of the
// catalogue; known is false for a model with no price (a subscription, a local model).
func PriceOf(provider, model string) (in, out float64, known bool) {
	p, ok := onboard.Lookup(provider)
	if !ok {
		return 0, 0, false
	}
	for _, m := range p.Models {
		if m.ID == model && (m.PriceIn > 0 || m.PriceOut > 0) {
			return m.PriceIn, m.PriceOut, true
		}
	}
	return 0, 0, false
}
