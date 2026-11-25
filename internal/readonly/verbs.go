package readonly

import "strings"

// unplacedVerb stands for a subcommand that could not be found because an option before it is
// not one Verb knows. It matches no verb a rule allows, so the rule refuses and says so.
const unplacedVerb = "(an option read-only mode cannot place)"

// optionsWithValue are the options a tool takes BEFORE its subcommand together with a value in
// the next word (`kubectl -n ns get`, `go -C dir test`, `systemctl -p Id status`). The value is not
// the subcommand: reading it as one let `kubectl -n get delete pod` pass as `get`, and
// `go -C list run .` as `list`.
var optionsWithValue = map[string]map[string]bool{
	"docker": {"-H": true, "--host": true, "-c": true, "--context": true, "-l": true, "--log-level": true,
		"--config": true, "--tlscacert": true, "--tlscert": true, "--tlskey": true},
	"kubectl": {"-n": true, "--namespace": true, "--context": true, "--cluster": true, "--user": true,
		"--kubeconfig": true, "-s": true, "--server": true, "--token": true, "--as": true, "--as-group": true,
		"--request-timeout": true, "--cache-dir": true},
	"go": {"-C": true},
	"npm": {"--prefix": true, "-w": true, "--workspace": true, "--registry": true, "--cache": true,
		"--userconfig": true, "--loglevel": true},
	"pnpm":  {"--dir": true, "-C": true, "--filter": true, "-F": true, "--reporter": true},
	"yarn":  {"--cwd": true, "--cache-folder": true, "--modules-folder": true},
	"pip":   {"--proxy": true, "--cache-dir": true, "--log": true, "--retries": true, "--timeout": true},
	"cargo": {"-C": true, "--manifest-path": true, "--color": true},
	"systemctl": {"-p": true, "--property": true, "-t": true, "--type": true, "-s": true, "--signal": true,
		"-H": true, "--host": true, "-M": true, "--machine": true, "-n": true, "--lines": true, "-o": true,
		"--output": true, "--state": true, "--root": true, "--kill-whom": true, "-P": true},
}

// harmlessSwitches are the options that take no value and change nothing about what a subcommand does.
var harmlessSwitches = map[string]bool{
	"-v": true, "--verbose": true, "-q": true, "--quiet": true, "-s": true, "--silent": true,
	"--json": true, "--no-color": true, "--no-audit": true, "--no-fund": true, "--no-pager": true,
	"--user": true, "--system": true, "--no-legend": true, "--full": true, "-l": true, "-a": true,
	"--all": true, "--failed": true,
}

// Verb is the subcommand of a tool: the first word that is neither an option nor the value of
// one. known is false when an option before it is not one the table can place (it may take a value,
// which would shift the subcommand), so the answer must not be trusted: refusing or asking is the
// cost of an option nobody listed.
func Verb(name string, args []string) (verb string, known bool) {
	values := optionsWithValue[name]
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case !strings.HasPrefix(a, "-"):
			return a, true
		case strings.Contains(a, "="):
			// --opt=value carries its value.
		case values[a]:
			i++ // the next word is the value, not the subcommand (a tool's own table wins: -s is a value for kubectl).
		case harmlessSwitches[a]:
			// a switch has no value.
		default:
			return "", false
		}
	}
	return "", true
}

// verbOf is Verb for the rules in this package: a subcommand it could not place is
// unplacedVerb, which no rule allows, instead of a guess.
func verbOf(name string, args []string) string {
	verb, known := Verb(name, args)
	if !known {
		return unplacedVerb
	}
	return verb
}
