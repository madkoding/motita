package config

import (
	"fmt"
	"strconv"
	"strings"
)

// ---------------------------------------------------------------------------
// Minimal hand-written YAML parser.
//
// The requirement is "no external dependencies beyond libc/libcurl", so in Go
// that means standard library only: gopkg.in/yaml cannot be used. This parser
// covers the subset the agent configuration files need:
//
//   - maps nested by indentation (spaces, never tabs)
//   - sequences with "- " and inline lists [a, b, c]
//   - inline maps {a: 1, b: 2}
//   - plain scalars, single- or double-quoted
//   - booleans (true/false/yes/no/on/off), integers, floats and null (~)
//   - block scalars | and > (with the - and + modifiers) for prompts
//   - comments with #
//
// What it does NOT support is rejected with an explicit message (anchors,
// aliases, tags, multiple documents, complex keys) instead of silently
// guessing.
// ---------------------------------------------------------------------------

type yamlLine struct {
	indent int
	text   string // without the indentation
	num    int    // line number for the error messages
}

type yamlParser struct {
	lines []yamlLine
	pos   int
}

// ParseYAML turns a YAML document into maps, sequences and scalars.
func ParseYAML(data []byte) (map[string]any, error) {
	p := &yamlParser{lines: make([]yamlLine, 0, 64)}

	for i, raw := range strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n") {
		if strings.HasPrefix(raw, "\t") {
			return nil, fmt.Errorf("line %d: the indentation cannot use tabs, use spaces", i+1)
		}
		indent := 0
		for indent < len(raw) && raw[indent] == ' ' {
			indent++
		}
		p.lines = append(p.lines, yamlLine{indent: indent, text: raw[indent:], num: i + 1})
	}

	p.skipBlank()

	if p.done() {
		return map[string]any{}, nil
	}

	value, err := p.parseBlock(p.current().indent)
	if err != nil {
		return nil, err
	}
	p.skipBlank()
	if !p.done() {
		return nil, p.errorf("unexpected content: %q", p.current().text)
	}

	m, ok := value.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("the root of the configuration must be a map (key: value), not a sequence")
	}
	return m, nil
}

// --- position helpers -------------------------------------------------------

func (p *yamlParser) done() bool        { return p.pos >= len(p.lines) }
func (p *yamlParser) current() yamlLine { return p.lines[p.pos] }
func (p *yamlParser) advance()          { p.pos++ }

func (p *yamlParser) errorf(format string, args ...any) error {
	if p.done() {
		return fmt.Errorf("end of document: "+format, args...)
	}
	return fmt.Errorf("line %d: "+format, append([]any{p.current().num}, args...)...)
}

// skipBlank leaves the cursor on the next line with real content: it skips both
// blank lines and whole-line comments. Without this, a file with a comment
// header (the normal thing in documentation) failed with
// "expected 'key: value'".
//
// Careful: it is not used inside block scalars, where a line starting with #
// is legitimate content.
func (p *yamlParser) skipBlank() {
	for !p.done() {
		text := p.lines[p.pos].text
		if strings.TrimSpace(text) == "" {
			p.pos++
			continue
		}
		if strings.TrimSpace(stripComment(text)) == "" {
			p.pos++
			continue
		}
		return
	}
}

// --- structure --------------------------------------------------------------

// parseBlock interprets the next structure at the given indentation.
func (p *yamlParser) parseBlock(indent int) (any, error) {
	if p.done() || p.current().indent < indent {
		return nil, nil
	}
	if isItem(p.current().text) {
		return p.parseSequence(indent)
	}
	return p.parseMap(indent)
}

func (p *yamlParser) parseMap(indent int) (map[string]any, error) {
	return p.parseMapWithFirst(indent, "", "")
}

// parseMapWithFirst allows starting the map with an already read key/value pair
// (the text that follows "- " in a sequence).
func (p *yamlParser) parseMapWithFirst(indent int, firstKey, firstRest string) (map[string]any, error) {
	m := map[string]any{}

	process := func(key, rest string, line int) error {
		switch {
		case rest == "":
			// It may be a nested block, a sequence at the same level or null.
			p.skipBlank()
			if p.done() {
				m[key] = nil
				return nil
			}
			next := p.current()
			if next.indent > indent {
				value, err := p.parseBlock(next.indent)
				if err != nil {
					return err
				}
				m[key] = value
				return nil
			}
			if next.indent == indent && isItem(next.text) {
				value, err := p.parseSequence(indent)
				if err != nil {
					return err
				}
				m[key] = value
				return nil
			}
			m[key] = nil
			return nil

		case isBlockIndicator(rest):
			m[key] = p.readBlockScalar(indent, rest)
			return nil

		default:
			value, err := parseScalar(rest, line)
			if err != nil {
				return err
			}
			m[key] = value
			return nil
		}
	}

	if firstKey != "" || firstRest != "" {
		if err := process(firstKey, firstRest, 0); err != nil {
			return nil, err
		}
	}

	for {
		p.skipBlank()
		if p.done() || p.current().indent != indent {
			break
		}
		line := p.current()
		if isItem(line.text) {
			break
		}
		key, rest, err := splitKey(line.text, line.num)
		if err != nil {
			return nil, err
		}
		p.advance()
		if err := process(key, rest, line.num); err != nil {
			return nil, err
		}
	}
	return m, nil
}

func (p *yamlParser) parseSequence(indent int) ([]any, error) {
	items := []any{}

	for {
		p.skipBlank()
		if p.done() || p.current().indent != indent || !isItem(p.current().text) {
			break
		}
		line := p.current()
		rest := strings.TrimSpace(line.text[1:])
		rest = stripComment(rest)
		p.advance()

		if rest == "" {
			// A bare "-": the item is a nested block.
			p.skipBlank()
			if !p.done() && p.current().indent > indent {
				value, err := p.parseBlock(p.current().indent)
				if err != nil {
					return nil, err
				}
				items = append(items, value)
			} else {
				items = append(items, nil)
			}
			continue
		}

		// "- key: value" opens a map that continues on the following lines.
		if key, value, err := tryKey(rest); err == nil {
			mapIndent := indent + 2
			// If the following lines are more indented, we respect their level.
			if !p.done() && p.current().indent > indent {
				mapIndent = p.current().indent
			}
			m, err := p.parseMapWithFirst(mapIndent, key, value)
			if err != nil {
				return nil, err
			}
			items = append(items, m)
			continue
		}

		value, err := parseScalar(rest, line.num)
		if err != nil {
			return nil, err
		}
		items = append(items, value)
	}
	return items, nil
}

// readBlockScalar consumes a block scalar (| or >) and its lines.
//
// It cannot fail: an unterminated block scalar is simply the block running to
// the end of the document, which is what the loop over the lines does.
func (p *yamlParser) readBlockScalar(parentIndent int, indicator string) string {
	literal := indicator[0] == '|'
	keep := strings.Contains(indicator, "+")
	singleLine := strings.Contains(indicator, "-")

	var raw []yamlLine
	for !p.done() {
		line := p.current()
		if strings.TrimSpace(line.text) == "" {
			raw = append(raw, line)
			p.advance()
			continue
		}
		if line.indent <= parentIndent {
			break
		}
		raw = append(raw, line)
		p.advance()
	}

	// Effective indentation = the smallest of the lines with content.
	minIndent := -1
	for _, l := range raw {
		if strings.TrimSpace(l.text) == "" {
			continue
		}
		if minIndent < 0 || l.indent < minIndent {
			minIndent = l.indent
		}
	}

	var parts []string
	for _, l := range raw {
		if strings.TrimSpace(l.text) == "" {
			parts = append(parts, "")
			continue
		}
		rawLine := strings.Repeat(" ", l.indent) + l.text
		if minIndent > 0 && minIndent <= len(rawLine) {
			rawLine = rawLine[minIndent:]
		}
		parts = append(parts, rawLine)
	}

	var text string
	if literal {
		text = strings.Join(parts, "\n")
	} else {
		// Folded: consecutive lines with content are joined with spaces.
		var sb strings.Builder
		for i, part := range parts {
			if i > 0 {
				if part == "" || parts[i-1] == "" {
					sb.WriteString("\n")
				} else {
					sb.WriteString(" ")
				}
			}
			sb.WriteString(part)
		}
		text = sb.String()
	}

	// Chomping: by default a single trailing line; "-" removes it; "+" keeps it.
	text = strings.TrimRight(text, "\n")
	switch {
	case singleLine:
		// no trailing newline
	case keep:
		text += "\n\n"
	default:
		text += "\n"
	}
	return text
}

// --- scalars ----------------------------------------------------------------

func isItem(text string) bool {
	return text == "-" || strings.HasPrefix(text, "- ") || strings.HasPrefix(text, "-\t")
}

func isBlockIndicator(s string) bool {
	if s == "" {
		return false
	}
	if s[0] != '|' && s[0] != '>' {
		return false
	}
	for _, r := range s[1:] {
		if r != '-' && r != '+' && r != '1' && r != '2' && r != '3' && r != '4' && r != '5' && r != '6' && r != '7' && r != '8' && r != '9' {
			return false
		}
	}
	return true
}

// splitKey separates "key: value" while respecting the quotes.
func splitKey(text string, num int) (string, string, error) {
	key, rest, err := tryKey(text)
	if err != nil {
		return "", "", fmt.Errorf("line %d: %v", num, err)
	}
	return key, rest, nil
}

func tryKey(text string) (string, string, error) {
	if strings.ContainsAny(text, "&*!") && !strings.Contains(text, ":") {
		return "", "", fmt.Errorf("unsupported YAML structure in %q (anchors, aliases and tags are not implemented)", text)
	}

	quote := byte(0)
	for i := 0; i < len(text); i++ {
		c := text[i]
		switch {
		case quote != 0:
			if c == quote {
				quote = 0
			}
		case c == '\'' || c == '"':
			quote = c
		case c == ':':
			// In YAML the key ends at ": " or ":<end>".
			if i+1 == len(text) || text[i+1] == ' ' {
				key := strings.TrimSpace(text[:i])
				rest := strings.TrimSpace(stripComment(strings.TrimSpace(text[i+1:])))
				if key == "" {
					return "", "", fmt.Errorf("empty key in %q", text)
				}
				if key[0] == '\'' || key[0] == '"' {
					unquoted, err := unquote(key)
					if err != nil {
						return "", "", err
					}
					key = unquoted
				}
				return key, rest, nil
			}
		}
	}
	return "", "", fmt.Errorf("expected 'key: value' and found %q", text)
}

func parseScalar(text string, num int) (any, error) {
	text = strings.TrimSpace(stripComment(text))
	if text == "" {
		return nil, nil
	}

	if text[0] == '\'' || text[0] == '"' {
		return unquote(text)
	}
	if text[0] == '[' {
		return parseInlineSequence(text, num)
	}
	if text[0] == '{' {
		return parseInlineMap(text, num)
	}
	if strings.HasPrefix(text, "&") || strings.HasPrefix(text, "*") || strings.HasPrefix(text, "!") {
		return nil, fmt.Errorf("line %d: anchors, aliases and tags are not supported (%q)", num, text)
	}

	// Only true/false are interpreted as boolean.
	//
	// YAML 1.1 also accepts yes/no/on/off, but they do NOT apply here: they are
	// legitimate text values of this configuration (sandbox.cgroups uses
	// "on"/"off" as strings). Turning them into booleans made `cgroups: off`
	// arrive as false and the validation rejected it with a bewildering error.
	// Whoever writes a boolean in a text field will get an explicit type error,
	// which is better than a silently wrong conversion.
	switch strings.ToLower(text) {
	case "null", "~":
		return nil, nil
	case "true":
		return true, nil
	case "false":
		return false, nil
	}
	if integer, err := strconv.ParseInt(text, 10, 64); err == nil {
		return integer, nil
	}
	if f, err := strconv.ParseFloat(text, 64); err == nil && strings.ContainsAny(text, ".eE") {
		return f, nil
	}
	return text, nil
}

func unquote(text string) (string, error) {
	if len(text) < 2 {
		return "", fmt.Errorf("unterminated quote in %q", text)
	}
	quote := text[0]
	if text[len(text)-1] != quote {
		return "", fmt.Errorf("unterminated quote in %q", text)
	}
	body := text[1 : len(text)-1]
	if quote == '\'' {
		// In single quotes the only special sequence is '' -> '.
		return strings.ReplaceAll(body, "''", "'"), nil
	}
	var sb strings.Builder
	for i := 0; i < len(body); i++ {
		if body[i] == '\\' && i+1 < len(body) {
			i++
			switch body[i] {
			case 'n':
				sb.WriteByte('\n')
			case 't':
				sb.WriteByte('\t')
			case 'r':
				sb.WriteByte('\r')
			case '"':
				sb.WriteByte('"')
			case '\\':
				sb.WriteByte('\\')
			default:
				sb.WriteByte('\\')
				sb.WriteByte(body[i])
			}
			continue
		}
		sb.WriteByte(body[i])
	}
	return sb.String(), nil
}

func parseInlineSequence(text string, num int) ([]any, error) {
	if !strings.HasSuffix(text, "]") {
		return nil, fmt.Errorf("line %d: missing ']' in the inline list %q", num, text)
	}
	body := strings.TrimSpace(text[1 : len(text)-1])
	if body == "" {
		return []any{}, nil
	}
	parts, err := splitTop(body, ',')
	if err != nil {
		return nil, fmt.Errorf("line %d: %v", num, err)
	}
	out := make([]any, 0, len(parts))
	for _, part := range parts {
		value, err := parseScalar(part, num)
		if err != nil {
			return nil, err
		}
		out = append(out, value)
	}
	return out, nil
}

func parseInlineMap(text string, num int) (map[string]any, error) {
	if !strings.HasSuffix(text, "}") {
		return nil, fmt.Errorf("line %d: missing '}' in the inline map %q", num, text)
	}
	body := strings.TrimSpace(text[1 : len(text)-1])
	m := map[string]any{}
	if body == "" {
		return m, nil
	}
	parts, err := splitTop(body, ',')
	if err != nil {
		return nil, fmt.Errorf("line %d: %v", num, err)
	}
	for _, part := range parts {
		key, rest, err := tryKey(strings.TrimSpace(part))
		if err != nil {
			return nil, fmt.Errorf("line %d: %v", num, err)
		}
		value, err := parseScalar(rest, num)
		if err != nil {
			return nil, err
		}
		m[key] = value
	}
	return m, nil
}

// splitTop separates by a separator that is outside quotes and brackets.
func splitTop(text string, sep byte) ([]string, error) {
	var parts []string
	depth := 0
	quote := byte(0)
	start := 0
	for i := 0; i < len(text); i++ {
		c := text[i]
		switch {
		case quote != 0:
			if c == quote {
				quote = 0
			}
		case c == '\'' || c == '"':
			quote = c
		case c == '[' || c == '{':
			depth++
		case c == ']' || c == '}':
			depth--
		case c == sep && depth == 0:
			parts = append(parts, strings.TrimSpace(text[start:i]))
			start = i + 1
		}
	}
	if quote != 0 {
		return nil, fmt.Errorf("unterminated quote in %q", text)
	}
	parts = append(parts, strings.TrimSpace(text[start:]))
	return parts, nil
}

// stripComment removes a "# comment" that is outside quotes.
func stripComment(text string) string {
	quote := byte(0)
	for i := 0; i < len(text); i++ {
		c := text[i]
		switch {
		case quote != 0:
			if c == quote {
				quote = 0
			}
		case c == '\'' || c == '"':
			quote = c
		case c == '#' && (i == 0 || text[i-1] == ' '):
			return strings.TrimRight(text[:i], " ")
		}
	}
	return text
}
