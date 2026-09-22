package policy

import (
	"os"
	"path/filepath"
	"testing"
)

// The tests here hold the three rules that decide what runs in SILENCE in the default policy.
//
// They exist because the default changed: a program nobody has classified used to run without
// a word, and now it is asked about. The whole design only stays livable because of these
// three ways of earning silence, so each one is pinned with the case that it is FOR and the
// case that it must NOT cover.

// --- localToolDecision -------------------------------------------------------

// TestAKnownBuildToolRunsSilently: the ordinary work of a project. If these ever start asking,
// the operator turns the policy off and it stops protecting anything.
func TestAKnownBuildToolRunsSilently(t *testing.T) {
	dir := t.TempDir()
	for _, line := range []string{
		"make", "make -j4 check", "gcc -o a a.c", "go build ./...", "cargo build",
		"pytest -q", "gofmt -l .", "shellcheck build.sh", "gradle test",
	} {
		d := Default().DecideLine(line, dir)
		if d.Verdict != Allow || d.Rule != "local-tool" {
			t.Errorf("%q must be silent local work, got %s (rule %s): %s", line, d.Verdict, d.Rule, d.Reason)
		}
	}
}

// TestAnInterpretersScriptDecides: a local interpreter is silent when it runs a script from
// inside the workspace, and a question when the script is somewhere else. The location of the
// program being run is the fact the policy can actually see.
func TestAnInterpretersScriptDecides(t *testing.T) {
	dir := t.TempDir()
	inside := filepath.Join(dir, "build.py")
	if err := os.WriteFile(inside, []byte("print('hi')\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	for _, line := range []string{"python3 build.py", "python3 ./build.py", "node app.js", "ruby task.rb"} {
		d := Default().DecideLine(line, dir)
		if d.Verdict != Allow {
			t.Errorf("%q runs a script inside the workspace and must be silent, got %s: %s",
				line, d.Verdict, d.Reason)
		}
	}

	outside := Default().DecideLine("python3 /opt/otro/script.py", dir)
	if outside.Verdict != Ask || outside.Rule != "script-outside-workspace" {
		t.Errorf("a script outside the workspace must be asked about, got %s (rule %s)",
			outside.Verdict, outside.Rule)
	}
}

// TestAnInterpreterWithNoScriptIsStillALocalTool: the script check only applies when there IS
// a script. `python3 --version` and `python3 -V` name no file, so there is no location to
// judge and the interpreter is left as the local tool it is.
func TestAnInterpreterWithNoScriptIsStillALocalTool(t *testing.T) {
	dir := t.TempDir()
	for _, line := range []string{"python3 --version", "node --version", "python3 -V", "python3"} {
		d := Default().DecideLine(line, dir)
		if d.Verdict != Allow || d.Rule != "local-tool" {
			t.Errorf("%q names no script and must stay silent, got %s (rule %s): %s",
				line, d.Verdict, d.Rule, d.Reason)
		}
	}
	// And the extractor itself: a non-interpreter has no script operand to find.
	if got := scriptOperands("make", []string{"check"}); got != nil {
		t.Errorf("make is not an interpreter, so it has no script operand: %q", got)
	}
	if got := scriptOperands("python3", []string{"-V"}); got != nil {
		t.Errorf("a flags-only invocation has no script operand: %q", got)
	}
}

// TestAnInlineProgramIsNotALocalTool: `python3 -c '...'` carries the program as TEXT, so there
// is no file to resolve and nothing the policy can see. It is exactly as opaque as a shell
// line, and it must not earn silence just because the interpreter is a known tool.
func TestAnInlineProgramIsNotALocalTool(t *testing.T) {
	dir := t.TempDir()
	for _, line := range []string{
		"python3 -c 'import os'",
		"node -e 'require(fs)'",
		"perl -e 'print 1'",
		"python3 -m http.server",
	} {
		if d := Default().DecideLine(line, dir); d.Verdict != Ask {
			t.Errorf("%q is opaque and must be asked about, got %s (rule %s): %s",
				line, d.Verdict, d.Rule, d.Reason)
		}
	}
	// The same flag means something else elsewhere: `gcc -c` is compile-only, not an inline
	// program, and it must stay silent.
	if d := Default().DecideLine("gcc -c a.c", dir); d.Verdict != Allow {
		t.Errorf("gcc -c is ordinary compilation and must stay silent, got %s: %s", d.Verdict, d.Reason)
	}
}

// TestABareNameIsNotAWorkspaceProgram: `mytool` is looked up on the PATH, so its location is
// not knowable from the line — only a PATH-named program (./x, /abs/x) can be placed.
func TestABareNameIsNotAWorkspaceProgram(t *testing.T) {
	dir := t.TempDir()
	if d := Default().DecideLine("mytool --check", dir); d.Verdict != Ask {
		t.Errorf("a bare unknown name must be asked about, got %s: %s", d.Verdict, d.Reason)
	}
}

// TestTheWorkspaceItselfCannotBeReachedThroughATilde: a `~` path is outside by construction,
// so a program named that way is never mistaken for the project's own tooling.
func TestTheWorkspaceItselfCannotBeReachedThroughATilde(t *testing.T) {
	dir := t.TempDir()
	if isInsideWorkspace("~/bin/script.sh", dir) {
		t.Error("a tilde path is never inside the workspace")
	}
	if isInsideWorkspace("./script.sh", "") {
		t.Error("with no workspace there is nothing to be inside of")
	}
	if d := Default().DecideLine("~/bin/mytool", dir); d.Verdict != Ask {
		t.Errorf("a tilde program must be asked about, got %s: %s", d.Verdict, d.Reason)
	}
}

// --- projectLocalDecision ----------------------------------------------------

// TestAProgramInsideTheWorkspaceIsTheProjectsOwn: `./scripts/deploy.sh` is the project's
// tooling doing the project's work, which is the same judgement the writers get.
func TestAProgramInsideTheWorkspaceIsTheProjectsOwn(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "scripts", "deploy.sh")
	if err := os.MkdirAll(filepath.Dir(script), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(script, []byte("#!/bin/sh\necho ok\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	for _, line := range []string{"./scripts/deploy.sh", "./scripts/deploy.sh --env prod", script} {
		d := Default().DecideLine(line, dir)
		if d.Verdict != Allow || d.Rule != "project-local" {
			t.Errorf("%q is the project's own program and must be silent, got %s (rule %s): %s",
				line, d.Verdict, d.Rule, d.Reason)
		}
	}
}

// TestAProjectProgramHandedAnOutsidePathIsAskedAbout: the program's location is not a licence
// for its arguments. This is the case that stops `./build.sh /etc/passwd` riding in on the
// script being local.
func TestAProjectProgramHandedAnOutsidePathIsAskedAbout(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "build.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\ntrue\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	d := Default().DecideLine("./build.sh /etc/passwd", dir)
	if d.Verdict != Ask || d.Rule != "project-script-arg-outside" {
		t.Errorf("a project program writing outside must be asked about, got %s (rule %s): %s",
			d.Verdict, d.Rule, d.Reason)
	}
}

// TestAProgramOutsideTheWorkspaceIsNotProjectLocal: the path resolves outside, so it is not
// the project's own tooling and the unclassified rule has to decide it.
func TestAProgramOutsideTheWorkspaceIsNotProjectLocal(t *testing.T) {
	dir := t.TempDir()
	if _, hit := projectLocalDecision("/usr/local/bin/something", nil, dir); hit {
		t.Error("a program outside the workspace is not project-local")
	}
	if _, hit := projectLocalDecision("./x", nil, ""); hit {
		t.Error("with no workspace there is nothing to be inside of")
	}
	if _, hit := projectLocalDecision("./x /tmp/other", nil, dir); !hit {
		t.Error("an outside argument must be caught even when the program itself is missing")
	}
}
