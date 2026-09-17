//go:build unix

package execx

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
)

// configureGroup isolates the command in its own process group. Without this,
// killing only `sh` leaves its children alive, and they keep the pipe open and
// block Wait until they finish on their own.
func configureGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// killGroup sends SIGKILL to the whole group (negative pid).
func killGroup(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return os.ErrProcessDone
	}
	if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); err != nil {
		return os.ErrProcessDone
	}
	return nil
}

// asExitError avoids depending on errors.As at every call site.
func asExitError(err error, target **exec.ExitError) bool {
	return errors.As(err, target)
}
