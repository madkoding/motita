package config

import (
	"os"
	"strings"
	"testing"
	"time"
)

// agent.approve_all_commands is off unless the file or the environment turns it on, and the
// settings write it in place; a value the environment overrides is undone and refused.
func TestApproveAllCommands(t *testing.T) {
	t.Setenv("MOTITA_AGENT_APPROVE_ALL_COMMANDS", "")
	cfg, err := LoadWithoutKey(diskFile(t, "agent:\n  max_retries: 2\n"))
	if err != nil || cfg.Agent.ApproveAllCommands {
		t.Fatalf("default = %v, %v", cfg.Agent.ApproveAllCommands, err)
	}
	path := diskFile(t, "agent:\n  max_retries: 2 # keep\n")
	if err := SetApproveAllCommands(path, true); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(path)
	if want := "agent:\n  approve_all_commands: true\n  max_retries: 2 # keep\n"; string(got) != want {
		t.Errorf("file = %q, want %q", got, want)
	}
	if cfg, _ := LoadWithoutKey(path); !cfg.Agent.ApproveAllCommands {
		t.Error("the saved value must load")
	}
	t.Setenv("MOTITA_AGENT_APPROVE_ALL_COMMANDS", "false")
	err = SetApproveAllCommands(path, true)
	if err == nil || !strings.Contains(err.Error(), "MOTITA_AGENT_APPROVE_ALL_COMMANDS") {
		t.Errorf("an overridden value must be refused: %v", err)
	}
}

// agent.approval_timeout is zero (wait for ever) unless set, is written in place by the settings,
// and a value the environment overrides is undone and refused.
func TestApprovalTimeout(t *testing.T) {
	t.Setenv("MOTITA_AGENT_APPROVAL_TIMEOUT", "")
	path := diskFile(t, "agent:\n  max_retries: 2\n")
	if cfg, err := LoadWithoutKey(path); err != nil || cfg.Agent.ApprovalTimeout != 0 {
		t.Fatalf("default = %v, %v", cfg.Agent.ApprovalTimeout, err)
	}
	if err := (Tx{path}).SetApprovalTimeout(15 * time.Minute); err != nil {
		t.Fatal(err)
	}
	if cfg, _ := LoadWithoutKey(path); cfg.Agent.ApprovalTimeout != 15*time.Minute {
		t.Errorf("the saved value must load: %v", cfg.Agent.ApprovalTimeout)
	}
	t.Setenv("MOTITA_AGENT_APPROVAL_TIMEOUT", "1h")
	err := Tx{path}.SetApprovalTimeout(5 * time.Minute)
	if err == nil || !strings.Contains(err.Error(), "MOTITA_AGENT_APPROVAL_TIMEOUT") {
		t.Errorf("an overridden value must be refused: %v", err)
	}
}
