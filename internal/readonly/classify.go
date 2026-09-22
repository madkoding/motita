package readonly

import (
	"fmt"
	"path/filepath"
	"strings"
)

// Kind is what a command does, as far as a name and its arguments can tell.
//
// It exists because two policies need the same knowledge and must not disagree. The
// read-only policy of plan mode asks "may this run at all?" and refuses everything that
// is not a reader. The wider policy of a mode where writing is the point asks "may this
// run silently, must the user be asked, or is it beyond asking?" — and it needs to know
// whether the command reads or writes to answer that. One table, one decision order, so
// a program added to the reader list does not become a writer in the other package.
type Kind int

const (
	// KindReader only observes. Running it changes nothing, anywhere, ever.
	KindReader Kind = iota
	// KindWriter changes something: a file, the index, the system. It may still be
	// ordinary work — where it writes is what decides that — but it is not a reader.
	KindWriter
	// KindShell is an interpreter that can do anything. A line handed to it cannot be
	// classified by looking at it, because the line is data until the shell interprets it.
	KindShell
	// KindUnknown is a program nobody has classified. It is NOT a reader: the default is
	// refusal, and a caller that prefers to ask instead of refuse can do that explicitly.
	KindUnknown
	// KindMissing is the empty command: there is nothing to run, which is a different
	// answer from "not on the list" and deserves a different message.
	KindMissing
)

// Classify reports what a command does, and why.
//
// command is the program as written (`grep` or `/usr/bin/grep`), args are its arguments.
// The returned reason is written for a person to read and, when the caller is relaying it
// to a model, to act on.
func Classify(command string, args []string) (Kind, string) {
	trimmed := strings.TrimSpace(command)
	if trimmed == "" || trimmed == "." || trimmed == "/" {
		return KindMissing, "there is no command to run"
	}
	name := strings.ToLower(filepath.Base(trimmed))

	// A shell is the biggest loophole there is: `sh -c 'rm -rf /'` would defeat any policy
	// that only looks at the program name, and deciding what a shell line does means
	// parsing shell. Classified as its own kind so each caller can answer it its own way.
	if isShell(name) {
		return KindShell, fmt.Sprintf("%q is a shell: a line can do anything, so it cannot be checked", name)
	}

	// env and command run their operand as a program, so they are whatever it is:
	// `env rm -rf x` is rm, `env sh -c ...` is a shell.
	if name == "env" || name == "command" {
		if kind, reason, ok := classifyWrapped(name, args); ok {
			return kind, reason
		}
	}

	// Writing programs, refused by name even when an argument would make them read.
	// `sed -n` only reads, but `sed -i` rewrites, and one policy is easier to keep honest
	// than a table of exceptions.
	if _, bad := writers[name]; bad {
		return KindWriter, ""
	}

	// Commands whose arguments decide: the writing form is named explicitly.
	if rule, ok := argumentRules[name]; ok {
		if reason, bad := rule(args); bad {
			return KindWriter, reason
		}
	}

	if _, ok := readers[name]; ok {
		return KindReader, fmt.Sprintf("%q only reads", name)
	}

	return KindUnknown, fmt.Sprintf("%q is not on the list of known programs", name)
}

// classifyWrapped classifies the program that env or command would run. ok is false
// when there is no program operand, and the wrapper is then the reader it looks like
// (env prints the environment, command -v looks a name up).
func classifyWrapped(name string, args []string) (Kind, string, bool) {
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--":
			if i+1 < len(args) {
				kind, reason := Classify(args[i+1], args[i+2:])
				return kind, reason, true
			}
			return 0, "", false
		case name == "command" && (a == "-v" || a == "-V"):
			return 0, "", false // only looks the name up
		case name == "command" && a == "-p":
		case name == "env" && (a == "-i" || a == "-0" || a == "-v" || a == "-" ||
			a == "--ignore-environment" || a == "--null" || a == "--debug"):
		case name == "env" && (a == "-u" || a == "-C" || a == "-P" || a == "--unset" || a == "--chdir"):
			i++ // the flag's value
		case name == "env" && (strings.HasPrefix(a, "--unset=") || strings.HasPrefix(a, "--chdir=")):
		case name == "env" && (strings.HasPrefix(a, "-S") || strings.HasPrefix(a, "--split-string")):
			return KindShell, "env -S splits a line into a command, so it cannot be checked", true
		case strings.HasPrefix(a, "-"):
			return KindUnknown, fmt.Sprintf("%s %s is not a known option", name, a), true
		case name == "env" && strings.Contains(a, "="):
			// VAR=value sets the environment of the program that follows.
		default:
			kind, reason := Classify(a, args[i+1:])
			return kind, reason, true
		}
	}
	return 0, "", false
}
