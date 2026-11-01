package config

import (
	"os"
	"strings"
	"testing"
)

// agent.direct is off unless the file or the environment turns it on, and the settings write it in
// place; a value the environment overrides is undone and refused.
func TestDirect(t *testing.T) {
	t.Setenv("MOTITA_AGENT_DIRECT", "")
	path := diskFile(t, "agent:\n  max_retries: 2 # keep\n")
	if cfg, err := LoadWithoutKey(path); err != nil || cfg.Agent.Direct {
		t.Fatalf("default = %v, %v", cfg.Agent.Direct, err)
	}
	if err := (Tx{path}).SetDirect(true); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(path)
	if want := "agent:\n  direct: true\n  max_retries: 2 # keep\n"; string(got) != want {
		t.Errorf("file = %q, want %q", got, want)
	}
	t.Setenv("MOTITA_AGENT_DIRECT", "true")
	if err := (Tx{path}).SetDirect(false); err == nil || !strings.Contains(err.Error(), "MOTITA_AGENT_DIRECT") {
		t.Errorf("an overridden value must be refused: %v", err)
	}
}
