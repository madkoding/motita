package policy

import "testing"

// A variable can be a program's back door: GIT_SSH_COMMAND names what git runs to reach a
// remote, GIT_EXTERNAL_DIFF what `git diff` runs, NODE_OPTIONS=--require what node loads before
// any script. The policy asked about `git -c core.fsmonitor=x status` and let the same thing
// through as an environment variable, by export, by a `NAME=value` prefix, by `env` and by a
// bare assignment. All four now go through one table (readonly.RunnerVariable).
func TestAVariableThatChangesWhatRunsIsAQuestionHoweverItIsSet(t *testing.T) {
	dir := "/tmp/w"
	cases := []struct {
		line string
		want Verdict
		rule string
	}{
		{"export GIT_SSH_COMMAND=/tmp/evil; git fetch origin", Ask, "unclassified"},
		{"export GIT_EXTERNAL_DIFF=/tmp/evil; git diff", Ask, "unclassified"},
		{"export GIT_CONFIG_COUNT=1 GIT_CONFIG_KEY_0=core.fsmonitor; git status", Ask, "unclassified"},
		{"export NODE_OPTIONS=--require=/tmp/evil.js; npm test", Ask, "unclassified"},
		{"GIT_EXTERNAL_DIFF=/tmp/evil.sh git diff", Ask, "environment-runs-code"},
		{"GIT_SEQUENCE_EDITOR=/tmp/evil git rebase -i HEAD~2", Ask, "environment-runs-code"},
		{"BASH_ENV=/tmp/evil.sh bash scripts/check.sh", Ask, "environment-runs-code"},
		{"PYTHONSTARTUP=/tmp/evil.py python3 scripts/x.py", Ask, "environment-runs-code"},
		{"LD_PRELOAD=/tmp/x.so ls", Ask, "environment-runs-code"},
		{"CI=1 NODE_OPTIONS=--require=/tmp/e.js npm test", Ask, "environment-runs-code"},
		{"env GIT_EXEC_PATH=/tmp/evil git status", Ask, "wrapper-unreadable"},
		{"PATH=/tmp/evil", Ask, "environment-runs-code"},
		{"PATH=/tmp/evil; git status", Ask, "environment-runs-code"},
		{"GIT_SSH=/tmp/evil; git fetch origin", Ask, "environment-runs-code"},
		// A name that only describes sets nothing a program runs.
		{"GIT_TERMINAL_PROMPT=0 git fetch origin", Allow, "git-fetch"},
		{"export GIT_AUTHOR_NAME=x; git status", Allow, "export"},
		{"CI=1 ls", Allow, "reader"},
		{"CI=1; ls", Allow, "assignment"},
		{"DEBUG=1", Allow, "assignment"},
		{"A=1 B=2", Allow, "assignment"},
		{"export CI=1 GOFLAGS", Allow, "export"},
		{"=x ls", Allow, "reader"},
	}
	for _, c := range cases {
		d := Mode{Enforce: true}.DecideLine(c.line, dir)
		if d.Verdict != c.want || d.Rule != c.rule {
			t.Errorf("%q: got %s (%s), want %s (%s): %s", c.line, d.Verdict, d.Rule, c.want, c.rule, d.Reason)
		}
	}
	strict := Mode{Enforce: true, Strict: true}.DecideLine("GIT_EXEC_PATH=/tmp/evil git status", dir)
	if strict.Verdict != Deny {
		t.Errorf("strict refuses a variable that changes what runs: %s (%s)", strict.Verdict, strict.Reason)
	}
}

// `rm -rf "$HOME"` is not five characters inside the workspace: the shell puts the home
// directory there. A path with a variable in it is held to be outside, so the user says where
// it goes; the unquoted `$HOME` was already refused by the tokeniser.
func TestAPathWithAVariableIsNotInsideTheWorkspace(t *testing.T) {
	dir := "/tmp/w"
	cases := []struct {
		line string
		want Verdict
		rule string
	}{
		{`rm -rf "$HOME"`, Ask, "write-outside-workspace"},
		{`rm -rf "${HOME}"`, Ask, "write-outside-workspace"},
		{`rm -rf "$PWD/.."`, Ask, "write-outside-workspace"},
		{`cp f "$X"`, Ask, "write-outside-workspace"},
		{`touch "$1"`, Ask, "write-outside-workspace"},
		{`echo hi > "$HOME/.bashrc"`, Ask, "write-outside-workspace"},
		{`cd "$HOME" && make`, Ask, "leaves-workspace"},
		{`export X=/home/user; rm -rf "$X"`, Ask, "write-outside-workspace"},
		// A lone `$` is the character.
		{`touch "a$"`, Allow, "write-inside-workspace"},
		{`touch "a$ b"`, Allow, "write-inside-workspace"},
		{`grep -n "end$" f`, Allow, "reader"},
	}
	for _, c := range cases {
		d := Mode{Enforce: true}.DecideLine(c.line, dir)
		if d.Verdict != c.want || d.Rule != c.rule {
			t.Errorf("%q: got %s (%s), want %s (%s): %s", c.line, d.Verdict, d.Rule, c.want, c.rule, d.Reason)
		}
	}
	for _, w := range []string{"$HOME", "${X}", "a$1", "$?", "$$", "x$@y"} {
		if !expandsVariable(w) {
			t.Errorf("%q expands a variable", w)
		}
	}
	for _, w := range []string{"", "$", "a$", "$ b", "end$", "$%", "a=b"} {
		if expandsVariable(w) {
			t.Errorf("%q is the character", w)
		}
	}
}

// A loop over the project's own files binds its variable: every pass writes inside, so the
// line is not a question. A list with a path outside, a variable, a substitution or a quote
// leaves the variable alone, and the write is asked about.
func TestALoopOverTheWorkspaceBindsItsVariable(t *testing.T) {
	dir := "/tmp/w"
	cases := []struct {
		line string
		want Verdict
	}{
		{`for f in a b; do cat > $f.txt; done`, Allow},
		{`for f in a b; do rm -rf "$f"; done`, Allow},
		{`for f in src/*.go; do rm "${f}"; done`, Allow},
		{`for d in a b; do cd $d && make; done`, Allow},
		{"for f in a b\ndo\n  touch \"$f.bak\"\ndone", Allow},
		{`ls && for f in a; do rm "$f"; done`, Allow},
		{`for f in /etc/x; do cat > "$f"; done`, Ask},
		{`for f in ../x; do rm -rf "$f"; done`, Ask},
		{`for f in "$HOME"; do rm -rf "$f"; done`, Ask},
		{`for f in $(ls); do rm "$f"; done`, Ask},
		{`for f in a; do rm -rf "$fx"; done`, Ask},
		{`for f in ; do rm -rf "$f"; done`, Ask},
		{`rm -rf "$f"`, Ask},
	}
	for _, c := range cases {
		d := Mode{Enforce: true}.DecideLine(c.line, dir)
		if d.Verdict != c.want {
			t.Errorf("%q: got %s (%s: %s), want %s", c.line, d.Verdict, d.Rule, d.Reason, c.want)
		}
	}
	if got := bindLoopVariables(`for f in a; do rm "$f"; done`, ""); got != `for f in a; do rm "$f"; done` {
		t.Errorf("with no workspace nothing binds: %q", got)
	}
	if got := bindLoopVariables("for f in a; do ls; done", dir); got != "for f in a; do ls; done" {
		t.Errorf("a line with no variable is left as it is: %q", got)
	}
	if got := replaceVariable("$f $fx ${f} $f", "f", "a"); got != "a $fx a a" {
		t.Errorf("replaceVariable = %q", got)
	}
}
