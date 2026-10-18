package config

import (
	"strings"
	"testing"
)

// The one-shot knobs: a subtask is retried once and the criteria audit is on by default, both
// can be changed from the file or the environment, and a negative retry count is refused.
func TestOneShotSettings(t *testing.T) {
	t.Setenv("MOTITA_AGENT_SUBTASK_RETRIES", "")
	t.Setenv("MOTITA_AGENT_CRITERIA_AUDIT", "")
	cfg, err := LoadWithoutKey(diskFile(t, "agent:\n  max_retries: 2\n"))
	if err != nil || cfg.Agent.SubtaskRetries != 1 || !cfg.Agent.CriteriaAudit ||
		cfg.Prompts.Audit.User == "" || !strings.Contains(cfg.Prompts.Audit.User, "## CRITERIA AUDIT") {
		t.Fatalf("defaults: %+v, %v", cfg.Agent, err)
	}
	cfg, err = LoadWithoutKey(diskFile(t, "agent:\n  subtask_retries: 3\n  criteria_audit: false\n"))
	if err != nil || cfg.Agent.SubtaskRetries != 3 || cfg.Agent.CriteriaAudit {
		t.Fatalf("file: %+v, %v", cfg.Agent, err)
	}
	t.Setenv("MOTITA_AGENT_SUBTASK_RETRIES", "0")
	t.Setenv("MOTITA_AGENT_CRITERIA_AUDIT", "true")
	cfg, err = LoadWithoutKey(diskFile(t, "agent:\n  subtask_retries: 3\n  criteria_audit: false\n"))
	if err != nil || cfg.Agent.SubtaskRetries != 0 || !cfg.Agent.CriteriaAudit {
		t.Fatalf("environment: %+v, %v", cfg.Agent, err)
	}
	t.Setenv("MOTITA_AGENT_SUBTASK_RETRIES", "")
	if _, err = LoadWithoutKey(diskFile(t, "agent:\n  subtask_retries: -1\n")); err == nil ||
		!strings.Contains(err.Error(), "agent.subtask_retries") {
		t.Errorf("a negative count must be refused: %v", err)
	}
}
