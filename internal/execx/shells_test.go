package execx

import (
	"context"
	"errors"
	"os/exec"
	"reflect"
	"strings"
	"testing"
	"time"
)

// Each shell is handed a line its own way. `-c` to cmd.exe was the bug: it starts an empty
// interactive shell and the line never runs.
func TestShellArgsSpeaksEachShellsLanguage(t *testing.T) {
	cases := []struct {
		shell string
		want  []string
	}{
		{"/bin/sh", []string{"-c", "ls"}},
		{"/system/bin/sh", []string{"-c", "ls"}},
		{"bash", []string{"-c", "ls"}},
		{`C:\Program Files\PowerShell\7\pwsh.exe`, []string{"-NoLogo", "-NoProfile", "-NonInteractive", "-Command", "ls"}},
		{`C:\Windows\System32\WindowsPowerShell\v1.0\PowerShell.EXE`,
			[]string{"-NoLogo", "-NoProfile", "-NonInteractive", "-Command", "ls"}},
		{"pwsh", []string{"-NoLogo", "-NoProfile", "-NonInteractive", "-Command", "ls"}},
		{`C:\Windows\system32\cmd.exe`, []string{"/d", "/s", "/c", "ls"}},
	}
	for _, c := range cases {
		if got := ShellArgs(c.shell, "ls"); !reflect.DeepEqual(got, c.want) {
			t.Errorf("ShellArgs(%q) = %q, want %q", c.shell, got, c.want)
		}
	}
}

func TestIsPowerShellKnowsBothEditions(t *testing.T) {
	for shell, want := range map[string]bool{
		"pwsh": true, "powershell.exe": true, `D:\x\PWSH.exe`: true,
		"/bin/sh": false, "cmd.exe": false, "": false, "/usr/bin/pwsh-preview": false,
	} {
		if got := IsPowerShell(shell); got != want {
			t.Errorf("IsPowerShell(%q) = %v, want %v", shell, got, want)
		}
	}
}

// A quoted word must come back as the same text, in the shell that reads it.
func TestQuoteKeepsTheWordWhole(t *testing.T) {
	cases := []struct{ shell, in, want string }{
		{"/bin/sh", "it's", `'it'\''s'`},
		{"/bin/sh", "plain", `'plain'`},
		{"pwsh.exe", "it's", `'it''s'`},
		{"powershell", "it\u2019s $x", "'it\u2019\u2019s $x'"},
		{"pwsh", "", "''"},
	}
	for _, c := range cases {
		if got := Quote(c.shell, c.in); got != c.want {
			t.Errorf("Quote(%q, %q) = %q, want %q", c.shell, c.in, got, c.want)
		}
	}
}

// PowerShell 7, then Windows PowerShell, then the command processor.
func TestWindowsShellPrefersPowerShell(t *testing.T) {
	has := func(names ...string) func(string) (string, error) {
		return func(n string) (string, error) {
			for _, m := range names {
				if m == n {
					return `C:\bin\` + n, nil
				}
			}
			return "", errors.New("not found")
		}
	}
	env := func(v string) func(string) string { return func(string) string { return v } }

	if got := WindowsShell(has("pwsh.exe", "powershell.exe"), env("")); got != `C:\bin\pwsh.exe` {
		t.Errorf("with both = %q", got)
	}
	if got := WindowsShell(has("powershell.exe"), env("")); got != `C:\bin\powershell.exe` {
		t.Errorf("with Windows PowerShell = %q", got)
	}
	if got := WindowsShell(has(), env(`C:\Windows\system32\cmd.exe`)); got != `C:\Windows\system32\cmd.exe` {
		t.Errorf("with ComSpec = %q", got)
	}
	if got := WindowsShell(has(), env("")); got != "cmd.exe" {
		t.Errorf("with nothing = %q", got)
	}
}

func TestIsCmdKnowsTheCommandProcessor(t *testing.T) {
	for shell, want := range map[string]bool{`C:\Windows\system32\CMD.EXE`: true, "cmd": true, "pwsh": false, "/bin/sh": false} {
		if got := IsCmd(shell); got != want {
			t.Errorf("IsCmd(%q) = %v, want %v", shell, got, want)
		}
	}
}

// TestPowerShellRunsTheLineItIsHanded runs a real PowerShell, where there is one: the Windows CI
// job, and the Linux runners, which ship PowerShell 7. The line is handed over with ShellArgs and
// its words quoted with Quote, so a quote, a `$`, a backtick and the typographic quotes in them
// must arrive as the literal text, and the exit code must come back.
func TestPowerShellRunsTheLineItIsHanded(t *testing.T) {
	shell := WindowsShell(func(name string) (string, error) {
		return exec.LookPath(strings.TrimSuffix(name, ".exe"))
	}, func(string) string { return "" })
	if !IsPowerShell(shell) {
		t.Skip("no PowerShell on this machine")
	}
	text := `it's "quoted" $HOME ` + "`n" + ` ; done`
	unicode := "it’s ‘a’ “b”"
	line := "Write-Output " + Quote(shell, text) + "; exit (" + Quote(shell, unicode) + ").Length"

	out, _, exit, err := Run(context.Background(), Request{Command: shell, Args: ShellArgs(shell, line),
		Timeout: 2 * time.Minute})
	if err != nil {
		t.Fatalf("%s: %v (%s)", shell, err, out)
	}
	if got := strings.TrimSpace(out); got != text {
		t.Errorf("output = %q, want %q", got, text)
	}
	if want := len([]rune(unicode)); exit != want {
		t.Errorf("exit = %d, want %d (the length of the quoted word)", exit, want)
	}
}
