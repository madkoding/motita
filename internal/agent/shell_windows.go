//go:build windows

package agent

import (
	"os"
	"os/exec"

	"github.com/madkoding/motita/internal/execx"
)

// defaultShell is the interpreter used to run an action written by the model when the
// agent is not in read-only mode. Windows has no sh: PowerShell is the shell the Windows
// procedures are written for (see execx.WindowsShell).
func defaultShell() string { return execx.WindowsShell(exec.LookPath, os.Getenv) }
