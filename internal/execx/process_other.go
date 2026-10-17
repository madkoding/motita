//go:build !unix

package execx

import (
	"errors"
	"os"
	"os/exec"
	"strconv"
)

// configureGroup does nothing outside Unix: there are no POSIX process groups.
func configureGroup(cmd *exec.Cmd) {
	// Intentionally empty: see the comment above.
}

// killGroup ends the command and what it started. Windows has no process group to signal, and
// Process.Kill ends only the direct process: the program a shell started kept running with no
// limit. taskkill /T walks the tree from the command's pid; Process.Kill is the fallback.
//
// It is only called while the command runs (cmd.Cancel). After Wait the pid may already belong
// to another process, and a tree kill there would end that one.
func killGroup(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return os.ErrProcessDone
	}
	if exec.Command("taskkill", "/T", "/F", "/PID", strconv.Itoa(cmd.Process.Pid)).Run() == nil {
		return nil
	}
	return cmd.Process.Kill()
}

// killLeftovers does nothing outside Unix: once the command exited there is no group left to
// find what it started.
func killLeftovers(*exec.Cmd) {}

func asExitError(err error, target **exec.ExitError) bool {
	return errors.As(err, target)
}
