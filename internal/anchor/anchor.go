// Package anchor implements Layer A: the deterministic validator.
//
// It does not reason and it does not call any LLM: it runs real checks (commands,
// invariants, expected output) over the agent's result and returns PASS/FAIL with
// structured records. If this layer cannot run, the result is FAIL, never an
// optimistic PASS.
package anchor

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/madkoding/motita/internal/config"
	"github.com/madkoding/motita/internal/execx"
	"github.com/madkoding/motita/internal/logx"
	"github.com/madkoding/motita/internal/sandbox"
)

// Result is the anchor's verdict.
type Result struct {
	Pass       bool       `json:"pass"`
	Checks     []CheckLog `json:"checks"`
	DurationMS int64      `json:"duration_ms"`
	Reason     string     `json:"reason"`
	// PreExisting names the checks that failed but failed the same way BEFORE the run changed
	// anything (see WithBaseline). A result with Pass true and a non-empty PreExisting is a pass
	// WITH A CAVEAT, and Reason says so in words: it is never reported as plain green.
	PreExisting []string `json:"pre_existing,omitempty"`
	// NoGate marks a FAIL that is not a failed check: kind=auto looked in the directory and found
	// nothing to run. The caller decides what that means for the run; retrying cannot change it.
	NoGate bool `json:"no_gate,omitempty"`
	// Unverified marks a PASS that nothing checked (see the agent's handling of NoGate). Reason
	// says so in words: it is never reported as plain green.
	Unverified bool `json:"unverified,omitempty"`
}

// CheckLog is the structured record of one check.
type CheckLog struct {
	Name       string `json:"name"`
	Command    string `json:"command"`
	Exit       int    `json:"exit"`
	Pass       bool   `json:"pass"`
	DurationMS int64  `json:"duration_ms"`
	Output     string `json:"output,omitempty"`
	Error      string `json:"error,omitempty"`
	Truncated  bool   `json:"output_truncated,omitempty"`
	// PreExisting marks a failing check that also failed on the baseline: the code as it was
	// before the run.
	PreExisting bool `json:"pre_existing,omitempty"`
	// PassedBefore marks a failing check that PASSED on the baseline: the run broke it.
	PassedBefore bool `json:"passed_before,omitempty"`
	// Failures is what the check's WHOLE output said failed, normalised (see failureSignature). It
	// is how the baseline tells "fails the same way" from "fails, and now also for a new reason".
	Failures []string `json:"-"`
	// FailCounts are the failure counts the output printed, by line (see failureCounts): the
	// signature hides numbers, and "2 failed" must still read as more than "1 failed".
	FailCounts map[string][]int `json:"-"`
}

// Anchor validates results according to the configuration.
type Anchor struct {
	cfg     config.Anchor
	dir     string // working directory the checks run in
	sandbox *sandbox.Sandbox
	log     *logx.Logger
	// tools is the sandbox's tools directory, put on PATH of a check that runs WITHOUT the
	// sandbox. checkTimeout is the least a DETECTED check gets. See WithTools.
	tools        string
	checkTimeout time.Duration
}

// New builds an anchor. The sandbox is optional: when it is nil, the checks run
// directly on the system.
//
// Design note: with a nil sandbox the anchor runs its checks directly on the system.
// The agent loop passes its sandbox (validateClaim), so in practice the checks run
// confined like the agent's own commands; with sandbox.kind=none they run with the
// process' full privileges. Either way the code under test is code the agent wrote.
func New(cfg config.Anchor, dir string, box *sandbox.Sandbox) *Anchor {
	return &Anchor{cfg: cfg, dir: dir, sandbox: box, log: logx.Global()}
}

// WithTools gives the anchor the agent's tools directory and its check timeout.
//
// Reported from a real session: the toolchain the agent had installed was not on the anchor's
// PATH, so `make check` failed in 3 ms with make not found and the run could never pass; and the
// detected gate got the anchor's 120 s default, less than a whole test suite needs. A check run
// through the sandbox already gets the tools on PATH from the sandbox itself; one run directly
// gets them here. The timeout applies to DETECTED checks only: an explicit check keeps the
// timeout its author chose.
func (a *Anchor) WithTools(tools string, checkTimeout time.Duration) *Anchor {
	a.tools, a.checkTimeout = tools, checkTimeout
	return a
}

// Validator is the abstraction, so it can be replaced in tests.
type Validator interface {
	Validate(ctx context.Context) Result
}

// failedOutputLogged is how much of a failing check's output goes to the log: enough to tell why it
// failed without copying a whole build log into it.
const failedOutputLogged = 400

// Validate runs every check. It returns PASS only when all of them pass.
func (a *Anchor) Validate(ctx context.Context) Result {
	start := time.Now()
	res := Result{Pass: true, Checks: []CheckLog{}}

	if strings.EqualFold(a.cfg.Kind, "none") || a.cfg.Kind == "" {
		// Without validation there is no possible verification, so PASS cannot
		// be declared. A PASS with no real check would be exactly the failure
		// this architecture exists to prevent.
		res.Pass = false
		res.Reason = "anchor.kind=none: there is no deterministic validation, the result can NOT be taken as verified"
		res.DurationMS = time.Since(start).Milliseconds()
		a.log.Error("anchor disabled: the result cannot be verified", "reason", res.Reason)
		return res
	}

	checks := a.checks()
	if len(checks) == 0 {
		// Only reachable with kind=auto: it is the one kind whose checks come from
		// the directory, and a directory that declares no gate produces an empty
		// list. Letting the loop below run over nothing would leave Pass true - an
		// optimistic PASS over a project nobody checked, which is the exact failure
		// this package exists to refuse.
		res.Pass, res.NoGate = false, true
		res.Reason = "anchor.kind=auto found no gate to run in " + a.dir +
			": looked for " + declaredGateFile + " (one command per line), a Makefile with check or test, " +
			"go.mod, package.json (lint/typecheck/test), Cargo.toml and pyproject.toml; " +
			"declare the project's gate in " + declaredGateFile + " or configure anchor.kind=command"
		res.DurationMS = time.Since(start).Milliseconds()
		a.log.Error("anchor found no gate: the result cannot be verified", "dir", a.dir, "reason", res.Reason)
		return res
	}
	for _, c := range checks {
		record := a.runCheck(ctx, c)
		res.Checks = append(res.Checks, record)
		if !record.Pass {
			res.Pass = false
		}
	}

	if res.Pass {
		a.describePass(&res, checks)
	} else {
		var failed []string
		for _, c := range res.Checks {
			if !c.Pass {
				failed = append(failed, c.Name)
				a.log.Warn("anchor check failed", "check", c.Name, "exit", c.Exit, "error", c.Error,
					"output", truncate(c.Output, failedOutputLogged))
			}
		}
		res.Reason = "failed checks: " + strings.Join(failed, ", ")
	}
	res.DurationMS = time.Since(start).Milliseconds()

	fields := []any{"pass", res.Pass, "reason", res.Reason, "duration_ms", res.DurationMS}
	if res.Pass {
		a.log.Info("anchor validation", fields...)
	} else {
		a.log.Warn("anchor validation", fields...)
	}
	return res
}

// describePass words a PASS. `go test ./...` over no test files exits 0: green, and nothing was
// run, so that PASS is marked unverified.
func (a *Anchor) describePass(res *Result, checks []config.Check) {
	res.Reason = fmt.Sprintf("%d check(s) passed", len(res.Checks))
	if a.vacuous(checks) {
		res.Unverified = true
		res.Reason += ", but the tree declares no tests, so nothing was actually verified"
	}
}

// vacuous says whether a PASS from kind=auto proves nothing: every check is a bare toolchain test
// command (the convention, not something the project wrote) and the tree has no tests for it to run.
func (a *Anchor) vacuous(checks []config.Check) bool {
	if !strings.EqualFold(a.cfg.Kind, "auto") {
		return false
	}
	for _, c := range checks {
		if c.Name != "go test" && c.Name != "cargo test" && c.Name != "pytest" {
			return false
		}
	}
	return a.TestCount() == 0
}

// Planned returns the checks Validate would run right now, without running them.
//
// It exists so the model can be TOLD what it will be measured against. With kind=auto the gate is
// read from the project's files, and a prompt that could not name it said "no validation is
// configured" about a project whose gate is `npm run lint`, `npm run typecheck` and `npm test`:
// the run then found out what decided PASS by failing it.
func (a *Anchor) Planned() []config.Check { return a.checks() }

// Failing returns the names of the checks that did not pass, in order.
func (r Result) Failing() []string {
	var names []string
	for _, c := range r.Checks {
		if !c.Pass {
			names = append(names, c.Name)
		}
	}
	return names
}

// Rerun runs only the named checks and reports, for each one that ran, its record.
//
// It is how the baseline is measured: the anchor is built over a clean checkout of the commit the
// run started from, and only the checks that failed on the claim are run there. Running the whole
// gate again would double the cost of every red claim for checks that are already green.
func (a *Anchor) Rerun(ctx context.Context, names []string) map[string]CheckLog {
	want := map[string]bool{}
	for _, n := range names {
		want[n] = true
	}
	ran := map[string]CheckLog{}
	for _, c := range a.checks() {
		name := c.Name
		if name == "" {
			name = "check"
		}
		if want[name] {
			ran[name] = a.runCheck(ctx, c)
		}
	}
	return ran
}

// failureLine is a line of a check's output that reports a failure: a test runner's FAIL, a
// compiler's or linter's error, a panic.
var failureLine = regexp.MustCompile(`(?i)\b(fail(s|ed|ure|ures)?|errors?|panic(s|ked)?)\b`)

// digitRun is normalised away in a failure line: durations, line numbers, counts, temporary names.
var digitRun = regexp.MustCompile(`[0-9]+`)

// failureSignature is the set of failure lines in a check's output, with the directory the check
// ran in and every number normalised, so the same failure reads the same on the baseline's
// checkout and in the workspace. It is a heuristic and errs one way only: a line that differs
// makes the failure "new", which keeps the result failed. The numbers it hides that count
// failures are kept apart, in failureCounts.
func failureSignature(output, dir string) []string {
	seen := map[string]bool{}
	var out []string
	for _, line := range strings.Split(output, "\n") {
		if !failureLine.MatchString(line) {
			continue
		}
		line = normalise(line, dir)
		if !seen[line] {
			seen[line] = true
			out = append(out, line)
		}
	}
	return out
}

// failCount is a runner's count of failures: Jest's "Tests: 2 failed", mocha's "3 failing", a
// linter's "4 errors".
var failCount = regexp.MustCompile(`(?i)\b([0-9]+)\s+(failed|failing|errors?)\b`)

// normalise is a failure line as the baseline's checkout and the workspace both read it: the
// directory the check ran in and every number replaced.
func normalise(line, dir string) string {
	if dir != "" {
		line = strings.ReplaceAll(line, dir, "<dir>")
	}
	return strings.TrimSpace(digitRun.ReplaceAllString(line, "#"))
}

// failureCounts are the failure counts an output printed, each under its line up to the count
// (normalised), in the order it printed them: "Tests: 2 failed" is compared with the baseline's
// "Tests: N failed", never with another tool's "N errors" that happened to print first.
func failureCounts(output, dir string) map[string][]int {
	out := map[string][]int{}
	for _, line := range strings.Split(output, "\n") {
		for _, m := range failCount.FindAllStringSubmatchIndex(line, -1) {
			n, _ := strconv.Atoi(line[m[2]:m[3]]) // out of range is the largest int: still "more"
			key := normalise(line[:m[1]], dir)
			out[key] = append(out[key], n)
		}
	}
	return out
}

// sameFailure reports whether a check that fails now fails the way it failed on the baseline:
// the same exit code, and nothing in its failure lines that the baseline did not also report.
// Fewer failures than before is the same failure (the run fixed some of them); a failure count
// that grew is not, even when the lines read the same once their numbers are normalised.
//
// An output cut at the size limit, or a check that hung, was killed or never started (exit < 0),
// proves nothing about the failures it did not show, so it is never the same failure.
func sameFailure(before, now CheckLog) bool {
	if before.Exit != now.Exit || before.Truncated || now.Truncated || now.Exit < 0 {
		return false
	}
	for key, counts := range now.FailCounts {
		for i, n := range counts {
			if i >= len(before.FailCounts[key]) || n > before.FailCounts[key][i] {
				return false
			}
		}
	}
	known := map[string]bool{}
	for _, l := range before.Failures {
		known[l] = true
	}
	for _, l := range now.Failures {
		if !known[l] {
			return false
		}
	}
	return true
}

// WithBaseline judges a failed result against how the same checks did on the baseline (before:
// check name -> its record there).
//
// A failing check that also failed before the run, IN THE SAME WAY (see sameFailure), is marked
// PreExisting; when that is ALL of them, the result passes WITH A CAVEAT, and the reason names
// those checks so neither the user nor the summary can read it as a clean pass. Comparing the
// name alone was not enough: a gate that was already red accepted any later failure of it, a test
// the run broke included. A failing check that passed before, that fails differently now, or that
// the baseline could not run, keeps the result failed: the first is a breakage the run caused
// (marked PassedBefore, so the rejection can say so), the second has a new failure in it, and the
// third is unproven either way, and unproven is not a pass.
func WithBaseline(res Result, before map[string]CheckLog) Result {
	if res.Pass {
		return res
	}
	allOld := true
	var old, broken, changed []string
	for i := range res.Checks {
		c := &res.Checks[i]
		if c.Pass {
			continue
		}
		there, ran := before[c.Name]
		switch {
		case ran && !there.Pass && sameFailure(there, *c):
			c.PreExisting = true
			old = append(old, c.Name)
		case ran && !there.Pass:
			changed = append(changed, c.Name)
			allOld = false
		case ran:
			c.PassedBefore = true
			broken = append(broken, c.Name)
			allOld = false
		default:
			allOld = false
		}
	}
	res.PreExisting = old
	if !allOld || len(old) == 0 {
		res.Reason += baselineNotes(old, changed, broken)
		return res
	}
	res.Pass = true
	res.Reason = fmt.Sprintf("%d check(s) passed; ALREADY FAILING BEFORE THIS CHANGE (they fail the same way "+
		"on the code the run started from, so they are not this change's doing, and they are not "+
		"verified either): %s", len(res.Checks)-len(old), strings.Join(old, ", "))
	return res
}

// baselineNotes is what a failed result adds to its reason about the baseline: the checks that
// were already failing the same way, the ones failing differently now, and the ones that passed.
func baselineNotes(old, changed, broken []string) string {
	notes := ""
	if len(old) > 0 {
		notes += "; already failing before this change: " + strings.Join(old, ", ")
	}
	if len(changed) > 0 {
		notes += "; failing before this change, but differently now: " + strings.Join(changed, ", ")
	}
	if len(broken) > 0 {
		notes += "; passed before this change: " + strings.Join(broken, ", ")
	}
	return notes
}

// checks normalises the configuration into a homogeneous list.
//
// With kind=auto the list comes from the PROJECT rather than from the file: the
// anchor reads the gate the directory declares (see detect.go). That is what makes
// one configuration work across projects, and why the detection is asked here - the
// only place that builds a check list - rather than at the call site.
func (a *Anchor) checks() []config.Check {
	if strings.EqualFold(a.cfg.Kind, "auto") {
		return a.detectChecks()
	}
	list := make([]config.Check, 0, 1+len(a.cfg.Checks))
	if a.cfg.Command != "" {
		list = append(list, config.Check{
			Name:         "main",
			Command:      a.cfg.Command,
			Args:         a.cfg.Args,
			Timeout:      a.cfg.Timeout,
			ExpectExit:   a.cfg.ExpectExit,
			ExpectOutput: a.cfg.ExpectOutput,
		})
	}
	list = append(list, a.cfg.Checks...)
	return list
}

// runCheck runs one check and evaluates its exit code and expected output.
func (a *Anchor) runCheck(ctx context.Context, c config.Check) CheckLog {
	name := c.Name
	if name == "" {
		name = "check"
	}
	timeout := c.Timeout
	if timeout <= 0 {
		timeout = 120 * time.Second
	}

	line := strings.TrimSpace(c.Command + " " + strings.Join(c.Args, " "))
	record := CheckLog{Name: name, Command: line}

	start := time.Now()
	var (
		output    string
		exit      int
		runErr    error
		truncated bool
	)

	request := execx.Request{
		Command: c.Command,
		Args:    c.Args,
		Dir:     a.dir,
		Timeout: timeout,
		// The tools directory is written by the agent: its make/go/npm must not shadow the system's.
		SystemToolsFirst: true,
	}
	if a.sandbox != nil {
		output, truncated, exit, runErr = a.sandbox.Run(ctx, request)
	} else {
		if a.tools != "" {
			request.Environment = sandbox.ToolEnvironmentLast(a.tools, nil)
			request.Command = sandbox.LookToolLast(a.tools, request.Command)
		}
		output, truncated, exit, runErr = runDirect(ctx, request)
	}

	record.DurationMS = time.Since(start).Milliseconds()
	record.Exit = exit
	record.Output = truncate(output, 4000)
	record.Failures = failureSignature(output, a.dir)
	record.FailCounts = failureCounts(output, a.dir)
	record.Truncated = truncated

	if runErr != nil {
		record.Pass = false
		record.Error = runErr.Error()
		return record
	}

	want := c.ExpectExit
	if exit != want {
		record.Pass = false
		if record.Error == "" {
			record.Error = fmt.Sprintf("expected exit code %d and got %d", want, exit)
		}
		return record
	}

	if c.ExpectOutput != "" {
		re, err := regexp.Compile(c.ExpectOutput)
		if err != nil {
			record.Pass = false
			record.Error = "expect_output is not a valid regular expression: " + err.Error()
			return record
		}
		if !re.MatchString(output) {
			record.Pass = false
			record.Error = fmt.Sprintf("the output does not match %q", c.ExpectOutput)
			return record
		}
	}

	record.Pass = true
	return record
}

// runDirect is the path without a sandbox (the anchor's checks when the sandbox
// is disabled or does not apply).
func runDirect(ctx context.Context, r execx.Request) (string, bool, int, error) {
	return execx.Run(ctx, r)
}

// truncate limits the size of the output kept in the record.
func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	// Back off to a rune boundary: cutting inside a multi-byte character leaves invalid UTF-8.
	for max > 0 && !utf8.RuneStart(s[max]) {
		max--
	}
	return s[:max] + "..."
}

// JSON serialises the result for the structured record. The result only holds
// plain fields (booleans, numbers, strings and a slice of the same), so the
// marshalling cannot fail.
func (r Result) JSON() string {
	data, _ := json.Marshal(r)
	return string(data)
}
