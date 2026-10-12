package readonly

import (
	"fmt"
	"strings"
)

// SplitCommand separates a line into the program and its arguments.
//
// It is deliberately not a shell parser: it understands quoting and backslash escapes, and
// it REFUSES anything a shell would interpret (a pipe, a redirection, a substitution, a
// separator). Refusing those is the whole point — the read-only mode does not need them, and
// allowing them would mean implementing a shell to decide what they do, which is where this
// kind of guarantee usually dies.
//
// In the mode where a shell IS used, the same refusal is what makes the classification
// honest: a line that survives this tokeniser contains no way to chain, redirect or
// substitute, so the program the policy sees is the program that will run.
//
// It lives here, beside the classification, so that the agent's executor and the policy
// cannot drift into two different ideas of what a line means.
func SplitCommand(line string) (string, []string, error) {
	var err error
	cmd, args, _, err := SplitCommandWithErr(line)
	return cmd, args, err
}

// SplitCommandWithErr is SplitCommand, also reporting the offending character when a shell
// metacharacter is what stopped it.
//
// The character is returned rather than only named in the message because a caller that goes
// on to analyse the line in SEGMENTS needs to know WHICH character it was: to split on it,
// and to stop splitting on the ones that are merely syntax (`>` inside a comparison).
func SplitCommandWithErr(line string) (string, []string, rune, error) {
	s := &splitter{}
	bad := rune(0)

	for _, r := range line {
		switch {
		case s.escaped:
			s.add(r)
			s.escaped = false
		case r == '\\' && s.quote != '\'':
			s.escaped = true
			s.started = true
		case s.quote != 0:
			s.inQuote(r)
		case r == '\'' || r == '"':
			s.quote = r
			s.started = true
		case r == ' ' || r == '\t':
			s.endField()
		case strings.ContainsRune("|&;<>()`$\\", r) || r == '\n' || r == '\r':
			// A shell metacharacter. It is refused by name so the model learns what to
			// avoid instead of guessing.
			return "", nil, r, fmt.Errorf(
				"the character %q needs a shell to be interpreted, and a command that "+
					"cannot be read as one program with arguments cannot be checked",
				string(r))
		default:
			s.add(r)
		}
	}
	if s.escaped {
		return "", nil, bad, fmt.Errorf("the command ends with a backslash")
	}
	if s.quote != 0 {
		return "", nil, bad, fmt.Errorf("the command has an unclosed quote")
	}
	s.endField()
	if len(s.fields) == 0 {
		return "", nil, bad, fmt.Errorf("there is no command to run")
	}
	return s.fields[0], s.fields[1:], bad, nil
}

// splitter holds the state of SplitCommandWithErr: the fields read so far and the one being
// read.
type splitter struct {
	fields  []string
	current strings.Builder
	quote   rune // 0, ' or "
	escaped bool
	started bool
}

// add appends r to the field being read.
func (s *splitter) add(r rune) {
	s.current.WriteRune(r)
	s.started = true
}

// inQuote reads r inside a quote: the closing quote ends it, anything else is literal.
func (s *splitter) inQuote(r rune) {
	if r == s.quote {
		s.quote = 0
	} else {
		s.current.WriteRune(r)
	}
}

// endField closes the field being read, if one was started.
func (s *splitter) endField() {
	if s.started {
		s.fields = append(s.fields, s.current.String())
		s.current.Reset()
		s.started = false
	}
}
