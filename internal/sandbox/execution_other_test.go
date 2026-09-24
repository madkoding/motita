//go:build !unix

package sandbox

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/madkoding/motita/internal/execx"
)

// These tests cover the non-Unix behaviour of the sandbox in real execution. The
// important one is the first: syscall.Exec only ever returns EWINDOWS on Windows,
// so a child that relied on it would fail **every** command it was asked to run,
// while still compiling perfectly. Running the binary is the only way to see it.
//
// They are tagged !unix because the Unix child replaces its own image and never
// returns, which is a different contract.

// TestSandboxedCommandReallyRuns: the whole isolation path — the child re-exec, the
// spec, the command — produces output and an exit code.
func TestSandboxedCommandReallyRuns(t *testing.T) {
	box, err := New(Options{Dir: t.TempDir()})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer box.Close()

	cmd, args := echoCommand("ran-inside-the-sandbox")
	out, _, exit, err := box.Run(context.Background(), execx.Request{Command: cmd, Args: args})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if exit != 0 {
		t.Errorf("exit = %d (out = %q)", exit, out)
	}
	if !strings.Contains(out, "ran-inside-the-sandbox") {
		t.Errorf("the command's output did not come back: %q", out)
	}
}

// TestSandboxedFailureKeepsTheExitCode: the code the command returns is what the
// anchor uses to decide PASS/FAIL, so it must survive the round trip. Without the
// forwarding in the child, every check would look like a success.
func TestSandboxedFailureKeepsTheExitCode(t *testing.T) {
	box, err := New(Options{Dir: t.TempDir()})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer box.Close()

	cmd, args := failCommand()
	_, _, exit, err := box.Run(context.Background(), execx.Request{Command: cmd, Args: args})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if exit == 0 {
		t.Error("a failing command must not come back as exit 0")
	}
}

// TestSandboxedMissingCommandIs127: the reserved code tells "the command could not
// be executed" apart from "the command ran and failed".
func TestSandboxedMissingCommandIs127(t *testing.T) {
	box, err := New(Options{Dir: t.TempDir()})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer box.Close()

	_, _, exit, err := box.Run(context.Background(), execx.Request{
		Command: "a-command-that-does-not-exist-anywhere",
	})
	if err == nil {
		t.Error("a command that cannot be executed must be reported")
	}
	if exit != 127 && exit != -1 {
		t.Errorf("exit = %d, want the reserved 127 or -1", exit)
	}
}

// TestSandboxedCommandRunsInTheWorkspace: the working directory is where the
// command's effects have to land, because that is where the validator looks.
func TestSandboxedCommandRunsInTheWorkspace(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "workspace")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	box, err := New(Options{Dir: dir})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer box.Close()

	cmd, args := printWorkingDirectory()
	// The request carries the directory the caller means. It is given absolutely
	// because that is what a configuration that works on every platform looks like
	// (a relative one is resolved against the process working directory, which is
	// not necessarily writable — that is a separate concern, covered by the
	// sandbox's own resolution tests).
	out, _, exit, err := box.Run(context.Background(), execx.Request{
		Command: cmd, Args: args, Dir: dir,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if exit != 0 {
		t.Fatalf("exit = %d (out = %q)", exit, out)
	}
	want, _ := filepath.EvalSymlinks(dir)
	got, _ := filepath.EvalSymlinks(strings.TrimSpace(out))
	if got != want {
		t.Errorf("the command ran in %q, want %q", got, want)
	}
}

// TestSandboxedTimeout: the deadline is enforced by the parent, so it works
// wherever the child runs.
func TestSandboxedTimeout(t *testing.T) {
	if testing.Short() {
		t.Skip("it waits for a timeout")
	}
	box, err := New(Options{Dir: t.TempDir()})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer box.Close()

	cmd, args := sleepCommand(30)
	start := time.Now()
	_, _, _, err = box.Run(context.Background(), execx.Request{
		Command: cmd, Args: args, Timeout: 2 * time.Second,
	})
	if err == nil {
		t.Errorf("exceeding the deadline must be reported (command %q %v ran in %s)",
			cmd, args, time.Since(start))
	}
	if elapsed := time.Since(start); elapsed > 15*time.Second {
		t.Errorf("the deadline was not honoured: %s", elapsed)
	}
}
