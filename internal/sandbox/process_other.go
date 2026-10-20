//go:build !unix

package sandbox

import (
	"os"
	"os/exec"
	"strconv"
)

// killGroup ends the isolation child and what it started. Windows has no process group to
// signal, and Process.Kill ends only the relay: the command it runs kept running with no limit.
// taskkill /T walks the tree from the child's pid; Process.Kill is the fallback.
//
// It is only called while the child runs (cmd.Cancel). After Wait the pid may already belong to
// another process, and a tree kill there would end that one.
func killGroup(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return os.ErrProcessDone
	}
	if exec.Command("taskkill", "/T", "/F", "/PID", strconv.Itoa(cmd.Process.Pid)).Run() == nil {
		return nil
	}
	return cmd.Process.Kill()
}

// killLeftovers does nothing outside Unix: once the child exited there is no group left to find
// what it started.
func killLeftovers(*exec.Cmd) {}
