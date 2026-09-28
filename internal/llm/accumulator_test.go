package llm

// The accumulator, and the bug that makes a provider's tool call disappear.
//
// Reported symptom: a real task failed three times with
//
//	could not obtain the action from the LLM: all 3 attempts were exhausted:
//	OpenAI returned an empty response (finish_reason="tool_calls")
//
// A provider that says "tool_calls" and a client that sees NONE means the calls were dropped on
// the way in. The accumulator decided identity by `ID`, and OpenAI sends the id ONCE — on the
// first fragment — while every continuation fragment arrives carrying only
// `{index, function:{arguments:"..."}}`, with no id at all. So the second and later fragments of
// a call were read as a NEW call, and the list filled with empty shells.
//
// These tests are about what the accumulator must do with the fragments a provider actually
// sends, which is not what the specification's example shows.

import (
	"encoding/json"
	"testing"
)

// TestFragmentsWithoutAnIDContinueTheSameCall is the exact wire shape OpenAI sends for a tool
// call: the first fragment carries id + name + the first slice of arguments, and each later
// fragment carries arguments only.
func TestFragmentsWithoutAnIDContinueTheSameCall(t *testing.T) {
	var sr StreamResult

	// First fragment: the call is announced, with its identity.
	sr.Handle(StreamChunk{Event: StreamToolCall, Call: &ToolCall{
		ID:   "call_abc",
		Type: "function",
		Function: FunctionCall{Name: "search_skills",
			Arguments: json.RawMessage(`{"query":"habilitar el`)},
	}})

	// Continuations: arguments only, NO id. This is what the provider really sends.
	sr.Handle(StreamChunk{Event: StreamToolCall, Call: &ToolCall{
		Function: FunctionCall{Arguments: json.RawMessage(` mantenedor`)},
	}})
	sr.Handle(StreamChunk{Event: StreamToolCall, Call: &ToolCall{
		Function: FunctionCall{Arguments: json.RawMessage(` para fichas"}`)},
	}})

	reply := sr.FinalReply()
	if len(reply.Calls) != 1 {
		t.Fatalf("the three fragments are ONE call, got %d: %+v", len(reply.Calls), reply.Calls)
	}
	got := string(reply.Calls[0].Function.Arguments)
	want := `{"query":"habilitar el mantenedor para fichas"}`
	if got != want {
		t.Errorf("the fragments must be joined, got %q want %q", got, want)
	}
	if reply.Calls[0].ID != "call_abc" {
		t.Errorf("the announced id must survive, got %q", reply.Calls[0].ID)
	}
	if reply.Calls[0].Function.Name != "search_skills" {
		t.Errorf("the announced name must survive, got %q", reply.Calls[0].Function.Name)
	}
}

// TestTwoRealCallsStayTwo: the fix must not merge calls that are genuinely distinct — two
// fragments carrying DIFFERENT ids are two calls, which is how a model asks for two things at
// once.
func TestTwoRealCallsStayTwo(t *testing.T) {
	var sr StreamResult
	sr.Handle(StreamChunk{Event: StreamToolCall, Call: &ToolCall{
		ID: "call_1", Function: FunctionCall{Name: "one", Arguments: json.RawMessage(`{"a":1}`)}}})
	sr.Handle(StreamChunk{Event: StreamToolCall, Call: &ToolCall{
		ID: "call_2", Function: FunctionCall{Name: "two", Arguments: json.RawMessage(`{"b":2}`)}}})

	reply := sr.FinalReply()
	if len(reply.Calls) != 2 {
		t.Fatalf("two ids are two calls, got %d: %+v", len(reply.Calls), reply.Calls)
	}
	if reply.Calls[0].Function.Name != "one" || reply.Calls[1].Function.Name != "two" {
		t.Errorf("the calls kept the wrong payloads: %+v", reply.Calls)
	}
}

// TestAnIdRestatedReplacesTheArguments: a fragment that carries the id again is a RESTATEMENT
// of the same call, not a piece of it, and the arguments it carries are the whole document.
//
// This was measured against a real gateway rather than assumed: it streams each call COMPLETE
// in a single fragment that carries id, name and the full arguments, and it sends several such
// fragments for several calls. Joining two restatements would produce two JSON documents back to
// back — which is exactly what a first version of this fix did, and what this test now pins.
func TestAnIdRestatedReplacesTheArguments(t *testing.T) {
	var sr StreamResult
	sr.Handle(StreamChunk{Event: StreamToolCall, Call: &ToolCall{
		ID: "call_x", Function: FunctionCall{Name: "f", Arguments: json.RawMessage(`{"a":1}`)}}})
	// The same call again, with the complete arguments: it replaces, it does not append.
	sr.Handle(StreamChunk{Event: StreamToolCall, Call: &ToolCall{
		ID: "call_x", Function: FunctionCall{Name: "f", Arguments: json.RawMessage(`{"a":2}`)}}})

	reply := sr.FinalReply()
	if len(reply.Calls) != 1 {
		t.Fatalf("a repeated id is the same call, got %d: %+v", len(reply.Calls), reply.Calls)
	}
	got := string(reply.Calls[0].Function.Arguments)
	if got != `{"a":2}` {
		t.Errorf("a restatement replaces the arguments, got %q", got)
	}
	if !json.Valid([]byte(got)) {
		t.Errorf("the result must still be one JSON document, got %q", got)
	}
}

// TestANamelessIDlessFragmentJoinsTheLastCall: some gateways send continuation fragments with a
// name as well as arguments. A NAME is not a new call when there is no id to go with it — only
// an id can announce one.
func TestANamelessIDlessFragmentJoinsTheLastCall(t *testing.T) {
	var sr StreamResult
	sr.Handle(StreamChunk{Event: StreamToolCall, Call: &ToolCall{
		ID: "call_y", Function: FunctionCall{Name: "f", Arguments: json.RawMessage(`{"a":`)}}})
	sr.Handle(StreamChunk{Event: StreamToolCall, Call: &ToolCall{
		Function: FunctionCall{Name: "f", Arguments: json.RawMessage(`1}`)}}})

	reply := sr.FinalReply()
	if len(reply.Calls) != 1 {
		t.Fatalf("a name without an id must not start a new call, got %d: %+v",
			len(reply.Calls), reply.Calls)
	}
	if got := string(reply.Calls[0].Function.Arguments); got != `{"a":1}` {
		t.Errorf("the arguments must be joined across the name repetition, got %q", got)
	}
}

// TestTheVeryFirstFragmentMayCarryNoID: a gateway that never sends an id at all still gets one
// call, not zero — an empty list is the failure the reported symptom is made of.
func TestTheVeryFirstFragmentMayCarryNoID(t *testing.T) {
	var sr StreamResult
	sr.Handle(StreamChunk{Event: StreamToolCall, Call: &ToolCall{
		Function: FunctionCall{Name: "f", Arguments: json.RawMessage(`{"a":1}`)}}})

	reply := sr.FinalReply()
	if len(reply.Calls) != 1 {
		t.Fatalf("a call with no id is still a call, got %d", len(reply.Calls))
	}
	if reply.Calls[0].Function.Name != "f" {
		t.Errorf("the name must be kept, got %q", reply.Calls[0].Function.Name)
	}
}

// TestANilCallIsIgnored: the chunk carries a pointer, and a provider-side oddity that produces
// a nil one must not panic the accumulator. It is a real shape — the event is emitted before the
// payload is filled in — so the guard is exercised rather than assumed.
func TestANilCallIsIgnored(t *testing.T) {
	var sr StreamResult
	if got := sr.Handle(StreamChunk{Event: StreamToolCall, Call: nil}); got {
		t.Error("a nil call does not end the stream")
	}
	if len(sr.FinalReply().Calls) != 0 {
		t.Error("a nil call must add nothing")
	}
}

// TestAFragmentWithNoArgumentsKeepsTheNameOnly: a gateway that sends the name and NO arguments
// (a call with an empty object) must still produce a call — the arguments being absent is not
// the same as the call being absent.
func TestAFragmentWithNoArgumentsKeepsTheNameOnly(t *testing.T) {
	var sr StreamResult
	sr.Handle(StreamChunk{Event: StreamToolCall, Call: &ToolCall{
		ID: "c", Function: FunctionCall{Name: "list_skills"}}})
	// A continuation that also brings no arguments, which must not erase anything.
	sr.Handle(StreamChunk{Event: StreamToolCall, Call: &ToolCall{
		Function: FunctionCall{Name: "list_skills"}}})

	reply := sr.FinalReply()
	if len(reply.Calls) != 1 {
		t.Fatalf("want one call, got %d", len(reply.Calls))
	}
	if reply.Calls[0].Function.Name != "list_skills" || reply.Calls[0].ID != "c" {
		t.Errorf("the announced fields must survive, got %+v", reply.Calls[0])
	}
}

// TestAContinuationCarriesTheTypeItSends: the type is part of the call, and a continuation that
// states it must not have it dropped on the floor — the caller dispatches on it.
func TestAContinuationCarriesTheTypeItSends(t *testing.T) {
	var sr StreamResult
	// Announced with an id but no type yet, which is what a provider that sends the type on a
	// later fragment looks like.
	sr.Handle(StreamChunk{Event: StreamToolCall, Call: &ToolCall{
		ID: "c", Function: FunctionCall{Name: "f", Arguments: json.RawMessage(`{"a":`)}}})
	// The continuation declares the type and finishes the arguments.
	sr.Handle(StreamChunk{Event: StreamToolCall, Call: &ToolCall{
		Type: "function", Function: FunctionCall{Arguments: json.RawMessage(`1}`)}}})

	reply := sr.FinalReply()
	if len(reply.Calls) != 1 {
		t.Fatalf("want one call, got %d", len(reply.Calls))
	}
	if reply.Calls[0].Type != "function" {
		t.Errorf("the type the continuation carried must be kept, got %q", reply.Calls[0].Type)
	}
	if got := string(reply.Calls[0].Function.Arguments); got != `{"a":1}` {
		t.Errorf("the arguments must be joined, got %q", got)
	}
}
