package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/madkoding/motita/internal/execx"
	"github.com/madkoding/motita/internal/policy"
	"github.com/madkoding/motita/internal/readonly"
)

// chainUnit is one program of a read-only line, the filters a pipe applies to its output, and how
// it is joined to the unit before.
type chainUnit struct {
	join    string
	request execx.Request
	filters []readonly.Filter
}

// refusal is the answer for a read-only line that cannot run, in the form RunCommand gives.
func refusal(reason string) (string, int, error) {
	return fmt.Sprintf("[refused: %s]\n", reason), 1, fmt.Errorf("command was refused: %s", reason)
}

// runReadOnly runs a line in read-only mode. The line may chain programs with `;`, `&&` and `||`,
// and end a program in a pipe into head, tail, wc or grep; there is still no shell. Every program
// is checked by the read-only policy BEFORE any of them runs, so a line is refused as a whole and
// never half-done, and each one is run directly with its own arguments, in the sandbox.
func (a *Agent) runReadOnly(ctx context.Context, command string) (string, int, error) {
	units, err := a.planChain(command)
	if err != nil {
		return refusal(err.Error())
	}
	var out strings.Builder
	exit := 0
	for _, u := range units {
		if !joinRuns(u.join, exit) {
			continue
		}
		text, _, code, err := a.exec(ctx, u.request)
		for _, f := range u.filters {
			text, code = f(text)
		}
		appendOutput(&out, text)
		exit = code
		if err != nil {
			return out.String(), code, err
		}
	}
	return out.String(), exit, nil
}

// planChain splits a line and checks every program in it, returning what to run.
func (a *Agent) planChain(command string) ([]chainUnit, error) {
	steps, err := readonly.SplitChain(command)
	if err != nil {
		return nil, err
	}
	var units []chainUnit
	for _, s := range steps {
		if s.Join == "|" {
			filter, err := readonly.ParseFilter(s.Command)
			if err != nil {
				return nil, err
			}
			last := &units[len(units)-1]
			last.filters = append(last.filters, filter)
			continue
		}
		p := a.planRequest(s.Command)
		if p.Verdict == policy.Deny {
			if len(steps) > 1 {
				return nil, fmt.Errorf("%s (in %q)", p.Reason, s.Command)
			}
			return nil, errors.New(p.Reason)
		}
		units = append(units, chainUnit{join: s.Join, request: p.Request})
	}
	return units, nil
}

// appendOutput adds a program's output after what came before, on a line of its own.
func appendOutput(out *strings.Builder, text string) {
	if out.Len() > 0 && !strings.HasSuffix(out.String(), "\n") {
		out.WriteString("\n")
	}
	out.WriteString(text)
}

// joinRuns reports whether a unit joined by join runs after one that exited with code.
func joinRuns(join string, code int) bool {
	switch join {
	case "&&":
		return code == 0
	case "||":
		return code != 0
	default:
		return true
	}
}
