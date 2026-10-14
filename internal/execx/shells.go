package execx

import "strings"

// The agent runs a line through a shell, and the shells disagree on how to be handed one:
// `sh -c line`, `powershell -Command line`, `cmd /d /s /c line`. They also disagree on how a
// word is quoted. Both answers live here, beside the execution primitive, so every caller that
// runs a line (the agent's actions, its final action, its escalation) asks the same function
// and none of them hard-codes `-c` — which is what made every action fail on Windows, where
// `cmd.exe -c line` starts an empty interactive shell instead of running the line.
//
// The file has no build tag on purpose: which shell a Windows machine gets is decided by
// functions the Linux test run can exercise, so the per-package coverage gate covers them.

// shellName is the program name of a shell path, lower-cased and without `.exe`, for either
// separator: `C:\Program Files\PowerShell\7\pwsh.exe` is `pwsh` on every platform.
func shellName(shell string) string {
	name := shell[strings.LastIndexAny(shell, `/\`)+1:]
	return strings.TrimSuffix(strings.ToLower(name), ".exe")
}

// IsPowerShell reports whether shell is Windows PowerShell or PowerShell 7.
func IsPowerShell(shell string) bool {
	switch shellName(shell) {
	case "powershell", "pwsh":
		return true
	default:
		return false
	}
}

// IsCmd reports whether shell is the Windows command processor.
func IsCmd(shell string) bool { return shellName(shell) == "cmd" }

// ShellArgs returns the arguments that make shell run line as a single command line.
//
// PowerShell is started without a profile (a user's profile is code the agent did not
// choose to run) and non-interactive (a prompt would hang the command until its timeout).
func ShellArgs(shell, line string) []string {
	switch {
	case IsPowerShell(shell):
		return []string{"-NoLogo", "-NoProfile", "-NonInteractive", "-Command", line}
	case IsCmd(shell):
		return []string{"/d", "/s", "/c", line}
	default:
		return []string{"-c", line}
	}
}

// Quote returns s as one literal word for shell.
//
// PowerShell's single quote is escaped by doubling it, and it also treats the typographic
// single quotes as quotes, so those are doubled too: a commit message with a curly apostrophe
// must not end the word. Every other shell gets the POSIX form.
func Quote(shell, s string) string {
	if IsPowerShell(shell) {
		var b strings.Builder
		b.WriteByte('\'')
		for _, r := range s {
			if strings.ContainsRune("'\u2018\u2019\u201a\u201b", r) {
				b.WriteRune(r)
			}
			b.WriteRune(r)
		}
		b.WriteByte('\'')
		return b.String()
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// WindowsShell picks the shell for Windows: PowerShell 7 when it is installed, Windows
// PowerShell (which every supported Windows ships) otherwise, and the command processor only
// on a machine that has neither.
//
// PowerShell rather than cmd.exe because it is the shell the procedures for Windows are written
// for: it reads files, walks trees and calls HTTP APIs with commands of its own, where cmd.exe
// would need programs a Windows machine does not carry.
func WindowsShell(lookPath func(string) (string, error), getenv func(string) string) string {
	for _, name := range []string{"pwsh.exe", "powershell.exe"} {
		if path, err := lookPath(name); err == nil {
			return path
		}
	}
	if s := getenv("ComSpec"); s != "" {
		return s
	}
	return "cmd.exe"
}
