package anchor

import (
	"os"
	"os/exec"

	"github.com/madkoding/motita/internal/execx"
)

// declaredShell runs the lines of .motita/anchor. Windows has no sh: the lines run under the
// same shell as the agent's own commands (see execx.WindowsShell).
var declaredShell = execx.WindowsShell(exec.LookPath, os.Getenv)
