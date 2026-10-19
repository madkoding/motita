package gateway

import (
	"testing"

	"github.com/madkoding/motita/internal/config"
)

// TestAnOllamaCloudKeyDoesNotMakeTheLocalOllamaFollowed: both Ollama entries read OLLAMA_API_KEY,
// so a cloud key alone must not list (or ask the cloud for the quotas of) the entry for the user's
// own server.
func TestAnOllamaCloudKeyDoesNotMakeTheLocalOllamaFollowed(t *testing.T) {
	srv := usageServer(t)
	t.Setenv("OLLAMA_API_KEY", "cloud-key-0123456789abcdef")
	v := readUsage(t, srv)
	if usageRow(v, "ollama") != nil {
		t.Error("the local Ollama must not show usage because Ollama Cloud has a key")
	}
	if usageRow(v, "ollama-cloud") == nil {
		t.Error("Ollama Cloud keeps its row")
	}
}

// TestAConfiguredOllamaIsFollowedOnlyWhenItReachesTheCloud: a configuration naming "ollama" with no
// base_url has always reached Ollama Cloud; one pointing at a server of its own has no usage.
func TestAConfiguredOllamaIsFollowedOnlyWhenItReachesTheCloud(t *testing.T) {
	for base, want := range map[string]bool{"": true, "http://localhost:11434/v1": false} {
		srv := usageServer(t)
		t.Setenv("OLLAMA_API_KEY", "cloud-key-0123456789abcdef")
		cfg := srv.opts.Service.Config()
		cfg.LLM.Provider, cfg.LLM.BaseURL = "ollama", base
		srv.opts.Service.(*fakeService).cfg = cfg
		if got := usageRow(readUsage(t, srv), "ollama") != nil; got != want {
			t.Errorf("base %q: row = %v, want %v", base, got, want)
		}
	}
}

// TestACloudKeyIsNeverTheLocalOllamasKey: the key of Ollama Cloud is not sent to a server of one's
// own, and an Ollama with no endpoint saved is the local address, not the cloud's.
func TestACloudKeyIsNeverTheLocalOllamasKey(t *testing.T) {
	llmEnv(t, "")
	t.Setenv("OLLAMA_API_KEY", "cloud-key-0123456789abcdef")
	configured := config.LLM{Provider: "anthropic"}
	if got := effectiveBase(configured, "ollama"); got != ollamaLocalURL {
		t.Errorf("local Ollama base = %q", got)
	}
	if got := effectiveKey(configured, "ollama"); got != "" {
		t.Errorf("the cloud key reached the local Ollama: %q", got)
	}
	if got := effectiveKey(configured, "ollama-cloud"); got == "" {
		t.Error("Ollama Cloud keeps its key")
	}
	legacy := config.LLM{Provider: "ollama", APIKey: "k"}
	if got := effectiveBase(legacy, "ollama"); got != "https://ollama.com/v1" {
		t.Errorf("a legacy configuration keeps reaching the cloud: %q", got)
	}
	if got := effectiveKey(legacy, "ollama"); got != "k" {
		t.Errorf("legacy cloud key = %q", got)
	}
	own := config.LLM{Provider: "ollama", BaseURL: ollamaLocalURL, APIKey: "k"}
	if got := effectiveKey(own, "ollama"); got != "" {
		t.Errorf("a configured local Ollama sends no key: %q", got)
	}
	if got := effectiveOwnBase(own, "ollama"); got != ollamaLocalURL {
		t.Errorf("own base = %q", got)
	}
}
