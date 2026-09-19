package config

import (
	"os"
	"path/filepath"
	"testing"
)

// LoadOrDefault is the path the TUI takes when there is no configuration file: the
// defaults plus whatever the environment provides. It is what makes
// OLLAMA_API_KEY alone enough to start the interface.

func TestLoadOrDefaultWithNoPathAppliesTheEnvironment(t *testing.T) {
	t.Setenv("OLLAMA_API_KEY", "a-key-from-the-environment")
	t.Setenv("STARLIGHT_LLM_PROVIDER", "ollama")

	cfg, err := LoadOrDefault("")
	if err != nil {
		t.Fatalf("LoadOrDefault: %v", err)
	}
	if cfg.LLM.Provider != "ollama" {
		t.Errorf("provider = %q, want the environment value", cfg.LLM.Provider)
	}
	if cfg.LLM.APIKey != "a-key-from-the-environment" {
		t.Errorf("the key must be picked up from the environment")
	}
	// The defaults are still there underneath.
	if cfg.Agent.WorkspaceDir == "" || cfg.Sandbox.Kind == "" {
		t.Errorf("the defaults must survive: %+v", cfg)
	}
}

// TestLoadOrDefaultReportsAnInvalidEnvironment: a variable that cannot be parsed
// must be reported, not silently ignored: a typo in a unit would otherwise change
// behaviour invisibly.
func TestLoadOrDefaultReportsAnInvalidEnvironment(t *testing.T) {
	t.Setenv("STARLIGHT_LLM_TIMEOUT", "not-a-duration")
	if _, err := LoadOrDefault(""); err == nil {
		t.Error("an unparsable duration must be reported")
	}
}

// TestLoadOrDefaultWithAPathDefersToTheLoader: when a path is given, the file is
// the source of truth and the key may stay missing (the caller decides what to do
// about that).
func TestLoadOrDefaultWithAPathDefersToTheLoader(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "starlight.yaml")
	body := "llm:\n  provider: anthropic\n  model: claude-x\n  api_key: from-the-file\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("could not write the configuration: %v", err)
	}

	cfg, err := LoadOrDefault(path)
	if err != nil {
		t.Fatalf("LoadOrDefault: %v", err)
	}
	if cfg.LLM.Provider != "anthropic" || cfg.LLM.Model != "claude-x" {
		t.Errorf("the file must win: %+v", cfg.LLM)
	}
}

// TestValidateNormalisesTheReasoningLevel: the level is compared as a lowercase
// word everywhere, so a configuration that spells it "HIGH" or pads it with spaces
// must be normalised on load rather than at every use.
func TestValidateNormalisesTheReasoningLevel(t *testing.T) {
	cases := []struct {
		name    string
		given   Reasoning
		level   string
		enabled bool
	}{
		// An empty level means "the default", and the default is medium — which is
		// an enabled level. A configuration that never mentions reasoning gets it
		// at medium, and that is the documented behaviour.
		{"empty becomes the documented default", Reasoning{}, "medium", true},
		{"case and padding are normalised", Reasoning{Enabled: true, Level: "  HIGH "}, "high", true},
		{"enabled with off falls back to medium", Reasoning{Enabled: true, Level: "off"}, "medium", true},
		{"a level implies enabled", Reasoning{Enabled: false, Level: "low"}, "low", true},
		{"off stays off", Reasoning{Enabled: false, Level: "off"}, "off", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := Default()
			c.LLM.Reasoning = tc.given
			if err := c.validate(false); err != nil {
				t.Fatalf("validate: %v", err)
			}
			if c.LLM.Reasoning.Level != tc.level {
				t.Errorf("level = %q, want %q", c.LLM.Reasoning.Level, tc.level)
			}
			if c.LLM.Reasoning.Enabled != tc.enabled {
				t.Errorf("enabled = %v, want %v", c.LLM.Reasoning.Enabled, tc.enabled)
			}
		})
	}
}

// TestReasoningEnvironmentOverrides: the two variables are read together, and an
// unsupported level is refused with a message naming the accepted words.
func TestReasoningEnvironmentOverrides(t *testing.T) {
	t.Setenv("STARLIGHT_LLM_REASONING_ENABLED", "yes")
	t.Setenv("STARLIGHT_LLM_REASONING_LEVEL", "high")
	c := Default()
	if err := ApplyEnvironment(&c); err != nil {
		t.Fatalf("ApplyEnvironment: %v", err)
	}
	if !c.LLM.Reasoning.Enabled || c.LLM.Reasoning.Level != "high" {
		t.Errorf("reasoning = %+v", c.LLM.Reasoning)
	}

	// The boolean accepts the usual spellings and refuses anything else by being
	// false, which is the conservative reading.
	t.Setenv("STARLIGHT_LLM_REASONING_ENABLED", "on")
	c = Default()
	if err := ApplyEnvironment(&c); err != nil {
		t.Fatalf("ApplyEnvironment: %v", err)
	}
	if !c.LLM.Reasoning.Enabled {
		t.Error("'on' must enable reasoning")
	}
	t.Setenv("STARLIGHT_LLM_REASONING_ENABLED", "maybe")
	c = Default()
	if err := ApplyEnvironment(&c); err != nil {
		t.Fatalf("ApplyEnvironment: %v", err)
	}
	if c.LLM.Reasoning.Enabled {
		t.Error("an unrecognised value must not enable reasoning")
	}

	t.Setenv("STARLIGHT_LLM_REASONING_LEVEL", "extreme")
	if err := ApplyEnvironment(&c); err == nil {
		t.Error("an unsupported level must be refused")
	}
}
