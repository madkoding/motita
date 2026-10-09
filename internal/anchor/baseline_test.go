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
	if len(got) != 3 || got["main"].Pass || !got["lint"].Pass || !got["check"].Pass {
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
	got := WithBaseline(failed("test", "lint"), map[string]CheckLog{"test": {}, "lint": {}})
	if !got.Pass || len(got.PreExisting) != 2 || !strings.Contains(got.Reason, "ALREADY FAILING BEFORE THIS CHANGE") ||
		!strings.Contains(got.Reason, "test, lint") || !strings.HasPrefix(got.Reason, "1 check(s) passed") {
		t.Errorf("all pre-existing: %+v", got)
	}
	if !got.Checks[1].PreExisting {
		t.Error("the check must be marked pre-existing")
	}

	// One old, one the run broke: still failed, and both are named.
	got = WithBaseline(failed("test", "lint"), map[string]CheckLog{"test": {}, "lint": {Pass: true}})
	if got.Pass || !strings.Contains(got.Reason, "already failing before this change: test") ||
		!strings.Contains(got.Reason, "passed before this change: lint") || !got.Checks[2].PassedBefore {
		t.Errorf("mixed: %+v", got)
	}

	// One old, one the baseline could not run: unproven is not a pass.
	got = WithBaseline(failed("test", "lint"), map[string]CheckLog{"test": {}})
	if got.Pass || !strings.Contains(got.Reason, "already failing before this change: test") ||
		strings.Contains(got.Reason, "passed before") {
		t.Errorf("old plus unknown: %+v", got)
	}

	// Only breakages.
	got = WithBaseline(failed("lint"), map[string]CheckLog{"lint": {Pass: true}})
	if got.Pass || !strings.HasSuffix(got.Reason, "; passed before this change: lint") {
		t.Errorf("broken: %+v", got)
	}

	// Nothing known about the baseline: the result is exactly what it was.
	got = WithBaseline(failed("lint"), nil)
	if got.Pass || got.Reason != "failed checks: lint" {
		t.Errorf("no baseline: %+v", got)
	}
}

// TestAnOldFailingCheckMustFailTheSameWay: a check that was red before the run is pre-existing only
// while it fails the way it did: the same exit code and no failure line the baseline did not
// report. Any other failure of the same check is a new one and keeps the result failed.
func TestAnOldFailingCheckMustFailTheSameWay(t *testing.T) {
	now := func(exit int, failures ...string) Result {
		return Result{Reason: "failed checks: test", Checks: []CheckLog{{Name: "test", Exit: exit, Failures: failures}}}
	}
	before := map[string]CheckLog{"test": {Exit: 1, Failures: []string{"FAIL: lint missing", "--- FAIL: TestOld"}}}

	// The same failure, or fewer of its lines: pre-existing.
	for _, r := range []Result{now(1, "FAIL: lint missing", "--- FAIL: TestOld"), now(1, "FAIL: lint missing")} {
		if got := WithBaseline(r, before); !got.Pass {
			t.Errorf("the same failure must be pre-existing: %+v", got)
		}
	}
	// A new failure line, or a different exit code: not pre-existing.
	for _, r := range []Result{now(1, "FAIL: lint missing", "--- FAIL: TestNew"), now(2, "FAIL: lint missing")} {
		got := WithBaseline(r, before)
		if got.Pass || got.Checks[0].PreExisting || !strings.HasSuffix(got.Reason, "; failing before this change, but differently now: test") {
			t.Errorf("a different failure must keep the result failed: %+v", got)
		}
	}
}

// TestFailureSignature: the failure lines of an output, with the run's directory and the numbers
// normalised so the baseline's checkout and the workspace read the same.
func TestFailureSignature(t *testing.T) {
	out := "ok  \tpkg/a\t0.10s\n--- FAIL: TestX (0.31s)\n/work/tree/x.go:12: error: boom\n--- FAIL: TestX (0.02s)\n--- PASS: TestErrorHandling\n"
	got := failureSignature(out, "/work/tree")
	want := []string{"--- FAIL: TestX (#.#s)", "<dir>/x.go:#: error: boom"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("signature = %q, want %q", got, want)
	}
	if got := failureSignature("/w/x: error\n", ""); len(got) != 1 || got[0] != "/w/x: error" {
		t.Errorf("no directory: %q", got)
	}
	// Measured end to end: the record of a check carries its signature.
	rec := New(config.Anchor{Kind: "command", Command: "sh", Args: []string{"-c", "echo FAIL: TestY; exit 3"},
		Timeout: 5 * time.Second}, t.TempDir(), nil).Rerun(context.Background(), []string{"main"})["main"]
	if rec.Exit != 3 || len(rec.Failures) != 1 || rec.Failures[0] != "FAIL: TestY" {
		t.Errorf("record = %+v", rec)
	}
}
