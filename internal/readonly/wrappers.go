package readonly

import (
	"fmt"
	"path/filepath"
	"strings"
)

// Unwrap returns the program a command wrapper runs, with that program's own arguments.
//
// A wrapper — `env`, `command`, `nice`, `nohup`, `timeout`, `stdbuf`, `setsid` — changes how a
// program runs and not WHAT it does, so the honest classification of `env curl …` is the
// classification of `curl …`. Judging the wrapper instead was a hole: `env` and `command` are
// readers on their own, and `env git push --force` ran in silence because the name in front
// of it only reads.
//
// It unwraps repeatedly (`env nice curl` is `curl`), skipping each wrapper's own options and,
// for `env`, its `NAME=value` assignments. A wrapper with no program after it is returned as
// it was: bare `env` prints the environment and is judged as itself. So is `command -v x`,
// which looks a name up and runs nothing.
//
// reason is non-empty when a wrapper carries an option this function does not know, or one
// that changes what runs in a way the program's arguments cannot show (`env -S` splits a
// string into a new command line, `env -C` moves the working directory). Such a line cannot
// be classified, and the caller must not treat the wrapper as the program it is.
//
// `sudo` and `doas` are NOT wrappers here: running as another user is the effect itself, and
// they stay writers by name.
func Unwrap(command string, args []string) (string, []string, string) {
	for {
		inner, innerArgs, reason, more := unwrapOnce(command, args)
		if !more {
			return command, args, reason
		}
		command, args = inner, innerArgs
	}
}

// unwrapOnce takes ONE wrapper off a command. more reports that it did, and that the result
// may be a wrapper again.
func unwrapOnce(command string, args []string) (string, []string, string, bool) {
	name := strings.ToLower(filepath.Base(strings.TrimSpace(command)))
	skip, ok := wrapperOptions[name]
	if !ok {
		return command, args, "", false
	}
	i, reason := skip(args)
	if reason != "" {
		return command, args, fmt.Sprintf("%s %s", name, reason), false
	}
	if i >= len(args) {
		// Nothing to run: the wrapper is the whole command.
		return command, args, "", false
	}
	return args[i], args[i+1:], "", true
}

// wrapperOptions maps each wrapper to the function that finds where its program starts. It
// returns the index of the program in args (len(args) when there is none), or a reason when an
// option cannot be accounted for.
var wrapperOptions = map[string]func([]string) (int, string){
	"env":     envProgram,
	"command": commandProgram,
	"nice":    niceProgram,
	"nohup":   nohupProgram,
	"timeout": timeoutProgram,
	"stdbuf":  stdbufProgram,
	"setsid":  setsidProgram,
}

// unknownOption is the reason given for an option a wrapper rule does not know.
func unknownOption(a string) string {
	return fmt.Sprintf("option %q is not one this policy can account for, so the program it runs "+
		"cannot be classified", a)
}

// flagSet is a set of option names.
type flagSet map[string]bool

var (
	envFlags = flagSet{"-": true, "-i": true, "--ignore-environment": true, "-0": true, "--null": true,
		"-v": true, "--debug": true}
	timeoutFlags = flagSet{"--preserve-status": true, "--foreground": true, "-v": true, "--verbose": true}
	stdbufModes  = flagSet{"-i": true, "-o": true, "-e": true, "--input": true, "--output": true, "--error": true}
	setsidFlags  = flagSet{"-c": true, "--ctty": true, "-f": true, "--fork": true, "-w": true, "--wait": true}
)

// hasAnyPrefix reports whether s starts with one of prefixes.
func hasAnyPrefix(s string, prefixes ...string) bool {
	for _, p := range prefixes {
		if strings.HasPrefix(s, p) {
			return true
		}
	}
	return false
}

// envGluedUnset reports whether a is `--unset=NAME` or `-uNAME`.
func envGluedUnset(a string) bool {
	return strings.HasPrefix(a, "--unset=") || (strings.HasPrefix(a, "-u") && !strings.HasPrefix(a, "--"))
}

// envProgram: `env [OPTION]... [-] [NAME=VALUE]... [COMMAND [ARG]...]`.
func envProgram(args []string) (int, string) {
	options := true
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case options && a == "--":
			options = false
		case options && envFlags[a]:
			// A flag that changes only the environment the program sees.
		case options && (a == "-u" || a == "--unset"):
			i++
		case options && envGluedUnset(a):
			// The name to unset is glued to the option.
		case options && strings.HasPrefix(a, "-"):
			// -S/--split-string builds a NEW command line out of one string, -C/--chdir moves
			// the directory the program runs in, -P replaces the PATH it is found through.
			return 0, unknownOption(a)
		case strings.ContainsRune(a, '='):
			options = false
		default:
			return i, ""
		}
	}
	return len(args), ""
}

// commandProgram: `command [-p] [-v|-V] NAME [ARG]...`. With -v or -V it looks NAME up and runs
// nothing, so there is no program to unwrap.
func commandProgram(args []string) (int, string) {
	for i, a := range args {
		switch {
		case a == "-v" || a == "-V":
			return len(args), ""
		case a == "-p":
			// -p only uses a default PATH to find the program.
		case a == "--":
			return i + 1, ""
		case strings.HasPrefix(a, "-"):
			return 0, unknownOption(a)
		default:
			return i, ""
		}
	}
	return len(args), ""
}

// niceProgram: `nice [-n N | --adjustment=N | -N] [COMMAND [ARG]...]`.
func niceProgram(args []string) (int, string) {
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "-n" || a == "--adjustment":
			i++
		case hasAnyPrefix(a, "--adjustment=", "-n") || (len(a) > 1 && a[0] == '-' && isNumber(a[1:])):
			// The adjustment is glued to the option.
		case a == "--":
			return i + 1, ""
		case strings.HasPrefix(a, "-"):
			return 0, unknownOption(a)
		default:
			return i, ""
		}
	}
	return len(args), ""
}

// nohupProgram: `nohup COMMAND [ARG]...`.
func nohupProgram(args []string) (int, string) {
	if len(args) > 0 && args[0] == "--" {
		return 1, ""
	}
	if len(args) > 0 && strings.HasPrefix(args[0], "-") {
		return 0, unknownOption(args[0])
	}
	return 0, ""
}

// timeoutProgram: `timeout [OPTION]... DURATION COMMAND [ARG]...`. The first operand is the
// duration, not the program.
func timeoutProgram(args []string) (int, string) {
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "-s" || a == "--signal" || a == "-k" || a == "--kill-after":
			i++
		case hasAnyPrefix(a, "--signal=", "--kill-after=", "-s", "-k"), timeoutFlags[a]:
			// A value glued to its option, or a flag that takes none.
		case a == "--":
			return i + 2, ""
		case strings.HasPrefix(a, "-"):
			return 0, unknownOption(a)
		default:
			return i + 1, ""
		}
	}
	return len(args), ""
}

// stdbufProgram: `stdbuf OPTION... COMMAND [ARG]...`, every option taking a mode.
func stdbufProgram(args []string) (int, string) {
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case stdbufModes[a]:
			i++
		case hasAnyPrefix(a, "--input=", "--output=", "--error=") || len(a) > 2 && hasAnyPrefix(a, "-i", "-o", "-e"):
			// The mode is glued to the option.
		case a == "--":
			return i + 1, ""
		case strings.HasPrefix(a, "-"):
			return 0, unknownOption(a)
		default:
			return i, ""
		}
	}
	return len(args), ""
}

// setsidProgram: `setsid [-c] [-f] [-w] COMMAND [ARG]...`.
func setsidProgram(args []string) (int, string) {
	for i, a := range args {
		switch {
		case setsidFlags[a]:
			// A flag that changes only the session the program runs in.
		case a == "--":
			return i + 1, ""
		case strings.HasPrefix(a, "-"):
			return 0, unknownOption(a)
		default:
			return i, ""
		}
	}
	return len(args), ""
}

// isNumber reports whether s is a run of ASCII digits. The caller guarantees it is non-empty.
func isNumber(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
