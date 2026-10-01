package onboard

// The anchor the wizard writes, and the defect this file exists to prevent.
//
// `motita -init` used to record option 2 ("Always pass, while I try the agent out") as
// `anchor: {kind: command, command: "true"}`. The comment in the test called it "the escape
// hatch", and it was not one: `true` exits 0, so the anchor PASSED on every run and the agent
// reported "task completed" over work it had not done. Measured on a real gateway's log after
// the fact: 17 anchor validations, 17 passes, 0 failures, 71 runs — the validator never once
// said no, which is the one property a validator cannot have.
//
// The two configurations are not equivalent, and the difference is the whole point:
//
//   - `kind: none` returns FAIL with the reason "there is no deterministic validation, the
//     result can NOT be taken as verified". The agent cannot declare PASS, and the user is
//     told why. That is the honest shape of "I am just trying this out".
//   - `kind: command` with `true` returns PASS having checked nothing. The system's own
//     guarantee — "only the anchor can declare PASS" — is then a guarantee about nothing.
//
// So the escape hatch stays, because trying the agent without preparing a gate is a real thing
// people do, but it is written as the honest configuration. A user who picks it gets an agent
// that says "I could not verify this", which is true, instead of a green check that is not.

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/madkoding/motita/internal/config"
)

// readConfig returns the configuration the wizard wrote, which is the artifact the running
// agent reads: a unit test of the in-memory struct would pass while the file said otherwise.
func readConfig(t *testing.T, dir string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, "config.yaml"))
	if err != nil {
		t.Fatalf("the wizard must write a config: %v", err)
	}
	return string(b)
}

// TestTheEscapeHatchIsAnHonestConfiguration: option 2 must produce a configuration that
// REFUSES to declare PASS, not one that always does.
//
// The assertion is on the YAML the wizard writes, because that is what the running agent
// reads: a unit test of the in-memory struct would pass while the file on disk said otherwise.
func TestTheEscapeHatchIsAnHonestConfiguration(t *testing.T) {
	dir := t.TempDir()
	// Answers: provider, endpoint, key, model, anchor=3 (the escape hatch), save.
	if _, _, err := run(t.Context(), t, dir, []string{"openai", "", "", "1", "3", ""}, Answers{}); err != nil {
		t.Fatalf("run: %v", err)
	}
	cfg := readConfig(t, dir)

	if strings.Contains(cfg, `command: "true"`) {
		t.Errorf("the wizard still writes `command: \"true\"`, an anchor that passes on every run "+
			"and therefore verifies nothing:\n%s", cfg)
	}
	if !strings.Contains(cfg, "kind: none") {
		t.Errorf("the escape hatch must record `kind: none`, which refuses to declare PASS:\n%s", cfg)
	}
}

// TestARealCommandIsStillARealAnchor: the fix must not have taken the other branch with it.
// Option 1 takes a command from the user and that command IS the validator.
func TestARealCommandIsStillARealAnchor(t *testing.T) {
	dir := t.TempDir()
	if _, _, err := run(t.Context(), t, dir, []string{"openai", "", "", "1", "2", "make test", ""}, Answers{}); err != nil {
		t.Fatalf("run: %v", err)
	}
	cfg := readConfig(t, dir)

	if !strings.Contains(cfg, "kind: command") {
		t.Errorf("a real check must be recorded as one:\n%s", cfg)
	}
	if !strings.Contains(cfg, "command: make") {
		t.Errorf("the command the user gave must be the one recorded:\n%s", cfg)
	}
	// "kind: none" appears in the OTHER branch's comment block, so the assertion has to be on
	// the uncommented line: a substring test over the whole file would pass or fail for a
	// reason that has nothing to do with which branch ran.
	for _, line := range strings.Split(cfg, "\n") {
		if strings.TrimSpace(line) == "kind: none" {
			t.Errorf("a real check must not be downgraded to `kind: none`:\n%s", cfg)
		}
	}
}

// TestTheDetectedGateIsWhatTheWizardWritesByDefault: option 1 records `kind: auto`,
// which reads the gate from the project the agent is working in. It is the default
// because it is the only option that does not have to be edited when the user moves
// to another project — and the assertion is on the YAML, because that is what the
// running agent reads.
func TestTheDetectedGateIsWhatTheWizardWritesByDefault(t *testing.T) {
	dir := t.TempDir()
	// provider, endpoint, key, model, anchor=1 (the detected gate), save.
	if _, _, err := run(t.Context(), t, dir, []string{"openai", "", "", "1", "1", ""}, Answers{}); err != nil {
		t.Fatalf("run: %v", err)
	}
	cfg := readConfig(t, dir)

	if !strings.Contains(cfg, "kind: auto") {
		t.Errorf("the detected gate must be written as `kind: auto`:\n%s", cfg)
	}
	// A hardcoded command would defeat the purpose: the whole point is that the gate
	// comes from the project, so nothing may be pinned in the file.
	if strings.Contains(cfg, "kind: command") {
		t.Errorf("nothing may be hardcoded when the gate is detected:\n%s", cfg)
	}
}

// TestTheDetectedGateConfigIsAcceptedByTheLoader: the wizard's file must load. A
// generated configuration the program refuses would be worse than no wizard at all.
func TestTheDetectedGateConfigIsAcceptedByTheLoader(t *testing.T) {
	dir := t.TempDir()
	if _, _, err := run(t.Context(), t, dir, []string{"openai", "", "", "1", "1", ""}, Answers{}); err != nil {
		t.Fatalf("run: %v", err)
	}
	cfgPath := filepath.Join(dir, "config.yaml")
	// LoadWithoutKey: the wizard writes the key to its own 0600 file, so the
	// configuration alone has none - and this test is about the anchor block loading.
	cfg, err := config.LoadWithoutKey(cfgPath)
	if err != nil {
		t.Fatalf("the generated configuration must load: %v", err)
	}
	if cfg.Anchor.Kind != "auto" {
		t.Errorf("anchor.kind = %q, want auto", cfg.Anchor.Kind)
	}
}

// TestACommandWithArgumentsIsSplitOnSpaces: the command the user types can carry its
// own arguments, and the split is what makes `go test ./...` a validator instead of a
// command named after the whole sentence.
func TestACommandWithArgumentsIsSplitOnSpaces(t *testing.T) {
	dir := t.TempDir()
	// provider, endpoint, key, model, anchor=2 (a command), the command with its arguments, save.
	if _, _, err := run(t.Context(), t, dir, []string{"openai", "", "", "1", "2", "go test ./...", ""}, Answers{}); err != nil {
		t.Fatalf("run: %v", err)
	}
	cfg := readConfig(t, dir)

	if !strings.Contains(cfg, "command: go") {
		t.Errorf("the executable must be the first field of what the user typed:\n%s", cfg)
	}
	if !strings.Contains(cfg, "args: [test, ./...]") {
		t.Errorf("the rest of the line must become the arguments:\n%s", cfg)
	}
}

// TestCancellingTheCheckQuestionIsReported: an EOF while asking for the check ends the
// wizard with an error rather than looping or writing a half-made configuration.
func TestCancellingTheCheckQuestionIsReported(t *testing.T) {
	dir := t.TempDir()
	// provider, model, anchor=2 (a command) and then NOTHING: the input ends while
	// the command is being asked for.
	in := strings.NewReader("openai\n1\n2\n")
	var out bytes.Buffer
	_, err := Run(context.Background(), in, &out, filepath.Join(dir, "config.yaml"), Answers{}, fixedTime())
	if err == nil {
		t.Fatal("an input that ends mid-question must be reported, not silently ignored")
	}
	if !strings.Contains(err.Error(), "cancelled") {
		t.Errorf("the error should say the question was cancelled, got: %v", err)
	}
}
