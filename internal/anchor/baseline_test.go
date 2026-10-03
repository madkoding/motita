package anchor

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/madkoding/motita/internal/config"
)

// TestRerunRunsOnlyTheNamedChecks: the baseline re-runs the checks that failed, and no others -
// the green ones would only double the cost of a red claim.
func TestRerunRunsOnlyTheNamedChecks(t *testing.T) {
	dir := t.TempDir()
	a := New(config.Anchor{Kind: "command", Command: "false", Timeout: 5 * time.Second, Checks: []config.Check{
		{Name: "lint", Command: "true", Timeout: 5 * time.Second},
		{Name: "slow", Command: "sh", Args: []string{"-c", "touch ran"}, Timeout: 5 * time.Second},
		{Command: "true", Timeout: 5 * time.Second}, // unnamed: "check"
	}}, dir, nil)
	got := a.Rerun(context.Background(), []string{"main", "lint", "check"})
	if len(got) != 3 || got["main"] || !got["lint"] || !got["check"] {
		t.Fatalf("Rerun = %v", got)
	}
	if res := a.Validate(context.Background()); len(res.Failing()) != 1 || res.Failing()[0] != "main" {
		t.Errorf("Failing = %v", res.Failing())
	}
}

// TestWithBaseline: every way a failed result can compare with the code before the run.
func TestWithBaseline(t *testing.T) {
	failed := func(names ...string) Result {
		r := Result{Reason: "failed checks: " + strings.Join(names, ", ")}
		r.Checks = append(r.Checks, CheckLog{Name: "green", Pass: true})
		for _, n := range names {
			r.Checks = append(r.Checks, CheckLog{Name: n})
		}
		return r
	}

	// A pass is left alone.
	if got := WithBaseline(Result{Pass: true, Reason: "ok"}, nil); !got.Pass || got.Reason != "ok" {
		t.Errorf("a pass must be untouched: %+v", got)
	}

	// Every failure is old: a pass WITH the caveat in words.
	got := WithBaseline(failed("test", "lint"), map[string]bool{"test": false, "lint": false})
	if !got.Pass || len(got.PreExisting) != 2 || !strings.Contains(got.Reason, "ALREADY FAILING BEFORE THIS CHANGE") ||
		!strings.Contains(got.Reason, "test, lint") || !strings.HasPrefix(got.Reason, "1 check(s) passed") {
		t.Errorf("all pre-existing: %+v", got)
	}
	if !got.Checks[1].PreExisting {
		t.Error("the check must be marked pre-existing")
	}

	// One old, one the run broke: still failed, and both are named.
	got = WithBaseline(failed("test", "lint"), map[string]bool{"test": false, "lint": true})
	if got.Pass || !strings.Contains(got.Reason, "already failing before this change: test") ||
		!strings.Contains(got.Reason, "passed before this change: lint") || !got.Checks[2].PassedBefore {
		t.Errorf("mixed: %+v", got)
	}

	// One old, one the baseline could not run: unproven is not a pass.
	got = WithBaseline(failed("test", "lint"), map[string]bool{"test": false})
	if got.Pass || !strings.Contains(got.Reason, "already failing before this change: test") ||
		strings.Contains(got.Reason, "passed before") {
		t.Errorf("old plus unknown: %+v", got)
	}

	// Only breakages.
	got = WithBaseline(failed("lint"), map[string]bool{"lint": true})
	if got.Pass || !strings.HasSuffix(got.Reason, "; passed before this change: lint") {
		t.Errorf("broken: %+v", got)
	}

	// Nothing known about the baseline: the result is exactly what it was.
	got = WithBaseline(failed("lint"), nil)
	if got.Pass || got.Reason != "failed checks: lint" {
		t.Errorf("no baseline: %+v", got)
	}
}
