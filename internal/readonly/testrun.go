package readonly

// Test runs, which plan mode allows. A plan that cannot run the project's tests cannot tell whether
// the suite is green today, so it cannot be checked against anything. A test run is not a read, but
// it is what the project's own checks do, and it runs inside the sandbox like every other command
// of this mode. What stays refused is everything that hands the runner a program to run, a plugin
// to load or a file to write, and the watch modes that never end.

import (
	"fmt"
	"strings"
)

// testRunners judge the programs that run a project's tests, by name.
var testRunners = map[string]func([]string) (string, bool){
	"go": goTestRule, "npm": scriptTestRule, "pnpm": scriptTestRule, "yarn": scriptTestRule,
	"cargo": subcommandTestRule, "dotnet": subcommandTestRule, "mvn": subcommandTestRule,
	"gradle": subcommandTestRule, "make": makeTestRule,
	"pytest": pytestRule, "jest": jestRule, "vitest": vitestRule, "phpunit": plainTestRule,
	"rspec": plainTestRule, "python": pythonTestRule, "python3": pythonTestRule,
}

// testRun decides a command that runs tests. ok is false when it is not a test run, so the caller
// goes on with the other plan-only reads.
func testRun(name string, args []string) (allowed bool, reason string, ok bool) {
	rule, runner := testRunners[name]
	if !runner || !runsTests(name, args) {
		return false, "", false
	}
	if why, bad := rule(args); bad {
		return false, why, true
	}
	return true, name + " only runs the tests, in the sandbox", true
}

// runsTests reports whether args ask name for a test run.
func runsTests(name string, args []string) bool {
	switch name {
	case "go", "cargo", "dotnet", "mvn", "gradle":
		return firstNonFlag(args) == "test"
	case "npm", "pnpm", "yarn":
		return scriptName(args) != ""
	case "make":
		return makeTarget(args) != ""
	case "python", "python3":
		return len(args) >= 2 && args[0] == "-m" && (args[1] == "pytest" || args[1] == "unittest")
	default:
		return true
	}
}

// scriptName is the test script `npm test`, `npm run test:unit` or `yarn test` names, or "".
func scriptName(args []string) string {
	sub := firstNonFlag(args)
	if sub == "run" || sub == "run-script" {
		for i, a := range args {
			if a == sub {
				sub = firstNonFlag(args[i+1:])
				break
			}
		}
	}
	if isTestScript(sub) {
		return sub
	}
	return ""
}

// isTestScript reports whether a script name is the project's tests: test, test:unit, coverage.
func isTestScript(name string) bool {
	switch name {
	case "t", "tst", "test", "coverage":
		return true
	default:
		return strings.HasPrefix(name, "test:")
	}
}

// makeTarget is the test target `make test` or `make check` names, or "".
func makeTarget(args []string) string {
	for _, a := range args {
		if a == "test" || a == "check" || a == "tests" {
			return a
		}
	}
	return ""
}

// testOutputFlags are the options that write a file of the caller's choosing or run a program.
var testOutputFlags = []string{
	"coverprofile", "cpuprofile", "memprofile", "blockprofile", "mutexprofile", "trace", "outputdir",
	"fuzz", "json-report", "junitxml", "junit-xml", "result-log", "basetemp", "plugin", "pyargs",
	"watch", "watchAll", "watch-all", "script", "runner", "setupFiles",
}

// Options one runner spells with a single letter that another spells differently: `go test -c`
// writes a binary but `go test -p 4` is a parallelism, `pytest -p` loads a plugin, `jest -w` is
// a worker count but `jest --config` loads a script.
var (
	goTestExtra = []string{"c"}
	pytestExtra = []string{"p", "c"}
	jestExtra   = []string{"c", "config"}
)

// flagTestRule refuses, for a runner whose every form runs tests, the options that write a file,
// load a plugin or keep watching.
func flagTestRule(args []string, extra ...string) (string, bool) {
	for _, a := range args {
		if a == "--" {
			return "", false
		}
		if !strings.HasPrefix(a, "-") {
			continue
		}
		name, _, _ := strings.Cut(strings.TrimLeft(a, "-"), "=")
		for _, f := range append(extra, testOutputFlags...) {
			if name == f {
				return fmt.Sprintf("%s writes a file, loads code or never ends, and read-only mode cannot check it", a), true
			}
		}
	}
	return "", false
}

// goTestRule runs the go option checks and then the output flags: `go test -cover` prints, and
// `-coverprofile` writes.
func goTestRule(args []string) (string, bool) {
	for i := range args {
		if why, bad := goOption(args, i); bad {
			return why, true
		}
	}
	return flagTestRule(withoutCoverProfile(args), goTestExtra...)
}

// coverProfileExts are the extensions a coverage profile may have: `coverage.out`, `cover.cov`.
var coverProfileExts = []string{".out", ".cov", ".coverprofile"}

// withoutCoverProfile drops `-coverprofile NAME` (or `=NAME`) when NAME is a bare file name with a
// profile extension, so measuring coverage in plan mode is possible and cannot name `go.mod`, a path
// outside the folder or a hidden file. Any other target is left in args, where it is refused.
func withoutCoverProfile(args []string) []string {
	var out []string
	for i := 0; i < len(args); i++ {
		name, value, glued := strings.Cut(strings.TrimLeft(args[i], "-"), "=")
		if !strings.HasPrefix(args[i], "-") || name != "coverprofile" {
			out = append(out, args[i])
			continue
		}
		next := i + 1
		if !glued && next < len(args) {
			value = args[next]
		}
		if !coverProfileName(value) {
			out = append(out, args[i])
			continue
		}
		if !glued {
			i++
		}
	}
	return out
}

// coverProfileName reports whether name is a bare, visible file name with a profile extension.
func coverProfileName(name string) bool {
	if name == "" || strings.HasPrefix(name, ".") || strings.ContainsAny(name, `/\:`) {
		return false
	}
	for _, e := range coverProfileExts {
		if strings.HasSuffix(name, e) {
			return true
		}
	}
	return false
}

// scriptTestRule allows `npm test`, `npm run test:unit` and their pnpm and yarn forms; the
// arguments after `--` go to the script and are the script's own.
func scriptTestRule(args []string) (string, bool) {
	for _, a := range args {
		if a == "--" {
			return "", false
		}
	}
	return flagTestRule(args)
}

// subcommandTestRule allows `cargo test`, `dotnet test`, `mvn test` and `gradle test`.
func subcommandTestRule(args []string) (string, bool) { return flagTestRule(args) }

// makeTestRule allows `make test` and `make check` with no variable assignment, which could swap
// the recipe's program (`make test SHELL=...`).
func makeTestRule(args []string) (string, bool) {
	for _, a := range args {
		if strings.Contains(a, "=") {
			return "make " + a + " overrides what the recipe runs", true
		}
		if strings.HasPrefix(a, "-") && !makeFlagIsHarmless(a) {
			return "make " + a + " changes how make runs, and read-only mode cannot check it", true
		}
	}
	return "", false
}

// makeFlagIsHarmless reports the make options that only set parallelism or how much it says.
func makeFlagIsHarmless(a string) bool {
	return strings.HasPrefix(a, "-j") || a == "-k" || a == "-s"
}

// vitestRule allows `vitest run`; bare `vitest` watches, which never ends.
func vitestRule(args []string) (string, bool) {
	if firstNonFlag(args) != "run" {
		return "vitest without `run` keeps watching; use `vitest run`", true
	}
	return flagTestRule(args)
}

// pythonTestRule allows `python -m pytest` and `python -m unittest`.
func pythonTestRule(args []string) (string, bool) { return pytestRule(args[2:]) }

func pytestRule(args []string) (string, bool)    { return flagTestRule(args, pytestExtra...) }
func jestRule(args []string) (string, bool)      { return flagTestRule(args, jestExtra...) }
func plainTestRule(args []string) (string, bool) { return flagTestRule(args) }
