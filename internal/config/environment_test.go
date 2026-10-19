package config

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestTheSkillsDirectoryIsOverridable: the library is where a user keeps their procedures, so
// where it lives has to be configurable the same way as everything else.
func TestTheSkillsDirectoryIsOverridable(t *testing.T) {
	t.Setenv("MOTITA_SKILLS_DIR", "/tmp/my-skills")

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
	t.Setenv("MOTITA_SKILLS_MAX_FILE_BYTES", "2048")

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
	t.Setenv("MOTITA_SKILLS_MAX_FILE_BYTES", "not-a-number")

	cfg := Default()
	err := ApplyEnvironment(&cfg)
	if err == nil {
		t.Fatal("a malformed number must be reported")
	}
	if !strings.Contains(err.Error(), "MOTITA_SKILLS_MAX_FILE_BYTES") {
		t.Errorf("the error must name the variable: %v", err)
	}
}

func TestReportsUsageSkipsOnlyASelfHostedOllama(t *testing.T) {
	other := filepath.Join(t.TempDir(), "motita.yaml")
	if err := SetCredential(other, ProviderBaseURLVariable("ollama"), "http://box:11434/v1"); err != nil {
		t.Fatal(err)
	}
	UseCredentialsBeside(other)
	t.Cleanup(func() { UseCredentialsBeside(File()) })
	if ReportsUsage("ollama") || !ReportsUsage("ollama-cloud") || !ReportsUsage("openai") {
		t.Error("only the self-hosted Ollama has no usage")
	}
}

func TestKeyForEndpointWithholdsTheKeyFromASelfHostedOllama(t *testing.T) {
	cases := []struct{ provider, base, want string }{
		{"ollama", "http://localhost:11434/v1", ""},
		{"ollama", "http://box.lan:11434/v1", ""},
		{"ollama", "https://ollama.com/v1", "k"},
		{"ollama-cloud", "https://ollama.com/v1", "k"},
		{"openai", "http://localhost:8080/v1", "k"},
	}
	for _, c := range cases {
		if got := KeyForEndpoint(c.provider, c.base, "k"); got != c.want {
			t.Errorf("%s at %s: key = %q, want %q", c.provider, c.base, got, c.want)
		}
	}
}

// TestASharedOllamaKeyDoesNotShowUpOnTheLocalProvider: OLLAMA_API_KEY is Ollama Cloud's, so the
// self-hosted entry reports "not needed" instead of claiming the cloud key as its own.
func TestASharedOllamaKeyDoesNotShowUpOnTheLocalProvider(t *testing.T) {
	t.Setenv("OLLAMA_API_KEY", "cloud-key")
	cfgPath := filepath.Join(t.TempDir(), "motita.yaml")
	if err := SetCredential(cfgPath, ProviderBaseURLVariable("ollama"), "http://localhost:11434/v1"); err != nil {
		t.Fatal(err)
	}
	UseCredentialsBeside(cfgPath)
	t.Cleanup(func() { UseCredentialsBeside(File()) })
	if got := KeySource(cfgPath, LLM{Provider: "anthropic"}, "ollama"); got != KeyNotNeeded {
		t.Errorf("local Ollama key source = %q", got)
	}
	local := LLM{Provider: "ollama", BaseURL: "http://localhost:11434/v1"}
	if got := KeySource(cfgPath, local, "ollama"); got != KeyNotNeeded {
		t.Errorf("configured local Ollama key source = %q", got)
	}
	if got := KeySource(cfgPath, LLM{Provider: "anthropic"}, "ollama-cloud"); got != KeyEnv {
		t.Errorf("Ollama Cloud key source = %q", got)
	}
}
