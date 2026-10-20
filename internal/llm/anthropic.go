package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

type anthropicResponse struct {
	Content []anthropicContent `json:"content"`
	Usage   *anthropicUsage    `json:"usage"`
	Error   *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

// anthropicContent is one content block of an Anthropic answer.
type anthropicContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
	ID   string `json:"id"`
	Name string `json:"name"`
	// Input is the Anthropic function arguments object.
	Input map[string]any `json:"input"`
}

// joinSystem adds one more system message to the system prompt, a blank line apart.
func joinSystem(system, content string) string {
	if system != "" {
		system += "\n\n"
	}
	return system + content
}

// anthropicTextConversation converts a text-only conversation for the Messages API. Anthropic
// receives the system prompt separately and does not accept the "system" role inside the list.
func anthropicTextConversation(messages []Message) (string, []map[string]any) {
	var system string
	conversation := make([]map[string]any, 0, len(messages))
	for _, m := range messages {
		if m.Role == "system" {
			system = joinSystem(system, m.Content)
			continue
		}
		role := m.Role
		if role != "assistant" {
			role = "user"
		}
		blocks := append([]map[string]any{{"type": "text", "text": m.Content}}, anthropicImages(m.Images)...)
		conversation = append(conversation, map[string]any{"role": role, "content": blocks})
	}
	return system, conversation
}

// acceptsTemperature reports whether an Anthropic model still takes `temperature`. Current
// models (Claude 4 and later, including the Claude 5 family) answer HTTP 400 "`temperature`
// is deprecated for this model", so it is only sent to the legacy Claude 2 and 3 families;
// top_p and top_k are never sent.
func acceptsTemperature(model string) bool {
	m := strings.ToLower(strings.TrimSpace(model))
	if i := strings.LastIndex(m, "/"); i >= 0 {
		m = m[i+1:] // "anthropic/claude-3-haiku" on a gateway
	}
	for _, p := range []string{"claude-2", "claude-3", "claude-instant"} {
		if strings.HasPrefix(m, p) {
			return true
		}
	}
	return false
}

// setAnthropicSampling adds the sampling parameters the configured model accepts.
func (c *Client) setAnthropicSampling(body map[string]any) {
	if acceptsTemperature(c.cfg.Model) {
		body["temperature"] = c.cfg.Temperature
	}
}

func (c *Client) callAnthropic(ctx context.Context, messages []Message) (string, error) {
	system, conversation := anthropicTextConversation(messages)

	body := map[string]any{
		"model":      c.cfg.Model,
		"messages":   conversation,
		"max_tokens": maxInt(c.cfg.MaxTokens, 1),
	}
	c.setAnthropicSampling(body)
	if system != "" {
		body["system"] = system
	}

	data, err := c.post(ctx, "/v1/messages", body)
	if err != nil {
		return "", err
	}
	var resp anthropicResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		return "", fmt.Errorf("unreadable Anthropic response: %w", err)
	}
	recordUsage(ctx, c.source(), resp.Usage.usage())
	if resp.Error != nil && resp.Error.Message != "" {
		return "", &HTTPError{Code: 400, Body: resp.Error.Message}
	}
	text := anthropicText(resp.Content)
	if text == "" {
		return "", errors.New("anthropic returned a response with no text")
	}
	return text, nil
}

// anthropicText is the text of an answer's text blocks.
func anthropicText(content []anthropicContent) string {
	var sb strings.Builder
	for _, part := range content {
		if part.Type == "text" || part.Type == "" {
			sb.WriteString(part.Text)
		}
	}
	return sb.String()
}

func (c *Client) callAnthropicTools(ctx context.Context, messages []Message, tools []Tool) (Reply, error) {
	system, conversation := toAnthropicMessages(messages)

	body := map[string]any{
		"model":      c.cfg.Model,
		"messages":   conversation,
		"max_tokens": maxInt(c.cfg.MaxTokens, 1),
		"tools":      toAnthropicTools(tools),
	}
	c.setAnthropicSampling(body)
	if system != "" {
		body["system"] = system
	}

	data, err := c.post(ctx, "/v1/messages", body)
	if err != nil {
		return Reply{}, err
	}
	var resp anthropicResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		return Reply{}, fmt.Errorf("unreadable Anthropic response: %w", err)
	}
	recordUsage(ctx, c.source(), resp.Usage.usage())
	if resp.Error != nil && resp.Error.Message != "" {
		return Reply{}, &HTTPError{Code: 400, Body: resp.Error.Message}
	}

	reply := Reply{FinishReason: "stop", Usage: resp.Usage.usage()}
	for _, part := range resp.Content {
		switch part.Type {
		case "text":
			reply.Content += part.Text
		case "tool_use":
			reply.Calls = append(reply.Calls, anthropicToolCall(part))
		default:
			// Thinking and other blocks are not part of the reply.
		}
	}
	return reply, nil
}

// anthropicToolCall is a tool_use block as a tool call.
func anthropicToolCall(part anthropicContent) ToolCall {
	args, _ := json.Marshal(part.Input)
	return ToolCall{
		ID:   part.ID,
		Type: "function",
		Function: FunctionCall{
			Name:      part.Name,
			Arguments: args,
		},
	}
}

func toAnthropicTools(tools []Tool) []map[string]any {
	out := make([]map[string]any, 0, len(tools))
	for _, t := range tools {
		out = append(out, map[string]any{
			"name":         t.Function.Name,
			"description":  t.Function.Description,
			"input_schema": toolSchema(t),
		})
	}
	return out
}

// toAnthropicMessages converts the conversation for the Messages API with tools.
//
// Three rules of that API shape it, and each one used to be broken:
//   - a tool result is a tool_result block in a USER turn, and nothing else: the
//     result text was also sent as a text block before it, and Anthropic refuses a
//     user turn whose text precedes its tool_result blocks;
//   - every tool_use must be answered in the very next turn, so the results of
//     parallel calls are merged into ONE user turn (consecutive turns of the same
//     role are merged in general);
//   - a tool_use input is an object, and a text block is never empty.
func toAnthropicMessages(messages []Message) (string, []map[string]any) {
	var system string
	var conversation []map[string]any
	for _, m := range messages {
		if m.Role == "system" {
			system = joinSystem(system, m.Content)
			continue
		}
		role := "user"
		if m.Role == "assistant" {
			role = "assistant"
		}
		blocks := anthropicBlocks(m)
		if n := len(conversation); n > 0 && conversation[n-1]["role"] == role {
			prev := conversation[n-1]["content"].([]map[string]any)
			conversation[n-1]["content"] = append(prev, blocks...)
			continue
		}
		conversation = append(conversation, map[string]any{"role": role, "content": blocks})
	}
	return system, conversation
}

// anthropicBlocks are the content blocks of one turn: a tool result, or the turn's text and tool
// calls. A turn with neither gets a placeholder, because a turn is never empty.
func anthropicBlocks(m Message) []map[string]any {
	var blocks []map[string]any
	if m.Role == "tool" || m.ToolCallID != "" {
		blocks = append(blocks, map[string]any{
			"type":        "tool_result",
			"tool_use_id": m.ToolCallID,
			"content":     nonEmpty(m.Content),
		})
	} else {
		blocks = anthropicTurnBlocks(m)
	}
	if len(blocks) == 0 {
		blocks = append(blocks, map[string]any{"type": "text", "text": "(empty)"})
	}
	return blocks
}

// anthropicTurnBlocks are a turn's text block, when it has text, and one tool_use per call.
func anthropicTurnBlocks(m Message) []map[string]any {
	var blocks []map[string]any
	if strings.TrimSpace(m.Content) != "" {
		blocks = append(blocks, map[string]any{"type": "text", "text": m.Content})
	}
	blocks = append(blocks, anthropicImages(m.Images)...)
	for _, tc := range m.ToolCalls {
		input := map[string]any{}
		_ = tc.Function.DecodeArguments(&input)
		blocks = append(blocks, map[string]any{
			"type":  "tool_use",
			"id":    tc.ID,
			"name":  tc.Function.Name,
			"input": input,
		})
	}
	return blocks
}

// nonEmpty keeps a block's text from being empty, which the Messages API refuses.
func nonEmpty(s string) string {
	if strings.TrimSpace(s) == "" {
		return "(no output)"
	}
	return s
}
