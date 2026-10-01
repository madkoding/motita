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
	// Short is the name in the wizard's menu, where the column beside it says how to connect.
	Short string
	// Access says, in a few words, what the user needs to connect: an API key, a login, nothing.
	// It is what a newcomer actually chooses by - most people know which account they HAVE, not
	// which protocol a vendor speaks.
	Access string
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
	// SupportsDirectAuth, when true, means the wizard offers a "connect
	// directly" option in addition to pasting an API key. The user visits a
	// URL (and enters a code), and the resulting login is stored and renewed
	// instead of a key.
	SupportsDirectAuth bool
	// AskBaseURL, when true, asks for the endpoint: the provider's protocol is
	// spoken by many hosts (OpenAI-compatible). The others have one endpoint, or
	// a login that names its own, and asking would only invite a wrong answer.
	AskBaseURL bool
	// Login, when set, is the command that logs the provider's own tool in: the
	// provider needs no key of motita's, so the wizard asks for no endpoint and no
	// key, writes no credentials file, and tells the user to run this instead.
	Login string
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
			Name:           "OpenAI (platform key)",
			Short:          "OpenAI",
			Access:         "API key from platform.openai.com",
			DefaultBaseURL: "https://api.openai.com/v1",
			EnvKey:         "MOTITA_LLM_API_KEY",
			ConsoleURL:     "https://platform.openai.com/api-keys",
			AskBaseURL:     true,
			Models: []Model{
				{ID: "gpt-5-mini", Label: "GPT-5 mini", Note: "cheap and fast, the right default"},
				{ID: "gpt-5", Label: "GPT-5", Note: "strongest reasoning, more expensive"},
				{ID: "gpt-4.1", Label: "GPT-4.1", Note: "no reasoning step, honours temperature"},
				{ID: "gpt-4o-mini", Label: "GPT-4o mini", Note: "previous generation, cheapest"},
			},
		},
		{
			// Codex models are served by /responses only; with a ChatGPT login the
			// requests are billed to the user's ChatGPT plan instead of API credit.
			ID:                 "codex",
			Name:               "OpenAI Codex (ChatGPT plan or OpenAI key)",
			Short:              "OpenAI Codex",
			Access:             "sign in with ChatGPT, or an API key",
			DefaultBaseURL:     "https://api.openai.com/v1",
			EnvKey:             "MOTITA_LLM_API_KEY",
			ConsoleURL:         "https://platform.openai.com/api-keys",
			SupportsDirectAuth: true,
			Models: []Model{
				{ID: "gpt-5.2-codex", Label: "GPT-5.2 Codex", Note: "agentic coding, strongest"},
				{ID: "gpt-5.1-codex", Label: "GPT-5.1 Codex", Note: "agentic coding, previous gen"},
				{ID: "gpt-5-codex", Label: "GPT-5 Codex", Note: "agentic coding, first gen"},
			},
		},
		{
			ID:                 "copilot",
			Name:               "GitHub Copilot (subscription)",
			Short:              "GitHub Copilot",
			Access:             "sign in with GitHub (Copilot plan)",
			DefaultBaseURL:     "https://api.githubcopilot.com",
			EnvKey:             "GITHUB_COPILOT_TOKEN",
			ConsoleURL:         "https://github.com/settings/copilot",
			SupportsDirectAuth: true,
			// The ids the Copilot API uses, which are NOT the vendors' own: Copilot
			// answers "claude-sonnet-4", and refuses "claude-sonnet-4-20250514".
			Models: []Model{
				{ID: "gpt-4.1", Label: "Copilot: GPT-4.1", Note: "included in every plan"},
				{ID: "gpt-5-mini", Label: "Copilot: GPT-5 mini", Note: "included in every plan"},
				{ID: "claude-sonnet-4", Label: "Copilot: Claude Sonnet 4", Note: "premium requests"},
				{ID: "gemini-2.5-pro", Label: "Copilot: Gemini 2.5 Pro", Note: "premium requests"},
			},
		},
		{
			ID:             "ollama",
			Name:           "Ollama (local or Ollama Cloud)",
			Short:          "Ollama",
			Access:         "free on your own machine, or Ollama Cloud",
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
			// An API key from the Claude Console. A Claude Pro/Max subscription is NOT
			// reached from here: Anthropic only allows it through its own Claude Code
			// client, which is the claude-code provider below.
			ID:             "anthropic",
			Name:           "Anthropic Claude (Console key)",
			Short:          "Anthropic Claude",
			Access:         "API key from console.anthropic.com",
			DefaultBaseURL: "https://api.anthropic.com",
			EnvKey:         "ANTHROPIC_API_KEY",
			ConsoleURL:     "https://console.anthropic.com/settings/keys",
			Models: []Model{
				{ID: "claude-sonnet-5-5", Label: "Claude Sonnet 5.5", Note: "the right default"},
				{ID: "claude-opus-5-5", Label: "Claude Opus 5.5", Note: "strongest, more expensive"},
				{ID: "claude-haiku-4-5", Label: "Claude Haiku 4.5", Note: "cheap and fast"},
			},
		},
		{
			// The local claude CLI on the user's own subscription: motita never sees
			// the credentials, `claude auth login` (Anthropic's own OAuth) holds them.
			ID:         "claude-code",
			Name:       "Claude subscription (Pro/Max, via Claude Code CLI)",
			Short:      "Claude Pro/Max",
			Access:     "your Claude plan, via the Claude Code CLI",
			ConsoleURL: "https://code.claude.com/docs/en/setup",
			Login:      "claude auth login",
			Models: []Model{
				{ID: "sonnet", Label: "Sonnet", Note: "the right default"},
				{ID: "opus", Label: "Opus"},
				{ID: "haiku", Label: "Haiku"},
				{ID: "fable", Label: "Fable"},
			},
		},
		{
			ID:                 "gemini",
			Name:               "Google Gemini (AI Studio key or Google login)",
			Short:              "Google Gemini",
			Access:             "sign in with Google, or an AI Studio key",
			DefaultBaseURL:     "https://generativelanguage.googleapis.com",
			EnvKey:             "GEMINI_API_KEY",
			ConsoleURL:         "https://aistudio.google.com/apikey",
			SupportsDirectAuth: true,
			Models: []Model{
				{ID: "gemini-2.5-flash", Label: "Gemini 2.5 Flash", Note: "cheap and fast"},
				{ID: "gemini-2.5-pro", Label: "Gemini 2.5 Pro", Note: "strong reasoning"},
				{ID: "gemini-2.5-flash-lite", Label: "Gemini 2.5 Flash-Lite", Note: "cheapest"},
			},
		},
		{
			// A DashScope (Alibaba Model Studio) key, or a qwen.ai account login.
			ID:                 "qwen",
			Name:               "Qwen (qwen.ai login or DashScope key)",
			Short:              "Qwen",
			Access:             "sign in with qwen.ai, or a DashScope key",
			DefaultBaseURL:     "https://dashscope-intl.aliyuncs.com/compatible-mode/v1",
			EnvKey:             "DASHSCOPE_API_KEY",
			ConsoleURL:         "https://modelstudio.console.alibabacloud.com/?tab=api#/api-key",
			SupportsDirectAuth: true,
			Models: []Model{
				{ID: "qwen3-coder-plus", Label: "Qwen3 Coder Plus", Note: "agentic coding, the right default"},
				{ID: "qwen3-coder-flash", Label: "Qwen3 Coder Flash", Note: "faster and cheaper"},
				{ID: "qwen-max", Label: "Qwen Max", Note: "general reasoning (DashScope key)"},
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

// String renders a provider for the menu.
func (p Provider) String() string {
	return fmt.Sprintf("%s (%s)", p.Name, p.ID)
}
