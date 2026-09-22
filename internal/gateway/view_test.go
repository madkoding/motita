package gateway

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/madkoding/starlight/internal/config"
)

// THE test of this file, and the reason configView exists at all. config.Config holds
// llm.api_key, so it must never be the value handed to a JSON encoder: a redaction that is a
// habit is a redaction that gets forgotten.
func TestTheConfigViewNeverCarriesTheAPIKey(t *testing.T) {
	const canary = "sk-canary-must-not-appear-0123456789"

	cfg := config.Default()
	cfg.LLM.APIKey = canary
	cfg.LLM.Provider = "openai"
	cfg.LLM.Model = "gpt-4o-mini"
	cfg.LLM.Reasoning.Enabled = true
	cfg.LLM.Reasoning.Level = "high"

	data, err := json.Marshal(viewOf(cfg))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	body := string(data)

	if strings.Contains(body, canary) {
		t.Fatalf("the API key reached the wire: %s", body)
	}
	if strings.Contains(body, "api_key\"") {
		t.Errorf("the key field must not exist at all, only its presence: %s", body)
	}

	var got configView
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	want := configView{
		Provider:      "openai",
		Model:         "gpt-4o-mini",
		Reasoning:     "high",
		ReasoningOn:   true,
		APIKeyPresent: true,
	}
	if got != want {
		t.Errorf("view = %+v, want %+v", got, want)
	}
}

// The stronger form of the same claim: no encoding of a whole Config may be reachable from the
// view. Marshalling the view is what a front end gets, and it must be a strict subset.
func TestTheViewIsSmallerThanTheConfiguration(t *testing.T) {
	cfg := config.Default()
	cfg.LLM.APIKey = "sk-canary"
	view, err := json.Marshal(viewOf(cfg))
	if err != nil {
		t.Fatalf("marshal view: %v", err)
	}
	whole, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("marshal config: %v", err)
	}
	if len(view) >= len(whole) {
		t.Errorf("the view (%d bytes) is not smaller than the configuration (%d bytes)", len(view), len(whole))
	}
	// And this documents WHY: the whole struct does leak it, which is what makes the DTO
	// mandatory rather than tidy.
	if !strings.Contains(string(whole), "sk-canary") {
		t.Skip("config.Config no longer serialises api_key; the DTO is still correct, review it")
	}
}

func TestTheViewReportsAMissingKey(t *testing.T) {
	cfg := config.Default()
	cfg.LLM.APIKey = ""
	if viewOf(cfg).APIKeyPresent {
		t.Error("api_key_present must be false when no key is configured")
	}
}

// The front end rebuilds a config.Config from the view. The only thing any front end asks of
// the key field is whether it is EMPTY (the status dot turns red when it is), so the sentinel
// is documented rather than clever and this pins the contract.
func TestConfigFromViewPutsASentinelWhereTheKeyWas(t *testing.T) {
	cfg := configFromView(configView{Provider: "ollama", Model: "m", Reasoning: "low", APIKeyPresent: true})
	if cfg.LLM.APIKey != RedactedKey {
		t.Errorf("api key = %q, want the sentinel %q", cfg.LLM.APIKey, RedactedKey)
	}
	if RedactedKey == "" {
		t.Error("the sentinel must be non-empty: the status bar turns red on an empty key")
	}

	empty := configFromView(configView{Provider: "ollama", Model: "m", Reasoning: "low"})
	if empty.LLM.APIKey != "" {
		t.Errorf("no key must stay no key, got %q", empty.LLM.APIKey)
	}
	if empty.LLM.Provider != "ollama" || empty.LLM.Model != "m" || empty.LLM.Reasoning.Level != "low" {
		t.Errorf("the fields a front end draws must survive: %+v", empty.LLM)
	}
	if empty.LLM.Reasoning.Enabled {
		t.Error("reasoning_enabled must survive as false")
	}
}

// The reasoning flag travels independently of the level, because a front end cycles the LEVEL
// and draws whether it is ON: the two are different questions and a view that conflated them
// would show "high" reasoning as off.
func TestTheReasoningFlagIsCarriedSeparately(t *testing.T) {
	in := configView{Provider: "openai", Model: "m", Reasoning: "off", ReasoningOn: false}
	if cfg := configFromView(in); cfg.LLM.Reasoning.Enabled || cfg.LLM.Reasoning.Level != "off" {
		t.Errorf("reasoning = %+v, want off/disabled", cfg.LLM.Reasoning)
	}
	in = configView{Provider: "openai", Model: "m", Reasoning: "high", ReasoningOn: true}
	if cfg := configFromView(in); !cfg.LLM.Reasoning.Enabled || cfg.LLM.Reasoning.Level != "high" {
		t.Errorf("reasoning = %+v, want high/enabled", cfg.LLM.Reasoning)
	}
}
