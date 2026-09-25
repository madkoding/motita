package session

import "strings"

// Context windows, by model.
//
// These numbers are DEFAULTS, not claims: the provider is the authority, and a model id
// nobody has heard of gets the smallest window rather than a guess. Getting this wrong in
// the optimistic direction is what makes an agent fail mid-task with a provider error, so
// the unknown answer is the conservative one.
//
// The list is deliberately short and readable. It is a compatibility table, not a
// catalogue: it exists so the common models compact at the right moment, and every entry
// names where the number comes from.
var contextWindows = map[string]int{
	// OpenAI. The 128k figure is the published context length for this generation.
	"gpt-4o":      128000,
	"gpt-4o-mini": 128000,
	"gpt-4-turbo": 128000,
	"gpt-4":       8192,
	"gpt-3.5":     16385,

	// Anthropic, whose published windows are these.
	"claude": 200000,

	// The Claude Code aliases the claude-code provider passes to the claude CLI. 200k is
	// the conservative figure: a [1m] route ("sonnet[1m]") matches here too, and its larger
	// window is set explicitly with llm.session.context_window.
	"sonnet": 200000,
	"opus":   200000,
	"haiku":  200000,
	"fable":  200000,
	// claude's own "best" and "default" picks resolve to one of the above.
	"best":    200000,
	"default": 200000,

	// Ollama Cloud and the open models it hosts. These are the values their cards
	// publish; a local Ollama may be configured lower, which is why the configuration
	// can override the window explicitly.
	"llama3":       8192,
	"llama3.1":     128000,
	"llama3.3":     128000,
	"llama4":       128000,
	"qwen2.5":      32768,
	"qwen3":        32768,
	"mistral":      32768,
	"mistral-nemo": 128000,
	"deepseek":     65536,
	"phi4":         16384,
	"phi3":         4096,
	"command-r":    128000,
	"qwq":          32768,
	"nomic":        8192,
	"gemma":        8192,
}

// DefaultContextWindow is what an unrecognised model gets.
//
// 8192 is the safe floor: every model in the list above reaches it, and a wrong guess
// downwards costs a compaction that was not strictly needed, while a wrong guess upwards
// costs a failed task.
const DefaultContextWindow = 8192

// ContextWindow returns the window to plan against for a model id.
//
// Matching is by prefix on purpose: providers append variants ("llama3.1:70b",
// "qwen2.5-72b", "deepseek-v4.1-flash") and an exact-match table would miss every one of
// them and fall back to the floor. The LONGEST matching prefix wins, so "llama3.1" is
// preferred over "llama3" for a model named llama3.1-8b.
func ContextWindow(model string) int {
	id := strings.ToLower(strings.TrimSpace(model))
	if id == "" {
		return DefaultContextWindow
	}
	// A provider may prefix the model with its vendor; the last segment is the name.
	if i := strings.LastIndex(id, "/"); i >= 0 {
		id = id[i+1:]
	}

	best, bestLen := 0, 0
	for prefix, window := range contextWindows {
		if strings.HasPrefix(id, prefix) && len(prefix) > bestLen {
			best, bestLen = window, len(prefix)
		}
	}
	if best == 0 {
		return DefaultContextWindow
	}
	return best
}
