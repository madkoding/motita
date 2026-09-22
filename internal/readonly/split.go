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
	var (
		fields  []string
		current strings.Builder
		quote   rune // 0, ' or "
		escaped bool
		started bool
	)
	bad := rune(0)

	for _, r := range line {
		switch {
		case escaped:
			current.WriteRune(r)
			escaped = false
			started = true
		case r == '\\' && quote != '\'':
			escaped = true
			started = true
		case quote != 0:
			if r == quote {
				quote = 0
			} else {
				current.WriteRune(r)
			}
		case r == '\'' || r == '"':
			quote = r
			started = true
		case r == ' ' || r == '\t':
			if started {
				fields = append(fields, current.String())
				current.Reset()
				started = false
			}
		case strings.ContainsRune("|&;<>()`$\\", r) || r == '\n' || r == '\r':
			// A shell metacharacter. It is refused by name so the model learns what to
			// avoid instead of guessing.
			return "", nil, r, fmt.Errorf(
				"the character %q needs a shell to be interpreted, and a command that "+
					"cannot be read as one program with arguments cannot be checked",
				string(r))
		default:
			current.WriteRune(r)
			started = true
		}
	}
	if escaped {
		return "", nil, bad, fmt.Errorf("the command ends with a backslash")
	}
	if quote != 0 {
		return "", nil, bad, fmt.Errorf("the command has an unclosed quote")
	}
	if started {
		fields = append(fields, current.String())
	}
	if len(fields) == 0 {
		return "", nil, bad, fmt.Errorf("there is no command to run")
	}
	return fields[0], fields[1:], bad, nil
}
