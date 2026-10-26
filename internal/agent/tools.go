package agent

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/madkoding/motita/internal/config"
	"github.com/madkoding/motita/internal/execx"
)

// describeTools tells the model where its HOME is and where installed tools persist, or nothing
// when the sandbox keeps the old layout (no tools directory, or a chroot that cannot see it).
// home is the sandbox's own HOME, a project's when the session belongs to one; empty is the
// shared one.
//
// Reported from a real session: the run did not know any of this, installed Go, Node and gh into
// the repository through HOME, then spent rounds moving them out and re-exporting PATH in every
// command. Knowing the layout is what lets the first install be the last one.
func describeTools(sb config.Sandbox, home string) string {
	tools := strings.TrimSpace(sb.ToolsDir)
	if tools == "" || strings.EqualFold(sb.Kind, "chroot") {
		return ""
	}
	if home == "" {
		home = filepath.Join(tools, "home")
	}
	return fmt.Sprintf(`
## TOOLS AND HOME
- HOME is %s, outside the repository. Tools you install persist across rounds AND sessions in %s.
- Before installing anything, check it is not already there: command -v <tool>, ls %s.
- To install a toolchain, unpack it to %s/<name>/ (its bin/ is on PATH automatically from the next command on), `+
		`or put single binaries in %s; the user approves that command, an unapproved one cannot write there. `+
		`Never install into the repository or system directories, use sudo only when it truly needs system privileges (the user is asked first and a command has no terminal for a password), and do not re-export PATH: `+
		`it is already set.
- Read the "installing-a-toolchain" procedure before installing one.
- Temporary files: /tmp is NOT writable from a confined command. Use $TMPDIR (private to one command and deleted
  when it ends, so no background job or later command may depend on it); to keep a file between commands put it
  in $MOTITA_SCRATCH (%s, kept a day) or in the working directory.
`, home, tools, filepath.Join(tools, "tools"), filepath.Join(tools, "tools"),
		filepath.Join(tools, "bin"), filepath.Join(home, "scratch"))
}

// powerShellSection is the prompt section of a run whose commands PowerShell runs.
const powerShellSection = `
## SHELL
- Commands run under PowerShell on Windows, not sh. Write PowerShell: Get-ChildItem, Get-Content,
  Select-String, Invoke-RestMethod; $null instead of /dev/null; paths in single quotes.
- Script blocks ({ }), $variables, $(...) and backticks make a line unreadable to the guardrails, so it
  is confirmed as a whole: prefer plain cmdlets with literal arguments. Write with relative paths inside the
  workspace; a drive (C:\), a rooted path or ~ is confirmed.
- Read the "files-and-directories" and "calling-an-http-api" procedures: they are written for this shell.
`

// cmdSection is the prompt section of a run whose commands the Windows command processor runs.
const cmdSection = `
## SHELL
- Commands run under the Windows command processor (cmd.exe), not sh: dir, type, findstr, where.
`

// describeShell tells the model which shell runs its commands when that shell is not POSIX.
//
// The procedures are looked up by name and the same names serve every platform, but a
// command written for sh fails under PowerShell (`ls -la`, `head -n`, `2>/dev/null`), and the
// model cannot tell from the work which one it got. A POSIX shell says nothing: it is what
// every prompt and procedure already assumes.
func describeShell(shell string) string {
	switch {
	case execx.IsPowerShell(shell):
		return powerShellSection
	case execx.IsCmd(shell):
		return cmdSection
	default:
		return ""
	}
}
