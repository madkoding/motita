package readonly

// The rules for the tools a project is built with: its package managers, its runtimes and `fd`.
// They were writers by name, which stopped a plan from asking `npm ls` what a project depends on
// or `node --version` what it runs on. Each is an allowlist of the forms that only report.

import (
	"fmt"
	"strings"
)

// isVersionFlag reports whether a asks a program for its version.
func isVersionFlag(a string) bool {
	switch a {
	case "--version", "-v", "-V", "-version", "version":
		return true
	default:
		return false
	}
}

// versionRule allows a runtime only as `prog --version`: it is the one form of a language runtime
// or build tool that cannot run the project's code.
func versionRule(args []string) (string, bool) {
	if len(args) == 1 && isVersionFlag(args[0]) {
		return "", false
	}
	return "this program runs code of its own, so read-only mode only allows asking its version", true
}

// dotnetRule allows `dotnet --version`, `--info`, `--list-sdks` and `--list-runtimes`.
func dotnetRule(args []string) (string, bool) {
	if len(args) == 1 {
		switch args[0] {
		case "--version", "--info", "--list-sdks", "--list-runtimes":
			return "", false
		default:
			// Not one of the reporting options.
		}
	}
	return "dotnet runs code of its own, so read-only mode only allows --version, --info, " +
		"--list-sdks and --list-runtimes", true
}

// packageReport allows a package manager only in the verbs that report what is installed or why,
// and in `--version`. Installing, removing, publishing and running scripts stay refused.
func packageReport(name string, verbs map[string]bool, args []string) (string, bool) {
	if len(args) == 1 && isVersionFlag(args[0]) && args[0] != "version" {
		return "", false
	}
	if verbs[firstNonFlag(args)] {
		return "", false
	}
	return fmt.Sprintf("%s can change the project or the system; only %s are read-only",
		name, joinKeys(verbs)), true
}

// joinKeys lists a verb set in a fixed order for a message.
func joinKeys(set map[string]bool) string {
	var out []string
	for k := range set {
		out = append(out, k)
	}
	sortStrings(out)
	return strings.Join(out, ", ")
}

// sortStrings sorts in place (insertion sort: the lists are a handful of words).
func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}

var (
	npmReporting  = map[string]bool{"ls": true, "list": true, "ll": true, "la": true, "explain": true, "why": true, "root": true, "prefix": true, "bin": true}
	pnpmReporting = map[string]bool{"ls": true, "list": true, "why": true, "root": true, "bin": true}
	yarnReporting = map[string]bool{"list": true, "ls": true, "why": true}
	pipReporting  = map[string]bool{"list": true, "show": true, "freeze": true, "check": true}
)

func npmRule(args []string) (string, bool)  { return packageReport("npm", npmReporting, args) }
func pnpmRule(args []string) (string, bool) { return packageReport("pnpm", pnpmReporting, args) }
func yarnRule(args []string) (string, bool) { return packageReport("yarn", yarnReporting, args) }
func pipRule(args []string) (string, bool)  { return packageReport("pip", pipReporting, args) }

// fdRule refuses `-x`/`--exec` and `-X`/`--exec-batch`, which run a command on every match.
func fdRule(args []string) (string, bool) {
	for _, a := range args {
		name, _, _ := strings.Cut(a, "=")
		if name == "--exec" || name == "--exec-batch" || shortClusterHas(a, "x") || shortClusterHas(a, "X") {
			return fmt.Sprintf("fd %s runs a command on every match", a), true
		}
	}
	return "", false
}
