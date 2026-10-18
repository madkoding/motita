package config

import (
	"os"
	"strings"
	"testing"
)

// agent.auto_apply_suggestions is off unless the file or the environment turns it on, and the
// settings write it in place; a value the environment overrides is undone and refused.
func TestAutoApplySuggestions(t *testing.T) {
	t.Setenv("MOTITA_AGENT_AUTO_APPLY_SUGGESTIONS", "")
	cfg, err := LoadWithoutKey(diskFile(t, "agent:\n  max_retries: 2\n"))
	if err != nil || cfg.Agent.AutoApplySuggestions {
		t.Fatalf("default = %v, %v", cfg.Agent.AutoApplySuggestions, err)
	}
	path := diskFile(t, "agent:\n  max_retries: 2 # keep\n")
	if err := SetAutoApplySuggestions(path, true); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(path)
	if want := "agent:\n  auto_apply_suggestions: true\n  max_retries: 2 # keep\n"; string(got) != want {
		t.Errorf("file = %q, want %q", got, want)
	}
	if cfg, _ := LoadWithoutKey(path); !cfg.Agent.AutoApplySuggestions {
		t.Error("the saved value must load")
	}
	t.Setenv("MOTITA_AGENT_AUTO_APPLY_SUGGESTIONS", "true")
	err = SetAutoApplySuggestions(path, false)
	if err == nil || !strings.Contains(err.Error(), "MOTITA_AGENT_AUTO_APPLY_SUGGESTIONS") {
		t.Errorf("an overridden value must be refused: %v", err)
	}
	if after, _ := os.ReadFile(path); string(after) != string(got) {
		t.Errorf("the file must be restored: %q", after)
	}
}
