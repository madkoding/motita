package readonly

// Reads that only plan mode allows: `git fetch` and the GitHub CLI's reporting verbs, which reach
// another machine, and a runtime asked for its version. They are judged here, and only by Check,
// not by Classify: the wider policy of the mode where writing is the point keeps its own answer
// for them (it asks about the network, and decides a runtime by the script it is given), while a
// plan that cannot look at a remote or a version cannot be checked against it. The sandbox's
// network setting still decides whether a connection can open at all.

import (
	"fmt"
	"strings"
)

// PlanRead decides the commands only plan mode allows. ok is false for a command that is not one
// of them, so the caller goes on with the ordinary classification; allowed is then the verdict and
// reason the explanation.
func PlanRead(command string, args []string) (allowed bool, reason string, ok bool) {
	name := strings.ToLower(baseOf(command))
	if rule, runtime := runtimes[name]; runtime {
		if why, bad := rule(args); bad {
			return false, why, true
		}
		return true, "only its version is asked", true
	}
	switch name {
	case "git":
		i, why := gitGlobalOptions(args)
		if why != "" || i >= len(args) || args[i] != "fetch" {
			return false, "", false
		}
		if why, bad := gitFetchRule(args[i+1:]); bad {
			return false, why, true
		}
		return true, "git fetch only updates the remote-tracking references and objects, never the working tree", true
	case "gh":
		if why, bad := ghRule(args); bad {
			return false, why, true
		}
		return true, "gh only reports here", true
	default:
		return false, "", false
	}
}

// baseOf is the file name of a program written with a path.
func baseOf(command string) string {
	command = strings.TrimSpace(command)
	if i := strings.LastIndexAny(command, `/\`); i >= 0 {
		return command[i+1:]
	}
	return command
}

// gitFetchLong are the long options of `git fetch` that change nothing but what it downloads.
// Anything else - `--upload-pack` and `--update-head-ok` among them - is refused: the first runs a
// program, the second lets a fetch move the checked-out branch.
var gitFetchLong = []string{
	"--all", "--append", "--atomic", "--depth", "--deepen", "--dry-run", "--filter", "--force",
	"--ipv4", "--ipv6", "--jobs", "--keep", "--multiple", "--no-progress", "--no-recurse-submodules",
	"--no-show-forced-updates", "--no-tags", "--no-write-fetch-head", "--progress", "--prune",
	"--prune-tags", "--quiet", "--recurse-submodules", "--refetch", "--shallow-exclude",
	"--shallow-since", "--show-forced-updates", "--tags", "--unshallow", "--verbose",
	"--write-fetch-head",
}

// gitFetchShorts are the single-letter options of `git fetch` that are safe.
const gitFetchShorts = "apPtvqfnkj"

// gitFetchRule allows `git fetch [options] [remote]`. A refspec (`main:main`) names a local
// reference to write, and `transport::address` picks a remote helper, which is a program; both
// contain a colon and are refused, so a fetch can only update remote-tracking references.
func gitFetchRule(rest []string) (string, bool) {
	for _, a := range rest {
		switch {
		case a == "--":
			return "", false
		case strings.HasPrefix(a, "--"):
			name, _, _ := strings.Cut(a, "=")
			if resolveLong(name, gitFetchLong) == "" {
				return fmt.Sprintf("git fetch %s is not an option read-only mode can check", name), true
			}
		case strings.HasPrefix(a, "-") && len(a) > 1:
			if strings.Trim(a[1:], gitFetchShorts+"0123456789") != "" {
				return fmt.Sprintf("git fetch %s is not an option read-only mode can check", a), true
			}
		case strings.Contains(a, ":"):
			return fmt.Sprintf("git fetch %s names a local reference to write or a remote helper to run", a), true
		default:
			// A remote name, a URL without a colon in a path, or an option's separate value.
		}
	}
	return "", false
}

// ghReading are the verbs of each `gh` command group that only report.
var ghReading = map[string][]string{
	"pr":       {"view", "list", "diff", "checks", "status"},
	"issue":    {"view", "list", "status"},
	"run":      {"view", "list"},
	"workflow": {"view", "list"},
	"release":  {"view", "list"},
	"repo":     {"view", "list"},
	"label":    {"list"},
	"search":   {"repos", "issues", "prs", "code", "commits"},
	"auth":     {"status"},
}

// ghRule allows the GitHub CLI only in its reporting verbs, and `gh api` only as a GET with no
// body. `--web` opens a browser, which is not a read.
func ghRule(args []string) (string, bool) {
	sub := firstNonFlag(args)
	if sub == "api" {
		return ghAPIRule(args)
	}
	verbs, ok := ghReading[sub]
	if !ok {
		return fmt.Sprintf("gh %s is not a reporting command; read-only mode allows gh pr, issue, run, "+
			"workflow, release, repo and label view/list, gh search, gh auth status and gh api (GET)", sub), true
	}
	verb := ""
	for i, a := range args {
		if a == sub {
			verb = firstNonFlag(args[i+1:])
			break
		}
	}
	for _, a := range args {
		if a == "--web" || a == "-w" {
			return "gh " + a + " opens a browser", true
		}
	}
	for _, v := range verbs {
		if verb == v {
			return "", false
		}
	}
	return fmt.Sprintf("gh %s %s can change GitHub; only %s is read-only", sub, verb, strings.Join(verbs, ", ")), true
}

// ghAPIBody are the `gh api` options that send a body, which makes the request a POST.
var ghAPIBody = map[string]bool{
	"-f": true, "-F": true, "--field": true, "--raw-field": true, "--input": true,
}

// ghAPIRule allows `gh api ENDPOINT` as a GET: no method other than GET, no body, no graphql.
func ghAPIRule(args []string) (string, bool) {
	for i, a := range args {
		name, value, glued := strings.Cut(a, "=")
		switch {
		case ghAPIBody[name]:
			return fmt.Sprintf("gh api %s sends a body", name), true
		case len(a) > 2 && (strings.HasPrefix(a, "-f") || strings.HasPrefix(a, "-F")) && !strings.HasPrefix(a, "--"):
			return fmt.Sprintf("gh api %s sends a body", a), true
		case name == "--method" || name == "-X":
			if !glued && i+1 < len(args) {
				value = args[i+1]
			}
			if !strings.EqualFold(value, "GET") {
				return fmt.Sprintf("gh api %s %s is not a read", name, value), true
			}
		case len(a) > 2 && strings.HasPrefix(a, "-X"):
			if !strings.EqualFold(a[2:], "GET") {
				return fmt.Sprintf("gh api %s is not a read", a), true
			}
		case strings.HasPrefix(a, "graphql"):
			return "gh api graphql is a POST", true
		default:
			// The endpoint, or an option that formats what is read.
		}
	}
	return "", false
}

// runtimes are the language runtimes and build tools, each allowed only to say what version it is.
var runtimes = map[string]func([]string) (string, bool){
	"python": versionRule, "python3": versionRule, "node": versionRule, "ruby": versionRule,
	"php": versionRule, "java": versionRule, "mvn": versionRule, "gradle": versionRule,
	"dotnet": dotnetRule,
}
