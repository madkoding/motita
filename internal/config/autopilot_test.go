package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func setAutopilot(path string, c AutopilotChange) error {
	return Transaction(path, func(tx Tx) error { return tx.SetAutopilot(c) })
}

func TestAutopilotIsOffByDefault(t *testing.T) {
	got := Default().Autopilot
	if got.WeeklyAudit || got.AutoPR || got.MaxOpenPRs != 3 {
		t.Fatalf("default = %+v: both switches must be off and the limit 3", got)
	}
}

func TestValidateAutopilotLimit(t *testing.T) {
	for n, ok := range map[int]bool{0: false, 1: true, 3: true, 10: true, 11: false, -2: false} {
		c := Default()
		c.Autopilot.MaxOpenPRs = n
		if err := c.validateAutopilot(); (err == nil) != ok {
			t.Errorf("max_open_prs %d: err = %v", n, err)
		}
	}
}

func TestSetAutopilotWritesOnlyWhatChanged(t *testing.T) {
	path := diskFile(t, "autopilot:\n  weekly_audit: false # keep\n  max_open_prs: 5\nllm:\n  provider: openai\n")
	err := setAutopilot(path, AutopilotChange{WeeklyAudit: ptr(true), AutoPR: ptr(true), MaxOpenPRs: ptr(2)})
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadWithoutKey(path)
	if err != nil || cfg.Autopilot != (Autopilot{WeeklyAudit: true, AutoPR: true, MaxOpenPRs: 2}) {
		t.Fatalf("loaded = %+v, %v", cfg.Autopilot, err)
	}
	body, _ := os.ReadFile(path)
	if !strings.Contains(string(body), "weekly_audit: true # keep") || !strings.Contains(string(body), "provider: openai") {
		t.Errorf("the rest of the file must stay: %q", body)
	}
	if err := setAutopilot(path, AutopilotChange{}); err != nil {
		t.Errorf("an empty change is nothing: %v", err)
	}
}

func TestSetAutopilotRefusals(t *testing.T) {
	bad := AutopilotChange{AutoPR: ptr(true)}
	if err := (Tx{filepath.Join(t.TempDir(), "missing.yaml")}).SetAutopilot(bad); err == nil {
		t.Error("a missing file")
	}
	if err := setAutopilot(diskFile(t, "autopilot: {}\n"), bad); err == nil {
		t.Error("an inline block must be refused")
	}
	if err := setAutopilot(diskFile(t, "llm:\n  provider: openai\n"), AutopilotChange{MaxOpenPRs: ptr(99)}); err == nil {
		t.Error("a limit that does not validate must be refused")
	}
	old := uiCreateTemp
	t.Cleanup(func() { uiCreateTemp = old })
	uiCreateTemp = func(string, string) (*os.File, error) { return nil, errors.New("disk full") }
	if err := setAutopilot(diskFile(t, "llm:\n  provider: openai\n"), bad); err == nil {
		t.Error("a failed write")
	}
}

func TestSetAutopilotRefusesWhatTheEnvironmentOverrides(t *testing.T) {
	t.Setenv("MOTITA_AUTOPILOT_AUTO_PR", "false")
	path := diskFile(t, "llm:\n  provider: openai\n")
	before, _ := os.ReadFile(path)
	err := setAutopilot(path, AutopilotChange{AutoPR: ptr(true)})
	if err == nil || !strings.Contains(err.Error(), "MOTITA_AUTOPILOT_") {
		t.Fatalf("err = %v", err)
	}
	if after, _ := os.ReadFile(path); string(after) != string(before) {
		t.Errorf("the file must be restored: %q", after)
	}
}

func TestAutopilotEnvironment(t *testing.T) {
	t.Setenv("MOTITA_AUTOPILOT_WEEKLY_AUDIT", "true")
	t.Setenv("MOTITA_AUTOPILOT_MAX_OPEN_PRS", "7")
	c := Default()
	if err := ApplyEnvironment(&c); err != nil || !c.Autopilot.WeeklyAudit || c.Autopilot.MaxOpenPRs != 7 {
		t.Fatalf("env = %+v, %v", c.Autopilot, err)
	}
}
