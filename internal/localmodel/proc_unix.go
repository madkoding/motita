//go:build !linux && !windows

package localmodel

import (
	"os/exec"
	"syscall"
)

// setProcAttr has nothing to set here: Motita stops the server on its own shutdown.
func setProcAttr(*exec.Cmd) {
	// Nothing to set on this system.
}

// terminate asks the server to stop.
func terminate(cmd *exec.Cmd) { _ = cmd.Process.Signal(syscall.SIGTERM) }
