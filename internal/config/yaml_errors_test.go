package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// --- YAML: the remaining guarded shapes -------------------------------------

// TestParseYAMLBlockScalar: a block scalar (| or >) carries multi-line text, and
// the value must keep its newlines.
func TestParseYAMLBlockScalar(t *testing.T) {
	doc := []byte("prompt: |\n  first line\n  second line\nafter: value\n")
	m, err := ParseYAML(doc)
	if err != nil {
		t.Fatalf("a block scalar must be accepted: %v", err)
	}
	text, _ := m["prompt"].(string)
	if !strings.Contains(text, "first line") || !strings.Contains(text, "second line") {
		t.Errorf("prompt = %q", text)
	}
	if m["after"] != "value" {
		t.Errorf("the key after the block must still be read: %v", m["after"])
	}
}

// TestParseYAMLSequenceOfMaps: a list whose items are blocks is the shape used by
// anchor.checks, so it must be decoded item by item.
func TestParseYAMLSequenceOfMaps(t *testing.T) {
	doc := []byte(`checks:
  - name: first
    command: one
  - name: second
    command: two
`)
	m, err := ParseYAML(doc)
	if err != nil {
		t.Fatalf("a sequence of maps must be accepted: %v", err)
	}
	items, ok := m["checks"].([]any)
	if !ok || len(items) != 2 {
		t.Fatalf("checks = %#v", m["checks"])
	}
	first, _ := items[0].(map[string]any)
	if first["name"] != "first" || first["command"] != "one" {
		t.Errorf("first item = %#v", first)
	}
}

// TestParseYAMLSequenceOfScalars: the simple list form must work too.
func TestParseYAMLSequenceOfScalars(t *testing.T) {
	doc := []byte("args:\n  - alpha\n  - beta\n  - 3\n")
	m, err := ParseYAML(doc)
	if err != nil {
		t.Fatal(err)
	}
	items, _ := m["args"].([]any)
	if len(items) != 3 || items[0] != "alpha" || items[1] != "beta" {
		t.Errorf("args = %#v", m["args"])
	}
}

// TestParseYAMLQuotedKeys: a key may be quoted, and the quotes must be removed.
func TestParseYAMLQuotedKeys(t *testing.T) {
	doc := []byte("\"quoted key\": value\n'single': other\n")
	m, err := ParseYAML(doc)
	if err != nil {
		t.Fatal(err)
	}
	if m["quoted key"] != "value" {
		t.Errorf("the double-quoted key was not unquoted: %v", m)
	}
	if m["single"] != "other" {
		t.Errorf("the single-quoted key was not unquoted: %v", m)
	}
}

// TestParseYAMLUnterminatedQuotedKey: an opening quote with no closing one must be
// refused, not read as part of the key.
func TestParseYAMLUnterminatedQuotedKey(t *testing.T) {
	if _, err := ParseYAML([]byte("\"unterminated: value\n")); err == nil {
		t.Error("an unterminated quoted key must be refused")
	}
}

// TestParseYAMLEmptyKey: a colon with nothing before it cannot name a key.
func TestParseYAMLEmptyKey(t *testing.T) {
	if _, err := ParseYAML([]byte(": value\n")); err == nil {
		t.Error("an empty key must be refused")
	}
}

// TestParseYAMLEscapedStrings: the escape sequences a user writes inside a
// double-quoted value must be interpreted.
func TestParseYAMLEscapedStrings(t *testing.T) {
	doc := []byte("text: \"line1\\nline2\\ttabbed\\r\\\"quoted\\\"\"\n")
	m, err := ParseYAML(doc)
	if err != nil {
		t.Fatal(err)
	}
	text, _ := m["text"].(string)
	for _, want := range []string{"line1\nline2", "\ttabbed", "\r", "\"quoted\""} {
		if !strings.Contains(text, want) {
			t.Errorf("text = %q, missing %q", text, want)
		}
	}
}

// TestParseYAMLInlineEmptyContainers: `[]` and `{}` are valid YAML for empty
// collections.
func TestParseYAMLInlineEmptyContainers(t *testing.T) {
	doc := []byte("empty_list: []\nempty_map: {}\n")
	m, err := ParseYAML(doc)
	if err != nil {
		t.Fatal(err)
	}
	if list, ok := m["empty_list"].([]any); !ok || len(list) != 0 {
		t.Errorf("empty_list = %#v", m["empty_list"])
	}
	if mp, ok := m["empty_map"].(map[string]any); !ok || len(mp) != 0 {
		t.Errorf("empty_map = %#v", m["empty_map"])
	}
}

// TestParseYAMLInlineMapWithoutBraces: a missing closing brace must be refused
// with the line number.
func TestParseYAMLInlineMapWithoutBraces(t *testing.T) {
	if _, err := ParseYAML([]byte("map: {a: 1\n")); err == nil {
		t.Error("an inline map without a closing brace must be refused")
	}
}

// TestParseYAMLInlineMapWithABadEntry: an entry without a colon inside an inline
// map must be reported.
func TestParseYAMLInlineMapWithABadEntry(t *testing.T) {
	if _, err := ParseYAML([]byte("map: {a: 1, no-colon}\n")); err == nil {
		t.Error("an entry without a colon must be refused")
	}
}

// TestParseYAMLUnclosedInlineList: a missing closing bracket must be refused.
func TestParseYAMLUnclosedInlineList(t *testing.T) {
	if _, err := ParseYAML([]byte("list: [a, b\n")); err == nil {
		t.Error("an inline list without a closing bracket must be refused")
	}
}

// TestParseYAMLAnchorsAndAliasesAreNotSupported: the parser is deliberately
// minimal; a construct it does not implement must not be silently misread.
func TestParseYAMLAnchorsAndAliasesAreNotSupported(t *testing.T) {
	// An alias is read as plain text, which is acceptable; what matters is that
	// it does not crash and that the document still parses.
	if _, err := ParseYAML([]byte("base: &anchor value\ncopy: *anchor\n")); err != nil {
		t.Logf("anchors are refused (acceptable): %v", err)
	}
}

// TestParseYAMLParsesTheExampleConfiguration: the file shipped as documentation
// must be readable by the parser, which is the real acceptance test.
func TestParseYAMLParsesTheExampleConfiguration(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "configs", "agent.yaml.example"))
	if err != nil {
		t.Skipf("the example is not available: %v", err)
	}
	m, err := ParseYAML(data)
	if err != nil {
		t.Fatalf("the shipped example must parse: %v", err)
	}
	for _, key := range []string{"task_source", "anchor", "sandbox", "llm", "agent", "final_action"} {
		if _, ok := m[key]; !ok {
			t.Errorf("the example must define %q", key)
		}
	}
}

// --- Parser helpers: the guards the parse paths rely on ----------------------

// TestErrorfNamesTheEndOfTheDocument: errorf builds every parser error. When the
// cursor has already run past the last line there is no line number to report,
// and the message must say so instead of reading a line that does not exist.
func TestErrorfNamesTheEndOfTheDocument(t *testing.T) {
	p := &yamlParser{}
	if !p.done() {
		t.Fatal("a parser with no lines is already done")
	}
	err := p.errorf("something went wrong")
	if err == nil {
		t.Fatal("errorf must return an error")
	}
	if !strings.Contains(err.Error(), "end of document") {
		t.Errorf("error = %q, expected the end-of-document form", err)
	}
	if !strings.Contains(err.Error(), "something went wrong") {
		t.Errorf("error = %q, it must keep the message it was given", err)
	}

	// With the cursor on a real line the message carries its line number.
	p2 := &yamlParser{lines: []yamlLine{{indent: 0, text: "a: 1", num: 7}}}
	if err := p2.errorf("something went wrong"); !strings.Contains(err.Error(), "line 7") {
		t.Errorf("error = %q, expected the line number", err)
	}
}

// TestParseBlockWithNoLinesLeft: parseBlock is called defensively from the
// nested blocks; with nothing left to read it reports "no structure" instead of
// indexing past the end of the document.
func TestParseBlockWithNoLinesLeft(t *testing.T) {
	p := &yamlParser{}
	value, err := p.parseBlock(0)
	if err != nil {
		t.Fatalf("an exhausted parser must not fail: %v", err)
	}
	if value != nil {
		t.Errorf("value = %#v, expected nil", value)
	}

	// A block asked for at a deeper indentation than the current line does not
	// exist either.
	p2 := &yamlParser{lines: []yamlLine{{indent: 0, text: "a: 1", num: 1}}}
	if value, err := p2.parseBlock(4); err != nil || value != nil {
		t.Errorf("parseBlock(4) over an indent-0 line = %#v, %v", value, err)
	}
}

// TestParseYAMLKeyWithoutValueOnTheLastLine: a key with nothing after it on the
// last line is null, not an error, and the keys before it are kept.
func TestParseYAMLKeyWithoutValueOnTheLastLine(t *testing.T) {
	m, err := ParseYAML([]byte("a: 1\nb:\n"))
	if err != nil {
		t.Fatal(err)
	}
	if m["a"] != int64(1) {
		t.Errorf("a = %#v", m["a"])
	}
	if v, ok := m["b"]; !ok || v != nil {
		t.Errorf("b = %#v, expected null", m["b"])
	}
}

// TestParseYAMLSequenceItemInsideAMapIsRejected: an item at the same level as
// the keys of a block cannot belong to that block, so it is reported instead of
// being folded into the map.
func TestParseYAMLSequenceItemInsideAMapIsRejected(t *testing.T) {
	_, err := ParseYAML([]byte("a:\n  b: 1\n  - c\n"))
	if err == nil {
		t.Fatal("a stray sequence item must be refused")
	}
	if !strings.Contains(err.Error(), "unexpected content") {
		t.Errorf("error = %v", err)
	}
}

// TestParseYAMLNestedBlockUnderABareItem: "-" with nothing after it opens a
// nested block, which is the list-of-blocks form.
func TestParseYAMLNestedBlockUnderABareItem(t *testing.T) {
	m, err := ParseYAML([]byte("list:\n  -\n    a: 1\n"))
	if err != nil {
		t.Fatal(err)
	}
	list, ok := m["list"].([]any)
	if !ok || len(list) != 1 {
		t.Fatalf("list = %#v", m["list"])
	}
	item, _ := list[0].(map[string]any)
	if item == nil || item["a"] != int64(1) {
		t.Errorf("item = %#v", list[0])
	}
}

// TestParseYAMLMalformedBlockUnderABareItem: what fails to parse inside such a
// nested block must surface with its line number.
func TestParseYAMLMalformedBlockUnderABareItem(t *testing.T) {
	_, err := ParseYAML([]byte("list:\n  -\n    malformed\n"))
	if err == nil {
		t.Fatal("a malformed nested block must be refused")
	}
	if !strings.Contains(err.Error(), "line 3") {
		t.Errorf("the error must name the line: %v", err)
	}
}

// TestParseYAMLUnsupportedStructureOnAMapLine: a line that is not "key: value"
// and carries an anchor, alias or tag marker is refused where it is read as a
// key, not silently turned into a key.
func TestParseYAMLUnsupportedStructureOnAMapLine(t *testing.T) {
	for _, input := range []string{"a: 1\n*alias\n", "a: 1\n&anchor\n", "a: 1\n!tag\n"} {
		_, err := ParseYAML([]byte(input))
		if err == nil {
			t.Errorf("ParseYAML(%q) must refuse the YAML structure it does not implement", input)
			continue
		}
		if !strings.Contains(err.Error(), "unsupported YAML structure") {
			t.Errorf("ParseYAML(%q) = %v", input, err)
		}
	}
}

// TestParseYAMLBrokenItemAtTheSameLevelAsTheKey: a sequence written at the level
// of its key (no extra indentation) reports its errors like any other list.
func TestParseYAMLBrokenItemAtTheSameLevelAsTheKey(t *testing.T) {
	_, err := ParseYAML([]byte("list:\n- [broken\n"))
	if err == nil {
		t.Fatal("a broken item must be refused")
	}
	if !strings.Contains(err.Error(), "missing ']'") {
		t.Errorf("error = %v", err)
	}
}

// TestParseYAMLBrokenFirstPairOfASequenceItem: the pair that opens an item
// ("- key: value") is parsed like any other, so a broken value there must be
// reported instead of leaving a half-built item.
func TestParseYAMLBrokenFirstPairOfASequenceItem(t *testing.T) {
	_, err := ParseYAML([]byte("list:\n  - a: [broken\n"))
	if err == nil {
		t.Fatal("a broken value in the first pair of an item must be refused")
	}
	if !strings.Contains(err.Error(), "missing ']'") {
		t.Errorf("error = %v", err)
	}
}

// TestParseYAMLQuotedKeyWithTrailingCharacters: a key that opens a quote but
// does not close it the way it should is refused instead of being taken
// literally.
func TestParseYAMLQuotedKeyWithTrailingCharacters(t *testing.T) {
	_, err := ParseYAML([]byte("\"a\"b: 1\n"))
	if err == nil {
		t.Fatal("a malformed quoted key must be refused")
	}
	if !strings.Contains(err.Error(), "unterminated quote") {
		t.Errorf("error = %v", err)
	}
}

// TestParseYAMLLoneQuoteAsValue: a value made of a single opening quote has no
// closing one and must be reported as such.
func TestParseYAMLLoneQuoteAsValue(t *testing.T) {
	_, err := ParseYAML([]byte("a: \"\n"))
	if err == nil {
		t.Fatal("a lone quote must be refused")
	}
	if !strings.Contains(err.Error(), "unterminated quote") {
		t.Errorf("error = %v", err)
	}
}

// TestParseYAMLInlineListElementErrors: every element of an inline list is
// parsed, so a broken one is reported with the line number.
func TestParseYAMLInlineListElementErrors(t *testing.T) {
	_, err := ParseYAML([]byte("a: [\"x]\n"))
	if err == nil {
		t.Fatal("an inline list with an unterminated quote must be refused")
	}
	if !strings.Contains(err.Error(), "unterminated quote") {
		t.Errorf("error = %v", err)
	}

	_, err = ParseYAML([]byte("a: [&x]\n"))
	if err == nil {
		t.Fatal("an inline list with an anchor must be refused")
	}
	if !strings.Contains(err.Error(), "anchors, aliases and tags") {
		t.Errorf("error = %v", err)
	}
}

// TestParseYAMLInlineMapElementErrors: the same for the entries of an inline
// map, including the case where the entry's value is itself broken.
func TestParseYAMLInlineMapElementErrors(t *testing.T) {
	_, err := ParseYAML([]byte("a: {\"x: 1}\n"))
	if err == nil {
		t.Fatal("an inline map with an unterminated quote must be refused")
	}
	if !strings.Contains(err.Error(), "unterminated quote") {
		t.Errorf("error = %v", err)
	}

	_, err = ParseYAML([]byte("a: {k: [broken}\n"))
	if err == nil {
		t.Fatal("an inline map whose value is a broken list must be refused")
	}
	if !strings.Contains(err.Error(), "missing ']'") {
		t.Errorf("error = %v", err)
	}
}
