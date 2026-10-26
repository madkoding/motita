package readonly

import (
	"fmt"
	"strings"
)

// maxSteps bounds a chained line: a plan looks, it does not script.
const maxSteps = 8

// Step is one program of a chained line, with how it is joined to the one before it.
type Step struct {
	// Join is "" for the first step, otherwise ";", "&&", "||" or "|".
	Join string
	// Command is the step's own text: one program and its arguments, for SplitCommand.
	Command string
}

// SplitChain divides a line at the operators that join programs: `;`, `&&`, `||` and `|`, outside
// quotes. It is NOT a shell: nothing here interprets anything, and every step still has to be one
// program with arguments that SplitCommand accepts (so `$()`, backticks, redirections and `&` stay
// refused). The caller runs the steps itself, one program at a time, which is why the guarantee
// that no shell is ever in the path still holds.
//
// A trailing `2>&1` is dropped from a step: the output of a command already carries its errors.
func SplitChain(line string) ([]Step, error) {
	var steps []Step
	var cur strings.Builder
	join := ""
	var quote rune
	escaped := false
	runes := []rune(line)

	flush := func(next string) error {
		text := strings.TrimSpace(cur.String())
		if text == "" {
			return fmt.Errorf("there is no command before %q", next)
		}
		steps = append(steps, Step{Join: join, Command: text})
		cur.Reset()
		join = next
		return nil
	}

	for i := 0; i < len(runes); i++ {
		r := runes[i]
		switch {
		case escaped:
			cur.WriteRune(r)
			escaped = false
		case r == '\\' && quote != '\'':
			cur.WriteRune(r)
			escaped = true
		case quote != 0:
			cur.WriteRune(r)
			if r == quote {
				quote = 0
			}
		case r == '\'' || r == '"':
			cur.WriteRune(r)
			quote = r
		case r == ';' || r == '|' || (r == '&' && i+1 < len(runes) && runes[i+1] == '&'):
			op := string(r)
			if i+1 < len(runes) && (r == '&' || (r == '|' && runes[i+1] == '|')) {
				op = string(r) + string(runes[i+1])
				i++
			}
			if err := flush(op); err != nil {
				return nil, err
			}
		case redirectErrToOut(runes, i):
			i += len("2>&1") - 1
		default:
			cur.WriteRune(r)
		}
	}
	if err := flush(""); err != nil {
		if join == "" {
			return nil, fmt.Errorf("there is no command to run")
		}
		return nil, fmt.Errorf("the line ends with %q and no command after it", join)
	}
	if len(steps) > maxSteps {
		return nil, fmt.Errorf("the line chains %d commands; read-only mode runs at most %d at a time",
			len(steps), maxSteps)
	}
	return steps, nil
}

// redirectErrToOut reports whether runes[i:] is a standalone `2>&1` token.
func redirectErrToOut(runes []rune, i int) bool {
	const token = "2>&1"
	end := i + len(token)
	if end > len(runes) || string(runes[i:end]) != token {
		return false
	}
	before := i == 0 || runes[i-1] == ' ' || runes[i-1] == '\t'
	after := end == len(runes) || strings.ContainsRune(" \t;|&", runes[end])
	return before && after
}
