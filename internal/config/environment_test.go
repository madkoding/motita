package config

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestTheSkillsDirectoryIsOverridable: the library is where a user keeps their procedures, so
// where it lives has to be configurable the same way as everything else.
func TestTheSkillsDirectoryIsOverridable(t *testing.T) {
	t.Setenv("STARLIGHT_SKILLS_DIR", "/tmp/my-skills")

	cfg := Default()
	if err := ApplyEnvironment(&cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.Skills.Dir != "/tmp/my-skills" {
		t.Errorf("Skills.Dir = %q, want the variable's value", cfg.Skills.Dir)
	}
}

// TestTheSkillsCapIsOverridable: the cap decides how large a document may be before it would
// cost more context than it returns, so it is a policy the user sets.
func TestTheSkillsCapIsOverridable(t *testing.T) {
	t.Setenv("STARLIGHT_SKILLS_MAX_FILE_BYTES", "2048")

	cfg := Default()
	if err := ApplyEnvironment(&cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.Skills.MaxFileBytes != 2048 {
		t.Errorf("Skills.MaxFileBytes = %d, want 2048", cfg.Skills.MaxFileBytes)
	}
}

// TestAMalformedSkillsCapIsReported: a value that cannot be read must be an error rather than
// a silent fallback to the default. A user who typed a size and got the old one would never
// know their setting was ignored.
func TestAMalformedSkillsCapIsReported(t *testing.T) {
	t.Setenv("STARLIGHT_SKILLS_MAX_FILE_BYTES", "not-a-number")

	cfg := Default()
	err := ApplyEnvironment(&cfg)
	if err == nil {
		t.Fatal("a malformed number must be reported")
	}
	if !strings.Contains(err.Error(), "STARLIGHT_SKILLS_MAX_FILE_BYTES") {
		t.Errorf("the error must name the variable: %v", err)
	}
}

// TestAnotherProviderDoesNotInheritTheOpenAIEndpoint: a provider named without a
// base_url keeps it empty, so the llm package picks that provider's own endpoint
// instead of sending its key to api.openai.com.
func TestAnotherProviderDoesNotInheritTheOpenAIEndpoint(t *testing.T) {
	for _, provider := range []string{"ollama", "anthropic", "gemini"} {
		path := filepath.Join(t.TempDir(), "config.yaml")
		mustWrite(t, path, "llm:\n  provider: "+provider+"\n  model: m\n  api_key: k\n")
		t.Setenv("OPENAI_BASE_URL", "https://openai.example/v1")
		cfg, err := Load(path)
		if err != nil {
			t.Fatal(err)
		}
		if cfg.LLM.BaseURL != "" {
			t.Errorf("%s: base_url = %q, want empty", provider, cfg.LLM.BaseURL)
		}
	}
	t.Setenv("STARLIGHT_LLM_PROVIDER", "ollama")
	cfg, err := LoadOrDefault("")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.LLM.BaseURL != "" {
		t.Errorf("env-only ollama: base_url = %q, want empty", cfg.LLM.BaseURL)
	}
}

// TestOpenAIKeyNeverReplacesTheYAMLKeyOrReachesAnotherProvider: OPENAI_API_KEY is a
// fallback for the openai provider only, never an override of a configured key.
func TestOpenAIKeyNeverReplacesTheYAMLKeyOrReachesAnotherProvider(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "sk-openai-ENV")
	t.Setenv("OPENAI_MODEL", "gpt-env")
	for _, provider := range []string{"openai", "anthropic"} {
		path := filepath.Join(t.TempDir(), "config.yaml")
		mustWrite(t, path, "llm:\n  provider: "+provider+"\n  api_key: sk-YAML\n")
		cfg, err := Load(path)
		if err != nil {
			t.Fatal(err)
		}
		if cfg.LLM.APIKey != "sk-YAML" {
			t.Errorf("%s: api_key = %q, want the YAML key", provider, cfg.LLM.APIKey)
		}
	}
	path := filepath.Join(t.TempDir(), "config.yaml")
	mustWrite(t, path, "llm:\n  provider: anthropic\n")
	if _, err := Load(path); err == nil {
		t.Error("anthropic must not borrow OPENAI_API_KEY")
	}
	cfg, err := LoadOrDefault("")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.LLM.Model != "gpt-env" {
		t.Errorf("openai default: model = %q, want OPENAI_MODEL", cfg.LLM.Model)
	}
}
