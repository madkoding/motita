package memory

import (
	"strings"
	"unicode"
)

// Contextual chunking, parent-child. An entry is cut into PARENTS, the sections under its headings
// (the larger block an answer is shown from), and each parent into small CHILDREN of a paragraph or
// two (the passages that are searched). A child is indexed together with its context line, the
// entry's title and the heading trail ("Hiring › Offers › Salary bands"), so a paragraph that only
// says "the band tops out at 90k" still matches a search for the hiring salary bands.
const (
	parentMax = 2400
	childMax  = 480
	trailSep  = " › "
)

// parent is a section of an entry: the block an answer is read from.
type parent struct {
	entry int
	trail []string
	line  int
	text  string
}

// child is a searched passage and the parent it belongs to.
type child struct {
	entry  int
	parent int
	line   int
	text   string
	ctx    string // the title and the heading trail
}

// section is a heading's text up to the next heading.
type section struct {
	trail []string
	line  int
	lines []string
}

// cut turns the text of an entry into parents and children. line0 is how many lines of the file
// came before text (its front matter).
func cut(entry int, title, text string, line0 int) ([]parent, []child) {
	var parents []parent
	var children []child
	for _, sec := range sections(text, line0) {
		for _, p := range splitSection(sec) {
			if strings.TrimSpace(p.text) == "" {
				continue
			}
			p.entry = entry
			ctx := contextLine(title, p.trail)
			at := len(parents)
			parents = append(parents, p)
			for _, c := range childrenOf(p) {
				c.entry, c.parent, c.ctx = entry, at, ctx
				children = append(children, c)
			}
		}
	}
	return parents, children
}

// contextLine is the title followed by the heading trail, without repeating the title when the
// entry opens with a heading of the same name.
func contextLine(title string, trail []string) string {
	if h := headingOf(title, trail); h != "" {
		return title + trailSep + h
	}
	return title
}

// headingLevel is the level of a Markdown heading line (0 when it is none) and its text.
func headingLevel(line string) (int, string) {
	n := 0
	for n < len(line) && line[n] == '#' {
		n++
	}
	if n == 0 || n > 6 || len(line) <= n || (line[n] != ' ' && line[n] != '\t') {
		return 0, ""
	}
	return n, strings.TrimSpace(strings.TrimRight(strings.TrimSpace(line[n:]), "#"))
}

// sections splits text at its headings, outside code fences, keeping the trail of headings above.
func sections(text string, line0 int) []section {
	var out []section
	var stack []string // the heading at each level, "" where a level is skipped
	cur := section{line: line0 + 1}
	fenced := false
	flush := func() {
		if len(cur.lines) > 0 {
			out = append(out, cur)
		}
	}
	for i, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "```") || strings.HasPrefix(strings.TrimSpace(line), "~~~") {
			fenced = !fenced
		}
		if lvl, h := headingLevel(line); lvl > 0 && !fenced && h != "" {
			flush()
			for len(stack) < lvl {
				stack = append(stack, "")
			}
			stack = append(stack[:lvl-1], h)
			cur = section{trail: trailOf(stack), line: line0 + i + 1}
		}
		cur.lines = append(cur.lines, line)
	}
	flush()
	return out
}

// trailOf is the headings of a stack, the skipped levels left out.
func trailOf(stack []string) []string {
	var out []string
	for _, h := range stack {
		if h != "" {
			out = append(out, h)
		}
	}
	return out
}

// splitSection keeps a section whole when it fits in a parent, and cuts it at blank lines when it
// does not, so no parent is longer than parentMax (a single paragraph longer than that is cut at a
// space).
func splitSection(sec section) []parent {
	text := strings.TrimRight(strings.Join(sec.lines, "\n"), "\n ")
	if len([]rune(text)) <= parentMax {
		return []parent{{trail: sec.trail, line: sec.line, text: text}}
	}
	var out []parent
	var cur []string
	size, start := 0, sec.line
	emit := func() {
		if t := strings.TrimSpace(strings.Join(cur, "\n")); t != "" {
			out = append(out, parent{trail: sec.trail, line: start, text: t})
		}
		cur, size = nil, 0
	}
	for _, para := range paragraphs(sec.lines, sec.line) {
		for _, piece := range splitRunes(para.text, parentMax) {
			n := len([]rune(piece))
			if size+n > parentMax && size > 0 {
				emit()
			}
			if len(cur) == 0 {
				start = para.line
			}
			cur = append(cur, piece, "")
			size += n
		}
	}
	emit()
	return out
}

// para is a paragraph of text and the line it starts at.
type para struct {
	line int
	text string
}

// paragraphs splits lines at blank lines.
func paragraphs(lines []string, line0 int) []para {
	var out []para
	var cur []string
	start := line0
	flush := func() {
		if t := strings.TrimSpace(strings.Join(cur, "\n")); t != "" {
			out = append(out, para{line: start, text: t})
		}
		cur = nil
	}
	for i, line := range lines {
		if strings.TrimSpace(line) == "" {
			flush()
			continue
		}
		if len(cur) == 0 {
			start = line0 + i
		}
		cur = append(cur, line)
	}
	flush()
	return out
}

// childrenOf cuts a parent into the passages that are searched: paragraphs joined while they fit in
// childMax, a longer one cut at a space. The heading line opens the parent but is not a passage of
// its own: the trail already carries it.
func childrenOf(p parent) []child {
	lines := strings.Split(p.text, "\n")
	line0 := p.line
	if lvl, _ := headingLevel(lines[0]); lvl > 0 {
		lines, line0 = lines[1:], p.line+1
	}
	var out []child
	var cur []string
	size, start := 0, line0
	emit := func() {
		if t := strings.TrimSpace(strings.Join(cur, "\n")); t != "" {
			out = append(out, child{line: start, text: t})
		}
		cur, size = nil, 0
	}
	for _, para := range paragraphs(lines, line0) {
		for _, piece := range splitRunes(para.text, childMax) {
			n := len([]rune(piece))
			if size+n > childMax && size > 0 {
				emit()
			}
			if len(cur) == 0 {
				start = para.line
			}
			cur = append(cur, piece)
			size += n
		}
	}
	emit()
	return out
}

// splitRunes cuts s into pieces of at most max runes, each ending at a space when it can.
func splitRunes(s string, max int) []string {
	r := []rune(s)
	if len(r) <= max {
		return []string{s}
	}
	var out []string
	for len(r) > max {
		at := max
		for i := max; i > max/2; i-- {
			if unicode.IsSpace(r[i]) {
				at = i
				break
			}
		}
		out = append(out, strings.TrimSpace(string(r[:at])))
		r = r[at:]
	}
	if t := strings.TrimSpace(string(r)); t != "" {
		out = append(out, t)
	}
	return out
}
