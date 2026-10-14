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
//
// Reported from a real session: the run did not know any of this, installed Go, Node and gh into
// the repository through HOME, then spent rounds moving them out and re-exporting PATH in every
// command. Knowing the layout is what lets the first install be the last one.
func describeTools(sb config.Sandbox) string {
	tools := strings.TrimSpace(sb.ToolsDir)
	if tools == "" || strings.EqualFold(sb.Kind, "chroot") {
		return ""
	}
	return fmt.Sprintf(`
## TOOLS AND HOME
- HOME is %s, outside the repository. Tools you install persist across rounds AND sessions in %s.
- Before installing anything, check it is not already there: command -v <tool>, ls %s.
- To install a toolchain, unpack it to %s/<name>/ (its bin/ is on PATH automatically from the next command on), `+
		`or put single binaries in %s; the user approves that command, an unapproved one cannot write there. `+
		`Never install into the repository or system directories, never use sudo, and do not re-export PATH: `+
		`it is already set.
- Read the "installing-a-toolchain" procedure before installing one.
`, filepath.Join(tools, "home"), tools, filepath.Join(tools, "tools"), filepath.Join(tools, "tools"),
		filepath.Join(tools, "bin"))
}

// describeShell tells the model which shell runs its commands when that shell is not POSIX.
//
// The procedures are looked up by name and the same names serve every platform, but a
// command written for sh fails under PowerShell (`ls -la`, `head -n`, `2>/dev/null`), and the
// model cannot tell from the work which one it got. A POSIX shell says nothing: it is what
// every prompt and procedure already assumes.
func describeShell(shell string) string {
	switch {
	case execx.IsPowerShell(shell):
		return `
## SHELL
- Commands run under PowerShell on Windows, not sh. Write PowerShell: Get-ChildItem, Get-Content,
  Select-String, Invoke-RestMethod; $null instead of /dev/null; paths in single quotes.
- Script blocks ({ }), $variables, $(...) and backticks make a line unreadable to the guardrails, so it
  is confirmed as a whole: prefer plain cmdlets with literal arguments. Anything but a read is confirmed.
- Read the "files-and-directories" and "calling-an-http-api" procedures: they are written for this shell.
`
	case execx.IsCmd(shell):
		return `
## SHELL
- Commands run under the Windows command processor (cmd.exe), not sh: dir, type, findstr, where.
`
	default:
		return ""
	}
}
