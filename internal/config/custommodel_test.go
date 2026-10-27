package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func fl(f float64) *float64 { return &f }

func TestCustomModelValidate(t *testing.T) {
	good := []CustomModel{{}, {Model: "m"}, {Model: "m", Temperature: fl(0), MaxTokens: 1, ContextWindow: 1},
		{Model: "m", Temperature: fl(2), MaxTokens: maxCustomTokens, ContextWindow: maxCustomTokens * 10}}
	for _, m := range good {
		if err := m.Validate(); err != nil {
			t.Errorf("%+v: %v", m, err)
		}
	}
	bad := map[string]CustomModel{
		"model":          {Model: "a\nb"},
		"temperature":    {Model: "m", Temperature: fl(2.5)},
		"max_tokens":     {Model: "m", MaxTokens: -1},
		"context_window": {Model: "m", ContextWindow: maxCustomTokens*10 + 1},
	}
	bad["model"] = CustomModel{Model: strings.Repeat("x", 201)}
	bad["temperature2"] = CustomModel{Model: "m", Temperature: fl(-0.1)}
	bad["max_tokens2"] = CustomModel{Model: "m", MaxTokens: maxCustomTokens + 1}
	bad["context_window2"] = CustomModel{Model: "m", ContextWindow: -1}
	for name, m := range bad {
		if err := m.Validate(); err == nil {
			t.Errorf("%s: %+v must be refused", name, m)
		}
	}
	if err := (CustomModel{Model: "a\tb"}).Validate(); err == nil {
		t.Error("a control character must be refused")
	}
}

func TestCustomModelTunesOnlyItsOwnModel(t *testing.T) {
	custom := CustomModel{Model: "mine", Temperature: fl(0.7), MaxTokens: 4096, ContextWindow: 64000}
	l := LLM{Provider: "custom", Model: "mine", MaxTokens: 16384, Custom: custom}
	l.Session.ContextWindow = 1000
	got := l.Tuned()
	if got.Temperature == nil || *got.Temperature != 0.7 || got.MaxTokens != 4096 {
		t.Errorf("tuned: %+v", got)
	}
	if l.WindowOverride() != 64000 {
		t.Errorf("window = %d", l.WindowOverride())
	}
	// Another model, another provider, or an empty block: nothing changes.
	for _, other := range []LLM{
		{Provider: "custom", Model: "other", MaxTokens: 1, Custom: custom},
		{Provider: "openai", Model: "mine", MaxTokens: 1, Custom: custom},
		{Provider: "custom", Model: "mine", MaxTokens: 1},
	} {
		other.Session.ContextWindow = 5
		if g := other.Tuned(); g.MaxTokens != 1 || g.Temperature != nil {
			t.Errorf("untouched: %+v", g)
		}
		if other.WindowOverride() != 5 {
			t.Errorf("window = %d", other.WindowOverride())
		}
	}
	// Zero and nil keep the general values.
	l = LLM{Provider: "Custom", Model: "mine", MaxTokens: 9, Custom: CustomModel{Model: "mine"}}
	if g := l.Tuned(); g.MaxTokens != 9 || g.Temperature != nil || l.WindowOverride() != 0 {
		t.Errorf("empty limits keep the general ones: %+v", g)
	}
}

func TestSetCustomModelWritesTheBlockInPlace(t *testing.T) {
	path := filepath.Join(t.TempDir(), "motita.yaml")
	if err := os.WriteFile(path, []byte("llm:\n  provider: custom\n  model: mine\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	m := CustomModel{Model: " mine ", Temperature: fl(0.25), MaxTokens: 8192, ContextWindow: 128000}
	if err := SetCustomModel(path, m); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadWithoutKey(path)
	if err != nil || cfg.CustomModel.Model != "mine" || *cfg.CustomModel.Temperature != 0.25 ||
		cfg.LLM.Custom.ContextWindow != 128000 {
		t.Fatalf("round trip: %v %+v", err, cfg.CustomModel)
	}
	// No temperature goes back to the endpoint's default.
	if err := SetCustomModel(path, CustomModel{Model: "mine"}); err != nil {
		t.Fatal(err)
	}
	cfg, err = LoadWithoutKey(path)
	if err != nil || cfg.CustomModel.Temperature != nil || cfg.CustomModel.MaxTokens != 0 {
		t.Fatalf("cleared: %v %+v", err, cfg.CustomModel)
	}
}

func TestSetCustomModelFailures(t *testing.T) {
	dir := t.TempDir()
	if SetCustomModel(filepath.Join(dir, "none.yaml"), CustomModel{Model: "m"}) == nil {
		t.Error("a missing file is an error")
	}
	if SetCustomModel(dir, CustomModel{Model: "m"}) == nil {
		t.Error("a directory cannot be read as the file")
	}
	if SetCustomModel(dir, CustomModel{Model: "m", MaxTokens: -1}) == nil {
		t.Error("an invalid block is refused")
	}
	path := filepath.Join(dir, "inline.yaml")
	_ = os.WriteFile(path, []byte("custom_model: {model: x}\n"), 0o600)
	if SetCustomModel(path, CustomModel{Model: "m"}) == nil {
		t.Error("an inline block is refused")
	}
	path = filepath.Join(dir, "ok.yaml")
	_ = os.WriteFile(path, []byte("llm:\n  provider: x\n"), 0o600)
	restore := uiCreateTemp
	uiCreateTemp = func(string, string) (*os.File, error) { return nil, errors.New("disk full") }
	defer func() { uiCreateTemp = restore }()
	if SetCustomModel(path, CustomModel{Model: "m"}) == nil {
		t.Error("a failed write is an error")
	}
}

func TestCustomModelEnvironmentAndValidation(t *testing.T) {
	t.Setenv("MOTITA_CUSTOM_MODEL_MODEL", "from-env")
	t.Setenv("MOTITA_CUSTOM_MODEL_TEMPERATURE", "0.4")
	t.Setenv("MOTITA_CUSTOM_MODEL_MAX_TOKENS", "2048")
	t.Setenv("MOTITA_CUSTOM_MODEL_CONTEXT_WINDOW", "32000")
	c := Default()
	c.CustomModel.Temperature = fl(1)
	if err := ApplyEnvironment(&c); err != nil {
		t.Fatal(err)
	}
	m := c.CustomModel
	if m.Model != "from-env" || *m.Temperature != 0.4 || m.MaxTokens != 2048 || m.ContextWindow != 32000 {
		t.Errorf("env: %+v", m)
	}
	c.CustomModel.MaxTokens = -3
	if err := c.ValidateWithoutKey(); err == nil || !strings.Contains(err.Error(), "custom_model.max_tokens") {
		t.Errorf("the whole configuration refuses it: %v", err)
	}
	t.Setenv("MOTITA_CUSTOM_MODEL_TEMPERATURE", "warm")
	if err := ApplyEnvironment(&c); err == nil {
		t.Error("a temperature that is not a number is refused")
	}
}

func TestCustomProviderKeyVariable(t *testing.T) {
	if v := ProviderKeyVariable("custom"); v != "MOTITA_CUSTOM_API_KEY" {
		t.Errorf("variable = %q", v)
	}
	if v := ProviderBaseURLVariable("custom"); v != "MOTITA_CUSTOM_BASE_URL" {
		t.Errorf("base variable = %q", v)
	}
}
