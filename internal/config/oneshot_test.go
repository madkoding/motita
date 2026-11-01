package config

import (
	"strings"
	"testing"
)

// The one-shot knobs: a subtask is retried twice and the criteria audit is on by default, both
// can be changed from the file or the environment, and a negative retry count is refused.
func TestOneShotSettings(t *testing.T) {
	t.Setenv("MOTITA_AGENT_SUBTASK_RETRIES", "")
	t.Setenv("MOTITA_AGENT_CRITERIA_AUDIT", "")
	cfg, err := LoadWithoutKey(diskFile(t, "agent:\n  max_retries: 2\n"))
	if err != nil || cfg.Agent.SubtaskRetries != 2 || !cfg.Agent.CriteriaAudit {
		t.Fatalf("defaults: %+v, %v", cfg.Agent, err)
	}
	if !strings.Contains(cfg.Prompts.Audit.User, "## CRITERIA AUDIT") {
		t.Errorf("the audit prompt is missing: %q", cfg.Prompts.Audit.User)
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

// A chat turn's tool rounds have their own knob, apart from max_retries: 40 by default, set from
// the file or the environment, and never negative.
func TestPlanLoopsSetting(t *testing.T) {
	t.Setenv("MOTITA_AGENT_PLAN_LOOPS", "")
	cfg, err := LoadWithoutKey(diskFile(t, "agent:\n  max_retries: 2\n"))
	if err != nil || cfg.Agent.PlanToolRounds() != DefaultPlanLoops {
		t.Fatalf("default: %d, %v", cfg.Agent.PlanToolRounds(), err)
	}
	cfg, err = LoadWithoutKey(diskFile(t, "agent:\n  plan_loops: 15\n"))
	if err != nil || cfg.Agent.PlanToolRounds() != 15 {
		t.Fatalf("file: %d, %v", cfg.Agent.PlanToolRounds(), err)
	}
	t.Setenv("MOTITA_AGENT_PLAN_LOOPS", "25")
	cfg, err = LoadWithoutKey(diskFile(t, "agent:\n  plan_loops: 15\n"))
	if err != nil || cfg.Agent.PlanToolRounds() != 25 {
		t.Fatalf("environment: %d, %v", cfg.Agent.PlanToolRounds(), err)
	}
	t.Setenv("MOTITA_AGENT_PLAN_LOOPS", "")
	if _, err = LoadWithoutKey(diskFile(t, "agent:\n  plan_loops: -1\n")); err == nil ||
		!strings.Contains(err.Error(), "agent.plan_loops") {
		t.Errorf("a negative count must be refused: %v", err)
	}
}
