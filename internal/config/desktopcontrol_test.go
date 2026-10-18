package config

import (
	"os"
	"strings"
	"testing"
)

// agent.desktop_control is off unless the file or the environment turns it on, and the settings
// write it in place; a value the environment overrides is undone and refused.
func TestDesktopControl(t *testing.T) {
	t.Setenv("MOTITA_AGENT_DESKTOP_CONTROL", "")
	path := diskFile(t, "agent:\n  max_retries: 2 # keep\n")
	if cfg, err := LoadWithoutKey(path); err != nil || cfg.Agent.DesktopControl {
		t.Fatalf("default = %v, %v", cfg.Agent.DesktopControl, err)
	}
	if err := (Tx{path}).SetDesktopControl(true); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(path)
	if want := "agent:\n  desktop_control: true\n  max_retries: 2 # keep\n"; string(got) != want {
		t.Errorf("file = %q, want %q", got, want)
	}
	t.Setenv("MOTITA_AGENT_DESKTOP_CONTROL", "true")
	if err := (Tx{path}).SetDesktopControl(false); err == nil ||
		!strings.Contains(err.Error(), "MOTITA_AGENT_DESKTOP_CONTROL") {
		t.Errorf("an overridden value must be refused: %v", err)
	}
}
