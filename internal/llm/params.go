package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
)

// isReasoningModel reports whether an OpenAI model id is one of the reasoning
// families (o1, o3, o4, gpt-5 and its codex variants). They refuse what the older
// chat models took: `max_tokens` (it is `max_completion_tokens`) and any
// temperature other than the default.
func isReasoningModel(model string) bool {
	m := strings.ToLower(strings.TrimSpace(model))
	if i := strings.LastIndex(m, "/"); i >= 0 {
		m = m[i+1:] // "openai/gpt-5" on a gateway
	}
	for _, p := range []string{"o1", "o3", "o4", "gpt-5"} {
		if m == p || strings.HasPrefix(m, p+"-") || strings.HasPrefix(m, p+".") {
			return true
		}
	}
	return false
}

// isOfficialOpenAI reports whether a base URL is OpenAI's own API, which
// deprecated `max_tokens` for every model.
func isOfficialOpenAI(base string) bool {
	u, err := url.Parse(base)
	return err == nil && strings.EqualFold(u.Hostname(), "api.openai.com")
}

// openAIBody builds a /chat/completions request with the parameters the target
// actually accepts. Sending `max_tokens` and a temperature to a reasoning model
// is a 400 ("Unsupported parameter"), which used to make every o-series and
// gpt-5 model unusable through the openai provider.
func (c *Client) openAIBody(messages []Message, tools []Tool) map[string]any {
	body := map[string]any{
		"model":    c.cfg.Model,
		"messages": toOpenAIMessages(messages),
	}
	if len(tools) > 0 {
		body["tools"] = tools
	}
	reasoning := isReasoningModel(c.cfg.Model)
	if c.cfg.MaxTokens > 0 {
		if reasoning || isOfficialOpenAI(c.baseURL(DefaultBaseURL(c.cfg.Provider))) {
			body["max_completion_tokens"] = c.cfg.MaxTokens
		} else {
			body["max_tokens"] = c.cfg.MaxTokens
		}
	}
	if !reasoning {
		body["temperature"] = c.cfg.Temperature
	}
	if c.cfg.Reasoning.Enabled && c.cfg.Reasoning.Level != "off" && c.cfg.Reasoning.Level != "" {
		// OpenAI's own chat models reject reasoning_effort; every other host (Ollama,
		// DeepSeek, OpenRouter, Copilot, Qwen) either uses it or ignores it.
		if reasoning || !isOfficialOpenAI(c.baseURL(DefaultBaseURL(c.cfg.Provider))) {
			body["reasoning_effort"] = c.cfg.Reasoning.Level
		}
	}
	return body
}

// maxBudgetTokens caps how far an answer cut off by the token budget is retried with a larger one.
const maxBudgetTokens = 65536

// raiseBudget multiplies the output-token limit of a request body by four, up to
// maxBudgetTokens, and reports whether it changed anything.
//
// It exists for reasoning models: their hidden thinking is billed against the same limit as
// the answer, so a modest max_tokens can be spent entirely on thinking and leave 0 bytes of
// text (finish_reason="length"). Repeating that request unchanged truncates again.
func raiseBudget(body map[string]any) bool {
	for _, key := range []string{"max_completion_tokens", "max_tokens"} {
		cur, ok := body[key].(int)
		if !ok || cur >= maxBudgetTokens {
			continue
		}
		body[key] = min(cur*4, maxBudgetTokens)
		return true
	}
	return false
}

// openAIChoice posts a /chat/completions request and returns its first choice.
//
// An answer that is empty because the token budget ran out is asked again with a larger
// budget (see raiseBudget) before it is reported as a failure.
func (c *Client) openAIChoice(ctx context.Context, messages []Message, tools []Tool) (openAIChoice, *openAIUsage, error) {
	body := c.openAIBody(messages, tools)
	for {
		data, err := c.post(ctx, "/chat/completions", body)
		if err != nil {
			return openAIChoice{}, nil, err
		}
		var resp openAIResponse
		if err := json.Unmarshal(data, &resp); err != nil {
			return openAIChoice{}, nil, fmt.Errorf("unreadable OpenAI response: %w", err)
		}
		// Recorded before anything is judged: a reply that is then refused was still billed.
		recordUsage(ctx, resp.Usage.usage())
		if resp.Error != nil && resp.Error.Message != "" {
			return openAIChoice{}, nil, &HTTPError{Code: 400, Body: resp.Error.Message}
		}
		if len(resp.Choices) == 0 {
			return openAIChoice{}, nil, errors.New("OpenAI returned empty choices")
		}
		choice := resp.Choices[0]
		if choice.FinishReason == "length" && strings.TrimSpace(choice.Message.Content) == "" &&
			len(choice.Message.ToolCalls) == 0 && raiseBudget(body) {
			c.log.Warn("the answer was cut off before any text; asking again with a larger token budget")
			continue
		}
		return choice, resp.Usage, nil
	}
}

// geminiPath is the generateContent path of the configured model. The model id
// may be written with or without the "models/" prefix the API lists it with.
func (c *Client) geminiPath() string {
	model := strings.TrimPrefix(strings.TrimSpace(c.cfg.Model), "models/")
	return "/v1beta/models/" + url.PathEscape(model) + ":generateContent"
}

// toolSchema returns a tool's parameters as a complete JSON Schema object.
//
// Parameters already IS the schema ({"type":"object","properties":...,
// "required":[...]}, see ObjectSchema). The Anthropic and Gemini paths used to
// wrap it again as the "properties" of a new object, so every tool was declared
// with three parameters literally named "type", "properties" and "required" —
// which both APIs reject, and which made tool calling unusable on them.
func toolSchema(t Tool) map[string]any {
	p := t.Function.Parameters
	if p == nil {
		return map[string]any{"type": "object", "properties": map[string]any{}}
	}
	if _, ok := p["type"]; ok {
		return p
	}
	// A bare map of properties, as some callers write it.
	return map[string]any{"type": "object", "properties": p}
}
