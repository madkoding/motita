package llm

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
)

// PROMPT CACHING ON THE PROVIDERS THAT ARE NOT ANTHROPIC (see anthropic.go for that one).
//
// Every provider below caches a repeated PREFIX of the prompt, so what they share is one rule:
// what is the same on every call goes first, and what changes each turn goes after it. Each adds
// one thing of its own:
//
//   - OpenAI (/chat/completions on api.openai.com, and /responses for codex) caches on its own and
//     routes by `prompt_cache_key`: requests with the same key land on the same cache. The key is
//     a hash of the model and the leading system messages, so every call of a run shares it.
//   - Qwen on DashScope has an EXPLICIT cache (`cache_control` on content parts, 10 % of the price
//     on a hit against 20 % for the implicit one, and a hit that is not best-effort) on the models
//     listed in qwenExplicitModels. The marks go on the system prompt, the stable start of the
//     task (Message.Stable) and the last turn, so a tool loop re-reads its history from cache.
//   - Gemini (2.5 and later) and DeepSeek cache implicitly; they need nothing but the order, which
//     gemini.go and responses.go keep by sending only the LEADING system messages as the system
//     prompt: a later one (the per-turn context brief) stays where it is in the conversation
//     instead of changing the very start of every request.
//
// What was read from the cache is counted from each one's usage block (usageparse.go).

// promptCacheKey is the OpenAI routing key of a request: the same for every call that starts
// with the same leading system messages on the same model, "" when there are none.
func promptCacheKey(model string, messages []Message) string {
	h := sha256.New()
	h.Write([]byte(model))
	n := 0
	for _, m := range messages {
		if m.Role != "system" {
			break
		}
		h.Write([]byte{0})
		h.Write([]byte(m.Content))
		n++
	}
	if n == 0 {
		return ""
	}
	return "motita-" + hex.EncodeToString(h.Sum(nil))[:24]
}

// sendsCacheKey reports whether this client's /chat/completions target is OpenAI's own API, the
// one that documents prompt_cache_key. Another server behind the openai provider may refuse an
// unknown field.
func (c *Client) sendsCacheKey() bool {
	return !c.noCache.Load() && isOfficialOpenAI(c.baseURL(DefaultBaseURL(c.cfg.Provider)))
}

// cachedOpenAIMessages converts a conversation for /chat/completions, with DashScope's cache
// marks when the target has the explicit cache.
func (c *Client) cachedOpenAIMessages(messages []Message) []openAIMessage {
	out := toOpenAIMessages(messages)
	if c.explicitQwenCache() {
		markQwenCache(out, messages)
	}
	return out
}

// addCacheKey adds the routing key to a /chat/completions body for OpenAI's own API.
func (c *Client) addCacheKey(body map[string]any, messages []Message) {
	if key := promptCacheKey(c.cfg.Model, messages); key != "" && c.sendsCacheKey() {
		body["prompt_cache_key"] = key
	}
}

// qwenExplicitModels are the DashScope model families with an explicit cache. A family matches
// its dated and suffixed ids too ("qwen-plus-2025-07-28", "qwen3-coder-plus-latest").
var qwenExplicitModels = []string{
	"qwen-plus", "qwen-flash", "qwen-max",
	"qwen3-max", "qwen3-coder-plus", "qwen3-coder-flash", "qwen3-vl-plus",
	"qwen3.5-plus", "qwen3.5-flash", "qwen3.6-plus", "qwen3.6-flash",
	"qwen3.7-max", "qwen3.7-plus", "qwen3.7-flash", "qwen3.8-max", "qwen3.8-flash",
}

// explicitQwenCache reports whether this client's requests carry DashScope cache marks: the
// target is DashScope (the qwen provider, or another one pointed at it) and the model has the
// explicit cache.
func (c *Client) explicitQwenCache() bool {
	if c.noCache.Load() {
		return false
	}
	base := c.baseURL(DefaultBaseURL(c.cfg.Provider))
	u, err := url.Parse(base)
	if err != nil || !strings.Contains(strings.ToLower(u.Hostname()), "dashscope") {
		return false
	}
	model := bareModel(c.cfg.Model)
	for _, family := range qwenExplicitModels {
		if model == family || strings.HasPrefix(model, family+"-") {
			return true
		}
	}
	return false
}

// markQwenCache puts the explicit cache marks on a converted conversation (out mirrors in):
// the leading system message, the stable start of the first turn that has one, and the last
// turn with text. DashScope keeps the last four marks; this places three at most.
func markQwenCache(out []openAIMessage, in []Message) {
	if len(in) > 0 && in[0].Role == "system" {
		out[0].Content = markLast(asParts(out[0].Content))
	}
	for i, m := range in {
		if m.Stable <= 0 || m.Stable >= len(m.Content) || len(m.Images) > 0 {
			continue
		}
		head, tail := m.Content[:m.Stable], m.Content[m.Stable:]
		if strings.TrimSpace(head) != "" && strings.TrimSpace(tail) != "" {
			out[i].Content = []map[string]any{
				{"type": "text", "text": head, "cache_control": ephemeral},
				{"type": "text", "text": tail},
			}
		}
		break
	}
	for i := len(in) - 1; i >= 0; i-- {
		if in[i].Role != "assistant" && strings.TrimSpace(in[i].Content) != "" {
			out[i].Content = markLast(asParts(out[i].Content))
			return
		}
	}
}

// asParts is a message content as a list of parts: a plain text becomes one text part.
func asParts(content any) []map[string]any {
	if parts, ok := content.([]map[string]any); ok {
		return parts
	}
	text, _ := content.(string)
	return []map[string]any{{"type": "text", "text": text}}
}

// markLast puts the cache mark on the last part, which caches everything up to and including it.
func markLast(parts []map[string]any) []map[string]any {
	parts[len(parts)-1]["cache_control"] = ephemeral
	return parts
}

// refusedCache reports whether a refusal of data names the cache fields it carries, and if so
// stops sending them: data is then asked once more without them. A model left out of the
// explicit-cache list is better asked without marks than not at all.
func (c *Client) refusedCache(data *[]byte, status int, answer []byte) bool {
	if status != http.StatusBadRequest || !bytes.Contains(bytes.ToLower(answer), []byte("cache")) {
		return false
	}
	stripped, ok := dropCache(*data)
	if !ok {
		return false
	}
	*data = stripped
	c.noCache.Store(true)
	c.log.Warn("the provider refused the prompt-cache fields; asking again without them")
	return true
}

// dropCache returns data without prompt_cache_key and without the cache marks of its messages,
// and reports whether it had any.
func dropCache(data []byte) ([]byte, bool) {
	var body map[string]any
	if json.Unmarshal(data, &body) != nil {
		return data, false
	}
	_, dropped := body["prompt_cache_key"]
	delete(body, "prompt_cache_key")
	messages, _ := body["messages"].([]any)
	for _, m := range messages {
		msg, _ := m.(map[string]any)
		parts, _ := msg["content"].([]any)
		for _, p := range parts {
			part, _ := p.(map[string]any)
			if _, ok := part["cache_control"]; ok {
				delete(part, "cache_control")
				dropped = true
			}
		}
	}
	if !dropped {
		return data, false
	}
	out, _ := json.Marshal(body)
	return out, true
}
