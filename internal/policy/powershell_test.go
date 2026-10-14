package policy

import (
	"strings"
	"testing"
)

// Under PowerShell, a line that would run code the POSIX reading calls a plain word is not
// classified: the reader it looks like is not what would run.
func TestPowerShellCodeInAReaderIsNotSilent(t *testing.T) {
	dir := t.TempDir()
	for _, line := range []string{
		`echo x | cat {Remove-Item -Recurse src}`, // a delay-bind script block runs
		`cat "$(Remove-Item src)"`,                // a subexpression in double quotes runs
		`cat @(Remove-Item src)`,
		`cat $env:USERPROFILE`,
		`ls [IO.File]::Delete('x')`,
		"cat a`;rm x",
		`cat a\;rm -r src`, // POSIX: one word; PowerShell: a backslash, then a separator
		`cat "a\" ; rm src ; echo "\"`,
		`cat "it` + "`" + `s"`,
		"cat \u2018a\u2019",
		`cat x #comment`,
		`cat a\ b`,
		`cat a\'b'`,
		`cat \\host\share\f`, // a network path is a connection to another machine
		`cat '\\host\share\f'`,
		`cat //host/share/f`,
		`cat -Path://host/share`,
		`cat x=//host/share`,
		`cat 1://host/share`,
		`cat ://host/share`,
	} {
		d := Mode{Enforce: true, PowerShell: true}.DecideLine(line, dir)
		if d.Verdict != Ask || d.Rule != "shell-line" || !strings.Contains(d.Reason, "PowerShell") {
			t.Errorf("%q: got %s/%s (%s), want ask/shell-line", line, d.Verdict, d.Rule, d.Reason)
		}
		if d := (Mode{Enforce: true, Strict: true, PowerShell: true}).DecideLine(line, dir); d.Verdict != Deny {
			t.Errorf("%q in strict mode: got %s, want deny", line, d.Verdict)
		}
		if d := (Mode{PowerShell: true}).DecideLine(line, dir); d.Verdict != Allow {
			t.Errorf("%q with the policy off: got %s, want allow", line, d.Verdict)
		}
	}
}

// What PowerShell and POSIX read the same way is judged as before: a read is silent, wherever
// the file is, and a Windows path in single quotes or with path characters after its
// backslashes is a plain word.
func TestPowerShellReadsStaySilent(t *testing.T) {
	dir := t.TempDir()
	for _, line := range []string{
		`cat README.md`,
		`ls src\internal\x_y-z.go`,
		`cat 'C:\Users\me\$x {y}.txt'`,
		`git log --oneline -n 5`,
		`cat "plain text, it's fine"`,
		`cat "a\b"`,
		`grep -n main src | sort`,
		`cat end\`,
		`echo https://example.com/a`, // the // of a URL is not a network path
		`echo -Uri=git+ssh://host/x`,
		`echo 'a/b'`,
		`Get-Content -Path README.md -TotalCount 40`,
		`Get-ChildItem -Path src -Recurse -Filter *.go`,
		`Select-String -Path *.go -Pattern main`,
	} {
		d := Mode{Enforce: true, PowerShell: true}.DecideLine(line, dir)
		if d.Verdict != Allow || d.Rule != "reader" {
			t.Errorf("%q: got %s/%s (%s), want allow/reader", line, d.Verdict, d.Rule, d.Reason)
		}
	}
}

// A write is judged on its path, and the policy reads paths with POSIX rules, so under
// PowerShell a write the POSIX reading would allow is confirmed.
func TestPowerShellConfirmsWhatOnlyAPathCheckWouldAllow(t *testing.T) {
	dir := t.TempDir()
	for _, line := range []string{"mkdir build", "ls > out.txt", "rm -r C:Users", "cat a && touch b"} {
		if d := (Mode{Enforce: true}).DecideLine(line, dir); d.Verdict != Allow {
			t.Fatalf("%q without PowerShell: got %s (%s), the premise is gone", line, d.Verdict, d.Reason)
		}
		d := Mode{Enforce: true, PowerShell: true}.DecideLine(line, dir)
		if d.Verdict != Ask || !strings.Contains(d.Reason, "under PowerShell") {
			t.Errorf("%q: got %s (%s), want ask", line, d.Verdict, d.Reason)
		}
	}
	// What is asked already keeps its own reason, and the floor is still the floor.
	if d := (Mode{Enforce: true, PowerShell: true}).DecideLine("curl -s https://x", dir); d.Verdict != Ask ||
		strings.Contains(d.Reason, "under PowerShell") {
		t.Errorf("curl: %+v", d)
	}
	if d := (Mode{Enforce: true, PowerShell: true}).DecideLine("rm -rf /", dir); !d.Mandatory {
		t.Errorf("the floor: %+v", d)
	}
}

// A request from PowerShell is asked about as what it is: an effect outside the machine.
func TestAWindowsRequestIsAnExternalEffect(t *testing.T) {
	for _, line := range []string{
		"Invoke-RestMethod -Uri https://api.example.com/x -TimeoutSec 20",
		"Invoke-WebRequest -Uri https://api.example.com/x -OutFile a.json",
		"irm https://x", "iwr https://x", "curl.exe -s -f https://x",
	} {
		d := Mode{Enforce: true, PowerShell: true}.DecideLine(line, t.TempDir())
		if d.Verdict != Ask || d.Rule != categoryExternalEffect {
			t.Errorf("%q: got %s/%s, want ask/%s", line, d.Verdict, d.Rule, categoryExternalEffect)
		}
	}
}
