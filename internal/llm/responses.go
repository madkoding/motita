package llm

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
)

// --- Responses API (codex) ----------------------------------------------------
//
// The Codex models (gpt-5-codex, gpt-5.x-codex) are served by POST /responses
// only: sent to /chat/completions they are a 404 ("This model is only supported
// in v1/responses"). A ChatGPT login speaks the same protocol against the
// ChatGPT backend, where the request must be streamed and must not be stored.
// So the codex provider always streams, and the non-streaming calls read the
// stream to its end.

// responsesBody builds a /responses request.
func (c *Client) responsesBody(messages []Message, tools []Tool, chatgpt bool) map[string]any {
	var instructions []string
	input := make([]map[string]any, 0, len(messages))
	for _, m := range messages {
		switch {
		case m.Role == "system":
			instructions = append(instructions, m.Content)
		case m.Role == "tool" || m.ToolCallID != "":
			input = append(input, map[string]any{
				"type":    "function_call_output",
				"call_id": m.ToolCallID,
				"output":  m.Content,
			})
		case m.Role == "assistant":
			if strings.TrimSpace(m.Content) != "" {
				input = append(input, map[string]any{
					"type": "message", "role": "assistant",
					"content": []map[string]any{{"type": "output_text", "text": m.Content}},
				})
			}
			for _, tc := range m.ToolCalls {
				input = append(input, map[string]any{
					"type":      "function_call",
					"call_id":   tc.ID,
					"name":      tc.Function.Name,
					"arguments": argumentsString(tc.Function.Arguments),
				})
			}
		default:
			input = append(input, map[string]any{
				"type": "message", "role": "user",
				"content": []map[string]any{{"type": "input_text", "text": m.Content}},
			})
		}
	}
	body := map[string]any{
		"model":        c.cfg.Model,
		"instructions": strings.Join(instructions, "\n\n"),
		"input":        input,
		"store":        false,
		"stream":       true,
	}
	if len(tools) > 0 {
		defs := make([]map[string]any, 0, len(tools))
		for _, t := range tools {
			defs = append(defs, map[string]any{
				"type":        "function",
				"name":        t.Function.Name,
				"description": t.Function.Description,
				"parameters":  toolSchema(t),
				"strict":      false,
			})
		}
		body["tools"] = defs
		body["tool_choice"] = "auto"
		body["parallel_tool_calls"] = true
	}
	if c.cfg.Reasoning.Enabled && c.cfg.Reasoning.Level != "off" && c.cfg.Reasoning.Level != "" {
		body["reasoning"] = map[string]any{"effort": c.cfg.Reasoning.Level, "summary": "auto"}
	}
	// The ChatGPT backend refuses an output cap; the API takes it.
	if !chatgpt && c.cfg.MaxTokens > 0 {
		body["max_output_tokens"] = c.cfg.MaxTokens
	}
	return body
}

// argumentsString returns tool-call arguments as the JSON text /responses wants:
// they are kept as a JSON string (the chat-completions spelling) or an object.
func argumentsString(raw json.RawMessage) string {
	t := bytes.TrimSpace(raw)
	if len(t) == 0 {
		return "{}"
	}
	if t[0] == '"' {
		var s string
		if json.Unmarshal(t, &s) == nil {
			return s
		}
	}
	return string(t)
}

// responsesEvent is one server-sent event of a /responses stream.
type responsesEvent struct {
	Type  string `json:"type"`
	Delta string `json:"delta"`
	Item  struct {
		Type      string `json:"type"`
		CallID    string `json:"call_id"`
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"item"`
	Response struct {
		Status            string `json:"status"`
		IncompleteDetails *struct {
			Reason string `json:"reason"`
		} `json:"incomplete_details"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
		Usage *responsesUsage `json:"usage"`
	} `json:"response"`
	Message string `json:"message"`
	Code    string `json:"code"`
}

func (c *Client) callResponsesStream(ctx context.Context, messages []Message, tools []Tool) (<-chan StreamChunk, error) {
	t, err := c.target(ctx, false)
	if err != nil {
		return nil, err
	}
	resp, _, err := c.send(ctx, "/responses", c.responsesBody(messages, tools, t.chatgpt), true)
	if err != nil {
		return nil, err
	}
	out := make(chan StreamChunk, 8)
	go func() {
		defer close(out)
		defer resp.Body.Close()
		reader := bufio.NewReader(resp.Body)
		var acc StreamResult
		var usage Usage
		finish := func() {
			r := acc.FinalReply()
			if len(r.Calls) > 0 {
				r.FinishReason = "tool_calls"
			}
			r.Usage = usage
			recordUsage(ctx, usage)
			out <- StreamChunk{Event: StreamDone, Reply: r}
		}
		for {
			line, err := reader.ReadBytes('\n')
			if err != nil {
				if err == io.EOF {
					if acc.Content.Len() > 0 || len(acc.Calls) > 0 {
						finish()
					} else {
						out <- StreamChunk{Event: StreamError, Error: errors.New("the /responses stream ended without a completed response")}
					}
				} else {
					out <- StreamChunk{Event: StreamError, Error: err}
				}
				return
			}
			line = bytes.TrimSpace(line)
			payload, ok := bytes.CutPrefix(line, []byte("data:"))
			if !ok {
				continue
			}
			payload = bytes.TrimSpace(payload)
			if len(payload) == 0 || string(payload) == "[DONE]" {
				continue
			}
			var ev responsesEvent
			if json.Unmarshal(payload, &ev) != nil {
				continue
			}
			// The final events (completed, done, incomplete) carry the response with its usage.
			if u := ev.Response.Usage.usage(); u.Total() > 0 {
				usage = u
			}
			switch ev.Type {
			case "response.output_text.delta":
				if ev.Delta != "" {
					acc.Handle(StreamChunk{Event: StreamText, Text: ev.Delta})
					out <- StreamChunk{Event: StreamText, Text: ev.Delta}
				}
			case "response.reasoning_summary_text.delta", "response.reasoning_text.delta":
				if ev.Delta != "" {
					out <- StreamChunk{Event: StreamThinking, Text: ev.Delta}
				}
			case "response.output_item.done":
				if ev.Item.Type == "function_call" {
					args, _ := json.Marshal(ev.Item.Arguments)
					call := ToolCall{ID: ev.Item.CallID, Type: "function", Function: FunctionCall{Name: ev.Item.Name, Arguments: args}}
					acc.Handle(StreamChunk{Event: StreamToolCall, Call: &call})
					out <- StreamChunk{Event: StreamToolCall, Call: &call}
				}
			case "response.completed", "response.done":
				finish()
				return
			case "response.incomplete":
				reason := "unknown"
				if ev.Response.IncompleteDetails != nil {
					reason = ev.Response.IncompleteDetails.Reason
				}
				if acc.Content.Len() > 0 || len(acc.Calls) > 0 {
					finish()
				} else {
					out <- StreamChunk{Event: StreamError, Error: fatalError{fmt.Errorf("the response is incomplete (%s)", reason)}}
				}
				return
			case "response.failed":
				msg := "the response failed"
				if ev.Response.Error != nil && ev.Response.Error.Message != "" {
					msg = ev.Response.Error.Message
				}
				out <- StreamChunk{Event: StreamError, Error: &HTTPError{Code: 500, Body: msg}}
				return
			case "error":
				out <- StreamChunk{Event: StreamError, Error: &HTTPError{Code: 400, Body: strings.TrimSpace(ev.Code + " " + ev.Message)}}
				return
			}
		}
	}()
	return out, nil
}

// callResponses reads a /responses stream to its end and returns the reply.
func (c *Client) callResponses(ctx context.Context, messages []Message, tools []Tool) (Reply, error) {
	ch, err := c.callResponsesStream(ctx, messages, tools)
	if err != nil {
		return Reply{}, err
	}
	var reply Reply
	var streamErr error
	for chunk := range ch {
		switch chunk.Event {
		case StreamDone:
			reply = chunk.Reply
		case StreamError:
			streamErr = chunk.Error
		}
	}
	if streamErr != nil {
		return Reply{}, streamErr
	}
	if strings.TrimSpace(reply.Content) == "" && len(reply.Calls) == 0 {
		return Reply{}, errors.New("codex returned no text and no tool calls")
	}
	return reply, nil
}
