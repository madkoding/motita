package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseModelChoice(t *testing.T) {
	for raw, want := range map[string]ModelChoice{
		"":                            {},
		"  ":                          {},
		"anthropic/claude-opus-5-5":   {Provider: "anthropic", Model: "claude-opus-5-5"},
		" OLLAMA / library/qwen3:8b ": {Provider: "ollama", Model: "library/qwen3:8b"},
	} {
		got, err := ParseModelChoice(raw)
		if err != nil || got != want {
			t.Errorf("%q: %+v %v", raw, got, err)
		}
	}
	for _, raw := range []string{"gpt-5", "openai/", "nope/m", "openai/" + strings.Repeat("m", 201), "openai/a\tb"} {
		if _, err := ParseModelChoice(raw); err == nil {
			t.Errorf("%q must be refused", raw)
		}
	}
	if (ModelChoice{}).String() != "" || (ModelChoice{Provider: "openai", Model: "m"}).String() != "openai/m" {
		t.Error("String")
	}
}

func TestModelsFor(t *testing.T) {
	m := Models{Plan: "openai/a", Code: "openai/b", Review: "openai/c", Research: "openai/d", Summary: "nope"}
	for kind, want := range map[string]string{TaskPlan: "a", TaskCode: "b", TaskReview: "c", TaskResearch: "d"} {
		if got := m.For(kind); got.Model != want {
			t.Errorf("%s: %+v", kind, got)
		}
	}
	if !m.For(TaskSummary).Empty() || !m.For("narrate").Empty() {
		t.Error("an unreadable entry and an unknown kind follow the default")
	}
}

func TestValidateModels(t *testing.T) {
	c := Default()
	c.Models.Review = "nope/m"
	if err := c.validateModels(); err == nil || !strings.Contains(err.Error(), "models.review") {
		t.Errorf("err = %v", err)
	}
	c.Models.Review = "openai/m"
	if err := c.validateModels(); err != nil {
		t.Error(err)
	}
}

func TestSetTaskModelsWritesTheBlockInPlace(t *testing.T) {
	path := filepath.Join(t.TempDir(), "motita.yaml")
	body := "# mine\nllm:\n  provider: openai\n  model: gpt-5-mini\n"
	if err := os.WriteFile(path, []byte(body), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := SetTaskModels(path, map[string]ModelChoice{
		TaskPlan:   {Provider: "Anthropic", Model: "claude-opus-5-5"},
		TaskReview: {Provider: "ollama", Model: "qwen3:8b"},
	}); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadWithoutKey(path)
	if err != nil || cfg.Models.Plan != "anthropic/claude-opus-5-5" || cfg.Models.Review != "ollama/qwen3:8b" ||
		cfg.Models.Code != "" {
		t.Fatalf("%v %+v", err, cfg.Models)
	}
	data, _ := os.ReadFile(path)
	if !strings.HasPrefix(string(data), body) {
		t.Errorf("the rest of the file stays:\n%s", data)
	}
	// A second write replaces the lines instead of adding more.
	if err := SetTaskModels(path, map[string]ModelChoice{}); err != nil {
		t.Fatal(err)
	}
	cfg, _ = LoadWithoutKey(path)
	data, _ = os.ReadFile(path)
	if cfg.Models != (Models{}) || strings.Count(string(data), "plan:") != 1 {
		t.Errorf("cleared: %+v\n%s", cfg.Models, data)
	}
	if info, _ := os.Stat(path); info.Mode().Perm() != 0o640 {
		t.Errorf("mode = %v", info.Mode().Perm())
	}
}

func TestSetTaskModelsRefusals(t *testing.T) {
	dir := t.TempDir()
	if err := SetTaskModels(filepath.Join(dir, "none.yaml"), nil); err == nil {
		t.Error("a missing file is an error")
	}
	if err := SetTaskModels(dir, nil); err == nil {
		t.Error("a directory is not a file to read")
	}
	path := filepath.Join(dir, "motita.yaml")
	if err := os.WriteFile(path, []byte("models: {plan: openai/m}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := SetTaskModels(path, nil); err == nil {
		t.Error("an inline block cannot be edited in place")
	}
	if err := SetTaskModels(path, map[string]ModelChoice{TaskCode: {Provider: "nope", Model: "m"}}); err == nil {
		t.Error("an unknown provider is refused")
	}
}

func TestModelsFromTheEnvironment(t *testing.T) {
	t.Setenv("MOTITA_MODELS_CODE", "openai/gpt-5")
	c := Default()
	if err := ApplyEnvironment(&c); err != nil {
		t.Fatal(err)
	}
	if c.Models.Code != "openai/gpt-5" {
		t.Errorf("models.code = %q", c.Models.Code)
	}
}

func TestLoadRefusesAnUnreadableModel(t *testing.T) {
	path := filepath.Join(t.TempDir(), "motita.yaml")
	if err := os.WriteFile(path, []byte("models:\n  plan: gpt-5\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadWithoutKey(path); err == nil || !strings.Contains(err.Error(), "models.plan") {
		t.Errorf("err = %v", err)
	}
}

// TestRetiredBrowserProviderFallsBack: a file saved while the browser model existed still loads.
func TestRetiredBrowserProviderFallsBack(t *testing.T) {
	c := Default()
	c.LLM.Provider, c.LLM.Model = " Browser ", "qwen2.5-0.5b-instruct"
	c.Models.Summary, c.Models.Cheap, c.Models.Plan = "browser/qwen2.5-0.5b-instruct", " BROWSER/m", "anthropic/claude-opus-5-5"
	c.normalize()
	d := Default().LLM
	if c.LLM.Provider != d.Provider || c.LLM.Model != d.Model || c.LLM.BaseURL != d.BaseURL {
		t.Errorf("llm = %+v, want the default %+v", c.LLM, d)
	}
	if c.Models.Summary != "" || c.Models.Cheap != "" || c.Models.Plan != "anthropic/claude-opus-5-5" {
		t.Errorf("models = %+v", c.Models)
	}
	if err := c.validateModels(); err != nil {
		t.Error(err)
	}
}
