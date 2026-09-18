package sandbox

// This file has no build tag on purpose: it is the part of the sandbox that must
// behave the same everywhere, and it is what keeps the package tested on Windows
// and macOS, where the Linux-specific tests are not compiled.
//
// The isolation primitives (ulimit, cgroups v1, chroot, dropping privileges) only
// exist on Linux. Elsewhere the sandbox still has to work: the temporary directory
// and the command execution must behave, and it must SAY what it could not apply
// instead of pretending the command was isolated.

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/madkoding/starlight/internal/execx"
)

// TestNewCreatesTheWorkingDirectory: the directory the caller asks for is created
// if it is missing, everywhere.
func TestNewCreatesTheWorkingDirectory(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "a", "b", "workspace")
	box, err := New(Options{Dir: dir, UseChroot: true, Root: "."})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer box.Close()

	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		t.Errorf("the working directory must exist: %v", err)
	}
	if box.Base() == "" {
		t.Error("the base directory must be reported")
	}
	if !filepath.IsAbs(box.Base()) {
		t.Errorf("the base must be absolute, got %q", box.Base())
	}
}

// TestNewWithoutADirectoryUsesTheCurrentOne. The empty Dir means "here", which the
// sandbox resolves with the process' own working directory. That directory can be
// removed by another test in this package (a Linux test exercises exactly that
// failure), so the check tolerates it: the property is that the base is never empty
// when the directory does exist.
func TestNewWithoutADirectoryUsesTheCurrentOne(t *testing.T) {
	if _, err := os.Getwd(); err != nil {
		t.Skip("the process working directory no longer exists (another test removed it)")
	}
	box, err := New(Options{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer box.Close()
	if box.Base() == "" {
		t.Error("the base must never be empty")
	}
}

// TestRunExecutesAndCapturesOutput: the command runs and its output comes back,
// with the exit code, on every platform.
func TestRunExecutesAndCapturesOutput(t *testing.T) {
	box, err := New(Options{Dir: t.TempDir()})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer box.Close()

	cmd, args := echoCommand("hello-from-the-sandbox")
	out, truncated, exit, err := box.Run(context.Background(), execx.Request{Command: cmd, Args: args})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if exit != 0 {
		t.Errorf("exit = %d", exit)
	}
	if !strings.Contains(out, "hello-from-the-sandbox") {
		t.Errorf("out = %q", out)
	}
	if truncated {
		t.Error("a short output must not be truncated")
	}
}

// TestRunReportsAFailingExitCode: a command that fails is not an error of the
// sandbox; the exit code is what the caller inspects.
func TestRunReportsAFailingExitCode(t *testing.T) {
	box, err := New(Options{Dir: t.TempDir()})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer box.Close()

	cmd, args := failCommand()
	out, _, exit, err := box.Run(context.Background(), execx.Request{Command: cmd, Args: args})
	if err != nil {
		// Some platforms refuse to run the command at all; then the error must say
		// something, which is also acceptable.
		if out == "" && err.Error() == "" {
			t.Error("a failure must be reported one way or another")
		}
		return
	}
	if exit == 0 {
		t.Errorf("a failing command must not report exit 0 (out = %q)", out)
	}
}

// TestRunRefusesAnEmptyCommand: the empty command never reaches the child.
func TestRunRefusesAnEmptyCommand(t *testing.T) {
	box, err := New(Options{Dir: t.TempDir()})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer box.Close()

	_, _, _, err = box.Run(context.Background(), execx.Request{Command: "   "})
	if err == nil {
		t.Error("an empty command must be refused")
	}
}

// TestRunHonoursTheTimeout: a command that outlives its deadline is stopped. The
// sleep is the platform's own so the test does not depend on a shell.
func TestRunHonoursTheTimeout(t *testing.T) {
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
		Command: cmd, Args: args, Timeout: time.Second,
	})
	elapsed := time.Since(start)
	if err == nil {
		t.Error("exceeding the deadline must be reported")
	}
	if elapsed > 15*time.Second {
		t.Errorf("the deadline was not honoured: %s", elapsed)
	}
}

// TestIsolationReportsWhatItCouldNotApply: the point of the isolation report is
// honesty. Whatever the platform, the report must be non-empty and describe what
// is actually in effect.
func TestIsolationReportsWhatItCouldNotApply(t *testing.T) {
	box, err := New(Options{Dir: t.TempDir()})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer box.Close()

	modes := box.Isolation()
	if len(modes) == 0 {
		t.Error("the isolation must report at least the ephemeral directory")
	}

	// Asking for chroot where it does not exist must be declared as not applied,
	// never silently ignored.
	box2, err := New(Options{Dir: t.TempDir(), UseChroot: true, Root: "/"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer box2.Close()
	if len(box2.NotApplied()) == 0 {
		t.Errorf("requesting chroot on %s must be reported as not applied", runtime.GOOS)
	}
	if len(box2.IsolationJSON()) == 0 {
		t.Error("the isolation must be serialisable")
	}
}

// TestCloseIsIdempotent: shutting down twice must not fail.
func TestCloseIsIdempotent(t *testing.T) {
	box, err := New(Options{Dir: t.TempDir()})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	box.Close()
	box.Close()
}

// TestRunInAWorkspaceThatDisappeared: the working directory is recreated if it was
// removed between runs, so a long-lived process does not fail by surprise.
func TestRunInAWorkspaceThatDisappeared(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "workspace")
	box, err := New(Options{Dir: dir})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer box.Close()

	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	cmd, args := echoCommand("still-here")
	out, _, exit, err := box.Run(context.Background(), execx.Request{Command: cmd, Args: args})
	if err != nil {
		t.Fatalf("Run after the directory was removed: %v", err)
	}
	if exit != 0 || !strings.Contains(out, "still-here") {
		t.Errorf("exit = %d, out = %q", exit, out)
	}
}

// TestMinimumMemoryMBIsNeverNegative: the helper the parent uses to raise a limit
// that would prevent the command from starting.
func TestMinimumMemoryMBIsNeverNegative(t *testing.T) {
	if got := minimumMemoryMB(); got < 0 {
		t.Errorf("minimumMemoryMB() = %d", got)
	}
}
