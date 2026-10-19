package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseThinkingLevel(t *testing.T) {
	for raw, want := range map[string]string{"": "", "  ": "", " HIGH ": "high", "off": "off", "low": "low"} {
		if got, err := ParseThinkingLevel(raw); err != nil || got != want {
			t.Errorf("%q: %q %v", raw, got, err)
		}
	}
	if _, err := ParseThinkingLevel("max"); err == nil {
		t.Error("an unknown level must be refused")
	}
}

func TestThinkingFor(t *testing.T) {
	th := Thinking{Plan: "high", Code: "low", Review: "medium", Research: "off", Summary: "bogus", Cheap: "low", Strong: "high"}
	for kind, want := range map[string]string{
		TaskPlan: "high", TaskCode: "low", TaskReview: "medium", TaskResearch: "off",
		TaskSummary: "", TaskCheap: "low", TaskStrong: "high", "narrate": "",
	} {
		if got := th.For(kind); got != want {
			t.Errorf("%s: %q, want %q", kind, got, want)
		}
	}
}

func TestValidateThinking(t *testing.T) {
	c := Default()
	c.Thinking.Review = "max"
	if err := c.validateThinking(); err == nil || !strings.Contains(err.Error(), "thinking.review") {
		t.Errorf("err = %v", err)
	}
	if err := c.ValidateWithoutKey(); err == nil || !strings.Contains(err.Error(), "thinking.review") {
		t.Errorf("the whole configuration refuses it too: %v", err)
	}
	c.Thinking.Review = "low"
	if err := c.validateThinking(); err != nil {
		t.Error(err)
	}
}

func TestSetTaskThinkingWritesTheBlockInPlace(t *testing.T) {
	path := filepath.Join(t.TempDir(), "motita.yaml")
	body := "# mine\nllm:\n  provider: openai\n  model: gpt-5-mini\n"
	if err := os.WriteFile(path, []byte(body), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := SetTaskThinking(path, map[string]string{TaskPlan: "High", TaskSummary: "off"}); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadWithoutKey(path)
	if err != nil || cfg.Thinking.Plan != "high" || cfg.Thinking.Summary != "off" || cfg.Thinking.Code != "" {
		t.Fatalf("%v %+v", err, cfg.Thinking)
	}
	if data, _ := os.ReadFile(path); !strings.HasPrefix(string(data), body) {
		t.Errorf("the rest of the file stays:\n%s", data)
	}
	if err := SetTaskThinking(path, map[string]string{}); err != nil {
		t.Fatal(err)
	}
	if cfg, _ := LoadWithoutKey(path); cfg.Thinking != (Thinking{}) {
		t.Errorf("a second write replaces the lines: %+v", cfg.Thinking)
	}
	if err := SetTaskThinking(path, map[string]string{TaskPlan: "max"}); err == nil {
		t.Error("an unknown level must be refused")
	}
	if err := SetTaskThinking(filepath.Join(t.TempDir(), "missing.yaml"), nil); err == nil {
		t.Error("a missing file must be refused")
	}
	if err := SetTaskThinking(t.TempDir(), nil); err == nil {
		t.Error("a directory is not a configuration file")
	}
	bad := filepath.Join(t.TempDir(), "inline.yaml")
	_ = os.WriteFile(bad, []byte("thinking: {plan: low}\n"), 0o600)
	if err := SetTaskThinking(bad, nil); err == nil {
		t.Error("an inline block must be refused")
	}
}
