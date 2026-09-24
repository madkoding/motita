package anchor

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/madkoding/motita/internal/config"
	"github.com/madkoding/motita/internal/logx"
	"github.com/madkoding/motita/internal/sandbox"
)

func init() {
	// Silence the global logger during the tests.
	l, _ := logx.New(logx.Options{Level: logx.Error, Console: false})
	logx.Install(l)
}

// TestAnchorPassWithRealCommand checks the anchor actually runs something.
func TestAnchorPassWithRealCommand(t *testing.T) {
	dir := t.TempDir()
	cfg := config.Anchor{
		Kind:       "command",
		Command:    "sh",
		Args:       []string{"-c", "echo all-good"},
		Timeout:    10 * time.Second,
		ExpectExit: 0,
	}

	res := New(cfg, dir, nil).Validate(context.Background())
	if !res.Pass {
		t.Fatalf("expected PASS, reason: %s", res.Reason)
	}
	if len(res.Checks) != 1 || res.Checks[0].Output != "all-good\n" {
		t.Errorf("unexpected check: %+v", res.Checks)
	}
}

// TestAnchorFailsOnExit verifies a non-zero code is a FAIL and that the reason
// says so clearly.
func TestAnchorFailsOnExit(t *testing.T) {
	cfg := config.Anchor{
		Kind:       "command",
		Command:    "sh",
		Args:       []string{"-c", "echo failure && exit 3"},
		Timeout:    10 * time.Second,
		ExpectExit: 0,
	}

	res := New(cfg, t.TempDir(), nil).Validate(context.Background())
	if res.Pass {
		t.Fatal("an exit 3 cannot give PASS")
	}
	if res.Checks[0].Exit != 3 {
		t.Errorf("recorded exit = %d", res.Checks[0].Exit)
	}
	if res.Checks[0].Error == "" {
		t.Error("the check record must explain the failure")
	}
}

// TestAnchorExpectOutput covers regex validation, which is what allows demanding
// an output contract and not just an exit code.
func TestAnchorExpectOutput(t *testing.T) {
	cases := []struct {
		name     string
		output   string
		pattern  string
		expected bool
	}{
		{"matches", "REPORT_OK rows=120\n", `REPORT_OK`, true},
		{"does not match", "all wrong\n", `REPORT_OK`, false},
		{"expression with numbers", "rows=120\n", `rows=\d+`, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := config.Anchor{
				Kind:         "command",
				Command:      "sh",
				Args:         []string{"-c", "printf " + "'" + tc.output + "'"},
				Timeout:      10 * time.Second,
				ExpectExit:   0,
				ExpectOutput: tc.pattern,
			}
			res := New(cfg, t.TempDir(), nil).Validate(context.Background())
			if res.Pass != tc.expected {
				t.Errorf("Pass = %v, expected %v (reason: %s)", res.Pass, tc.expected, res.Reason)
			}
		})
	}
}

// TestAnchorMultipleChecksAllMustPass: a single failing check invalidates the
// whole result.
func TestAnchorMultipleChecksAllMustPass(t *testing.T) {
	cfg := config.Anchor{
		Kind:       "command",
		Command:    "sh",
		Args:       []string{"-c", "exit 0"},
		Timeout:    10 * time.Second,
		ExpectExit: 0,
		Checks: []config.Check{
			{Name: "two", Command: "sh", Args: []string{"-c", "exit 0"}, Timeout: 5 * time.Second, ExpectExit: 0},
			{Name: "three", Command: "sh", Args: []string{"-c", "exit 1"}, Timeout: 5 * time.Second, ExpectExit: 0},
		},
	}
	res := New(cfg, t.TempDir(), nil).Validate(context.Background())
	if res.Pass {
		t.Fatal("with a failing check there can be no PASS")
	}
	if len(res.Checks) != 3 {
		t.Errorf("expected 3 checks, got %d", len(res.Checks))
	}
	if res.Checks[1].Pass != true || res.Checks[2].Pass != false {
		t.Errorf("unexpected states: %+v", res.Checks)
	}
}

// TestAnchorKindNone: with no configuration nothing is validated, so PASS cannot
// be declared.
func TestAnchorKindNone(t *testing.T) {
	res := New(config.Anchor{Kind: "none"}, t.TempDir(), nil).Validate(context.Background())
	if res.Pass {
		t.Fatal("anchor.kind=none must never give PASS")
	}
	if res.Reason == "" {
		t.Error("it must explain that no validation was applied")
	}
}

// TestAnchorTimeout: a command that never finishes cannot hang the agent.
func TestAnchorTimeout(t *testing.T) {
	cfg := config.Anchor{
		Kind:       "command",
		Command:    "sh",
		Args:       []string{"-c", "sleep 30"},
		Timeout:    1 * time.Second,
		ExpectExit: 0,
	}
	start := time.Now()
	res := New(cfg, t.TempDir(), nil).Validate(context.Background())
	if res.Pass {
		t.Fatal("a timeout must be a FAIL")
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Errorf("the timeout was not applied in time: %s", elapsed)
	}
	if res.Checks[0].Error == "" {
		t.Error("it must record the timeout error")
	}
}

// TestAnchorMissingCommand: a command that does not exist is an explainable
// FAIL, not a panic.
func TestAnchorMissingCommand(t *testing.T) {
	cfg := config.Anchor{
		Kind:       "command",
		Command:    "/does/not/exist/this/command",
		Timeout:    5 * time.Second,
		ExpectExit: 0,
	}
	res := New(cfg, t.TempDir(), nil).Validate(context.Background())
	if res.Pass {
		t.Fatal("a missing command must be a FAIL")
	}
	if res.Checks[0].Error == "" {
		t.Error("the error must be recorded")
	}
}

// TestAnchorInvalidRegex: a badly written regular expression is detected and
// reported, instead of silently accepting any output.
func TestAnchorInvalidRegex(t *testing.T) {
	cfg := config.Anchor{
		Kind:         "command",
		Command:      "true",
		Timeout:      5 * time.Second,
		ExpectOutput: "(unclosed",
	}
	res := New(cfg, t.TempDir(), nil).Validate(context.Background())
	if res.Pass {
		t.Fatal("an invalid regex must prevent PASS")
	}
	if res.Checks[0].Error == "" {
		t.Error("it must explain the regex does not compile")
	}
}

// TestAnchorRunsInTheGivenDirectory: the anchor must see the attempt's files, not
// those of the directory the agent was launched from.
func TestAnchorRunsInTheGivenDirectory(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "marker.txt"), []byte("here"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := config.Anchor{
		Kind:       "command",
		Command:    "sh",
		Args:       []string{"-c", "cat marker.txt"},
		Timeout:    10 * time.Second,
		ExpectExit: 0,
	}
	res := New(cfg, dir, nil).Validate(context.Background())
	if !res.Pass {
		t.Fatalf("it did not find the file in the working directory: %s", res.Reason)
	}
}

// TestResultJSON: the structured record must be valid JSON, because that is what
// is handed to the LLM on the next attempt.
func TestResultJSON(t *testing.T) {
	cfg := config.Anchor{Kind: "command", Command: "true", Timeout: 5 * time.Second}
	res := New(cfg, t.TempDir(), nil).Validate(context.Background())
	text := res.JSON()
	if len(text) == 0 || text[0] != '{' {
		t.Fatalf("invalid JSON: %s", text)
	}
	if !strings.Contains(text, `"pass"`) {
		t.Errorf("the JSON must include the verdict: %s", text)
	}
	if !strings.Contains(text, `"reason"`) {
		t.Errorf("the JSON must include the reason: %s", text)
	}
}

// TestTruncate keeps the record bounded.
func TestTruncate(t *testing.T) {
	if got := truncate("short", 10); got != "short" {
		t.Errorf("truncate = %q", got)
	}
	got := truncate(strings.Repeat("x", 100), 10)
	if len(got) > 15 || !strings.HasSuffix(got, "...") {
		t.Errorf("truncate = %q", got)
	}
}

// TestValidatorInterfaceIsSatisfied: the abstraction must be usable in place of
// the concrete anchor.
func TestValidatorInterfaceIsSatisfied(t *testing.T) {
	var v Validator = New(config.Anchor{Kind: "command", Command: "true", Timeout: 5 * time.Second}, t.TempDir(), nil)
	if v.Validate(context.Background()).Pass != true {
		t.Error("a trivial passing check should validate")
	}
}

// TestCheckNameDefaultsToCheck: a check with no name still gets a readable label
// in the record.
func TestCheckNameDefaultsToCheck(t *testing.T) {
	cfg := config.Anchor{
		Kind:    "command",
		Command: "true",
		Checks: []config.Check{
			{Command: "true"}, // no name, no timeout
		},
	}
	res := New(cfg, t.TempDir(), nil).Validate(context.Background())
	if len(res.Checks) != 2 {
		t.Fatalf("checks = %d", len(res.Checks))
	}
	if res.Checks[1].Name != "check" {
		t.Errorf("name = %q, expected \"check\"", res.Checks[1].Name)
	}
}

// TestAnchorUsesTheSandboxWhenGiven: when a sandbox is passed in, the checks run
// through it (that is how validation can be isolated too).
func TestAnchorUsesTheSandboxWhenGiven(t *testing.T) {
	dir := t.TempDir()
	box, err := sandbox.New(sandbox.Options{
		Dir:     dir,
		Limits:  sandbox.Limits{MemoryMB: 256},
		Timeout: 20 * time.Second,
		Log:     logx.Global(),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer box.Close()

	cfg := config.Anchor{
		Kind:       "command",
		Command:    "sh",
		Args:       []string{"-c", "echo from-the-sandbox"},
		Timeout:    10 * time.Second,
		ExpectExit: 0,
	}
	res := New(cfg, dir, box).Validate(context.Background())
	if !res.Pass {
		t.Fatalf("it should pass: %s", res.Reason)
	}
	if !strings.Contains(res.Checks[0].Output, "from-the-sandbox") {
		t.Errorf("output = %q", res.Checks[0].Output)
	}
}
