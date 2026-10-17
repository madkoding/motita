package anchor

import (
	"context"
	"fmt"
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
	failed := failedChecks

	// A pass is left alone.
	if got := WithBaseline(Result{Pass: true, Reason: "ok"}, nil); !got.Pass || got.Reason != "ok" {
		t.Errorf("a pass must be untouched: %+v", got)
	}

	checkEveryFailureIsOld(t)

	// One old, one the run broke: still failed, and both are named.
	got := WithBaseline(failed("test", "lint"), map[string]CheckLog{"test": {}, "lint": {Pass: true}})
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

// failedChecks is a failed result: one green check, then a failed one for each name.
func failedChecks(names ...string) Result {
	r := Result{Reason: "failed checks: " + strings.Join(names, ", ")}
	r.Checks = append(r.Checks, CheckLog{Name: "green", Pass: true})
	for _, n := range names {
		r.Checks = append(r.Checks, CheckLog{Name: n})
	}
	return r
}

// checkEveryFailureIsOld: every failure is old, so the result is a pass WITH the caveat in words.
func checkEveryFailureIsOld(t *testing.T) {
	got := WithBaseline(failedChecks("test", "lint"), map[string]CheckLog{"test": {}, "lint": {}})
	wrong := !got.Pass || len(got.PreExisting) != 2 ||
		!strings.Contains(got.Reason, "ALREADY FAILING BEFORE THIS CHANGE")
	if wrong || !strings.Contains(got.Reason, "test, lint") || !strings.HasPrefix(got.Reason, "1 check(s) passed") {
		t.Errorf("all pre-existing: %+v", got)
	}
	if !got.Checks[1].PreExisting {
		t.Error("the check must be marked pre-existing")
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
		if got.Pass || got.Checks[0].PreExisting ||
			!strings.HasSuffix(got.Reason, "; failing before this change, but differently now: test") {
			t.Errorf("a different failure must keep the result failed: %+v", got)
		}
	}
}

// TestAnUnprovenOldFailureIsNotTheSameFailure: an output cut at the size limit hides what came
// after the cut, and a check that hung, was killed or never started (exit < 0) did not run its
// tests to the end. Neither proves the failure is the old one, on either side.
func TestAnUnprovenOldFailureIsNotTheSameFailure(t *testing.T) {
	old := CheckLog{Name: "test", Exit: 1, Failures: []string{"--- FAIL: TestOld"}}
	cut := old
	cut.Truncated = true
	hung := CheckLog{Name: "test", Exit: -1, Error: "the command exceeded the limit"}
	for _, c := range []struct{ before, now CheckLog }{
		{old, cut}, {cut, old}, {cut, cut}, {hung, hung},
	} {
		got := WithBaseline(Result{Checks: []CheckLog{c.now}}, map[string]CheckLog{"test": c.before})
		if got.Pass || got.Checks[0].PreExisting {
			t.Errorf("before %+v, now %+v: an unproven failure must keep the result failed: %+v", c.before, c.now, got)
		}
	}
	// Measured end to end: the failure the run added is past the cut, and both sides read the same.
	a := New(config.Anchor{}, t.TempDir(), nil)
	script := "echo '--- FAIL: TestOld'; head -c 300000 /dev/zero | tr '\\0' x; echo"
	run := func(tail string) CheckLog {
		return a.runCheck(context.Background(), config.Check{Name: "test", Command: "sh",
			Args: []string{"-c", script + tail + "; exit 1"}, ExpectExit: 0, Timeout: 10 * time.Second})
	}
	before, now := run(""), run("; echo '--- FAIL: TestNewlyBroken'")
	if !now.Truncated || strings.Join(before.Failures, "|") != strings.Join(now.Failures, "|") {
		t.Fatalf("the setup must hide the new failure past the cut: %+v / %+v", before.Failures, now.Failures)
	}
	if got := WithBaseline(Result{Checks: []CheckLog{now}}, map[string]CheckLog{"test": before}); got.Pass {
		t.Errorf("a truncated failure must not pass as pre-existing: %+v", got.Reason)
	}
}

// TestMoreFailuresInTheSameFileAreNew: Jest names a failing test on a line no failure pattern
// matches, so only its summary counts tell one broken test from two. A count that grew is a new
// failure; a count that fell is the run fixing some of them.
func TestMoreFailuresInTheSameFileAreNew(t *testing.T) {
	jest := func(failed int, extra string) CheckLog {
		out := fmt.Sprintf("FAIL src/a.test.js\n  ● sum › adds\n%s\nTest Suites: 1 failed, 1 total\n"+
			"Tests:       %d failed, %d passed, 10 total\n", extra, failed, 10-failed)
		return CheckLog{Name: "npm test", Exit: 1, Failures: failureSignature(out, ""), FailCounts: failureCounts(out, "")}
	}
	before := map[string]CheckLog{"npm test": jest(2, "  ● mul › multiplies")}
	if got := WithBaseline(Result{Checks: []CheckLog{jest(3, "  ● div › divides")}}, before); got.Pass {
		t.Errorf("a third failing test must keep the result failed: %+v", got.Reason)
	}
	if got := WithBaseline(Result{Checks: []CheckLog{jest(1, "")}}, before); !got.Pass {
		t.Errorf("fewer failures is the same failure: %+v", got.Reason)
	}
	if got := WithBaseline(Result{Checks: []CheckLog{jest(2, "")}}, before); !got.Pass {
		t.Errorf("the same counts are the same failure: %+v", got.Reason)
	}
	// A count the baseline did not print at all is a new one (mocha's "N failing").
	now := jest(2, "")
	now.FailCounts["# failing"] = []int{1}
	if got := WithBaseline(Result{Checks: []CheckLog{now}}, before); got.Pass {
		t.Errorf("an extra count must keep the result failed: %+v", got.Reason)
	}
	// A count is compared with the same line's count only: the run fixed the 10 type errors and
	// broke a second mocha test, and "2 failing" is not "fewer than 10 errors".
	mixed := func(out string) CheckLog {
		return CheckLog{Name: "check", Exit: 1, Failures: failureSignature(out, ""), FailCounts: failureCounts(out, "")}
	}
	assertion := "     AssertionError: expected 3 to equal 4\n"
	was := mixed("Found 10 errors.\n  1 failing\n" + assertion)
	if got := WithBaseline(Result{Checks: []CheckLog{mixed("  2 failing\n" + assertion + assertion)}},
		map[string]CheckLog{"check": was}); got.Pass {
		t.Errorf("another tool's count must not hide a grown one: %+v", got.Reason)
	}
	got := failureCounts("  3 failing\n1 error, 2 errors\nTests: 0 failed\n  3 failing\n/w/a.go: 12 errors\n", "/w")
	if fmt.Sprint(got) != "map[# error:[1] # error, # errors:[2] # failing:[3 3] <dir>/a.go: # errors:[12] Tests: # failed:[0]]" {
		t.Errorf("counts = %v", got)
	}
	// Measured end to end: the record of a check carries its counts.
	rec := New(config.Anchor{}, t.TempDir(), nil).runCheck(context.Background(),
		config.Check{Name: "t", Command: "sh", Args: []string{"-c", "echo 'Tests: 4 failed'; exit 1"}, Timeout: 5 * time.Second})
	if fmt.Sprint(rec.FailCounts) != "map[Tests: # failed:[4]]" {
		t.Errorf("record = %+v", rec)
	}
}

// TestFailureSignature: the failure lines of an output, with the run's directory and the numbers
// normalised so the baseline's checkout and the workspace read the same.
func TestFailureSignature(t *testing.T) {
	out := "ok  \tpkg/a\t0.10s\n--- FAIL: TestX (0.31s)\n/work/tree/x.go:12: error: boom\n--- FAIL: TestX (0.02s)\n--- " +
		"PASS: TestErrorHandling\n"
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
