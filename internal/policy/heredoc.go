package policy

import (
	"fmt"
	"strings"
)

// ---------------------------------------------------------------------------
// Heredocs
// ---------------------------------------------------------------------------

// heredoc is one `<< DELIM` body found in a line.
type heredoc struct {
	delim string
	// quoted is true for <<'EOF', <<"EOF" and <<\EOF: the shell then expands nothing in the body.
	quoted bool
	body   string
}

// splitHeredocs takes the bodies of heredocs out of a line. It returns the line WITHOUT them (the
// `<< DELIM` operator stays, so the segment reader still sees an input redirection) and the
// bodies, each with its delimiter.
//
// It is a scanner and not a parser, like the rest of this file: quotes are tracked so that a `<<`
// inside a string is not an operator, and a body ends at the first line that is exactly its
// delimiter (leading tabs ignored for `<<-`). A body with no closing line runs to the end of
// the line, which is what the shell does too.
func splitHeredocs(line string) (string, []heredoc) {
	if !strings.Contains(line, "<<") {
		return line, nil
	}
	s := heredocScanner{runes: []rune(line)}
	for i := 0; i < len(s.runes); i++ {
		i = s.step(i)
	}
	return s.out.String(), s.docs
}

// pendingHeredoc is a `<< DELIM` operator whose body starts at the next line.
type pendingHeredoc struct {
	delim         string
	quoted, strip bool
}

// heredocScanner is the state of splitHeredocs while it reads a line.
type heredocScanner struct {
	runes  []rune
	out    strings.Builder
	docs   []heredoc
	queue  []pendingHeredoc
	quote  rune
	escape bool
	sh     shellWords
}

// step reads the rune at i and returns the index of the last rune it consumed.
func (s *heredocScanner) step(i int) int {
	r := s.runes[i]
	switch {
	case s.escape:
		s.escape = false
		if r != '\n' {
			s.sh.see('a')
		}
	case r == '\\' && s.quote != '\'':
		s.escape = true
	case s.quote != 0:
		if r == s.quote {
			s.quote = 0
		}
		s.sh.see('a')
	case s.sh.comment(r):
		// A `<<` or a quote in a comment is text: it opens nothing.
		return s.copyComment(i)
	case r == '\'' || r == '"':
		s.quote = r
		s.sh.see('a')
	case r == '<' && s.heredocOperator(i):
		s.sh.see('a')
		return s.readOperator(i)
	case r == '\n' && len(s.queue) > 0:
		s.sh.see(r)
		return s.readBodies(i)
	default:
		s.sh.see(r)
	}
	s.out.WriteRune(r)
	return i
}

// copyComment copies the comment that starts at i, up to its newline, and returns the index of
// its last rune.
func (s *heredocScanner) copyComment(i int) int {
	end := i
	for end+1 < len(s.runes) && s.runes[end+1] != '\n' {
		end++
	}
	s.out.WriteString(string(s.runes[i : end+1]))
	return end
}

// shellWords follows, rune by rune, what sh needs to tell a comment from a `#` inside a word.
//
// sh starts a comment only at a `#` that begins a word: after a blank, a newline or an operator,
// and never inside `${...}`. Reading a `#` anywhere else as a comment would hide text sh runs,
// and missing one would read its quote as the start of a string that swallows the lines after
// it: `ls #'` + newline + `curl ...` + newline + `#'` is `ls` and `curl` to sh.
type shellWords struct {
	// prev is the last rune read outside quotes and escapes, 'a' for one that was quoted or
	// escaped (it belongs to a word), and 0 at the start of the line.
	prev rune
	// param counts the `${` still open.
	param int
}

// see records a rune. Quoted and escaped runes are recorded as 'a'.
func (w *shellWords) see(r rune) {
	switch {
	case r == '{' && w.prev == '$':
		w.param++
	case r == '}' && w.param > 0:
		w.param--
	default:
		// Any other rune only moves prev.
	}
	w.prev = r
}

// comment reports whether r, read outside quotes and escapes, starts a comment.
func (w *shellWords) comment(r rune) bool {
	if r != '#' || w.param > 0 {
		return false
	}
	switch w.prev {
	case 0, ' ', '\t', '\n', ';', '&', '|', '(', ')', '<', '>':
		return true
	default:
		return false
	}
}

// heredocOperator reports whether the `<` at i opens a `<<` (a here-STRING is `<<<`, which has
// no body).
func (s *heredocScanner) heredocOperator(i int) bool {
	runes := s.runes
	return i+1 < len(runes) && runes[i+1] == '<' && (i+2 >= len(runes) || runes[i+2] != '<')
}

// readOperator reads a `<<` operator and its delimiter at i, queues the body it announces, and
// returns the index of the last rune of the operator.
func (s *heredocScanner) readOperator(i int) int {
	runes := s.runes
	j := i + 2
	strip := false
	if j < len(runes) && runes[j] == '-' {
		strip = true
		j++
	}
	for j < len(runes) && (runes[j] == ' ' || runes[j] == '\t') {
		j++
	}
	delim, quoted, end := readDelimiter(runes, j)
	if delim != "" {
		s.queue = append(s.queue, pendingHeredoc{delim, quoted, strip})
	}
	s.out.WriteString(string(runes[i:end]))
	return end - 1
}

// readDelimiter reads a heredoc delimiter starting at j: the word without its quotes, whether it
// was quoted, and the index just past it.
func readDelimiter(runes []rune, j int) (string, bool, int) {
	var d strings.Builder
	quoted := false
	for j < len(runes) && !strings.ContainsRune(" \t\n;|&()<>", runes[j]) {
		switch runes[j] {
		case '\'', '"', '\\':
			quoted = true
		default:
			d.WriteRune(runes[j])
		}
		j++
	}
	return d.String(), quoted, j
}

// readBodies takes the bodies of the queued heredocs out of what follows the newline at i, and
// returns the index of the last rune they consumed.
func (s *heredocScanner) readBodies(i int) int {
	s.out.WriteRune('\n')
	rest := string(s.runes[i+1:])
	consumed := 0
	for _, h := range s.queue {
		var body string
		body, consumed = heredocBody(rest, consumed, h)
		s.docs = append(s.docs, heredoc{delim: h.delim, quoted: h.quoted, body: body})
	}
	s.queue = nil
	return i + len([]rune(rest[:consumed]))
}

// heredocBody reads one body from rest, starting at consumed, up to its delimiter line. It
// returns the body and where the next one starts.
func heredocBody(rest string, consumed int, h pendingHeredoc) (string, int) {
	var body strings.Builder
	for consumed < len(rest) {
		var ln string
		ln, consumed = nextLine(rest, consumed)
		if heredocEnds(ln, h) {
			break
		}
		body.WriteString(ln + "\n")
	}
	return body.String(), consumed
}

// nextLine returns the line of text that starts at from, without its newline, and where the
// following one starts.
func nextLine(text string, from int) (string, int) {
	nl := strings.IndexByte(text[from:], '\n')
	if nl < 0 {
		return text[from:], len(text)
	}
	return text[from : from+nl], from + nl + 1
}

// heredocEnds reports whether a line is the delimiter that closes a heredoc (leading tabs
// ignored for `<<-`).
func heredocEnds(ln string, h pendingHeredoc) bool {
	cmp := strings.TrimRight(ln, "\r")
	if h.strip {
		cmp = strings.TrimLeft(cmp, "\t")
	}
	return cmp == h.delim
}

// heredocRunners are the shells and builtins, besides the interpreters, that run a heredoc
// body as a program.
var heredocRunners = map[string]bool{
	"sh": true, "bash": true, "zsh": true, "dash": true, "ksh": true, "fish": true,
	"eval": true, "source": true, ".": true,
}

// heredocVerdict judges what a heredoc body can do. The body is text; it becomes a danger only
// when something EXECUTES it, and there are two ways it can:
//
//   - the receiving program is an interpreter or a shell (`sh << EOF`, `python3 - << EOF`,
//     `node - << EOF`): the body is then a program given as text, exactly like `python3 -c`, and
//     is offered to the user whole;
//   - the delimiter is unquoted and the body has `$(...)` or a backtick: the shell runs that
//     substitution while it expands the body, before any program sees it.
//
// Writing text into a file (`cat > f << 'EOF'`, `tee f << EOF`) is neither, and is judged by the
// redirection and the program like any other write.
func (m Mode) heredocVerdict(main string, docs []heredoc) (Decision, bool) {
	if len(docs) == 0 {
		return Decision{}, false
	}
	for _, seg := range lineSegments(main) {
		if seg.redirect {
			continue
		}
		words, syntax := stripShellKeyword(seg.words)
		if syntax || len(words) == 0 {
			continue
		}
		name := baseName(unquote(words[0]))
		if interpreters[name] || heredocRunners[name] {
			return m.unclassified(fmt.Sprintf("%q is given a program as a heredoc, and text that is "+
				"run cannot be checked", name), "heredoc-program"), true
		}
	}
	for _, d := range docs {
		if !d.quoted && (strings.Contains(d.body, "$(") || strings.Contains(d.body, "`")) {
			return m.unclassified("a heredoc with an unquoted delimiter runs the substitutions in its body",
				"heredoc-substitution"), true
		}
	}
	return Decision{}, false
}
