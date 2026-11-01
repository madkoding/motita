package policy

import (
	"fmt"
	"regexp"

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
