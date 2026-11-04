package localmodel

import (
	"os/exec"
	"syscall"
)

// setProcAttr ends the server with Motita: the kernel sends it SIGTERM when Motita dies, so a
// crash never leaves a model holding the GPU.
func setProcAttr(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Pdeathsig: syscall.SIGTERM}
}

// terminate asks the server to stop.
func terminate(cmd *exec.Cmd) { _ = cmd.Process.Signal(syscall.SIGTERM) }
