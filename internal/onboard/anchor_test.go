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
	"os"
	"path/filepath"
	"strings"
	"testing"
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
	// Answers: provider, model, anchor=2 (the escape hatch), then blanks for the rest.
	if _, _, err := run(t.Context(), t, dir, []string{"openai", "1", "2", "", ""}, Answers{}); err != nil {
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
	if _, _, err := run(t.Context(), t, dir, []string{"openai", "1", "1", "make test", "", ""}, Answers{}); err != nil {
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
