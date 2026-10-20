//go:build unix

package sandbox

import (
	"os"
	"os/exec"
	"syscall"
)

// killGroup sends SIGKILL to the whole process group (negative pid).
//
// It is what makes a deadline come true: if only the direct child died, its
// descendants would stay alive with the output pipe open and Wait would keep
// waiting.
func killGroup(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return os.ErrProcessDone
	}
	if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); err != nil {
		return os.ErrProcessDone
	}
	return nil
}

// killLeftovers ends what the child left running in its group after it exited.
func killLeftovers(cmd *exec.Cmd) { _ = killGroup(cmd) }
