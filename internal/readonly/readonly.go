// Package readonly decides whether a command may run when the agent is in
// read-only mode (the "plan" mode: explore and prepare a plan, change nothing).
//
// It exists because the alternative — telling the model "do not write anything" —
// is not a guarantee: it is a request the model can ignore or misread. The point of
// this project is that the model proposes and something deterministic disposes, so
// here the decision is a list, it is testable, and it is applied by the sandbox
// before anything runs.
//
// The policy is deliberately conservative: a command is allowed only when it is
// known to be a reader. Anything unknown, ambiguous or simply not on the list is
// refused, and the refusal is explained so the user (and the model) can see why.
package readonly

import (
	"fmt"
	"path/filepath"
	"strings"
)

// Decision is the verdict for one command.
type Decision struct {
	// Allowed is true only when the command is known to only read.
	Allowed bool
	// Reason explains the verdict, in a form a person can act on.
	Reason string
}

// Check decides whether a command may run in read-only mode.
//
// command is the program (as written, so `grep` or `/usr/bin/grep`), args are its
// arguments.
//
// It is written on top of Classify so that the read-only answer and the wider policy's
// answer come from ONE decision order. The messages below are the read-only mode's own:
// they name the mode and the file to change, because the reader of this one is an
// operator deciding whether to extend the list.
func Check(command string, args []string) Decision {
	kind, reason := Classify(command, args)
	// The name the messages quote is the program that would run, not the wrapper in front of it.
	inner, _, _ := Unwrap(command, args)
	name := strings.ToLower(filepath.Base(strings.TrimSpace(inner)))

	switch kind {
	case KindReader:
		return Decision{true, reason}
	case KindMissing:
		return Decision{false, reason}
	case KindShell:
		return Decision{false, reason + ". Run the reader directly (for example " +
			"`grep -n x file` instead of `sh -c \"grep x file\"`)"}
	case KindWriter:
		// A writer by nature has no rule of its own to quote; a writer because of its
		// arguments does, and that reason is the specific one.
		if reason != "" {
			return Decision{false, reason}
		}
		return Decision{false, fmt.Sprintf(
			"%q can change the system, and read-only mode refuses it", name)}
	default:
		return Decision{false, fmt.Sprintf(
			"%q is not on the read-only list, so it is refused. If it only reads, add it to "+
				"internal/readonly/readers.go with a test", name)}
	}
}

func isShell(name string) bool {
	switch name {
	case "sh", "bash", "zsh", "dash", "ksh", "csh", "tcsh", "fish", "ash", "busybox":
		return true
	}
	return false
}

// writers, readers and argumentRules live in readers.go, which is the file the refusal
// message names. They were in this one — and the message pointed at a file that did not
// exist — until the tables were moved next to the file the operator is told to edit.
