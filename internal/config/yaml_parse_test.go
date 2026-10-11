package config

import (
	"strings"
	"testing"
)

// --- ParseUser --------------------------------------------------------------

func TestParseUserAllCases(t *testing.T) {
	cases := []struct {
		input string
		uid   int
		gid   int
		fails bool
	}{
		{"1000:1001", 1000, 1001, false},
		{"1000", 1000, 1000, false},
		{" 5 : 6 ", 5, 6, false},
		{"", 0, 0, true},
		{"thousand", 0, 0, true},
		{"1:two", 0, 0, true},
		{"1:2:3", 0, 0, true},
	}
	for _, tc := range cases {
		uid, gid, err := ParseUser(tc.input)
		if tc.fails {
			if err == nil {
				t.Errorf("ParseUser(%q) had to fail", tc.input)
			}
			continue
		}
		if err != nil || uid != tc.uid || gid != tc.gid {
			t.Errorf("ParseUser(%q) = %d,%d,%v", tc.input, uid, gid, err)
		}
	}
}

// --- YAML: error paths and formats ------------------------------------------

func TestParseYAMLCommentAtTheEnd(t *testing.T) {
	// A comment at the end of the document, with no key: it must be ignored.
	m, err := ParseYAML([]byte("a: 1\n# just a comment\n"))
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if m["a"] != int64(1) {
		t.Errorf("m = %#v", m)
	}
}

func TestParseYAMLEmpty(t *testing.T) {
	for _, input := range []string{"", "\n\n", "# comments only\n"} {
		m, err := ParseYAML([]byte(input))
		if err != nil {
			t.Errorf("ParseYAML(%q) gave an error: %v", input, err)
		}
		if len(m) != 0 {
			t.Errorf("ParseYAML(%q) = %#v", input, m)
		}
	}
}

func TestParseYAMLKeyWithoutValueAtTheEnd(t *testing.T) {
	m, err := ParseYAML([]byte("task_source:\nanchor:\n  kind: command\n"))
	if err != nil {
		t.Fatal(err)
	}
	if v, ok := m["task_source"]; !ok || v != nil {
		t.Errorf("task_source without a value must be nil: %#v", m["task_source"])
	}
}

func TestParseYAMLListNestedUnderKey(t *testing.T) {
	m, err := ParseYAML([]byte("anchor:\n  checks:\n    - name: a\n    - name: b\n"))
	if err != nil {
		t.Fatal(err)
	}
	anchor := m["anchor"].(map[string]any)
	list, ok := anchor["checks"].([]any)
	if !ok || len(list) != 2 {
		t.Fatalf("checks = %#v", anchor["checks"])
	}
}

func TestParseYAMLBlankItem(t *testing.T) {
	m, err := ParseYAML([]byte("list:\n  -\n  - two\n"))
	if err != nil {
		t.Fatal(err)
	}
	list := m["list"].([]any)
	if len(list) != 2 || list[0] != nil || list[1] != "two" {
		t.Errorf("list = %#v", list)
	}
}

func TestParseYAMLQuotedKey(t *testing.T) {
	m, err := ParseYAML([]byte(`"key with spaces": value`))
	if err != nil {
		t.Fatal(err)
	}
	if m["key with spaces"] != "value" {
		t.Errorf("m = %#v", m)
	}
}

func TestParseYAMLEmptyKeyError(t *testing.T) {
	if _, err := ParseYAML([]byte(": value\n")); err == nil {
		t.Error("an empty key must give an error")
	}
}

func TestParseYAMLUnterminatedQuoteError(t *testing.T) {
	if _, err := ParseYAML([]byte(`a: "unterminated`)); err == nil {
		t.Error("an unterminated quote must give an error")
	}
}

func TestParseYAMLUnterminatedListError(t *testing.T) {
	if _, err := ParseYAML([]byte("a: [1, 2\n")); err == nil {
		t.Error("an unterminated list must give an error")
	}
}

func TestParseYAMLUnterminatedMapError(t *testing.T) {
	if _, err := ParseYAML([]byte("a: {x: 1\n")); err == nil {
		t.Error("an unterminated inline map must give an error")
	}
}

func TestParseYAMLUnsupportedStructureError(t *testing.T) {
	for _, input := range []string{"a: &anchor value\n", "a: *alias\n", "a: !tag value\n"} {
		if _, err := ParseYAML([]byte(input)); err == nil {
			t.Errorf("ParseYAML(%q) should reject what is not supported", input)
		}
	}
}

func TestParseYAMLSingleQuoteWithEscape(t *testing.T) {
	m, err := ParseYAML([]byte("a: 'it is '' not a problem'\n"))
	if err != nil {
		t.Fatal(err)
	}
	if m["a"] != "it is ' not a problem" {
		t.Errorf("a = %q", m["a"])
	}
}

func TestParseYAMLEscapesInDoubleQuotes(t *testing.T) {
	m, err := ParseYAML([]byte(`a: "line1\nline2\ttab\\slash\"quote"` + "\n"))
	if err != nil {
		t.Fatal(err)
	}
	want := "line1\nline2\ttab\\slash\"quote"
	if m["a"] != want {
		t.Errorf("a = %q, expected %q", m["a"], want)
	}
}

func TestParseYAMLUnknownEscapes(t *testing.T) {
	// An unforeseen escape is kept as is.
	m, err := ParseYAML([]byte(`a: "x\qy"`))
	if err != nil {
		t.Fatal(err)
	}
	if m["a"] != "x\\qy" {
		t.Errorf("a = %q", m["a"])
	}
}

func TestParseYAMLFoldedScalarWithBlanks(t *testing.T) {
	m, err := ParseYAML([]byte("a: >\n  first part\n  second\n\n  another block\n"))
	if err != nil {
		t.Fatal(err)
	}
	text, _ := m["a"].(string)
	if !strings.Contains(text, "first part") || !strings.Contains(text, "another block") {
		t.Errorf("a = %q", text)
	}
}

func TestParseYAMLBlockScalarKeepingTrailingNewline(t *testing.T) {
	m, err := ParseYAML([]byte("a: |+\n  line\n"))
	if err != nil {
		t.Fatal(err)
	}
	text, _ := m["a"].(string)
	if !strings.HasSuffix(text, "\n") {
		t.Errorf("with |+ the trailing newline is kept: %q", text)
	}
}

func TestIsBlockIndicator(t *testing.T) {
	cases := map[string]bool{
		"|": true, ">": true, "|-": true, ">+": true, "|2": true,
		"": false, "value": false, "||": false,
	}
	for input, want := range cases {
		if got := isBlockIndicator(input); got != want {
			t.Errorf("isBlockIndicator(%q) = %v", input, got)
		}
	}
}

func TestIsItemList(t *testing.T) {
	for input, want := range map[string]bool{
		"-": true, "- value": true, "-value": false, "value": false,
	} {
		if got := isItem(input); got != want {
			t.Errorf("isItem(%q) = %v", input, got)
		}
	}
}

func TestSplitTopWithQuotes(t *testing.T) {
	parts, err := splitTop(`a, "b, c", d[1,2]`, ',')
	if err != nil {
		t.Fatal(err)
	}
	if len(parts) != 3 {
		t.Fatalf("parts = %#v", parts)
	}
	if parts[1] != `"b, c"` || parts[2] != "d[1,2]" {
		t.Errorf("parts = %#v", parts)
	}
}

func TestSplitTopUnterminatedQuote(t *testing.T) {
	if _, err := splitTop(`a, "b`, ','); err == nil {
		t.Error("an unterminated quote must give an error")
	}
}

func TestParseScalarBlankValues(t *testing.T) {
	for input, want := range map[string]any{
		"": nil, "  ": nil, "null": nil, "~": nil,
		"true": true, "false": false,
		"3": int64(3), "3.5": float64(3.5),
		"text": "text",
	} {
		got, err := parseScalar(input, 1)
		if err != nil {
			t.Errorf("parseScalar(%q) gave an error: %v", input, err)
			continue
		}
		if got != want {
			t.Errorf("parseScalar(%q) = %#v, expected %#v", input, got, want)
		}
	}
}

// TestParseScalarOnOffAreText: the important fix in the parser. YAML 1.1 turns
// on/off/yes/no into booleans, but in this configuration they are legitimate
// text values (sandbox.cgroups accepts "on"/"off"). Turning them made
// `cgroups: off` arrive as false and the validation rejected it.
func TestParseScalarOnOffAreText(t *testing.T) {
	for _, input := range []string{"on", "off", "yes", "no", "On", "OFF"} {
		got, err := parseScalar(input, 1)
		if err != nil {
			t.Fatal(err)
		}
		if _, isBool := got.(bool); isBool {
			t.Errorf("parseScalar(%q) = %v: on/off/yes/no must be text, not boolean", input, got)
		}
		if got != input {
			t.Errorf("parseScalar(%q) = %#v", input, got)
		}
	}
}

// TestDecodeMapWhereStructIsExpected: a simple value where a nested block is
// expected.
func TestDecodeSimpleValueWhereBlockIsExpected(t *testing.T) {
	m, err := ParseYAML([]byte("llm: text\n"))
	if err != nil {
		t.Fatal(err)
	}
	var cfg Config
	err = Decode(m, &cfg)
	if err == nil || !strings.Contains(err.Error(), "expected a configuration block") {
		t.Errorf("error = %v", err)
	}
	// The message must carry the field path so it can be fixed.
	if !strings.Contains(err.Error(), "llm") {
		t.Errorf("the error must name the block: %v", err)
	}
}

// TestDecodeListWhereBlockIsExpected.
func TestDecodeListWhereBlockIsExpected(t *testing.T) {
	m, err := ParseYAML([]byte("llm:\n  - one\n"))
	if err != nil {
		t.Fatal(err)
	}
	var cfg Config
	if err := Decode(m, &cfg); err == nil {
		t.Error("a list where a block is expected must fail")
	}
}

// TestDecodeNestedMapOnDeepPath checks that the errors name the full path
// (block.subfield), not just the loose name.
func TestDecodeNestedMapOnDeepPath(t *testing.T) {
	m, err := ParseYAML([]byte("anchor:\n  checks:\n    - name: a\n      expect_exit: not-a-number\n"))
	if err != nil {
		t.Fatal(err)
	}
	var cfg Config
	err = Decode(m, &cfg)
	if err == nil {
		t.Fatal("a type error was expected")
	}
	if !strings.Contains(err.Error(), "anchor.checks") {
		t.Errorf("the error must name the nested path: %v", err)
	}
}

// TestDecodeIntegerOverflow: an oversized number is handled without a panic
// (the parser delivers it as text and the validation decides).
func TestDecodeIntegerOverflow(t *testing.T) {
	m, err := ParseYAML([]byte("sandbox:\n  memory_mb: 99999999999999999999\n"))
	if err != nil {
		t.Fatalf("the parser should not fail here: %v", err)
	}
	var cfg Config
	// Either it decodes as text (and the validation will reject it later), or
	// it fails with a type error. Both are acceptable; what is not acceptable is
	// a panic.
	_ = Decode(m, &cfg)
}

// TestParseYAMLLineError: the error message includes the line number.
func TestParseYAMLLineError(t *testing.T) {
	_, err := ParseYAML([]byte("a: 1\nb: 2\nc: 3\nmalformed\n"))
	if err == nil {
		t.Fatal("an error was expected")
	}
	if !strings.Contains(err.Error(), "line 4") {
		t.Errorf("the error must name the line: %v", err)
	}
}

// TestParseYAMLErrorAtTheEnd: a key without a value on the last line.
func TestParseYAMLErrorAtTheEnd(t *testing.T) {
	// "a:" at the end is valid (null). The line error path is covered with a
	// malformed sequence at the end.
	_, err := ParseYAML([]byte("list:\n  - value\nmalformed\n"))
	if err == nil {
		t.Fatal("an error was expected")
	}
}

// TestParseYAMLSeveralNestingLevels.
func TestParseYAMLSeveralNestingLevels(t *testing.T) {
	m, err := ParseYAML([]byte(`
n1:
  n2:
    n3:
      n4: deep
`))
	if err != nil {
		t.Fatal(err)
	}
	n1 := m["n1"].(map[string]any)
	n2 := n1["n2"].(map[string]any)
	n3 := n2["n3"].(map[string]any)
	if n3["n4"] != "deep" {
		t.Errorf("nesting = %#v", n3)
	}
}

// TestParseYAMLMapInsideSequence with several levels.
func TestParseYAMLMapInsideSequence(t *testing.T) {
	m, err := ParseYAML([]byte(`
items:
  - name: first
    options:
      a: 1
    list:
      - x
      - y
  - name: second
`))
	if err != nil {
		t.Fatal(err)
	}
	items := m["items"].([]any)
	if len(items) != 2 {
		t.Fatalf("items = %#v", items)
	}
	first := items[0].(map[string]any)
	if first["name"] != "first" {
		t.Errorf("first = %#v", first)
	}
	if options, ok := first["options"].(map[string]any); !ok || options["a"] != int64(1) {
		t.Errorf("options = %#v", first["options"])
	}
	if list, ok := first["list"].([]any); !ok || len(list) != 2 {
		t.Errorf("list = %#v", first["list"])
	}
}

// TestParseYAMLMapAtTheSameLevelAsTheKey covers the sequence whose indentation
// matches the key's.
func TestParseYAMLMapAtTheSameLevel(t *testing.T) {
	m, err := ParseYAML([]byte("list:\n- one\n- two\n"))
	if err != nil {
		t.Fatal(err)
	}
	if list, ok := m["list"].([]any); !ok || len(list) != 2 {
		t.Errorf("list = %#v", m["list"])
	}
}

func TestStripComment(t *testing.T) {
	cases := map[string]string{
		"value # comment": "value",
		"# comment only":  "",
		`"with # inside"`: `"with # inside"`,
		"no hash":         "no hash",
		"joined#no space": "joined#no space",
	}
	for input, want := range cases {
		if got := stripComment(input); got != want {
			t.Errorf("stripComment(%q) = %q, expected %q", input, got, want)
		}
	}
}

// --- YAML parser error paths ------------------------------------------------

// TestParseYAMLRejectsSequenceAtTheRoot: a configuration must be a map; a list at
// the top level is refused with a message that says so.
func TestParseYAMLRejectsSequenceAtTheRoot(t *testing.T) {
	if _, err := ParseYAML([]byte("- one\n- two\n")); err == nil {
		t.Error("a sequence at the root must be refused")
	}
}

// TestParseYAMLRejectsTrailingContent: two documents in one file must be refused
// instead of silently ignoring the second.
func TestParseYAMLRejectsTrailingContent(t *testing.T) {
	if _, err := ParseYAML([]byte("a: 1\nb: 2\nc: 3\n")); err != nil {
		t.Fatalf("a plain map must be accepted: %v", err)
	}
	// A line that does not belong to the root block is trailing content.
	if _, err := ParseYAML([]byte("agent:\n  max_retries: 1\nplain_key\n")); err == nil {
		t.Error("trailing content must be refused")
	}
}

// TestParseYAMLRejectsTabsInIndentation: a tab is not valid YAML indentation and
// must be reported with the line number instead of producing a confusing tree.
func TestParseYAMLRejectsTabsInIndentation(t *testing.T) {
	if _, err := ParseYAML([]byte("agent:\n\tmax_retries: 1\n")); err == nil {
		t.Error("a tab used for indentation must be refused")
	}
}

// TestParseYAMLHandlesEveryScalarShape: the parser must accept the values a user
// actually writes (quoted text, numbers, booleans, inline lists and maps, empty
// values, comments).
func TestParseYAMLHandlesEveryScalarShape(t *testing.T) {
	doc := []byte(`# a comment
text: plain
quoted: "with: colon"
single: 'single quoted'
number: 42
negative: -7
float: 1.5
boolean: true
empty:
inline_list: [a, b, "c d"]
inline_map: {a: 1, b: two}
list:
  - one
  - two
nested:
  key: value
`)
	m, err := ParseYAML(doc)
	if err != nil {
		t.Fatalf("the document must parse: %v", err)
	}
	if m["text"] != "plain" || m["quoted"] != "with: colon" || m["single"] != "single quoted" {
		t.Errorf("text values = %v / %v / %v", m["text"], m["quoted"], m["single"])
	}
	if m["number"] != int64(42) && m["number"] != 42 {
		t.Errorf("number = %#v", m["number"])
	}
	if m["boolean"] != true {
		t.Errorf("boolean = %#v", m["boolean"])
	}
	if _, ok := m["empty"]; !ok {
		t.Errorf("an empty value must still be present as a key: %v", m)
	}
	if list, ok := m["list"].([]any); !ok || len(list) != 2 {
		t.Errorf("list = %#v", m["list"])
	}
	if _, ok := m["nested"].(map[string]any); !ok {
		t.Errorf("nested = %#v", m["nested"])
	}
}

// TestParseYAMLRejectsMalformedLines: the shapes a user can get wrong must be
// refused with the line number, not silently misread.
func TestParseYAMLRejectsMalformedLines(t *testing.T) {
	cases := map[string]string{
		"line without a colon":  "just text with no separator\n",
		"unclosed inline list":  "list: [a, b\n",
		"unclosed inline map":   "map: {a: 1\n",
		"unclosed quote":        "text: \"unclosed\n",
		"bad indentation jump":  "a: 1\n   b: 2\n",
		"item without a parent": "  - orphan\n",
	}
	for name, doc := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseYAML([]byte(doc)); err == nil {
				t.Errorf("%s must be refused", name)
			}
		})
	}
}

// TestParseYAMLHandlesCommentsAndBlankLines: comments and blank lines are
// skipped everywhere, including inside blocks.
func TestParseYAMLHandlesCommentsAndBlankLines(t *testing.T) {
	doc := []byte(`# header

agent:
  # a comment inside the block

  max_retries: 3

  # another
  max_tasks: 5
`)
	m, err := ParseYAML(doc)
	if err != nil {
		t.Fatal(err)
	}
	agent, _ := m["agent"].(map[string]any)
	if agent == nil {
		t.Fatalf("agent = %#v", m["agent"])
	}
	if agent["max_retries"] != int64(3) && agent["max_retries"] != 3 {
		t.Errorf("max_retries = %#v", agent["max_retries"])
	}
	if agent["max_tasks"] != int64(5) && agent["max_tasks"] != 5 {
		t.Errorf("max_tasks = %#v", agent["max_tasks"])
	}
}
