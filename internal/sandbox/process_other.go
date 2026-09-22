//go:build !unix

package sandbox

import (
	"os"
	"os/exec"
	"syscall"
)

// killGroup without POSIX groups can only kill the direct process.
func killGroup(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return os.ErrProcessDone
	}
	return cmd.Process.Kill()
}

// ownProcessGroup: there are no POSIX groups to create here.
func ownProcessGroup(attr *syscall.SysProcAttr) *syscall.SysProcAttr { return attr }
