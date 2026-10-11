package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

type geminiResponse struct {
	Candidates []struct {
		Content struct {
			Role  string       `json:"role"`
			Parts []geminiPart `json:"parts"`
		} `json:"content"`
		FinishReason string `json:"finishReason"`
	} `json:"candidates"`
	UsageMetadata *geminiUsage `json:"usageMetadata"`
	Error         *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

// geminiPart is one part of a Gemini answer.
type geminiPart struct {
	Text             string          `json:"text"`
	Thought          bool            `json:"thought"`
	FunctionCall     json.RawMessage `json:"functionCall"`
	ThoughtSignature string          `json:"thoughtSignature"`
}

type geminiFunctionCall struct {
	ID   string          `json:"id"`
	Name string          `json:"name"`
	Args json.RawMessage `json:"args"`
}

func (c *Client) callGemini(ctx context.Context, messages []Message) (string, error) {
	var system string
	var contents []map[string]any
	for _, m := range messages {
		if m.Role == "system" {
			system += m.Content + "\n"
			continue
		}
		role := "user"
		if m.Role == "assistant" {
			role = "model"
		}
		contents = append(contents, map[string]any{
			"role":  role,
			"parts": []map[string]any{{"text": m.Content}},
		})
	}

	body := map[string]any{
		"contents": contents,
		"generationConfig": map[string]any{
			"maxOutputTokens": c.cfg.MaxTokens,
			"temperature":     c.cfg.Temperature,
		},
	}
	if system != "" {
		body["systemInstruction"] = map[string]any{
			"parts": []map[string]any{{"text": system}},
		}
	}

	data, err := c.post(ctx, c.geminiPath(), body)
	if err != nil {
		return "", err
	}
	var resp geminiResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		return "", fmt.Errorf("unreadable Gemini response: %w", err)
	}
	recordUsage(ctx, resp.UsageMetadata.usage())
	if resp.Error != nil && resp.Error.Message != "" {
		return "", &HTTPError{Code: 400, Body: resp.Error.Message}
	}
	if len(resp.Candidates) == 0 {
		return "", errors.New("gemini returned a response with no candidates (safety block?)")
	}
	var sb strings.Builder
	for _, part := range resp.Candidates[0].Content.Parts {
		sb.WriteString(part.Text)
	}
	if sb.Len() == 0 {
		return "", fmt.Errorf("gemini returned an empty response (finishReason=%q)", resp.Candidates[0].FinishReason)
	}
	return sb.String(), nil
}

func (c *Client) callGeminiTools(ctx context.Context, messages []Message, tools []Tool) (Reply, error) {
	system, contents := toGeminiContents(messages)

	body := map[string]any{
		"contents": contents,
		"generationConfig": map[string]any{
			"maxOutputTokens": c.cfg.MaxTokens,
			"temperature":     c.cfg.Temperature,
		},
	}
	if len(tools) > 0 {
		body["tools"] = []map[string]any{
			{"functionDeclarations": toGeminiToolDeclarations(tools)},
		}
	}
	if system != "" {
		body["systemInstruction"] = map[string]any{
			"parts": []map[string]any{{"text": system}},
		}
	}

	data, err := c.post(ctx, c.geminiPath(), body)
	if err != nil {
		return Reply{}, err
	}
	var resp geminiResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		return Reply{}, fmt.Errorf("unreadable Gemini response: %w", err)
	}
	recordUsage(ctx, resp.UsageMetadata.usage())
	if resp.Error != nil && resp.Error.Message != "" {
		return Reply{}, &HTTPError{Code: 400, Body: resp.Error.Message}
	}
	if len(resp.Candidates) == 0 {
		return Reply{}, errors.New("gemini returned a response with no candidates (safety block?)")
	}

	reply := Reply{FinishReason: resp.Candidates[0].FinishReason, Usage: resp.UsageMetadata.usage()}
	for i, part := range resp.Candidates[0].Content.Parts {
		if part.Text != "" && !part.Thought {
			reply.Content += part.Text
		}
		if call, ok := geminiToolCall(i, part); ok {
			reply.Calls = append(reply.Calls, call)
		}
	}
	if len(reply.Calls) > 0 {
		reply.FinishReason = "tool_calls"
	}
	return reply, nil
}

// geminiToolCall reads the function call of part i, when it carries a readable one. Gemini names
// its calls only sometimes; the agent needs an id to pair each result with its call, and the
// result needs the function NAME.
func geminiToolCall(i int, part geminiPart) (ToolCall, bool) {
	var fc geminiFunctionCall
	if len(part.FunctionCall) == 0 || json.Unmarshal(part.FunctionCall, &fc) != nil {
		return ToolCall{}, false
	}
	id := fc.ID
	if id == "" {
		id = fmt.Sprintf("gemini-%d-%s", i, fc.Name)
	}
	args := fc.Args
	if len(args) == 0 || string(args) == "null" {
		args = json.RawMessage("{}")
	}
	return ToolCall{
		ID:        id,
		Type:      "function",
		Function:  FunctionCall{Name: fc.Name, Arguments: args},
		signature: part.ThoughtSignature,
	}, true
}

func toGeminiToolDeclarations(tools []Tool) []map[string]any {
	out := make([]map[string]any, 0, len(tools))
	for _, t := range tools {
		decl := map[string]any{
			"name":        t.Function.Name,
			"description": t.Function.Description,
		}
		// Gemini refuses an OBJECT with no properties: a tool that takes nothing is
		// declared with no parameters at all.
		schema := toolSchema(t)
		if props, _ := schema["properties"].(map[string]any); len(props) > 0 {
			decl["parameters"] = schema
		}
		out = append(out, decl)
	}
	return out
}

// geminiSkipSignature is the placeholder Google documents for a function call
// whose thought signature is not available (a history replayed from storage or
// from another model). Gemini 3 refuses a function call without one.
const geminiSkipSignature = "skip_thought_signature_validator"

// toGeminiContents converts the conversation for generateContent with tools.
//
// A tool result is a functionResponse part that names the FUNCTION (not the call
// id, which is what used to be sent) and carries an OBJECT (a bare string is
// refused); the results of parallel calls go in one turn, right after the turn
// that made the calls.
func toGeminiContents(messages []Message) (string, []map[string]any) {
	var system string
	var contents []map[string]any
	names := map[string]string{} // call id -> function name
	for _, m := range messages {
		if m.Role == "system" {
			system += m.Content + "\n"
			continue
		}
		role := "user"
		if m.Role == "assistant" {
			role = "model"
		}
		var parts []map[string]any
		if m.Role == "tool" || m.ToolCallID != "" {
			parts = append(parts, geminiToolResultPart(m, names))
		} else {
			parts = geminiTurnParts(m, names)
		}
		if len(parts) == 0 {
			parts = append(parts, map[string]any{"text": " "})
		}
		if n := len(contents); n > 0 && contents[n-1]["role"] == role {
			prev := contents[n-1]["parts"].([]map[string]any)
			contents[n-1]["parts"] = append(prev, parts...)
			continue
		}
		contents = append(contents, map[string]any{"role": role, "parts": parts})
	}
	return system, contents
}

// geminiToolResultPart is a tool result as a functionResponse part, which names the FUNCTION the
// call went to (names maps call ids to them) and carries an object.
func geminiToolResultPart(m Message, names map[string]string) map[string]any {
	name := names[m.ToolCallID]
	if name == "" {
		name = m.ToolCallID
	}
	return map[string]any{
		"functionResponse": map[string]any{
			"name":     name,
			"response": map[string]any{"content": m.Content},
		},
	}
}

// geminiTurnParts are a turn's text part, when it has text, and one functionCall part per call;
// each call's function is recorded in names for the results that follow.
func geminiTurnParts(m Message, names map[string]string) []map[string]any {
	var parts []map[string]any
	if m.Content != "" {
		parts = append(parts, map[string]any{"text": m.Content})
	}
	for i, tc := range m.ToolCalls {
		names[tc.ID] = tc.Function.Name
		args := map[string]any{}
		_ = tc.Function.DecodeArguments(&args)
		part := map[string]any{
			"functionCall": map[string]any{"name": tc.Function.Name, "args": args},
		}
		// Only the first call of a turn carries the signature.
		if i == 0 {
			part["thoughtSignature"] = geminiSignature(tc.signature)
		}
		parts = append(parts, part)
	}
	return parts
}

// geminiSignature is the thought signature a replayed call carries: its own, or the documented
// placeholder when it has none.
func geminiSignature(sig string) string {
	if sig == "" {
		sig = geminiSkipSignature
	}
	return sig
}
