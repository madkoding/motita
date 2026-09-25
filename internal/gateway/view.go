package gateway

import (
	"os"
	"strings"

	"github.com/madkoding/motita/internal/config"
	"github.com/madkoding/motita/internal/onboard"
)

// RedactedKey is what a front end is given in place of a configured API key.
//
// It exists because Service.Config returns a config.Config, which HAS a key field, and the
// front end has to put something in it. Nothing is lost: the only question any front end asks
// of that field is whether it is EMPTY, which is what turns the status dot red in the terminal
// interface and what a client draws as "no key".
//
// Front ends must test emptiness only. Printing this value would be printing a lie.
const RedactedKey = "<set>"

// configView is what a front end is told about the configuration.
//
// config.Config itself is never serialised. It carries llm.api_key, and a struct that holds a
// secret must not be the thing handed to a network encoder, because the day somebody adds a
// field the encoder will carry that too. What is here is exactly what a front end draws, so
// adding to it is a decision rather than an accident.
//
// This is the ONE place the two rules meet, and it is why the type exists at all: the DTO
// cannot carry a field it does not declare, so the leak is prevented by construction instead
// of by remembering.
type configView struct {
	Provider      string `json:"provider"`
	Model         string `json:"model"`
	Reasoning     string `json:"reasoning"`
	ReasoningOn   bool   `json:"reasoning_enabled"`
	APIKeyPresent bool   `json:"api_key_present"`
}

// viewOf reduces a configuration to what may leave the process.
func viewOf(cfg config.Config) configView {
	return configView{
		Provider:      cfg.LLM.Provider,
		Model:         cfg.LLM.Model,
		Reasoning:     cfg.LLM.Reasoning.Level,
		ReasoningOn:   cfg.LLM.Reasoning.Enabled,
		APIKeyPresent: cfg.LLM.APIKey != "",
	}
}

// configFromView rebuilds the part of a configuration a front end reads.
//
// The rest of the configuration is the default, because a front end does not act on it: the
// agent that acts lives behind the gateway. Only the fields a front end draws are carried
// back, and only so that a client can answer Config() with something truthful.
func configFromView(v configView) config.Config {
	cfg := config.Default()
	cfg.LLM.Provider = v.Provider
	cfg.LLM.Model = v.Model
	cfg.LLM.Reasoning.Level = v.Reasoning
	cfg.LLM.Reasoning.Enabled = v.ReasoningOn
	if v.APIKeyPresent {
		// The sentinel, never a key: this side of the wire has no key to carry.
		cfg.LLM.APIKey = RedactedKey
	}
	return cfg
}

// providerInfo is one entry in the providers list a front end draws.
type providerInfo struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Models      []string `json:"models"`
	FetchModels bool     `json:"fetch_models"`
	KeyPresent  bool     `json:"key_present"`
	IsCurrent   bool     `json:"is_current"`
}

// configuredProviders returns the providers a front end may switch to: those
// whose key is available (env var or YAML), plus the current one even when its
// key is missing (so the user can see what is active).
func configuredProviders(cfg config.Config) []providerInfo {
	providers := onboard.Providers()
	out := make([]providerInfo, 0, len(providers))
	for _, p := range providers {
		keyPresent := providerKeyPresent(cfg, p.ID)
		isCurrent := strings.EqualFold(cfg.LLM.Provider, p.ID)
		if !keyPresent && !isCurrent {
			continue
		}
		models := make([]string, 0, len(p.Models))
		for _, m := range p.Models {
			models = append(models, m.ID)
		}
		out = append(out, providerInfo{
			ID:          p.ID,
			Name:        p.Name,
			Models:      models,
			FetchModels: p.FetchModels,
			KeyPresent:  keyPresent,
			IsCurrent:   isCurrent,
		})
	}
	return out
}

// providerKeyPresent reports whether a key is available for the given provider:
// either its provider-specific env var is set, or the YAML api_key is set and
// the provider uses the generic MOTITA_LLM_API_KEY variable.
func providerKeyPresent(cfg config.Config, providerID string) bool {
	if cfg.LLM.APIKey != "" && config.ProviderKeyVariable(providerID) == "MOTITA_LLM_API_KEY" {
		return true
	}
	if _, ok := os.LookupEnv(config.ProviderKeyVariable(providerID)); ok {
		return true
	}
	return false
}
