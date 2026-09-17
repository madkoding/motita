//go:build unix

package main

import (
	"context"
	"os"
	"os/exec"
	"syscall"
)

// shellCommand builds the process that runs a line the way a person would type it
// in the terminal. On Unix that interpreter is `sh`.
func shellCommand(ctx context.Context, command string) *exec.Cmd {
	return exec.CommandContext(ctx, "sh", "-c", command)
}

// configureGroup isolates the command in its own process group. Without this,
// killing only `sh` leaves its children alive (`sleep`, scripts, pipes), which
// keep the output pipe open and block Wait until they finish on their own.
func configureGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// killGroup sends SIGKILL to the whole process group (negative pid).
func killGroup(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return os.ErrProcessDone
	}
	// The negative pid reaches `sh` and every descendant in the group.
	if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); err != nil {
		return os.ErrProcessDone
	}
	return nil
}
