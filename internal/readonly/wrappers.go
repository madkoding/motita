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
		name := strings.ToLower(filepath.Base(strings.TrimSpace(command)))
		skip, ok := wrapperOptions[name]
		if !ok {
			return command, args, ""
		}
		i, reason := skip(args)
		if reason != "" {
			return command, args, fmt.Sprintf("%s %s", name, reason)
		}
		if i >= len(args) {
			// Nothing to run: the wrapper is the whole command.
			return command, args, ""
		}
		command, args = args[i], args[i+1:]
	}
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

// envProgram: `env [OPTION]... [-] [NAME=VALUE]... [COMMAND [ARG]...]`.
func envProgram(args []string) (int, string) {
	options := true
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case options && a == "--":
			options = false
		case options && (a == "-" || a == "-i" || a == "--ignore-environment" ||
			a == "-0" || a == "--null" || a == "-v" || a == "--debug"):
		case options && (a == "-u" || a == "--unset"):
			i++
		case options && (strings.HasPrefix(a, "--unset=") || (strings.HasPrefix(a, "-u") && !strings.HasPrefix(a, "--"))):
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
		case strings.HasPrefix(a, "--adjustment=") || strings.HasPrefix(a, "-n") ||
			(len(a) > 1 && a[0] == '-' && isNumber(a[1:])):
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
		case strings.HasPrefix(a, "--signal=") || strings.HasPrefix(a, "--kill-after=") ||
			strings.HasPrefix(a, "-s") && !strings.HasPrefix(a, "--") || strings.HasPrefix(a, "-k"),
			a == "--preserve-status" || a == "--foreground" || a == "-v" || a == "--verbose":
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
		case a == "-i" || a == "-o" || a == "-e" || a == "--input" || a == "--output" || a == "--error":
			i++
		case strings.HasPrefix(a, "--input=") || strings.HasPrefix(a, "--output=") ||
			strings.HasPrefix(a, "--error=") ||
			len(a) > 2 && (strings.HasPrefix(a, "-i") || strings.HasPrefix(a, "-o") || strings.HasPrefix(a, "-e")):
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
		case a == "-c" || a == "--ctty" || a == "-f" || a == "--fork" || a == "-w" || a == "--wait":
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
