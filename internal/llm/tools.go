package llm

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Tool calling: the model asks for a function to be run instead of writing JSON in
// its text. It is the difference between a request and a protocol — a model given
// `tools` answers with structured calls, and a model given only a prompt answers with
// text that has to be parsed and guessed at.
//
// This is additive on purpose: Complete (the text path) is untouched, so the agent's
// task mode keeps working exactly as it does today, and only the interactive mode
// uses what is declared here.

// Tool describes one function the model may call.
type Tool struct {
	Type     string      `json:"type"`
	Function FunctionDef `json:"function"`
}

// FunctionDef is the schema of a callable function.
type FunctionDef struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"`
}

// ToolCall is one function the model asked for.
type ToolCall struct {
	ID       string       `json:"id"`
	Type     string       `json:"type"`
	Function FunctionCall `json:"function"`
}

// FunctionCall carries the name and the arguments of a call.
//
// Arguments is a json.RawMessage because providers disagree: the specification says a
// string containing JSON, and several gateways send an object instead. Decoding is
// the caller's job (see DecodeArguments), which is where that difference is handled
// once instead of everywhere.
type FunctionCall struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

// DecodeArguments decodes the arguments of a function call into dest.
//
// It accepts two shapes returned by different providers and gateways:
//   - a JSON string containing an object ("{\"a\":1}"), which is the OpenAI spec;
//   - a JSON object directly ({"arguments":{"a":1}}), which some gateways emit.
func (fc FunctionCall) DecodeArguments(dest any) error {
	if len(fc.Arguments) == 0 {
		return nil
	}

	trimmed := trimSpace(fc.Arguments)
	if len(trimmed) == 0 {
		return nil
	}
	if trimmed[0] == '"' {
		var str string
		if err := json.Unmarshal(trimmed, &str); err != nil {
			return fmt.Errorf("could not decode stringified arguments: %w", err)
		}
		trimmed = trimSpace(json.RawMessage(str))
	}
	if len(trimmed) == 0 {
		return nil
	}
	if err := json.Unmarshal(trimmed, dest); err != nil {
		return fmt.Errorf("could not decode arguments object: %w", err)
	}
	return nil
}

// trimSpace returns a trimmed copy of the raw message.
func trimSpace(r json.RawMessage) json.RawMessage {
	start := 0
	for ; start < len(r); start++ {
		if r[start] != ' ' && r[start] != '\t' && r[start] != '\n' && r[start] != '\r' {
			break
		}
	}
	end := len(r)
	for ; end > start; end-- {
		if r[end-1] != ' ' && r[end-1] != '\t' && r[end-1] != '\n' && r[end-1] != '\r' {
			break
		}
	}
	return r[start:end]
}

// Reply is what a model answered: either text, or calls, or both.
type Reply struct {
	// Content is the text of the answer, empty when the model only asked for calls.
	Content string
	// Calls are the functions the model wants run, in the order it asked for them.
	Calls []ToolCall
	// FinishReason is what the provider reported ("stop", "tool_calls", "length").
	FinishReason string
}

// WantsTools reports whether the model is waiting for function results.
func (r Reply) WantsTools() bool { return len(r.Calls) > 0 }

// StreamEvent describes one chunk from a streaming completion.
type StreamEvent int

const (
	StreamText StreamEvent = iota
	StreamToolCall
	StreamError
	StreamDone
	// StreamThinking carries the model's own reasoning tokens, for a provider that streams
	// them apart from the answer (reasoning_content, reasoning). They are shown to the user
	// while the answer is being written, and are never part of the answer.
	StreamThinking
)

// StreamChunk is one piece of a streaming response. It carries either a text
// fragment, a tool call, an error, or a done signal.
type StreamChunk struct {
	Event StreamEvent
	Text  string    // for StreamText
	Call  *ToolCall // for StreamToolCall (may be partial/accumulated)
	Error error     // for StreamError
	Reply Reply     // final accumulated reply on StreamDone
}

// StreamResult collects the pieces of a streaming reply.
type StreamResult struct {
	Content  strings.Builder
	Calls    []ToolCall
	LastCall *ToolCall
}

// Handle adds a chunk to the accumulator and returns true when the stream ended.
//
// ## Identity: an ID announces a call, and only an ID does
//
// A provider streams a tool call in FRAGMENTS, and the fragments do not all look alike. Measured
// against the wire format a real gateway sends: the first fragment carries `id`, `function.name`
// and the first slice of `arguments`; every CONTINUATION carries `arguments` only, with no id at
// all. Some gateways repeat the id on every fragment, and some send the name again on
// continuations.
//
// So identity is decided by the ID ALONE, and a fragment without one CONTINUES the call in
// progress rather than starting a new one. The rule this replaces compared `LastCall.ID !=
// chunk.Call.ID`, which is true for every id-less continuation — so each one was appended as a
// new, empty call and the real arguments were lost.
//
// That is not a cosmetic defect: the caller then saw a list of empty shells while the provider
// had sent a usable call, and the run failed with "the model produced nothing usable" over a
// reply that contained everything. It is why `finish_reason="tool_calls"` and an empty list
// appeared in the same message.
func (sr *StreamResult) Handle(chunk StreamChunk) bool {
	switch chunk.Event {
	case StreamDone:
		return true
	case StreamError:
		return true
	case StreamText:
		sr.Content.WriteString(chunk.Text)
	case StreamToolCall:
		if chunk.Call == nil {
			return false
		}
		c := chunk.Call
		// A call is announced by an ID we have not seen yet. A repeated ID is the same call
		// restated (a gateway that sends the id on every fragment), and an absent ID is by
		// definition a continuation.
		startsCall := c.ID != "" && (sr.LastCall == nil || sr.LastCall.ID != c.ID)
		if startsCall || sr.LastCall == nil {
			sr.Calls = append(sr.Calls, *c)
			// LastCall must point at the copy that is now in the slice. Pointing it at the
			// incoming chunk (which the caller may reuse) made the later fragments of the same
			// call land outside the slice, so the arguments kept only their first piece.
			sr.LastCall = &sr.Calls[len(sr.Calls)-1]
			return false
		}
		// A continuation: it contributes the pieces it carries and must not erase what the
		// announcing fragment already said. Overwriting the whole struct dropped the ID and the
		// NAME of the call on the first continuation, which left the caller with a call it could
		// not dispatch.
		//
		// `arguments` is the one field whose treatment depends on WHICH provider is speaking,
		// and the difference was measured against each rather than assumed:
		//
		//   - OpenAI (and the specification) streams the arguments as PIECES of one JSON
		//     document: the first fragment ends mid-string and the next continues it, so they
		//     must be JOINED. This is the id-less case, and joining is the only reading that
		//     produces parseable JSON.
		//   - Other gateways RESTATE the whole call on a fragment that repeats the id, which is
		//     the case the "last one wins" rule was written for: joining there would produce two
		//     JSON documents back to back.
		//
		// So a fragment that repeats the id REPLACES the arguments, and one without an id
		// APPENDS to them.
		if c.ID != "" {
			sr.LastCall.ID = c.ID
		}
		if c.Type != "" {
			sr.LastCall.Type = c.Type
		}
		if c.Function.Name != "" {
			sr.LastCall.Function.Name = c.Function.Name
		}
		if len(c.Function.Arguments) > 0 {
			if c.ID != "" {
				// A restated call: the fragment carries the arguments whole.
				sr.LastCall.Function.Arguments = c.Function.Arguments
			} else {
				// A piece of the arguments: it continues the document in progress.
				sr.LastCall.Function.Arguments = append(sr.LastCall.Function.Arguments, c.Function.Arguments...)
			}
		}
	}
	return false
}

// FinalReply builds the accumulated Reply from the stream.
func (sr StreamResult) FinalReply() Reply {
	return Reply{Content: sr.Content.String(), Calls: sr.Calls, FinishReason: "stop"}
}

func (sc StreamChunk) String() string {
	switch sc.Event {
	case StreamText:
		return sc.Text
	case StreamToolCall:
		if sc.Call != nil {
			return fmt.Sprintf("[tool call: %s]", sc.Call.Function.Name)
		}
		return "[tool call]"
	case StreamError:
		return fmt.Sprintf("[error: %v]", sc.Error)
	case StreamDone:
		return ""
	}
	return ""
}

// NewTool is a small helper for building the tool list.
func NewTool(name, description string, parameters map[string]any) Tool {
	return Tool{
		Type: "function",
		Function: FunctionDef{
			Name:        name,
			Description: description,
			Parameters:  parameters,
		},
	}
}

// ObjectSchema builds the JSON Schema of a function that takes the given properties.
// It exists so the tool definitions in the code read as data, not as nested maps.
//
// required names the properties that must be present; the rest are optional.
func ObjectSchema(properties map[string]any, required ...string) map[string]any {
	schema := map[string]any{
		"type":       "object",
		"properties": properties,
	}
	if len(required) > 0 {
		schema["required"] = required
	}
	return schema
}

// StringProperty describes a property of type string.
func StringProperty(description string) map[string]any {
	return map[string]any{"type": "string", "description": description}
}

// IntegerProperty describes a property of type integer.
func IntegerProperty(description string) map[string]any {
	return map[string]any{"type": "integer", "description": description}
}
