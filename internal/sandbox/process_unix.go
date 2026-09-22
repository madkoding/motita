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

// ownProcessGroup makes the child the leader of a new process group. Without it
// (darwin and the BSDs got no SysProcAttr) killGroup's kill(-pid) found no group,
// nothing was killed on a timeout and the descendants outlived the run.
func ownProcessGroup(attr *syscall.SysProcAttr) *syscall.SysProcAttr {
	if attr == nil {
		attr = &syscall.SysProcAttr{}
	}
	attr.Setpgid = true
	return attr
}
