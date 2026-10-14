package doctor

import (
	"os"
	"os/exec"

	"github.com/madkoding/motita/internal/execx"
)

// shellPath is the interpreter the agent runs actions through on Windows (see
// internal/agent): PowerShell, or the command processor where it lives by default on a
// machine without it.
func shellPath() string {
	return execx.WindowsShell(exec.LookPath, func(name string) string {
		if s := os.Getenv(name); s != "" {
			return s
		}
		return `C:\Windows\System32\cmd.exe`
	})
}
