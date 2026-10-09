package agent

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/madkoding/motita/internal/config"
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
- To install a toolchain, unpack it to %s/<name>/ (its bin/ is on PATH automatically from the next command on), or put single binaries in %s; the user approves that command, an unapproved one cannot write there. Never install into the repository or system directories, never use sudo, and do not re-export PATH: it is already set.
- Read the "installing-a-toolchain" procedure before installing one.
`, filepath.Join(tools, "home"), tools, filepath.Join(tools, "tools"), filepath.Join(tools, "tools"), filepath.Join(tools, "bin"))
}
