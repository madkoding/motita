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

	var firstBalanced, firstList []byte
	opened := false
	for _, region := range regions {
		for from := 0; ; {
			i := strings.IndexAny(region[from:], "{[")
			if i < 0 {
				break
			}
			start := from + i
			from = start + 1
			opened = true
			raw, ok := balancedAt(region, start)
			if !ok {
				continue
			}
			if firstBalanced == nil {
				firstBalanced = raw
			}
			if !json.Valid(raw) {
				if raw = escapeControls(raw); !json.Valid(raw) {
					continue
				}
			}
			if raw[0] == '{' || holdsObject(raw) {
				return raw, nil
			}
			if firstList == nil {
				firstList = raw
			}
		}
	}
	switch {
	case firstList != nil:
		return firstList, nil
	case firstBalanced != nil:
		return firstBalanced, nil
	case !opened:
		return nil, fmt.Errorf("the response contains no JSON: %q", truncate(clean, 200))
	}
	return nil, fmt.Errorf("the JSON in the response is truncated: %q", truncate(clean, 200))
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
			switch ch {
			case '\n':
				out.WriteString(`\n`)
			case '\t':
				out.WriteString(`\t`)
			case '\r':
				out.WriteString(`\r`)
			default:
				fmt.Fprintf(&out, `\u%04x`, ch)
			}
			continue
		}
		out.WriteByte(ch)
	}
	return out.Bytes()
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
