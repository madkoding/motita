package app

import (
	"context"
	"fmt"
	"time"

	"github.com/madkoding/motita/internal/agent"
	"github.com/madkoding/motita/internal/config"
	"github.com/madkoding/motita/internal/llm"
	"github.com/madkoding/motita/internal/logx"
	"github.com/madkoding/motita/internal/plan"
	"github.com/madkoding/motita/internal/procedures"
	"github.com/madkoding/motita/internal/review"
	"github.com/madkoding/motita/internal/sandbox"
)

// runPlan runs the read-only plan/chat mode. It uses the reasoning engine and the
// sandbox, but it never delegates to the task/anchor flow.
func (op Options) runPlan(
	ctx context.Context, fl flags, cfg config.Config, engine *llm.Client, box *sandbox.Sandbox, log *logx.Logger,
	procs *procedures.Store,
) int {
	cfg.Agent.ReadOnly = true
	ag := agent.New(cfg, log, engine, box, nil)
	ag.SetLibrary(procs.Library)
	ag.SetReward(procs.Ledger)
	ag.SetUsage(procs.Usage)

	// Background self-improvement: the review fork runs on the conversation's engine.
	//
	// The second engine this used to build (cfg.Review.LLM, a cheaper model for the fork) was
	// UNREACHABLE, and it was removed rather than left as an uncovered branch:
	//
	//   - the YAML decoder refuses a pointed-to struct. Review.LLM is *config.LLM, so a
	//     `review: {llm: {...}}` block fails with "expected a simple value and found a nested
	//     block", which is the one shape the decoder cannot fill;
	//   - there is no MOTITA_REVIEW_LLM_* overlay, unlike the other LLM settings;
	//   - the option appears in neither configs/agent.yaml.example nor docs/.
	//
	// So no configuration could ever reach it, and the fallback below is the path every real
	// deployment takes. Restoring the feature means wiring ONE of those three - which is a change
	// with a config surface, and should land with one. The field stays in config.Review so that a
	// wired implementation has somewhere to read from.
	var reviewer *review.Review
	if cfg.Review.Enabled {
		reviewer = review.New(cfg.Review, engine, procs, log, buildSkillRunner)
	}

	planner := plan.New(engine, ag).
		WithTimeout(planDefaultTimeout(cfg)).
		WithLoops(planDefaultLoops(cfg)).
		// Without the model the session falls back to the 8192-token floor and a one-shot
		// run that reads a single file dies with "context is full".
		WithSessionPolicy(cfg.LLM.Model, cfg.LLM.Session.ContextWindow, cfg.LLM.Session.Reserve,
			cfg.LLM.Session.CompactAt, cfg.LLM.Session.KeepRecent).
		// The same library the task path uses, so a procedure written down in one mode is
		// reachable from the other and a verdict lands on one shelf rather than two.
		WithLibrary(procs.Library).
		WithReward(procs.Ledger).
		WithUsage(procs.Usage).
		WithReview(reviewer).
		WithCodeIndex(cfg.CodeIndex.Enabled, cfg.CodeIndex.Options()).
		WithTrace(func(format string, args ...any) { fmt.Fprintf(op.Err, format, args...) })

	if fl.prompt != "" {
		answer, err := planner.Run(ctx, fl.prompt)
		if err != nil {
			fmt.Fprintf(op.Err, "❌ %v\n", err)
			return RunError
		}
		fmt.Fprintln(op.Out, answer)
		return Success
	}

	// No one-shot prompt: send the user to the TUI, which has a dedicated plan mode.
	return op.runTUI(ctx, fl, cfg, engine, box, log)
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

// buildSkillRunner is the RunnerBuilder for the background review fork and the
// curator's LLM consolidation pass. It creates a *plan.Planner with only the
// skill tools (no filesystem, no command execution), marked as a review fork
// so save_skill sets created_by="agent". The planner is constructed with a nil
// CommandRunner, which disables execute_command; the skill tools do not need
// it. The soul is the review-specific prompt, not the normal operational prompt.
func buildSkillRunner(engine *llm.Client, procs *procedures.Store, soul string, maxLoops int) review.SkillRunner {
	p := plan.New(engine, nil).
		WithLoops(maxLoops).
		WithLibrary(procs.Library).
		WithReward(procs.Ledger).
		WithUsage(procs.Usage).
		WithSoul(soul)
	p.IsReviewFork = true
	return p
}
