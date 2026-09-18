// Package onboard implements the first-run wizard: it asks which provider and
// which model to use, writes a working configuration and explains what is left to
// do. It is a package of its own so the whole conversation can be tested without a
// terminal.
//
// The catalogue below mirrors exactly what internal/llm implements. A provider
// that the client cannot talk to must never appear here: offering it would be a
// promise the program cannot keep.
package onboard

import (
	"fmt"
	"sort"
	"strings"
)

// Provider is an LLM endpoint the client knows how to talk to.
type Provider struct {
	// ID is the value written to llm.provider.
	ID string
	// Name is what the user sees.
	Name string
	// DefaultBaseURL is written to llm.base_url.
	DefaultBaseURL string
	// EnvKey is the variable the key is read from, for the instructions.
	EnvKey string
	// Where the key is obtained.
	ConsoleURL string
	// Models are the ones known to work with this provider, best first.
	Models []Model
	// FetchModels, when true, tells the wizard to query the provider's own API
	// for the list of available models instead of using the static catalogue.
	FetchModels bool
}

// Model is a model the wizard can offer.
type Model struct {
	ID    string
	Label string
	// Note is a short reason to pick it, shown next to the label.
	Note string
}

// Providers returns the supported providers, in the order they are offered.
func Providers() []Provider {
	return []Provider{
		{
			ID:             "openai",
			Name:           "OpenAI-compatible",
			DefaultBaseURL: "https://api.openai.com/v1",
			EnvKey:         "STARLIGHT_LLM_API_KEY",
			ConsoleURL:     "https://platform.openai.com/api-keys",
			Models: []Model{
				{ID: "gpt-4o-mini", Label: "GPT-4o mini", Note: "cheap and fast, the right default for OpenAI"},
				{ID: "gpt-4o", Label: "GPT-4o", Note: "better reasoning, more expensive"},
				{ID: "gpt-4.1-mini", Label: "GPT-4.1 mini", Note: "newer small model"},
				{ID: "o4-mini", Label: "o4-mini", Note: "reasoning model, slow and costly"},
			},
		},
		{
			ID:             "ollama",
			Name:           "Ollama Cloud",
			DefaultBaseURL: "https://ollama.com/v1",
			EnvKey:         "OLLAMA_API_KEY",
			ConsoleURL:     "https://ollama.com/settings/keys",
			// Populated at runtime from the live catalogue. The list below is what
			// is offered when the API cannot be reached, so the wizard never leaves
			// the user with an empty menu and no idea what to type.
			Models: []Model{
				{ID: "gpt-oss:120b", Label: "gpt-oss:120b", Note: "open weights, strong general model"},
				{ID: "qwen3.5:397b", Label: "qwen3.5:397b", Note: "large open model"},
				{ID: "deepseek-v4.1-flash", Label: "deepseek-v4.1-flash", Note: "fast and inexpensive"},
				{ID: "glm-5.3", Label: "glm-5.3", Note: "strong reasoning"},
				{ID: "kimi-k2.6", Label: "kimi-k2.6", Note: "long context"},
				{ID: "gemma4:31b", Label: "gemma4:31b", Note: "small and quick"},
				{ID: "nemotron-3-nano:30b", Label: "nemotron-3-nano:30b", Note: "lightweight"},
				{ID: "gpt-oss:20b", Label: "gpt-oss:20b", Note: "lightest, good on an i386 box"},
			},
			FetchModels: true,
		},
		{
			ID:             "anthropic",
			Name:           "Anthropic",
			DefaultBaseURL: "https://api.anthropic.com",
			EnvKey:         "STARLIGHT_LLM_API_KEY",
			ConsoleURL:     "https://console.anthropic.com/settings/keys",
			Models: []Model{
				{ID: "claude-3-5-haiku-latest", Label: "Claude 3.5 Haiku", Note: "cheap and fast"},
				{ID: "claude-sonnet-4-20250514", Label: "Claude Sonnet 4", Note: "strong reasoning"},
				{ID: "claude-3-7-sonnet-latest", Label: "Claude 3.7 Sonnet", Note: "previous generation"},
			},
		},
		{
			ID:             "gemini",
			Name:           "Google Gemini",
			DefaultBaseURL: "https://generativelanguage.googleapis.com",
			EnvKey:         "STARLIGHT_LLM_API_KEY",
			ConsoleURL:     "https://aistudio.google.com/apikey",
			Models: []Model{
				{ID: "gemini-2.0-flash", Label: "Gemini 2.0 Flash", Note: "cheap and fast"},
				{ID: "gemini-2.5-flash", Label: "Gemini 2.5 Flash", Note: "newer, still cheap"},
				{ID: "gemini-2.5-pro", Label: "Gemini 2.5 Pro", Note: "strong reasoning"},
			},
		},
	}
}

// Lookup finds a provider by its ID (case-insensitive).
func Lookup(id string) (Provider, bool) {
	for _, p := range Providers() {
		if strings.EqualFold(p.ID, id) {
			return p, true
		}
	}
	return Provider{}, false
}

// Names lists the provider IDs, for an error message that tells the user what is
// accepted instead of only what is wrong.
func Names() string {
	ids := make([]string, 0, len(Providers()))
	for _, p := range Providers() {
		ids = append(ids, p.ID)
	}
	sort.Strings(ids)
	return strings.Join(ids, ", ")
}

// DefaultBaseURL returns the endpoint of a provider, or "" when it is unknown, so
// a caller can fall back to whatever the configuration already had.
func DefaultBaseURL(id string) string {
	if p, ok := Lookup(id); ok {
		return p.DefaultBaseURL
	}
	return ""
}

// EnvKey returns the variable the instructions should mention for a provider.
func EnvKey(id string) string {
	if p, ok := Lookup(id); ok {
		return p.EnvKey
	}
	return "STARLIGHT_LLM_API_KEY"
}

// String renders a provider for the menu.
func (p Provider) String() string {
	return fmt.Sprintf("%s (%s)", p.Name, p.ID)
}
