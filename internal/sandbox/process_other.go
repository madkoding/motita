//go:build !unix

package sandbox

import (
	"os"
	"os/exec"
)

// killGroup without POSIX groups can only kill the direct process.
func killGroup(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return os.ErrProcessDone
	}
	return cmd.Process.Kill()
}

// killLeftovers does nothing outside Unix: once the child exited there is no group left to find
// what it started.
func killLeftovers(*exec.Cmd) {}
