//go:build !unix

package execx

import (
	"errors"
	"os"
	"os/exec"
)

// configureGroup does nothing outside Unix: there are no POSIX process groups.
func configureGroup(cmd *exec.Cmd) {}

// killGroup without SysProcAttr can only kill the direct process.
func killGroup(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return os.ErrProcessDone
	}
	return cmd.Process.Kill()
}

func asExitError(err error, target **exec.ExitError) bool {
	return errors.As(err, target)
}
