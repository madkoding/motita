package llm

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/madkoding/motita/internal/config"
)

// PER-MODEL SAMPLING PRESETS.
//
// Every request used to go out with one temperature (llm.temperature, or 0.2) whatever model it
// was for. The model cards say otherwise: Qwen3 thinks at 0.6 / top_p 0.95 / top_k 20 and loops
// at lower values, Gemini 3 is meant to stay at 1.0, Gemma 3 at 1.0 / top_k 64. The preset is
// looked up from the model of each request (the chat, a task, the summary model each have their
// own client), so changing the model changes the sampling with it.
//
// A temperature the user set (llm.temperature, or the custom model's) still wins; the preset's
// other fields apply anyway. A model with no preset keeps the old 0.2.

// Sampling is one model's recommended sampling parameters; nil leaves the server's default.
type Sampling struct {
	Temperature     *float64 `json:"temperature,omitempty"`
	TopP            *float64 `json:"top_p,omitempty"`
	TopK            *float64 `json:"top_k,omitempty"`
	MinP            *float64 `json:"min_p,omitempty"`
	PresencePenalty *float64 `json:"presence_penalty,omitempty"`
}

// samplingPreset is one family of model ids and its sampling.
type samplingPreset struct {
	name  string
	match func(model string) bool
	s     Sampling
}

func fp(v float64) *float64 { return &v }

// containsAll reports whether model contains every one of parts.
func containsAll(model string, parts ...string) bool {
	for _, p := range parts {
		if !strings.Contains(model, p) {
			return false
		}
	}
	return true
}

// containsAny reports whether model contains at least one of parts.
func containsAny(model string, parts ...string) bool {
	for _, p := range parts {
		if strings.Contains(model, p) {
			return true
		}
	}
	return false
}

// Family names written more than once below.
const (
	familyQwen3  = "qwen3"
	familyKimiK2 = "kimi-k2"
	keyTopK      = "top_k"
	keyMinP      = "min_p"
)

// samplingPresets are tried in order, so a narrower family comes before the one that contains it
// (qwen3-coder before qwen3). Values are the ones each model card recommends.
var samplingPresets = []samplingPreset{
	{"qwen3-coder", func(m string) bool { return containsAll(m, familyQwen3, "coder") },
		Sampling{Temperature: fp(0.7), TopP: fp(0.8), TopK: fp(20)}},
	{"qwen3-instruct", func(m string) bool { return containsAll(m, familyQwen3) && containsAny(m, "instruct", "max") },
		Sampling{Temperature: fp(0.7), TopP: fp(0.8), TopK: fp(20), MinP: fp(0)}},
	// Hybrid Qwen3 models think unless told not to: the thinking preset.
	{familyQwen3, func(m string) bool { return containsAll(m, familyQwen3) },
		Sampling{Temperature: fp(0.6), TopP: fp(0.95), TopK: fp(20), MinP: fp(0)}},
	{"qwq", func(m string) bool { return containsAll(m, "qwq") },
		Sampling{Temperature: fp(0.6), TopP: fp(0.95), TopK: fp(40), MinP: fp(0)}},
	{"qwen2.5", func(m string) bool { return containsAny(m, "qwen2.5", "qwen2_5") },
		Sampling{Temperature: fp(0.7), TopP: fp(0.8), TopK: fp(20)}},
	{"deepseek-r1", func(m string) bool { return containsAny(m, "deepseek-r1", "deepseek-reasoner") },
		Sampling{Temperature: fp(0.6), TopP: fp(0.95)}},
	{"gemma-3", func(m string) bool { return containsAny(m, "gemma-3", "gemma3") },
		Sampling{Temperature: fp(1), TopP: fp(0.95), TopK: fp(64), MinP: fp(0)}},
	{"gemini-3", func(m string) bool { return containsAll(m, "gemini-3") },
		Sampling{Temperature: fp(1)}},
	{"gpt-oss", func(m string) bool { return containsAll(m, "gpt-oss") },
		Sampling{Temperature: fp(1), TopP: fp(1)}},
	{"kimi-k2-thinking", func(m string) bool { return containsAll(m, familyKimiK2, "thinking") },
		Sampling{Temperature: fp(1)}},
	{familyKimiK2, func(m string) bool { return containsAll(m, familyKimiK2) },
		Sampling{Temperature: fp(0.6)}},
	{"llama-3", func(m string) bool { return containsAny(m, "llama-3", "llama3") },
		Sampling{Temperature: fp(0.6), TopP: fp(0.9)}},
	{"mistral-small", func(m string) bool { return containsAny(m, "mistral-small", "devstral") },
		Sampling{Temperature: fp(0.15)}},
}

// localPresencePenalty is added for Motita's own local model: a 2-bit quantised Qwen3 repeats
// itself without it, and Qwen recommends 1.5 for quantised weights.
const localPresencePenalty = 1.5

// PresetFor is the sampling preset of a provider's model and the name of its family, "" when
// the model has none. Anthropic and claude-code get none: current Claude models refuse sampling
// parameters (see acceptsTemperature).
func PresetFor(provider, model string) (Sampling, string) {
	p := strings.ToLower(provider)
	if p == "anthropic" || p == providerClaudeCode {
		return Sampling{}, ""
	}
	m := bareModel(model)
	for _, preset := range samplingPresets {
		if !preset.match(m) {
			continue
		}
		s := preset.s
		if p == config.ProviderLocal {
			s.PresencePenalty = fp(localPresencePenalty)
		}
		return s, preset.name
	}
	return Sampling{}, ""
}

// sampling is what this client's requests carry: the model's preset, with the temperature the
// user set in place of the preset's, and the old default when there is neither.
func (c *Client) sampling() Sampling {
	s, _ := PresetFor(c.cfg.Provider, c.cfg.Model)
	switch {
	case c.cfg.Temperature != nil:
		s.Temperature = c.cfg.Temperature
	case s.Temperature == nil:
		s.Temperature = fp(defaultTemperature)
	default:
		// The preset's own temperature.
	}
	return s
}

// extraSamplingKeys are the /chat/completions fields past temperature: not every server takes
// them, so they are dropped from a request a server refuses for one of them.
var extraSamplingKeys = []string{"top_p", keyTopK, keyMinP, "presence_penalty"}

// addOpenAISampling puts the preset past temperature on a /chat/completions body. top_k and min_p
// are not OpenAI parameters: its own API is not sent them.
func (c *Client) addOpenAISampling(body map[string]any, s Sampling) {
	if c.noSampling.Load() {
		return
	}
	official := isOfficialOpenAI(c.baseURL(DefaultBaseURL(c.cfg.Provider)))
	for key, v := range map[string]*float64{
		"top_p": s.TopP, keyTopK: s.TopK, keyMinP: s.MinP, "presence_penalty": s.PresencePenalty,
	} {
		if v == nil || (official && (key == keyTopK || key == keyMinP)) {
			continue
		}
		body[key] = *v
	}
}

// refusedSampling reports whether a refusal names one of the extra sampling fields data carries,
// and if so stops sending them: data is then asked once more without them.
func (c *Client) refusedSampling(data *[]byte, status int, answer []byte) bool {
	if status != http.StatusBadRequest {
		return false
	}
	var body map[string]any
	if json.Unmarshal(*data, &body) != nil {
		return false
	}
	lower := bytes.ToLower(answer)
	named := false
	for _, k := range extraSamplingKeys {
		if _, sent := body[k]; sent && bytes.Contains(lower, []byte(k)) {
			named = true
		}
	}
	if !named {
		return false
	}
	for _, k := range extraSamplingKeys {
		delete(body, k)
	}
	stripped, _ := json.Marshal(body)
	*data = stripped
	c.noSampling.Store(true)
	c.log.Warn("the provider refused a sampling field of the model's preset; asking again without them",
		"model", c.cfg.Model)
	return true
}

// geminiSampling puts the preset on a Gemini generationConfig.
func (c *Client) geminiSampling(generation map[string]any) {
	s := c.sampling()
	generation["temperature"] = *s.Temperature
	if s.TopP != nil {
		generation["topP"] = *s.TopP
	}
	if s.TopK != nil {
		generation["topK"] = *s.TopK
	}
}
