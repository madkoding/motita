package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// jsonFence finds a markdown code block. Only its body is captured, and it is scanned like the
// rest of the reply: the lazy `\{.*?\}` this used to be stopped at the first nested brace and
// handed back half an object.
var jsonFence = regexp.MustCompile("(?s)```(?:json)?[ \\t]*\\n?(.*?)```")

// schemaKey carries a JSON schema the reply must follow. See WithJSONSchema.
type schemaKey struct{}

// WithJSONSchema asks the provider for a reply that IS a JSON value following schema, where the
// provider can enforce it.
//
// Reported from a real session: about 40% of the rounds of a long task were spent on replies that
// could not be used - pseudo tool calls written as text, a shell test `[ -s file ]` read as the
// JSON, literal tabs inside strings. A provider that can constrain its output (claude-code,
// through claude's --json-schema) makes those replies impossible; the others ignore the schema and
// the reply goes through ExtractJSON as before.
func WithJSONSchema(ctx context.Context, schema json.RawMessage) context.Context {
	return context.WithValue(ctx, schemaKey{}, schema)
}

// jsonSchemaFrom returns the schema WithJSONSchema put in ctx, or nil.
func jsonSchemaFrom(ctx context.Context) json.RawMessage {
	schema, _ := ctx.Value(schemaKey{}).(json.RawMessage)
	return schema
}

// ExtractJSON gets the JSON value out of an LLM response, tolerating the usual decorations:
// markdown blocks, text before and after, and nested braces.
//
// Every '{' and '[' is a candidate, the bodies of code blocks first, and the first one that is
// VALID JSON wins. Taking the first bracket whatever it opened is what failed in real use: a
// reasoning line quoting `[ -s /tmp/check.exit ]`, or a Go literal `{Author: AuthorAgent}`, was
// returned as "the JSON" and the real object after it was never looked at. An object, or a list
// that holds objects, is preferred over a bare list (`[ "$x" ]` is valid JSON too). A candidate
// whose only fault is a raw control character inside a string (a literal tab in a Go snippet) is
// repaired rather than refused.
//
// When nothing valid exists, the first balanced candidate is returned so DecodeJSON can say how it
// does not fit; with no balanced candidate the response is truncated, and with no bracket at all
// it contains no JSON.
func ExtractJSON(text string) ([]byte, error) {
	clean := strings.TrimSpace(text)
	if clean == "" {
		return nil, errors.New("the LLM response is empty")
	}

	var regions []string
	for _, m := range jsonFence.FindAllStringSubmatch(clean, -1) {
		regions = append(regions, m[1])
	}
	regions = append(regions, clean)

	var c jsonCandidates
	for _, region := range regions {
		if raw, ok := c.scan(region); ok {
			return raw, nil
		}
	}
	switch {
	case c.firstList != nil:
		return c.firstList, nil
	case c.firstBalanced != nil:
		return c.firstBalanced, nil
	case !c.opened:
		return nil, fmt.Errorf("the response contains no JSON: %q", truncate(clean, 200))
	default:
		return nil, fmt.Errorf("the JSON in the response is truncated: %q", truncate(clean, 200))
	}
}

// jsonCandidates is what ExtractJSON has seen so far: whether any bracket opened, the first
// balanced candidate and the first valid list.
type jsonCandidates struct {
	firstBalanced, firstList []byte
	opened                   bool
}

// scan looks at every bracket of region in turn, and returns the first valid object, or list that
// holds objects, it starts.
func (c *jsonCandidates) scan(region string) ([]byte, bool) {
	for from := 0; ; {
		i := strings.IndexAny(region[from:], "{[")
		if i < 0 {
			return nil, false
		}
		start := from + i
		from = start + 1
		c.opened = true
		if raw, ok := c.consider(region, start); ok {
			return raw, true
		}
	}
}

// consider reads the candidate at the bracket at start, and reports whether it is the answer: a
// valid object, or a valid list that holds objects. A bare list is kept as a fallback.
func (c *jsonCandidates) consider(region string, start int) ([]byte, bool) {
	raw, ok := balancedAt(region, start)
	if !ok {
		return nil, false
	}
	if c.firstBalanced == nil {
		c.firstBalanced = raw
	}
	if raw, ok = repairedJSON(raw); !ok {
		return nil, false
	}
	if raw[0] == '{' || holdsObject(raw) {
		return raw, true
	}
	if c.firstList == nil {
		c.firstList = raw
	}
	return nil, false
}

// repairedJSON returns raw when it is valid JSON, or raw with the control characters inside its
// strings escaped when that makes it valid.
func repairedJSON(raw []byte) ([]byte, bool) {
	if json.Valid(raw) {
		return raw, true
	}
	raw = escapeControls(raw)
	return raw, json.Valid(raw)
}

// balancedAt returns the text from the bracket at start to its balanced partner, respecting
// strings and escapes, or false when the text ends first.
func balancedAt(s string, start int) ([]byte, bool) {
	open := s[start]
	closing := byte('}')
	if open == '[' {
		closing = ']'
	}
	depth := 0
	inString := false
	escaped := false
	for i := start; i < len(s); i++ {
		ch := s[i]
		switch {
		case escaped:
			escaped = false
		case ch == '\\' && inString:
			escaped = true
		case ch == '"':
			inString = !inString
		case inString:
			// nothing
		case ch == open:
			depth++
		case ch == closing:
			depth--
			if depth == 0 {
				return []byte(s[start : i+1]), true
			}
		default:
			// Any other character outside a string changes nothing.
		}
	}
	return nil, false
}

// escapeControls escapes the raw control characters inside the strings of raw, which JSON forbids
// and models write anyway when a value holds code.
func escapeControls(raw []byte) []byte {
	var out bytes.Buffer
	inString := false
	escaped := false
	for _, ch := range raw {
		switch {
		case escaped:
			escaped = false
		case ch == '\\' && inString:
			escaped = true
		case ch == '"':
			inString = !inString
		case inString && ch < 0x20:
			writeEscapedControl(&out, ch)
			continue
		default:
			// Any other character is copied as it is.
		}
		out.WriteByte(ch)
	}
	return out.Bytes()
}

// writeEscapedControl writes the JSON escape of a control character.
func writeEscapedControl(out *bytes.Buffer, ch byte) {
	switch ch {
	case '\n':
		out.WriteString(`\n`)
	case '\t':
		out.WriteString(`\t`)
	case '\r':
		out.WriteString(`\r`)
	default:
		fmt.Fprintf(out, `\u%04x`, ch)
	}
}

// holdsObject reports whether a valid JSON list has an object among its items.
func holdsObject(raw []byte) bool {
	var items []json.RawMessage
	_ = json.Unmarshal(raw, &items)
	for _, it := range items {
		if it[0] == '{' {
			return true
		}
	}
	return false
}
