package llm

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"
)

// TestExtractJSONSurvivesWhatARealRunSent replays the replies that a real session could not use:
// every one of them carried a valid object, and every one was lost to what came before it.
func TestExtractJSONSurvivesWhatARealRunSent(t *testing.T) {
	cases := []struct {
		name, input, want string
	}{
		{"a shell test before the object",
			"I poll with [ -s /tmp/check.exit ] until it ends.\n{\"done\":false}", `{"done":false}`},
		{"a Go literal before the object",
			"The test builds {Author: AuthorAgent, Text: label} and checks it.\n{\"done\":true}", `{"done":true}`},
		{"a literal tab inside a string",
			"{\"command\":\"printf 'a\tb'\"}", `{"command":"printf 'a\tb'"}`},
		{"raw newlines, carriage returns and other controls inside a string",
			"{\"c\":\"x\ny\rz\x01\"}", `{"c":"x\ny\rz\u0001"}`},
		{"a fenced block with nested braces",
			"Here:\n```json\n{\"a\":{\"b\":{\"c\":1}},\"d\":2}\n```", `{"a":{"b":{"c":1}},"d":2}`},
		{"a fenced block whose body is not the JSON falls back to the text",
			"```\ngo test ./...\n```\n{\"a\":1}", `{"a":1}`},
		{"an object is preferred over a list of scalars",
			"see step [1] and [ \"$x\" ], then {\"a\":1}", `{"a":1}`},
		{"a list of objects is kept whole",
			"[{\"kind\":\"command\"}]", `[{"kind":"command"}]`},
		{"a list of scalars when nothing else is there",
			"the values are [1, 2]", `[1, 2]`},
		{"an escaped quote inside a repaired string",
			"{\"c\":\"say \\\"hi\\\"\tnow\"}", `{"c":"say \"hi\"\tnow"}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			raw, err := ExtractJSON(tc.input)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if string(raw) != tc.want {
				t.Errorf("extracted %s, want %s", raw, tc.want)
			}
		})
	}
}

// TestExtractJSONKeepsItsErrors: what no candidate can fix is still named the way it was.
func TestExtractJSONKeepsItsErrors(t *testing.T) {
	if _, err := ExtractJSON("no brackets at all"); err == nil || !strings.Contains(err.Error(), "contains no JSON") {
		t.Errorf("err = %v", err)
	}
	if _, err := ExtractJSON(`{"a":1`); err == nil || !strings.Contains(err.Error(), "truncated") {
		t.Errorf("err = %v", err)
	}
	// Balanced but not JSON: handed back so DecodeJSON can say how it does not fit.
	raw, err := ExtractJSON("{Author: AuthorAgent}")
	if err != nil || string(raw) != "{Author: AuthorAgent}" {
		t.Errorf("raw = %s, err = %v", raw, err)
	}
	var dest map[string]any
	if err := DecodeJSON("{Author: AuthorAgent}", &dest); err == nil || !strings.Contains(err.Error(), "does not fit") {
		t.Errorf("err = %v", err)
	}
}

func TestJSONSchemaTravelsInTheContext(t *testing.T) {
	if got := jsonSchemaFrom(context.Background()); got != nil {
		t.Errorf("no schema was set: %s", got)
	}
	schema := json.RawMessage(`{"type":"object"}`)
	if got := jsonSchemaFrom(WithJSONSchema(context.Background(), schema)); string(got) != string(schema) {
		t.Errorf("schema = %s", got)
	}
}

// structuredScript is what claude 2.1 prints for a --json-schema turn: the answer is a call to its
// own StructuredOutput tool, the message stops for tool_use, and the result repeats the object.
var structuredScript = lines(
	evStart,
	`{"type":"assistant","message":{"content":[{"type":"tool_use","id":"toolu_9","name":"StructuredOutput","input":{"done":true,"actions":[]}}]}}`,
	stopReason("tool_use"), evStop,
	"sleep",
)

func TestClaudeCodeEnforcesTheSchema(t *testing.T) {
	schema := json.RawMessage(`{"type":"object","properties":{"done":{"type":"boolean"}}}`)
	ctx := WithJSONSchema(context.Background(), schema)

	c, record := claudeTestClient(t, structuredScript, nil)
	text, err := c.Complete(ctx, hi)
	if err != nil || text != `{"done":true,"actions":[]}` {
		t.Fatalf("Complete = %q, %v", text, err)
	}
	rec := readRecord(t, record)
	if i := slices.Index(rec.Args, "--json-schema"); i < 0 || rec.Args[i+1] != string(schema) {
		t.Errorf("argv = %q, want --json-schema with the schema", rec.Args)
	}

	// Streaming reaches the same answer: no text streams, the reply carries it.
	c, _ = claudeTestClient(t, structuredScript, nil)
	text, err = c.CompleteStream(ctx, hi, func(string, bool) {})
	if err != nil || text != `{"done":true,"actions":[]}` {
		t.Fatalf("CompleteStream = %q, %v", text, err)
	}

	// Only the result carries it: the result is the answer.
	c, _ = claudeTestClient(t, `{"type":"result","subtype":"success","num_turns":2,"structured_output":{"done":false}}`, nil)
	if text, err := c.Complete(ctx, hi); err != nil || text != `{"done":false}` {
		t.Fatalf("Complete = %q, %v", text, err)
	}

	// A null structured output is no answer.
	c, _ = claudeTestClient(t, `{"type":"result","subtype":"success","num_turns":1,"structured_output":null}`, nil)
	if _, err := c.Complete(ctx, hi); err == nil || !strings.Contains(err.Error(), "no text") {
		t.Errorf("err = %v", err)
	}

	// With tools the turn answers with calls, so the schema is not passed.
	c, record = claudeTestClient(t, toolScript, nil)
	if _, err := c.CompleteTools(ctx, hi, []Tool{{Type: "function", Function: FunctionDef{Name: "read_file"}}}); err != nil {
		t.Fatal(err)
	}
	if rec := readRecord(t, record); slices.Contains(rec.Args, "--json-schema") {
		t.Errorf("a tool turn must not carry a schema: %q", rec.Args)
	}
}
