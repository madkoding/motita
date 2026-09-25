// Package review implements the background self-improvement fork: after a turn
// completes, a separate planner replays the conversation transcript with a
// review-specific prompt and patches the skill library.
//
// The fork runs in its own goroutine, with its own session and optionally a
// cheaper LLM engine. It has NO filesystem access and NO command execution —
// only the four skill tools (list_skills, search_skills, read_skill,
// save_skill). Skills it creates are marked created_by="agent", making them
// eligible for curator maintenance.
//
// The fork is best-effort: if the process exits, the review is lost. Partial
// writes are safe because save_skill is atomic.
package review

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/madkoding/motita/internal/config"
	"github.com/madkoding/motita/internal/llm"
	"github.com/madkoding/motita/internal/logx"
	"github.com/madkoding/motita/internal/procedures"
)

// SkillRunner is the interface the review fork uses to run a planner with
// review-only tools. It is satisfied by *plan.Planner, but lives here so this
// package does not import plan (which would create a cycle: plan imports
// review for the Review type).
type SkillRunner interface {
	// Run executes the planner with the given input and returns the final
	// answer.
	Run(ctx context.Context, input string) (string, error)
}

// RunnerBuilder builds a SkillRunner for the review fork. The caller (app.go)
// supplies this so review does not need to import plan.
type RunnerBuilder func(engine *llm.Client, procs *procedures.Store, soul string, maxLoops int) SkillRunner

// Review is the post-turn background fork.
type Review struct {
	cfg         config.Review
	engine      *llm.Client
	procs       *procedures.Store
	log         *logx.Logger
	buildRunner RunnerBuilder
	mu          sync.Mutex
	running     bool
	cancel      context.CancelFunc
}

// New creates a background review fork. The engine may be a separate, cheaper
// client; if nil, the caller must pass the main engine. buildRunner is the
// factory that creates a SkillRunner (plan.Planner) for the fork.
func New(cfg config.Review, engine *llm.Client, procs *procedures.Store, log *logx.Logger, buildRunner RunnerBuilder) *Review {
	return &Review{cfg: cfg, engine: engine, procs: procs, log: log, buildRunner: buildRunner}
}

// MaybeRun triggers a review if enough tool-iterations have elapsed since the
// last save_skill. Non-blocking: the review runs in a goroutine and the caller
// returns immediately. A new turn cancels any in-flight review.
func (r *Review) MaybeRun(transcript []llm.Message, itersSinceSkill int) {
	if !r.cfg.Enabled {
		return
	}
	if itersSinceSkill < r.cfg.Interval {
		return
	}

	// Cancel any in-flight review: a new turn takes priority.
	r.mu.Lock()
	if r.cancel != nil {
		r.cancel()
	}
	ctx, cancel := context.WithTimeout(context.Background(), r.cfg.Timeout)
	r.cancel = cancel
	r.running = true
	r.mu.Unlock()

	// Copy the transcript so the goroutine owns its own snapshot.
	msgs := make([]llm.Message, len(transcript))
	copy(msgs, transcript)

	go r.run(ctx, msgs)
}

func (r *Review) run(ctx context.Context, transcript []llm.Message) {
	defer func() {
		r.mu.Lock()
		r.running = false
		r.mu.Unlock()
	}()

	runner := r.buildRunner(r.engine, r.procs, reviewSystemPrompt, r.cfg.MaxIterations)

	userMsg := buildReviewInput(transcript)
	result, err := runner.Run(ctx, userMsg)
	if err != nil {
		if r.log != nil {
			r.log.Warn("background review failed", "error", err)
		}
		return
	}
	if r.log != nil && !strings.Contains(strings.ToLower(result), "nothing to save") {
		preview := result
		if len(preview) > 200 {
			preview = preview[:200] + "…"
		}
		r.log.Info("background review complete", "result", preview)
	}

	// Persist usage telemetry.
	if r.procs.Usage != nil {
		if err := r.procs.Usage.Save(); err != nil {
			r.log.Warn("usage ledger save failed after review", "error", err)
		}
	}
}

func buildReviewInput(transcript []llm.Message) string {
	var b strings.Builder
	b.WriteString("## CONVERSATION TRANSCRIPT\n\n")
	b.WriteString("The following is the conversation that just completed. ")
	b.WriteString("Review it for skill updates.\n\n")
	for _, m := range transcript {
		role := m.Role
		if m.ToolCallID != "" {
			role = "tool"
		}
		content := m.Content
		if len(content) > 2000 {
			content = content[:2000] + "…[truncated]"
		}
		fmt.Fprintf(&b, "### %s\n%s\n\n", role, content)
		if len(m.ToolCalls) > 0 {
			for _, tc := range m.ToolCalls {
				fmt.Fprintf(&b, "[tool call: %s(%s)]\n", tc.Function.Name, tc.Function.Arguments)
			}
		}
	}
	b.WriteString(reviewInstruction)
	return b.String()
}

// IsRunning reports whether a review is in flight (for graceful shutdown).
func (r *Review) IsRunning() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.running
}

// Wait blocks until any in-flight review completes or the timeout elapses.
func (r *Review) Wait(timeout time.Duration) {
	deadline := time.Now().Add(timeout)
	for {
		if !r.IsRunning() || time.Now().After(deadline) {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// reviewSystemPrompt is the soul of the review fork. It replaces the normal
// operational prompt: no file tools, no commands, just the skill library.
const reviewSystemPrompt = `You are the skill review pass of an autonomous agent.
Your ONLY job is to update the skill library based on the conversation that just
completed. You have four tools: list_skills, search_skills, read_skill, and
save_skill. You have NO filesystem access and NO command execution.

## When to act

Act if ANY of these signals fired in the conversation:
- The user corrected the agent's style, tone, format, verbosity, or workflow.
- A non-trivial technique, fix, workaround, or debugging path emerged.
- A skill that was loaded turned out wrong, missing a step, or outdated.
- The agent discovered a procedure that would save time next time.

## Preference order (pick the earliest that fits)

1. PATCH a skill that was loaded or read in the conversation. Call read_skill
   first to get the current content, modify it, then save_skill with the same
   name (it replaces the file).
2. SEARCH for an existing skill that covers the territory and patch it.
3. CREATE a new class-level skill only when nothing exists.

## Skill format

Each skill is one markdown file. The name is lowercase, hyphenated, at the
CLASS level — never a PR number, error string, date, or "fix-X" session artifact.

Structure:
  # Skill Title
  One line: when to use this skill.
  ## Procedure
  1. Step one (concrete command or tool call)
  2. Step two
  ## Pitfalls
  - Rule + one clause of WHY (the mechanism), imperative.

## What NOT to capture

- Environment-dependent failures (missing binaries, unconfigured credentials).
- Negative claims about tools ("X is broken").
- Session-specific transient errors that resolved before the conversation ended.
- One-off task narratives.
- Unresolved failures: if nothing worked, do NOT write the dead ends as a
  "reliable workflow".

## Read-before-write (ENFORCED)

Before patching an existing skill, call read_skill on it FIRST. A save_skill
without a prior read_skill for an existing skill may produce a stale overwrite.

If nothing is worth saving, say "Nothing to save." and stop.`

const reviewInstruction = `
## YOUR TASK

Review the transcript above. If a skill should be created or patched, do it now
using your tools. If nothing warrants a skill update, say "Nothing to save." and
stop. Act on real signal — a pass that does nothing when there is signal is a
missed learning opportunity, but "Nothing to save." is valid when the session
was smooth.`
