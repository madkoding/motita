package config

import (
	"os"
	"strings"
	"testing"
)

// ask_unconfined_runs is on unless the file or the environment turns it off, and the settings
// write it in place; a value the environment overrides is undone and refused.
func TestAskUnconfinedRuns(t *testing.T) {
	t.Setenv("MOTITA_SANDBOX_ASK_UNCONFINED_RUNS", "")
	cfg, err := LoadWithoutKey(diskFile(t, "sandbox:\n  kind: none\n"))
	if err != nil || !cfg.Sandbox.AskUnconfinedRuns {
		t.Fatalf("default = %v, %v", cfg.Sandbox.AskUnconfinedRuns, err)
	}
	path := diskFile(t, "sandbox:\n  kind: none # keep\n")
	if err := SetAskUnconfinedRuns(path, false); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(path)
	if want := "sandbox:\n  ask_unconfined_runs: false\n  kind: none # keep\n"; string(got) != want {
		t.Errorf("file = %q, want %q", got, want)
	}
	if cfg, _ := LoadWithoutKey(path); cfg.Sandbox.AskUnconfinedRuns {
		t.Error("the saved value must load")
	}
	t.Setenv("MOTITA_SANDBOX_ASK_UNCONFINED_RUNS", "false")
	err = SetAskUnconfinedRuns(path, true)
	if err == nil || !strings.Contains(err.Error(), "MOTITA_SANDBOX_ASK_UNCONFINED_RUNS") {
		t.Errorf("an overridden value must be refused: %v", err)
	}
	if after, _ := os.ReadFile(path); string(after) != string(got) {
		t.Errorf("the file must be restored: %q", after)
	}
}
