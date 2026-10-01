package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/madkoding/motita/internal/oauth"
)

// TestOpenAIKeyIsNotHandedToOtherVendors: an OPENAI_API_KEY exported for another
// tool used to become the Anthropic or Gemini key too, which the vendor refused.
func TestOpenAIKeyIsNotHandedToOtherVendors(t *testing.T) {
	t.Setenv("MOTITA_AUTH_DIR", t.TempDir())
	for _, v := range []string{"MOTITA_LLM_API_KEY", "ANTHROPIC_API_KEY", "GEMINI_API_KEY", "DASHSCOPE_API_KEY", "OLLAMA_API_KEY"} {
		t.Setenv(v, "")
		os.Unsetenv(v)
	}
	t.Setenv("OPENAI_API_KEY", "sk-openai")
	for provider, want := range map[string]string{"openai": "sk-openai", "codex": "sk-openai", "anthropic": "", "gemini": "", "qwen": "", "ollama": ""} {
		c := Default()
		c.LLM.Provider = provider
		c.LLM.APIKey = ""
		if err := applyLLMEnvironment(&c); err != nil {
			t.Fatal(err)
		}
		if c.LLM.APIKey != want {
			t.Errorf("%s: key = %q, want %q", provider, c.LLM.APIKey, want)
		}
		if got := ProviderKeyFromEnv(provider); got != want {
			t.Errorf("%s: ProviderKeyFromEnv = %q", provider, got)
		}
	}
	t.Setenv("ANTHROPIC_API_KEY", "sk-ant")
	c := Default()
	c.LLM.Provider, c.LLM.APIKey = "anthropic", ""
	_ = applyLLMEnvironment(&c)
	if c.LLM.APIKey != "sk-ant" {
		t.Errorf("anthropic must read ANTHROPIC_API_KEY, got %q", c.LLM.APIKey)
	}
}

func TestLLMNeedsKey(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("MOTITA_AUTH_DIR", dir)
	cases := []struct {
		l    LLM
		want bool
	}{
		{LLM{Provider: "openai"}, true},
		{LLM{Provider: "claude-code"}, false},
		{LLM{Provider: "ollama"}, true},
		{LLM{Provider: "ollama", BaseURL: "https://ollama.com/v1"}, true},
		{LLM{Provider: "ollama", BaseURL: "http://localhost:11434/v1"}, false},
		{LLM{Provider: "ollama", BaseURL: "http://192.168.1.20:8080/v1"}, false},
		{LLM{Provider: "ollama", BaseURL: "http://gpu-box:11434/v1"}, false},
		{LLM{Provider: "ollama", BaseURL: "https://api.openai.com/v1"}, true},
		{LLM{Provider: "qwen"}, true},
		{LLM{Provider: "anthropic"}, true},
	}
	for _, tc := range cases {
		if got := LLMNeedsKey(tc.l); got != tc.want {
			t.Errorf("%+v: %v", tc.l, got)
		}
	}
	if err := oauth.SaveCredential(dir, oauth.Credential{Provider: "qwen", RefreshToken: "r"}); err != nil {
		t.Fatal(err)
	}
	if LLMNeedsKey(LLM{Provider: "qwen"}) || !HasLogin("qwen") {
		t.Error("a stored login must stand in for the key")
	}
	_ = oauth.SaveCredential(dir, oauth.Credential{Provider: "anthropic", RefreshToken: "r"})
	if !LLMNeedsKey(LLM{Provider: "anthropic"}) {
		t.Error("anthropic has no login: a file must not stand in for its key")
	}
	t.Setenv("MOTITA_AUTH_DIR", "")
	t.Setenv("HOME", "/home/someone")
	if AuthDir() != filepath.Join("/home/someone", ".motita", "auth") {
		t.Errorf("AuthDir = %q", AuthDir())
	}
}

func TestEveryProviderIsValid(t *testing.T) {
	for _, p := range Providers {
		c := Default()
		c.LLM.Provider, c.LLM.APIKey = p, "k"
		if err := c.validateLLM(true); err != nil {
			t.Errorf("%s: %v", p, err)
		}
	}
}

func TestProviderKeyFromEnvReadsTheProvidersOwnVariable(t *testing.T) {
	t.Setenv("DASHSCOPE_API_KEY", " sk-dash ")
	t.Setenv("OPENAI_API_KEY", "")
	if got := ProviderKeyFromEnv("qwen"); got != "sk-dash" {
		t.Errorf("qwen = %q", got)
	}
	t.Setenv("MOTITA_LLM_API_KEY", "generic")
	if got := ProviderKeyFromEnv("openai"); got != "" {
		t.Errorf("the generic variable belongs to the configured provider only, got %q", got)
	}
}

func TestAuthDirWithoutAHome(t *testing.T) {
	t.Setenv("MOTITA_AUTH_DIR", "")
	t.Setenv("HOME", "")
	restore := osUserHomeDir
	osUserHomeDir = func() (string, error) { return "", os.ErrNotExist }
	defer func() { osUserHomeDir = restore }()
	if got := AuthDir(); got != "" {
		t.Errorf("AuthDir = %q, want none rather than a relative directory", got)
	}
}

func TestIsSelfHostedOllamaEdges(t *testing.T) {
	cases := map[string]bool{
		"://bad":                              false,
		"localhost-without-scheme":            false,
		"https://ollama.example.com:11434/v1": true,
		"http://box.local/v1":                 true,
		"http://[fe80::1]/v1":                 true,
		"https://example.com/v1":              false,
	}
	for base, want := range cases {
		if got := IsSelfHostedOllama(LLM{Provider: "ollama", BaseURL: base}); got != want {
			t.Errorf("%s: %v", base, got)
		}
	}
	if IsSelfHostedOllama(LLM{Provider: "openai", BaseURL: "http://localhost:11434/v1"}) {
		t.Error("only ollama can be self-hosted Ollama")
	}
}

func TestMissingKeyForALoginProviderSuggestsTheLogin(t *testing.T) {
	t.Setenv("MOTITA_AUTH_DIR", t.TempDir())
	c := Default()
	c.LLM.Provider, c.LLM.APIKey = "codex", ""
	err := c.validateLLM(true)
	if err == nil || !strings.Contains(err.Error(), "motita -init") {
		t.Errorf("err = %v", err)
	}
}
