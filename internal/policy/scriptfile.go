package policy

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/madkoding/motita/internal/readonly"
)

// Two lines the policy used to ask about although it already allows what they do.

// posixShells are the shells that run a script file named as their first operand.
var posixShells = map[string]bool{"sh": true, "bash": true, "dash": true, "zsh": true, "ksh": true}

// shellScriptFlags are the options that change only how a script is traced or stopped on error
// (`bash -eu script.sh`, `sh -x script.sh`). Every other option is left to the question: `-c` and
// `-s` hand the shell a line instead of a file, and `-o`, `--rcfile` and `--init-file` take a
// value that is not the script.
var shellScriptFlags = regexp.MustCompile(`^-[euxvn]+$`)

// shellScriptDecision answers a shell handed a script file: `bash scripts/check.sh` runs exactly
// what `./scripts/check.sh` runs, which is allowed when the script is inside the workspace (see
// projectLocalDecision). Asked about, it was the same program behind a second question. A script
// outside the workspace, a line handed with `-c`, or a shell reading its standard input is not
// this rule's and is still asked about.
func shellScriptDecision(name string, args []string, dir string) (Decision, bool) {
	if !posixShells[name] {
		return Decision{}, false
	}
	i := 0
	for i < len(args) && shellScriptFlags.MatchString(args[i]) {
		i++
	}
	if i >= len(args) || args[i] == "" || args[i][0] == '-' || !isInsideWorkspace(args[i], dir) {
		return Decision{}, false
	}
	if outside := firstOutside(rmTargets(args[i+1:]), dir); outside != "" {
		return Decision{Ask, fmt.Sprintf("%q takes %q, which is outside the directory this task "+
			worksInFormat, args[i], outside, dir), "project-script-arg-outside", false}, true
	}
	return Decision{Allow, fmt.Sprintf("%q runs the script %q, which is inside the workspace (%s), so "+
		"its work stays where the task is", name, args[i], dir), "project-script", false}, true
}

// gitFetchDecision answers `git fetch`, which plan mode - the mode that changes nothing - already
// runs (see readonly.PlanRead): it updates the remote-tracking references and never the working
// tree, the checked-out branch or the remote. Asked about here, the stricter mode allowed what
// this one stopped for. Whether it can reach the network at all is still the sandbox's to say.
func gitFetchDecision(name string, args []string) (Decision, bool) {
	if name != "git" {
		return Decision{}, false
	}
	allowed, reason, ok := readonly.PlanRead(name, args)
	if !ok || !allowed {
		return Decision{}, false
	}
	return Decision{Allow, reason, "git-fetch", false}, true
}

// xargsReaders are the readers xargs may hand its input to without a question: programs whose
// arguments can only name what they read, so a name that arrives on standard input - and might be
// spelled like an option (`-o/etc/passwd` to sort, `--pre` to rg, `-C` to file) - cannot make
// them write or run something.
var xargsReaders = map[string]bool{
	"grep": true, "egrep": true, "fgrep": true, "cat": true, "head": true, "tail": true, "wc": true,
	"ls": true, "stat": true, "du": true, "md5sum": true, "sha1sum": true, "sha256sum": true,
	"sha512sum": true,
}

// xargsValued are the xargs options that take a value, as their own argument or glued to them.
var xargsValued = map[string]bool{"-I": true, "-n": true, "-L": true, "-P": true, "-s": true, "-d": true,
	"-E": true, "-a": true}

// xargsFlags are the xargs options that take no value.
var xargsFlags = map[string]bool{"-0": true, "-r": true, "-t": true, "-x": true, "--null": true,
	"--no-run-if-empty": true, "--verbose": true, "--exit": true}

// xargsDecision answers `find . -name '*.go' | xargs grep foo`: xargs running one of xargsReaders
// is a reader, and was asked about only because the policy did not look past the name xargs. Any
// other program, or an option this does not know, is left to the question.
func xargsDecision(name string, args []string) (Decision, bool) {
	if name != "xargs" {
		return Decision{}, false
	}
	i := 0
	for i < len(args) {
		a := args[i]
		switch {
		case xargsFlags[a]:
			i++
		case xargsValued[a]:
			i += 2
		case len(a) > 2 && xargsValued[a[:2]]:
			i++ // the value is glued to the option
		default:
			return xargsProgram(args[i:])
		}
	}
	return Decision{}, false
}

// xargsProgram decides the program xargs runs, given as the first of rest.
func xargsProgram(rest []string) (Decision, bool) {
	if len(rest) == 0 || !xargsReaders[baseName(rest[0])] {
		return Decision{}, false
	}
	return Decision{Allow, fmt.Sprintf("xargs hands its input to %q, which only reads", rest[0]),
		"xargs-reader", false}, true
}

// exportedRunners are the variables that change which program runs or what a shell runs on its
// own, so exporting one is a question: the commands after it on the line run with it.
var exportedRunners = map[string]bool{"PATH": true, "BASH_ENV": true, "ENV": true, "IFS": true,
	"PROMPT_COMMAND": true, "SHELLOPTS": true, "BASHOPTS": true, "CDPATH": true}

// exportVar is a NAME or NAME=value an export sets.
var exportVar = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*(=.*)?$`)

// exportDecision answers `export NAME=value`, which only sets a variable for the rest of the line
// - as the `NAME=value cmd` in front of a command already does without a question. A variable
// that changes which program runs (PATH, the dynamic loader's LD_*/DYLD_*, the shell's start-up
// files) and an export with options are still asked about.
func exportDecision(name string, args []string) (Decision, bool) {
	if name != "export" || len(args) == 0 {
		return Decision{}, false
	}
	for _, a := range args {
		if !exportVar.MatchString(a) {
			return Decision{}, false
		}
		v, _, _ := strings.Cut(a, "=")
		if exportedRunners[v] || strings.HasPrefix(v, "LD_") || strings.HasPrefix(v, "DYLD_") {
			return Decision{}, false
		}
	}
	return Decision{Allow, "export only sets a variable for the commands after it", "export", false}, true
}
