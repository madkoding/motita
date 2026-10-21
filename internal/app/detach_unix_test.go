//go:build unix

package app

import (
	"os/exec"
	"syscall"
	"testing"
)

// detach takes the child out of the terminal's process group. Checked by looking at what was set,
// because the property IS the setting: a child in the same group receives the terminal's Ctrl+C,
// which is the difference between a service and a program that dies with the window.
func TestDetachPutsTheChildInItsOwnSession(t *testing.T) {
	cmd := exec.Command("true")
	detach(cmd)
	if cmd.SysProcAttr == nil || !cmd.SysProcAttr.Setsid {
		t.Fatalf("the child was not put in its own session: %+v", cmd.SysProcAttr)
	}
}

// termSignal is SIGTERM and not SIGKILL: the gateway closes its listener and its clients cleanly,
// which is the difference between stopping a service and cutting one off.
func TestTermSignalIsTheGracefulOne(t *testing.T) {
	if got := termSignal(); got != syscall.SIGTERM {
		t.Fatalf("termSignal() = %v, want SIGTERM", got)
	}
}
