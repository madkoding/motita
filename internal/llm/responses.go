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

	"github.com/madkoding/motita/internal/config"
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
			input = append(input, responsesAssistantItems(m)...)
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
		body["tools"] = responsesTools(tools)
		body["tool_choice"] = "auto"
		body["parallel_tool_calls"] = true
	}
	if reasoningRequested(c.cfg.Reasoning) {
		body["reasoning"] = map[string]any{"effort": c.cfg.Reasoning.Level, "summary": "auto"}
	}
	// The ChatGPT backend refuses an output cap; the API takes it.
	if !chatgpt && c.cfg.MaxTokens > 0 {
		body["max_output_tokens"] = c.cfg.MaxTokens
	}
	return body
}

// responsesAssistantItems are an assistant turn's input items: its text, when it has any, and one
// function_call per tool call.
func responsesAssistantItems(m Message) []map[string]any {
	var items []map[string]any
	if strings.TrimSpace(m.Content) != "" {
		items = append(items, map[string]any{
			"type": "message", "role": "assistant",
			"content": []map[string]any{{"type": "output_text", "text": m.Content}},
		})
	}
	for _, tc := range m.ToolCalls {
		items = append(items, map[string]any{
			"type":      "function_call",
			"call_id":   tc.ID,
			"name":      tc.Function.Name,
			"arguments": argumentsString(tc.Function.Arguments),
		})
	}
	return items
}

// responsesTools declares the tools as /responses functions.
func responsesTools(tools []Tool) []map[string]any {
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
	return defs
}

// reasoningRequested reports whether the reasoning setting names an effort to ask for.
func reasoningRequested(r config.Reasoning) bool {
	return r.Enabled && r.Level != "off" && r.Level != ""
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

func (c *Client) callResponsesStream(
	ctx context.Context, messages []Message, tools []Tool,
) (<-chan StreamChunk, error) {
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
		s := &responsesReader{ctx: ctx, out: out}
		s.read(bufio.NewReader(resp.Body))
	}()
	return out, nil
}

// responsesReader reads a /responses stream into chunks for the caller.
type responsesReader struct {
	ctx   context.Context
	out   chan<- StreamChunk
	acc   StreamResult
	usage Usage
}

// read forwards the stream until it ends.
func (s *responsesReader) read(reader *bufio.Reader) {
	for {
		line, err := reader.ReadBytes('\n')
		if err != nil {
			s.end(err)
			return
		}
		payload, ok := responsesPayload(line)
		if !ok {
			continue
		}
		var ev responsesEvent
		if json.Unmarshal(payload, &ev) != nil {
			continue
		}
		if s.handle(ev) {
			return
		}
	}
}

// responsesPayload is the data of one server-sent event line, when it carries an event.
func responsesPayload(line []byte) ([]byte, bool) {
	payload, ok := bytes.CutPrefix(bytes.TrimSpace(line), []byte("data:"))
	if !ok {
		return nil, false
	}
	payload = bytes.TrimSpace(payload)
	if len(payload) == 0 || string(payload) == "[DONE]" {
		return nil, false
	}
	return payload, true
}

// hasAnswer reports whether the stream has delivered text or a tool call so far.
func (s *responsesReader) hasAnswer() bool {
	return s.acc.Content.Len() > 0 || len(s.acc.Calls) > 0
}

// finish sends the whole reply, with its usage.
func (s *responsesReader) finish() {
	r := s.acc.FinalReply()
	if len(r.Calls) > 0 {
		r.FinishReason = "tool_calls"
	}
	r.Usage = s.usage
	recordUsage(s.ctx, s.usage)
	s.out <- StreamChunk{Event: StreamDone, Reply: r}
}

// fail ends the stream with an error.
func (s *responsesReader) fail(err error) {
	s.out <- StreamChunk{Event: StreamError, Error: err}
}

// end closes the stream on a read error: the end of a body that delivered an answer is that
// answer, and the end of one that did not is a failure.
func (s *responsesReader) end(err error) {
	switch {
	case err != io.EOF:
		s.fail(err)
	case s.hasAnswer():
		s.finish()
	default:
		s.fail(errors.New("the /responses stream ended without a completed response"))
	}
}

// handle reads one event, and reports whether it ended the stream.
func (s *responsesReader) handle(ev responsesEvent) bool {
	// The final events (completed, done, incomplete) carry the response with its usage.
	if u := ev.Response.Usage.usage(); u.Total() > 0 {
		s.usage = u
	}
	switch ev.Type {
	case "response.output_text.delta":
		s.text(ev.Delta)
	case "response.reasoning_summary_text.delta", "response.reasoning_text.delta":
		if ev.Delta != "" {
			s.out <- StreamChunk{Event: StreamThinking, Text: ev.Delta}
		}
	case "response.output_item.done":
		s.itemDone(ev)
	case "response.completed", "response.done":
		s.finish()
		return true
	case "response.incomplete":
		s.incomplete(ev)
		return true
	case "response.failed":
		s.fail(&HTTPError{Code: 500, Body: responsesFailure(ev)})
		return true
	case "error":
		s.fail(&HTTPError{Code: 400, Body: strings.TrimSpace(ev.Code + " " + ev.Message)})
		return true
	default:
		// Other events carry nothing the reply keeps.
	}
	return false
}

// text forwards a piece of the answer and adds it to the reply.
func (s *responsesReader) text(delta string) {
	if delta != "" {
		s.acc.Handle(StreamChunk{Event: StreamText, Text: delta})
		s.out <- StreamChunk{Event: StreamText, Text: delta}
	}
}

// itemDone forwards a finished function call and adds it to the reply.
func (s *responsesReader) itemDone(ev responsesEvent) {
	if ev.Item.Type != "function_call" {
		return
	}
	args, _ := json.Marshal(ev.Item.Arguments)
	call := ToolCall{ID: ev.Item.CallID, Type: "function", Function: FunctionCall{Name: ev.Item.Name, Arguments: args}}
	s.acc.Handle(StreamChunk{Event: StreamToolCall, Call: &call})
	s.out <- StreamChunk{Event: StreamToolCall, Call: &call}
}

// incomplete ends a response the server cut short: what it delivered stands, and nothing at all
// is a failure that asking again the same way will not fix.
func (s *responsesReader) incomplete(ev responsesEvent) {
	reason := "unknown"
	if ev.Response.IncompleteDetails != nil {
		reason = ev.Response.IncompleteDetails.Reason
	}
	if s.hasAnswer() {
		s.finish()
	} else {
		s.fail(fatalError{fmt.Errorf("the response is incomplete (%s)", reason)})
	}
}

// responsesFailure is the message of a failed response.
func responsesFailure(ev responsesEvent) string {
	msg := "the response failed"
	if ev.Response.Error != nil && ev.Response.Error.Message != "" {
		msg = ev.Response.Error.Message
	}
	return msg
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
		default:
			// The text and the calls arrive whole on StreamDone.
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
