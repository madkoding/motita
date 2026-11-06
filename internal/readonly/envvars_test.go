package readonly

import (
	"strings"
	"testing"
)

// TestRunnerVariableNamesWhatChangesWhatRuns: the one table every way of setting a variable
// consults. A name that only describes (CI, GOFLAGS unset, an author) is not in it; a name
// that hands a program something to run or load is, with a reason that names the variable.
func TestRunnerVariableNamesWhatChangesWhatRuns(t *testing.T) {
	runs := []string{"PATH", "BASH_ENV", "GIT_SSH_COMMAND", "GIT_EXTERNAL_DIFF", "GIT_CONFIG_KEY_0",
		"GIT_EXEC_PATH", "GIT_DIR", "LD_PRELOAD", "DYLD_INSERT_LIBRARIES", "NODE_OPTIONS", "RUBYOPT",
		"PERL5OPT", "PYTHONSTARTUP", "JAVA_TOOL_OPTIONS", "MAVEN_OPTS", "GOFLAGS", "RUSTC_WRAPPER",
		"CARGO_TARGET_X86_64_UNKNOWN_LINUX_GNU_RUNNER", "GCONV_PATH", "HOME", "EDITOR"}
	for _, name := range runs {
		reason, ok := RunnerVariable(name)
		if !ok {
			t.Errorf("%s changes what runs and must be in the table", name)
		}
		if !strings.HasPrefix(reason, name+" ") {
			t.Errorf("%s: the reason must name the variable: %q", name, reason)
		}
	}
	for _, name := range []string{"CI", "GOOS", "GIT_AUTHOR_NAME", "GIT_TERMINAL_PROMPT", "GIT_TRACE",
		"NO_COLOR", "TERM", "LANG", "", "LDFLAGS_X", "DEBUG"} {
		if reason, ok := RunnerVariable(name); ok {
			t.Errorf("%q only describes and must not be a question: %q", name, reason)
		}
	}
}

// TestEnvWithARunnerVariableIsNotUnwrapped: `env GIT_SSH_COMMAND=x git fetch` is not `git fetch`.
func TestEnvWithARunnerVariableIsNotUnwrapped(t *testing.T) {
	_, _, reason := Unwrap("env", []string{"GIT_SSH_COMMAND=/tmp/x", "git", "fetch"})
	if !strings.Contains(reason, "GIT_SSH_COMMAND") {
		t.Errorf("env with a variable that changes what git runs must be refused: %q", reason)
	}
	inner, args, reason := Unwrap("env", []string{"CI=1", "git", "fetch"})
	if reason != "" || inner != "git" || len(args) != 1 || args[0] != "fetch" {
		t.Errorf("env with a plain variable is the program after it: %q %v %q", inner, args, reason)
	}
}
