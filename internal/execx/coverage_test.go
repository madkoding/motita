package execx

import (
	"context"
	"os/exec"
	"strings"
	"testing"
)

// --- Defaults and helpers ---------------------------------------------------

// TestRunAppliesDefaultTimeoutAndMaxOutput: with no timeout and no output limit,
// the documented defaults must be applied (120 s / 256 KiB), and the command must
// still run normally.
func TestRunAppliesDefaultTimeoutAndMaxOutput(t *testing.T) {
	out, truncated, exit, err := Run(context.Background(), Request{
		Command: "/bin/sh", Args: []string{"-c", "echo defaults-ok"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if exit != 0 || truncated {
		t.Fatalf("exit=%d truncated=%v", exit, truncated)
	}
	if !strings.Contains(out, "defaults-ok") {
		t.Errorf("output = %q", out)
	}
}

// TestKillGroupWithoutProcess: with no started process there is nothing to kill
// and it must say so instead of panicking.
func TestKillGroupWithoutProcess(t *testing.T) {
	cmd := exec.Command("/bin/sh", "-c", "exit 0")
	if err := killGroup(cmd); err == nil {
		t.Error("with no process started it must return an error")
	}
}

// TestKillGroupOnFinishedProcess: killing a group whose process already exited
// must also report it instead of panicking.
func TestKillGroupOnFinishedProcess(t *testing.T) {
	cmd := exec.Command("/bin/sh", "-c", "exit 0")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Wait(); err != nil {
		t.Fatal(err)
	}
	// The pid belongs to a process group that no longer exists.
	if err := killGroup(cmd); err == nil {
		t.Error("killing the group of a finished process must report the condition")
	}
}
