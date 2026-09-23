//go:build unix

package app

import (
	"os"
	"os/exec"
	"syscall"
)

// termSignal is the signal that asks a process to stop rather than ends it outright.
//
// It is a function rather than a constant because the two builds genuinely differ: the Windows side
// has no SIGTERM and uses os.Kill, so there is nothing to share.
func termSignal() os.Signal { return syscall.SIGTERM }

// detach puts the child in its own session, so it is not attached to the terminal it was started
// from.
//
// Setsid does two things this command needs and neither is optional. It takes the child out of the
// terminal's process group, so a Ctrl+C meant for the user's shell does not reach the gateway; and
// it removes the controlling terminal, so a gateway left running does not compete for input with
// whatever the user starts next.
//
// Without it, `gateway start` would mean "a gateway that dies when you close the window" - which is
// the opposite of a service, and the failure would only show up after the user had come to rely on
// it.
func detach(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}
