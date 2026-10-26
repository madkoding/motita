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
	c := &chainScan{runes: []rune(line)}
	for c.i = 0; c.i < len(c.runes); c.i++ {
		if err := c.next(); err != nil {
			return nil, err
		}
	}
	if err := c.flush(""); err != nil {
		if c.join == "" {
			return nil, fmt.Errorf("there is no command to run")
		}
		return nil, fmt.Errorf("the line ends with %q and no command after it", c.join)
	}
	if len(c.steps) > maxSteps {
		return nil, fmt.Errorf("the line chains %d commands; read-only mode runs at most %d at a time",
			len(c.steps), maxSteps)
	}
	return c.steps, nil
}

// chainScan is the state of SplitChain: the steps read, the one being read, and the quote or
// escape the scanner is inside.
type chainScan struct {
	runes   []rune
	i       int
	steps   []Step
	cur     strings.Builder
	join    string
	quote   rune
	escaped bool
}

// flush closes the step being read; next is how the following one is joined.
func (c *chainScan) flush(next string) error {
	text := strings.TrimSpace(c.cur.String())
	if text == "" {
		return fmt.Errorf("there is no command before %q", next)
	}
	c.steps = append(c.steps, Step{Join: c.join, Command: text})
	c.cur.Reset()
	c.join = next
	return nil
}

// next reads the rune at c.i, moving c.i past anything longer than one rune.
func (c *chainScan) next() error {
	r := c.runes[c.i]
	switch {
	case c.escaped:
		c.cur.WriteRune(r)
		c.escaped = false
	case r == '\\' && c.quote != '\'':
		c.cur.WriteRune(r)
		c.escaped = true
	case c.quote != 0:
		c.cur.WriteRune(r)
		if r == c.quote {
			c.quote = 0
		}
	case r == '\'' || r == '"':
		c.cur.WriteRune(r)
		c.quote = r
	case c.operator() != "":
		return c.closeAtOperator()
	case redirectErrToOut(c.runes, c.i):
		c.i += len("2>&1") - 1
	default:
		c.cur.WriteRune(r)
	}
	return nil
}

// operator is the joining operator that starts at c.i, or "" when none does. A lone `&` is not
// one: it stays in the step, where the tokeniser refuses it.
func (c *chainScan) operator() string {
	r := c.runes[c.i]
	double := c.i+1 < len(c.runes) && c.runes[c.i+1] == r
	switch {
	case r == ';':
		return ";"
	case r == '|' && double:
		return "||"
	case r == '|':
		return "|"
	case r == '&' && double:
		return "&&"
	default:
		return ""
	}
}

// closeAtOperator closes the step at the operator that starts at c.i and moves past it.
func (c *chainScan) closeAtOperator() error {
	op := c.operator()
	c.i += len(op) - 1
	return c.flush(op)
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
