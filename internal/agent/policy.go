package agent

import (
	"fmt"
	"strings"

	"github.com/madkoding/starlight/internal/config"
	"github.com/madkoding/starlight/internal/execx"
	"github.com/madkoding/starlight/internal/readonly"
)

// buildRequest turns an action written by the model into the request that will be
// executed, applying the read-only policy when it is active.
//
// Two modes, and the difference matters:
//
//   - read-only (plan mode): the command is split with a shell-aware tokeniser and
//     checked against the allowlist. The request runs the program directly, with no
//     shell, so `;`, `>`, `>>`, `&&` or `$(...)` are impossible: there is no
//     interpreter to honour them. A command that is not on the list is refused with
//     the reason, and the reason goes back to the model so it can propose something
//     it is allowed to do.
//   - otherwise: the line goes to the shell unchanged, which is what lets the model
//     use pipes and redirections.
//
// The second return value is the refusal reason, empty when the request is usable.
func (a *Agent) buildRequest(command string) (execx.Request, string) {
	timeout := a.cfg.Sandbox.Timeout

	if !a.readOnly() {
		return execx.Request{
			Command: shellFor(a.cfg),
			Args:    []string{"-c", command},
			Timeout: timeout,
		}, ""
	}

	name, args, err := splitCommand(command)
	if err != nil {
		return execx.Request{}, err.Error()
	}
	decision := readonly.Check(name, args)
	if !decision.Allowed {
		return execx.Request{}, decision.Reason
	}
	return execx.Request{Command: name, Args: args, Timeout: timeout}, ""
}

// readOnly reports whether the agent is in the mode that changes nothing.
func (a *Agent) readOnly() bool {
	return a.cfg.Agent.ReadOnly
}

// shellFor returns the interpreter for the platform this binary runs on. It is a
// small indirection so the agent does not import the platform files of cmd/chat.
func shellFor(cfg config.Config) string {
	if cfg.Agent.Shell != "" {
		return cfg.Agent.Shell
	}
	return defaultShell()
}

// splitCommand separates a line into the program and its arguments.
//
// It is deliberately not a shell parser: it understands quoting and backslash
// escapes, and it REFUSES anything that a shell would interpret (a pipe, a
// redirection, a substitution, a separator). Refusing those is the whole point: the
// read-only mode does not need them, and allowing them would mean implementing a
// shell to decide what they do, which is where this kind of guarantee usually dies.
func splitCommand(line string) (string, []string, error) {
	var (
		fields  []string
		current strings.Builder
		quote   rune // 0, ' or "
		escaped bool
		started bool
	)

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
			// A shell metacharacter. It is refused by name so the model learns what
			// to avoid instead of guessing.
			return "", nil, fmt.Errorf(
				"the character %q needs a shell to be interpreted, and read-only mode runs "+
					"commands directly so that nothing can be redirected or chained. "+
					"Propose a single reader with arguments (for example `grep -n pattern file`)",
				string(r))
		default:
			current.WriteRune(r)
			started = true
		}
	}
	if escaped {
		return "", nil, fmt.Errorf("the command ends with a backslash")
	}
	if quote != 0 {
		return "", nil, fmt.Errorf("the command has an unclosed quote")
	}
	if started {
		fields = append(fields, current.String())
	}
	if len(fields) == 0 {
		return "", nil, fmt.Errorf("there is no command to run")
	}
	return fields[0], fields[1:], nil
}
