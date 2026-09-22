package llm

import (
	"encoding/json"
	"fmt"
	"slices"
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
	// Index is the position of the call in a streamed reply. OpenAI sends the id
	// and the name only on the first delta of a call and the index on every one,
	// so the index is what ties the later argument fragments to their call.
	Index    *int         `json:"index,omitempty"`
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
			break
		}
		if chunk.Call.Index != nil {
			sr.mergeIndexed(*chunk.Call)
		} else if sr.LastCall == nil || sr.LastCall.ID != chunk.Call.ID {
			sr.Calls = append(sr.Calls, *chunk.Call)
			// LastCall must point at the copy that is now in the slice.
			// Pointing it at the incoming chunk (which the caller may reuse)
			// made the later fragments of the same call land outside the
			// slice, so the arguments kept only their first piece.
			sr.LastCall = &sr.Calls[len(sr.Calls)-1]
		} else {
			*sr.LastCall = *chunk.Call
		}
	}
	return false
}

// mergeIndexed folds one delta of an indexed call into the call with the same
// index: the first non-empty id, type and name are kept, and the argument
// fragments are concatenated, because each delta carries only the next piece.
func (sr *StreamResult) mergeIndexed(delta ToolCall) {
	for i := range sr.Calls {
		dst := &sr.Calls[i]
		if dst.Index == nil || *dst.Index != *delta.Index {
			continue
		}
		if dst.ID == "" {
			dst.ID = delta.ID
		}
		if dst.Type == "" {
			dst.Type = delta.Type
		}
		if dst.Function.Name == "" {
			dst.Function.Name = delta.Function.Name
		}
		dst.Function.Arguments = appendArguments(dst.Function.Arguments, delta.Function.Arguments)
		sr.LastCall = dst
		return
	}
	sr.Calls = append(sr.Calls, delta)
	sr.LastCall = &sr.Calls[len(sr.Calls)-1]
}

// appendArguments adds one streamed argument fragment to what has arrived so far.
// A fragment in the specification's shape is a JSON string holding the next piece
// of the arguments text, so the pieces are joined and kept as one JSON string. A
// fragment in any other shape (an object from a gateway) is complete on its own
// and replaces what was there.
func appendArguments(have, fragment json.RawMessage) json.RawMessage {
	frag := trimSpace(fragment)
	if len(frag) == 0 {
		return have
	}
	var piece string
	if frag[0] != '"' || json.Unmarshal(frag, &piece) != nil {
		return fragment
	}
	var prev string
	_ = json.Unmarshal(have, &prev) // not a string yet (empty or object): start over
	joined, _ := json.Marshal(prev + piece)
	return joined
}

// FinalReply builds the accumulated Reply from the stream.
func (sr StreamResult) FinalReply() Reply {
	// The stream index only ties fragments together; it is not part of the call
	// that goes back to the provider in the next assistant turn.
	calls := slices.Clone(sr.Calls)
	for i := range calls {
		calls[i].Index = nil
	}
	return Reply{Content: sr.Content.String(), Calls: calls, FinishReason: "stop"}
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
