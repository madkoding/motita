package policy

import (
	"strings"
	"testing"
)

// Under cmd.exe a line holding anything the POSIX reading may misread is approved as a whole: an
// expansion, an escape, a path separator, a drive, a word divider of cmd's own.
func TestCmdLinesWithItsSyntaxAreShellLines(t *testing.T) {
	dir := t.TempDir()
	for _, line := range []string{
		`echo x > %USERPROFILE%\evil.bat`,
		`type C:\Users\u\.motita\auth\x`,
		`type !X!`,
		`echo a^&del x`,
		`type a,b`,
		`dir /s`,
		`type "a b"`,
		`git log --output=x`,
		"type \u00e9",
	} {
		d := Mode{Enforce: true, Cmd: true}.DecideLine(line, dir)
		if d.Verdict != Ask || d.Rule != "shell-line" || !strings.Contains(d.Reason, "cmd.exe") {
			t.Errorf("%q: got %s/%s (%s), want ask/shell-line", line, d.Verdict, d.Rule, d.Reason)
		}
		if d := (Mode{Enforce: true, Strict: true, Cmd: true}).DecideLine(line, dir); d.Verdict != Deny {
			t.Errorf("%q in strict mode: got %s, want deny", line, d.Verdict)
		}
		if d := (Mode{Cmd: true}).DecideLine(line, dir); d.Verdict != Allow {
			t.Errorf("%q with the policy off: got %s, want allow", line, d.Verdict)
		}
	}
	// The floor is applied before anything else.
	if d := (Mode{Enforce: true, Cmd: true}).DecideLine("rm -rf /", dir); !d.Mandatory {
		t.Errorf("the floor: %+v", d)
	}
}

// A line of plain words reads the same to both, and is judged as any other.
func TestCmdPlainLinesAreJudged(t *testing.T) {
	dir := t.TempDir()
	m := Mode{Enforce: true, Cmd: true}
	for _, line := range []string{"type README.md", "git status", "echo hello world", "dir"} {
		if d := m.DecideLine(line, dir); d.Verdict != Allow {
			t.Errorf("%q: got %s/%s (%s), want allow", line, d.Verdict, d.Rule, d.Reason)
		}
	}
	unconfined := Mode{Enforce: true, Cmd: true, Unconfined: true}
	for _, line := range []string{"type ..", "python t.py"} {
		if d := unconfined.DecideLine(line, dir); d.Verdict != Ask {
			t.Errorf("unconfined %q: got %s/%s, want ask", line, d.Verdict, d.Rule)
		}
	}
}
