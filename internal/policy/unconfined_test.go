package policy

import (
	"os"
	"path/filepath"
	"testing"
)

// Without confinement a reader that names a path outside the workspace, or among the user's keys,
// is confirmed: nothing else stops `Get-Content ~/.motita/auth/x`.
func TestUnconfinedReadsOutsideTheWorkspaceAreConfirmed(t *testing.T) {
	dir := t.TempDir()
	ps := Mode{Enforce: true, PowerShell: true, Unconfined: true}
	for _, line := range []string{
		`Get-Content ~/.motita/auth/x`,
		`Get-Content ..\..\Users\u\.ssh\id_rsa`,
		`Select-String -Path ~/.ssh/* -Pattern x`,
		`Get-Content -Path:C:/Users/u/.ssh/id_rsa`,
		`Get-Content C:\Users\u\x`,
		`cat /Windows/win.ini`,
		`cat .ssh\id_rsa`,
		`cat home\.motita\auth\x`,
		`cp ..\..\secret .`,
		`git log --output=..\..\x`,
		`grep x < ..\..\x`,
		`grep -f../../.ssh/id_rsa .`,
		`grep -f~/.ssh/id_rsa .`,
		`grep -f/home/u/.ssh/id_rsa .`,
	} {
		d := ps.DecideLine(line, dir)
		if d.Verdict != Ask {
			t.Errorf("%q: got %s/%s (%s), want ask", line, d.Verdict, d.Rule, d.Reason)
		}
	}
	sh := Mode{Enforce: true, Unconfined: true}
	for _, line := range []string{
		"cat ~/.ssh/id_rsa", "cat ../x", "grep -r x /etc", "cat < ../x", "cat .aws/credentials",
		"cat x/.motita/motita.env", "head -n 1 --file=../x",
	} {
		if d := sh.DecideLine(line, dir); d.Verdict != Ask || d.Rule != "unconfined-read" {
			t.Errorf("%q: got %s/%s (%s), want ask/unconfined-read", line, d.Verdict, d.Rule, d.Reason)
		}
	}
	// With the policy off nobody is asked, as everywhere else.
	if d := (Mode{Unconfined: true}).DecideLine("cat ../x", dir); d.Verdict != Allow {
		t.Errorf("policy off: %+v", d)
	}
}

// Reading the workspace is the work, confined or not.
func TestUnconfinedReadsInsideTheWorkspaceStaySilent(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".motita"), 0o755); err != nil {
		t.Fatal(err)
	}
	ps := Mode{Enforce: true, PowerShell: true, Unconfined: true}
	for _, line := range []string{
		`Get-Content -Path README.md -TotalCount 40`,
		`Get-ChildItem -Path src -Recurse -Filter *.go`,
		`Select-String -Path *.go -Pattern main`,
		`cat src\a.go`,
		`echo https://example.com/a`,
		`cat .motita\anchor`,
		`grep x < in.txt`,
		`git log --format=%H -n 5 -- -`,
		`ls > out.txt`,
	} {
		if d := ps.DecideLine(line, dir); d.Verdict != Allow {
			t.Errorf("%q: got %s/%s (%s), want allow", line, d.Verdict, d.Rule, d.Reason)
		}
	}
}

// Without confinement the project's own code runs only once a person says so: a script, a build
// or a test can read anything the user can.
func TestUnconfinedProjectCodeIsConfirmed(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "x.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	m := Mode{Enforce: true, Unconfined: true}
	for _, line := range []string{"python t.py", "go test ./...", "make", script + " a", "npm ci"} {
		if d := m.DecideLine(line, dir); d.Verdict != Ask || d.Rule != "unconfined-run" {
			t.Errorf("%q: got %s/%s (%s), want ask/unconfined-run", line, d.Verdict, d.Rule, d.Reason)
		}
	}
	// A shell handed the script is the same run as the script itself.
	if err := os.WriteFile(filepath.Join(dir, "evil.sh"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, line := range []string{"bash evil.sh", "sh -eu evil.sh"} {
		if d := m.DecideLine(line, dir); d.Verdict != Ask || d.Rule != "unconfined-run" {
			t.Errorf("%q: got %s/%s (%s), want ask/unconfined-run", line, d.Verdict, d.Rule, d.Reason)
		}
		if d := (Mode{Enforce: true}).DecideLine(line, dir); d.Verdict != Allow || d.Rule != "project-script" {
			t.Errorf("confined %q: got %s/%s, want allow/project-script", line, d.Verdict, d.Rule)
		}
	}
	// Confined, the same lines run as before.
	for _, line := range []string{"python t.py", "go test ./...", script + " a"} {
		if d := (Mode{Enforce: true}).DecideLine(line, dir); d.Verdict != Allow {
			t.Errorf("confined %q: got %s/%s, want allow", line, d.Verdict, d.Rule)
		}
	}
}

// The operator may trust the project's code without confinement; reads outside are still asked.
func TestTrustedUnconfinedRunsStaySilent(t *testing.T) {
	dir := t.TempDir()
	m := Mode{Enforce: true, Unconfined: true, TrustUnconfinedRuns: true}
	if d := m.DecideLine("python t.py", dir); d.Verdict != Allow || d.Rule != "local-tool" {
		t.Errorf("python t.py: got %s/%s, want allow/local-tool", d.Verdict, d.Rule)
	}
	if d := m.DecideLine("python ../t.py", dir); d.Verdict != Ask {
		t.Errorf("a script outside: got %s/%s, want ask", d.Verdict, d.Rule)
	}
	if d := m.DecideLine("cat ~/.ssh/id_rsa", dir); d.Verdict != Ask || d.Rule != "unconfined-read" {
		t.Errorf("a read outside: got %s/%s, want ask/unconfined-read", d.Verdict, d.Rule)
	}
	cmd := Mode{Enforce: true, Cmd: true, TrustUnconfinedRuns: true}
	if d := cmd.DecideLine(`type %X%`, dir); d.Rule != "shell-line" {
		t.Errorf("a cmd.exe line: got %s/%s, want shell-line", d.Verdict, d.Rule)
	}
}

func TestPathOperandAndPrivatePath(t *testing.T) {
	for w, want := range map[string]string{
		"x": "x", "--output=y": "y", "-Path:C:/z": "C:/z", "-r": "", "--x=": "", `'a b'`: "a b",
	} {
		got, ok := pathOperand(w)
		if got != want || ok != (want != "") {
			t.Errorf("pathOperand(%q) = %q, %v", w, got, ok)
		}
	}
	for p, want := range map[string]bool{
		"a/.SSH/x": true, `C:\Users\u\.motita\auth`: true, ".motita/anchor": false, ".motita": false,
		"src/a.go": false, ".netrc": true,
	} {
		if got := privatePath(p); got != want {
			t.Errorf("privatePath(%q) = %v, want %v", p, got, want)
		}
	}
}

// A trace variable that names a file is a write outside the workspace, in every spelling of the line.
func TestGitTraceToAFileIsAsked(t *testing.T) {
	dir := t.TempDir()
	m := Mode{Enforce: true}
	for _, line := range []string{
		"GIT_TRACE=/tmp/x git status", "GIT_TRACE=/tmp/x", "export GIT_TRACE=/tmp/x", "env GIT_TRACE=/tmp/x git status",
	} {
		if d := m.DecideLine(line, dir); d.Verdict == Allow {
			t.Errorf("%q: got %s/%s, want a question", line, d.Verdict, d.Rule)
		}
	}
	if d := m.DecideLine("GIT_TRACE=1 git status", dir); d.Verdict != Allow {
		t.Errorf("a switch stays silent: got %s/%s", d.Verdict, d.Rule)
	}
}

// An option's value is not the subcommand: `kubectl -n get delete` deletes, and an option nobody
// listed may take a value too, so the line is asked about instead of read as its first word.
func TestOptionValuesAreNotTheSubcommand(t *testing.T) {
	dir := t.TempDir()
	m := Mode{Enforce: true}
	for _, line := range []string{
		"docker --log-level info run -v /:/h x", "docker -l info run x", "kubectl -n get delete pod x",
		"npm --prefix bin exec -c x", "go --unknown ps test",
	} {
		if d := m.DecideLine(line, dir); d.Verdict == Allow {
			t.Errorf("%q: got %s/%s, want a question", line, d.Verdict, d.Rule)
		}
	}
	for _, line := range []string{
		"kubectl -n ns get pods", "go -C sub test ./...", "docker --log-level=info ps", "npm --silent test",
		"npm --prefix web test", "go test -v ./...", "docker ps",
	} {
		if d := m.DecideLine(line, dir); d.Verdict != Allow {
			t.Errorf("%q: got %s/%s, want allow", line, d.Verdict, d.Rule)
		}
	}
}
