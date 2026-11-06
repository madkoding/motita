package readonly

import "strings"

// RunnerVariable reports whether setting the environment variable name changes which program
// the commands after it run, or what those programs load, and says how. It is one table for
// every way a line can set a variable - `export NAME=value`, the `NAME=value cmd` prefix and
// `env NAME=value cmd` - so the three cannot drift into three different ideas of which names
// are harmless.
//
// A variable here is a program's back door: `GIT_SSH_COMMAND` names what git runs to reach a
// remote, `GIT_EXTERNAL_DIFF` what `git diff` runs, `NODE_OPTIONS=--require` a module node loads
// before any script, `BASH_ENV` a file bash reads before any script. A policy that asks about
// `git -c core.fsmonitor=x status` and not about `GIT_CONFIG_KEY_0=core.fsmonitor git status`
// is asking about the door and leaving the window open.
func RunnerVariable(name string) (string, bool) {
	if reason, ok := runnerVariables[name]; ok {
		return name + " " + reason, true
	}
	if strings.HasPrefix(name, "GIT_") && !harmlessGitVariables[name] {
		return name + " changes what git runs, reads or trusts", true
	}
	for _, p := range runnerPrefixes {
		if strings.HasPrefix(name, p.prefix) {
			return name + " " + p.reason, true
		}
	}
	return "", false
}

// runnerVariables are the exact names, with what each one changes.
var runnerVariables = map[string]string{
	// The shell's own.
	"PATH": "changes where every program is found", "BASH_ENV": "names a file bash runs first",
	"ENV": "names a file sh runs first", "IFS": "changes how the shell splits words",
	"PROMPT_COMMAND": "is run by the shell on its own", "SHELLOPTS": "changes how the shell runs",
	"BASHOPTS": "changes how the shell runs", "CDPATH": "changes where cd goes",
	"SHELL": "names the shell make and others run", "HOME": "changes whose configuration files are read",
	"XDG_CONFIG_HOME": "changes which configuration files are read",
	// Programs started by others.
	"EDITOR": "names the program an editor is wanted from", "VISUAL": "names the program an editor is wanted from",
	"PAGER": "names the program output is shown through", "LESSOPEN": "names a program less runs",
	"LESSCLOSE": "names a program less runs", "SSH_ASKPASS": "names a program ssh runs",
	// Runtimes that load code from the environment.
	"NODE_OPTIONS": "loads code into node", "NODE_PATH": "changes where node finds modules",
	"RUBYOPT": "loads code into ruby", "RUBYLIB": "changes where ruby finds code",
	"PERL5OPT": "loads code into perl", "PERL5LIB": "changes where perl finds code",
	"PERLLIB": "changes where perl finds code", "PYTHONSTARTUP": "names a file python runs first",
	"PYTHONPATH": "changes where python finds modules", "PYTHONHOME": "changes where python finds modules",
	"PYTHONUSERBASE": "changes where python finds modules",
	"_JAVA_OPTIONS":  "loads code into java", "JAVA_TOOL_OPTIONS": "loads code into java",
	"JDK_JAVA_OPTIONS": "loads code into java", "CLASSPATH": "changes where java finds code",
	"MAVEN_OPTS": "loads code into maven", "GRADLE_OPTS": "loads code into gradle",
	"GOFLAGS": "can name a program go runs for every tool", "GOROOT": "changes which go toolchain runs",
	"RUSTC": "names the compiler cargo runs", "RUSTC_WRAPPER": "names a program cargo runs",
	"RUSTC_WORKSPACE_WRAPPER": "names a program cargo runs", "CARGO_BUILD_RUSTC": "names the compiler cargo runs",
	"CARGO_BUILD_RUSTC_WRAPPER": "names a program cargo runs",
	// The C library.
	"GCONV_PATH": "loads code into every program", "LOCPATH": "loads data into every program",
	"NLSPATH": "loads data into every program",
}

// runnerPrefixes are the families of names, with what the family changes.
var runnerPrefixes = []struct{ prefix, reason string }{
	{"LD_", "changes what the dynamic loader loads"},
	{"DYLD_", "changes what the dynamic loader loads"},
	{"CARGO_TARGET_", "can name the program cargo runs its builds through"},
}

// harmlessGitVariables are the GIT_* names that only describe or report: who the author is, what
// to trace, whether to prompt. Everything else in the family names a program, a path or a
// configuration git then trusts.
var harmlessGitVariables = map[string]bool{
	"GIT_AUTHOR_NAME": true, "GIT_AUTHOR_EMAIL": true, "GIT_AUTHOR_DATE": true,
	"GIT_COMMITTER_NAME": true, "GIT_COMMITTER_EMAIL": true, "GIT_COMMITTER_DATE": true,
	"GIT_TERMINAL_PROMPT": true, "GIT_TRACE": true, "GIT_TRACE_PACKET": true, "GIT_TRACE_PERFORMANCE": true,
	"GIT_TRACE_SETUP": true, "GIT_CURL_VERBOSE": true, "GIT_LFS_SKIP_SMUDGE": true,
	"GIT_OPTIONAL_LOCKS": true, "GIT_ADVICE": true, "GIT_FLUSH": true, "GIT_MERGE_AUTOEDIT": true,
	"GIT_PROTOCOL_FROM_USER": true, "GIT_NO_REPLACE_OBJECTS": true, "GIT_LITERAL_PATHSPECS": true,
	"GIT_GLOB_PATHSPECS": true, "GIT_NOGLOB_PATHSPECS": true, "GIT_ICASE_PATHSPECS": true,
	"GIT_REFLOG_ACTION": true, "GIT_PAGER_IN_USE": true,
}
