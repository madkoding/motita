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
	// Confined, the same lines run as before.
	for _, line := range []string{"python t.py", "go test ./...", script + " a"} {
		if d := (Mode{Enforce: true}).DecideLine(line, dir); d.Verdict != Allow {
			t.Errorf("confined %q: got %s/%s, want allow", line, d.Verdict, d.Rule)
		}
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
