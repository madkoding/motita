package policy

import "strings"

// lineSegment is one piece of a command line: the words of one command, or the target of
// one redirection.
type lineSegment struct {
	words []string
	// redirect is true when the segment came from a redirection, so its words are a
	// DESTINATION and not a program.
	redirect bool
	// op is the operator that produced a redirect segment, or the separator that produced
	// a command one.
	op string
	// unreadable says why sh may run text in this segment that the segment does not show as a
	// command (a substitution inside double quotes), or "" when nothing is hidden.
	unreadable string
}

// lineSegments splits a line into the pieces a shell would run or open separately: the
// COMMANDS of the line, and the REDIRECTIONS it performs.
//
// It is a character scanner rather than a regexp or a chain of splits, and that is
// deliberate, because each of the three shapes that break a naive split has to survive it:
//
//   - quotes must be preserved, so `grep -n "two words" f | wc -l` is two commands and the
//     quoted argument is still one word when the command is re-read;
//   - a redirection's target must stay attached to its operator, so `2> errors.log` is a
//     file and not a program called `errors.log`;
//   - the words AROUND a redirection belong to the same command, so `echo hi > f more` is
//     one command with two arguments and one redirection, not two commands.
//
// It never fails. Every piece of a line can be described as a segment; whether a segment is
// acceptable is the caller's decision, and leaving that to the caller is what lets the same
// line be refused in strict mode and asked about in the default one.
func lineSegments(line string) []lineSegment {
	var s segmentScanner
	for _, r := range line {
		s.feed(r)
	}
	if s.op != "" {
		s.takeTarget()
	}
	s.flushCommand("")
	return s.out
}

// segmentScanner is the state of lineSegments while it reads a line.
type segmentScanner struct {
	out       []lineSegment
	words     []string
	redirects []lineSegment
	current   strings.Builder
	quote     rune
	escaped   bool
	// op is the redirection being read, non-empty from its `>` until its target has
	// been taken. An `&` reference (`2>&1`) closes it on the spot, because its
	// destination is part of the operator and there is no file to come.
	op string
	// fdRef is true right after the `&` of `2>&1`: the descriptor that follows belongs to
	// the redirection. Left in the stream, `npm ci 2>&1` reached the command as `npm ci 1`.
	fdRef bool
	// sh follows where sh starts a word, and comment is true from a `#` that starts one to the
	// end of its line: sh discards that text, quotes and operators included.
	sh      shellWords
	comment bool
	// unreadable is the reason the segment being read hides a command, for lineSegment.
	unreadable string
}

// feed reads one character of the line.
func (s *segmentScanner) feed(r rune) {
	if s.comment {
		if r != '\n' {
			return
		}
		s.comment = false
	}
	if s.fdRef {
		s.fdRef = false
		if (r >= '0' && r <= '9') || r == '-' {
			s.sh.see('a')
			return
		}
	}
	switch {
	case s.escaped:
		s.escaped = false
		// A backslash before a newline joins two lines into one word: sh drops both.
		if r != '\n' {
			s.current.WriteRune(r)
			s.sh.see('a')
		}
	case r == '\\' && s.quote != '\'':
		s.escaped = true
	case s.quote != 0:
		s.quoted(r)
		s.sh.see('a')
	case s.sh.comment(r):
		s.comment = true
	default:
		if r == '#' && s.sh.prev == '`' {
			// After the backtick that opens a substitution, sh starts a comment that only the
			// closing backtick ends. Read as a word, the `#` and a quote after it hide lines.
			s.hides("a `#` right after a backtick may start a comment that hides what follows")
		}
		s.sh.see(r)
		s.unquoted(r)
	}
}

// hides records why the segment being read may run what it does not show.
func (s *segmentScanner) hides(reason string) {
	if s.unreadable == "" {
		s.unreadable = reason
	}
}

// unquoted reads a character outside quotes and escapes.
func (s *segmentScanner) unquoted(r rune) {
	switch {
	case r == '\'' || r == '"':
		s.current.WriteRune(r)
		s.quote = r
	case r == '>':
		s.redirectOutput(r)
	case r == '<':
		s.flushWord()
		s.op += string(r)
	case r == '&' && s.op != "":
		s.descriptorReference()
	case r == ' ' || r == '\t':
		s.blank()
	case isBreakRune(r):
		if s.op != "" {
			s.takeTarget()
		}
		s.flushCommand(string(r))
	default:
		s.current.WriteRune(r)
	}
}

// quoted reads a character inside quotes.
//
// The quote characters are KEPT in the word. The segment is re-read as a command afterwards, and
// `grep -n "two words" f` only produces one argument if the quoting survives the round trip:
// stripping it here would hand the tokeniser five words and change what the program is given.
//
// Double quotes keep a substitution's OUTPUT one word, but sh still runs it: `echo "$(curl ...)"`
// runs curl. The segment records that it hides a command.
func (s *segmentScanner) quoted(r rune) {
	if s.quote == '"' && (r == '`' || (r == '(' && strings.HasSuffix(s.current.String(), "$"))) {
		s.hides("a command substitution inside double quotes runs a command")
	}
	s.current.WriteRune(r)
	if r == s.quote {
		s.quote = 0
	}
}

// redirectOutput reads a `>`.
func (s *segmentScanner) redirectOutput(r rune) {
	if isDigits(s.current.String()) {
		// A file descriptor on the left of the operator (`2>`, `1>>`): it belongs
		// to the redirection, not to the command's arguments.
		s.op += s.current.String()
		s.current.Reset()
	} else {
		s.flushWord()
	}
	s.op += string(r)
}

// descriptorReference reads the `&` of `2>&1`, `>&2`, `>&-`: the destination is part of the
// operator, so the redirection is complete and takes no file.
func (s *segmentScanner) descriptorReference() {
	s.flushWord()
	s.redirects = append(s.redirects, lineSegment{redirect: true, op: s.op + "&"})
	s.op = ""
	s.fdRef = true
}

// blank reads a space or a tab.
func (s *segmentScanner) blank() {
	if s.op != "" {
		// A word while a redirection is open is its target. A blank before any
		// word is just spacing.
		if s.current.Len() > 0 {
			s.takeTarget()
		}
		return
	}
	s.flushWord()
}

// flushWord ends the word being read.
func (s *segmentScanner) flushWord() {
	if s.current.Len() > 0 {
		s.words = append(s.words, s.current.String())
		s.current.Reset()
	}
}

// takeTarget closes a redirection with the word being read, which may be empty when the
// operator had no destination (`ls >` alone).
func (s *segmentScanner) takeTarget() {
	s.redirects = append(s.redirects, lineSegment{redirect: true, op: s.op, words: []string{s.current.String()}})
	s.current.Reset()
	s.op = ""
}

// flushCommand emits the command read so far, followed by the redirections that were
// interleaved with it. A command with no words and no redirections emits nothing.
func (s *segmentScanner) flushCommand(sep string) {
	s.flushWord()
	first := len(s.out)
	if len(s.words) > 0 {
		s.out = append(s.out, lineSegment{words: s.words, op: sep})
		s.words = nil
	}
	if len(s.redirects) > 0 {
		s.out = append(s.out, s.redirects...)
		s.redirects = nil
	}
	if s.unreadable != "" && len(s.out) > first {
		s.out[first].unreadable, s.unreadable = s.unreadable, ""
	}
}
