package tui

// The background self-improvement fork, and the counter that decides when it fires. The fork is
// a per-TURN collaborator — a planner is built fresh for every turn — so the only thing that
// lives long enough to hand it over is the runner. Keeping it there is what turns "the agent can
// write a skill by hand" into "the agent reviews its own conversation", and the symptom of
// getting it wrong is silent: skills saved by explicit request, and nothing ever reviewing
// anything. That is exactly the state the web interface was in.

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/madkoding/motita/internal/agent"
	"github.com/madkoding/motita/internal/config"
	"github.com/madkoding/motita/internal/llm"
	"github.com/madkoding/motita/internal/logx"
	"github.com/madkoding/motita/internal/procedures"
	"github.com/madkoding/motita/internal/review"
	"github.com/madkoding/motita/internal/sandbox"
	"github.com/madkoding/motita/internal/task"
)

// quietRunner builds a runner whose model answers "ready" and whose agent never acts. A turn
// against it runs the whole planner path without a network or a real decision being made.
func quietRunner(t *testing.T) *AppRunner {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `{"choices":[{"message":{"content":"ready"}}]}`)
	}))
	t.Cleanup(srv.Close)

	cfg := config.Default()
	cfg.LLM.Provider = "openai"
	cfg.LLM.APIKey = "k"
	cfg.LLM.BaseURL = srv.URL
	cfg.LLM.MaxAttempts = 1
	cfg.Sandbox.Kind = "none"
	cfg.Skills.Dir = t.TempDir()

	r := NewAppRunner(&bytes.Buffer{}, &bytes.Buffer{}, cfg, nil, nil, logx.Global())
	engine, err := llm.New(cfg.LLM, r.Log)
	if err != nil {
		t.Fatalf("llm.New: %v", err)
	}
	r.Engine = engine
	r.newAgent = func(config.Config, *logx.Logger, *llm.Client, *sandbox.Sandbox, task.Source, bool) AgentRunner {
		return &resultAgent{tr: agent.TaskResult{Pass: true, Reason: "1 check passed"}}
	}
	return r
}

// TestTheReviewForkFiresThroughTheRunnersTurn: the fork is installed by the RUNNER into each
// turn's planner, and the proof is the effect rather than the field: a review whose builder
// records the call fires after RunPlan, and one the runner was never given does not.
//
// The builder is the seam this test uses instead of adding one to internal/review: the fork
// calls it to construct the planner for its own pass, so a call to it means the whole chain
// (runner -> turn -> MaybeRun -> fork) was wired. Without SetReview being read by RunPlan the
// builder is never called, which is the silent failure this asserts against.
func TestTheReviewForkFiresThroughTheRunnersTurn(t *testing.T) {
	r := quietRunner(t)

	// A fork the runner was never given: nothing may fire.
	quiet := make(chan string, 1)
	fork := review.New(config.Review{Enabled: true, Interval: 0, Timeout: 5 * time.Second, MaxIterations: 1},
		r.Engine, r.procedures(), r.Log,
		func(*llm.Client, *procedures.Store, string, int) review.SkillRunner {
			return &recordedRunner{seen: quiet}
		})
	if _, err := r.RunPlan(context.Background(), "a prompt", func(string, ...any) {}); err != nil {
		t.Fatalf("RunPlan: %v", err)
	}
	select {
	case got := <-quiet:
		t.Fatalf("a fork the runner never saw fired: %q", got)
	case <-time.After(150 * time.Millisecond):
	}

	// The same fork, now installed. The turn has to hand it to the planner, because the
	// planner is what decides to call MaybeRun.
	r.SetReview(fork)
	if _, err := r.RunPlan(context.Background(), "a prompt", func(string, ...any) {}); err != nil {
		t.Fatalf("RunPlan: %v", err)
	}
	select {
	case <-quiet:
	case <-time.After(5 * time.Second):
		t.Fatal("the review never fired: the turn was not given the runner's fork")
	}
}

// recordedRunner stands in for the fork's own planner: the test is about whether the fork is
// reached at all, not about what a review pass would write.
type recordedRunner struct{ seen chan<- string }

func (r *recordedRunner) Run(_ context.Context, input string) (string, error) {
	r.seen <- input
	return "Nothing to save.", nil
}

// TestTheIterationCounterAccumulatesAcrossTurns: the budget the fork keys on spans TURNS, while
// the counter the planner keeps is per-run. The runner is the only thing that sees both, so it
// adds each finished turn's count to its own running total - and a turn that saved a skill
// reports zero and therefore brings the next review no closer, which is the arrangement that
// stops a turn from being reviewed twice.
func TestTheIterationCounterAccumulatesAcrossTurns(t *testing.T) {
	r := quietRunner(t)

	if got := r.countReviewIters(6); got != 6 {
		t.Errorf("after one turn of 6, iters = %d, want 6", got)
	}
	if got := r.countReviewIters(0); got != 6 {
		t.Errorf("a turn that saved a skill brought the review closer: iters = %d", got)
	}
	if got := r.countReviewIters(3); got != 9 {
		t.Errorf("iters = %d, want 9", got)
	}
	if r.reviewIters != 9 {
		t.Errorf("the runner holds %d, want the running total 9", r.reviewIters)
	}
}
