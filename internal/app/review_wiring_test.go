package app

// The wiring that had no test: the curator's failure on session start, the review fork built on
// its own engine when the configuration names one, and the runner builder that fork uses.
//
// The configuration comes from planConfig, the same helper the -plan tests use: a hand-written
// one drifts from what the program accepts, and the first version of this file failed on that
// rather than on the behaviour it was written to check.

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/madkoding/motita/internal/agent"
	"github.com/madkoding/motita/internal/config"
	"github.com/madkoding/motita/internal/llm"
	"github.com/madkoding/motita/internal/logx"
	"github.com/madkoding/motita/internal/plan"
	"github.com/madkoding/motita/internal/procedures"
)

// TestACuratorPassThatFailsDoesNotStopTheSession: the curation pass tidies the library before
// the session starts, and it is housekeeping - a library that cannot be curated is still a
// library. Losing the whole session over it would trade a tidiness problem for a work stoppage.
//
// The failure comes from saveState: curator.state_file is pointed at a path that cannot be
// created, which is a real configuration mistake and the only way this pass fails on a healthy
// library. It is what makes MaybeRun return non-nil at all.
func TestACuratorPassThatFailsDoesNotStopTheSession(t *testing.T) {
	inTempDir(t, func() {
		silence(t)
		t.Setenv("MOTITA_LLM_API_KEY", "x")
		dir := t.TempDir()
		srv := planServer(t, []string{"done"})
		defer srv.Close()

		// A FILE where the state file's directory needs to be: MkdirAll cannot create it.
		blocker := filepath.Join(dir, "not-a-directory")
		if err := os.WriteFile(blocker, []byte("in the way"), 0o644); err != nil {
			t.Fatal(err)
		}
		path := planConfig(t, srv)
		withExtraConfig(t, path, fmt.Sprintf(
			"curator:\n  enabled: true\n  interval_hours: 0\n  state_file: %s\n",
			filepath.Join(blocker, "curator-state.json")))

		var errs bytes.Buffer
		code := Run(Options{
			Args:     []string{"-config", path, "-task", "something"},
			Out:      &bytes.Buffer{},
			Err:      &errs,
			RunAgent: func(context.Context, *agent.Agent) error { return nil },
		})
		if code != 0 {
			t.Fatalf("a failed curator pass must not fail the session: code = %d, errs = %q", code, errs.String())
		}
	})
}

// withExtraConfig appends YAML to a configuration the helpers already wrote, so a test can turn
// on one more section without re-deriving a whole config that the program will accept.
func withExtraConfig(t *testing.T, path, extra string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	if _, err := f.WriteString(extra); err != nil {
		t.Fatal(err)
	}
}

// TestTheReviewForkReusesTheMainEngineByDefault: the review fork runs on the conversation's
// engine unless a separate one is configured, and this is the path every real deployment takes.
// A fork that quietly built a second engine would double the cost of every session.
//
// The `cfg.Review.LLM != nil` branch above it is NOT covered here because it is not reachable
// through any configuration: the YAML decoder refuses a pointed-to struct ("expected a simple
// value and found a nested block"), there is no MOTITA_REVIEW_LLM_* overlay, and the field
// appears in neither the example config nor the docs. It is left as written rather than deleted
// so the intent stays visible; wiring it is a feature, not a test.
func TestTheReviewForkReusesTheMainEngineByDefault(t *testing.T) {
	silence(t)
	srv := planServer(t, []string{"the plan"})
	defer srv.Close()
	path := planConfig(t, srv)

	built := 0
	var out, errs bytes.Buffer
	code := Run(Options{
		Args: []string{"-config", path, "-plan", "-p", "list files"},
		Out:  &out,
		Err:  &errs,
		NewEngine: func(c config.LLM, l *logx.Logger) (*llm.Client, error) {
			built++
			return llm.New(c, l)
		},
	})

	if code != Success {
		t.Fatalf("code = %d, errs = %q", code, errs.String())
	}
	if built != 1 {
		t.Errorf("exactly one engine must be built when no review LLM is configured, built = %d", built)
	}
}

// TestTheSkillRunnerIsMarkedAsAReviewFork: buildSkillRunner is the RunnerBuilder the review fork
// and the curator's LLM pass both use, and the mark is what attribution depends on - a skill
// saved through it belongs to the agent and not to the user.
func TestTheSkillRunnerIsMarkedAsAReviewFork(t *testing.T) {
	t.Setenv("MOTITA_LLM_API_KEY", "x")
	l, err := logx.New(logx.Options{Level: logx.Error, Console: false})
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.LLM.APIKey = "x"
	engine, err := llm.New(cfg.LLM, l)
	if err != nil {
		t.Fatal(err)
	}

	cfg.Skills.Dir = t.TempDir()
	got := buildSkillRunner(engine, procedures.Open(cfg, nil), "the review soul", 4)

	p, ok := got.(*plan.Planner)
	if !ok {
		t.Fatalf("buildSkillRunner returned %T, want a *plan.Planner", got)
	}
	if !p.IsReviewFork {
		t.Error("the runner must be marked as a review fork, or a saved skill is attributed to the user")
	}
}
