//go:build windows

package execx

// The Windows side of RunShell. The Unix tests are written in POSIX shell and only
// describe `sh`; here the interpreter is the command processor, so the lines are
// written in its dialect. What matters is the same on both: the line runs, its
// output comes back, a failure is reported with its exit code, and the deadline is
// enforced.

import (
	"context"
	"strings"
	"testing"
	"time"
)

// TestRunShellWindowsRunsALine: the command processor is found and the output comes
// back. This is what makes the tool usable at all on Windows.
func TestRunShellWindowsRunsALine(t *testing.T) {
	res, err := RunShell(context.Background(), "echo hello-from-cmd", t.TempDir(), 10*time.Second, 4096)
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if res.Exit != 0 {
		t.Errorf("exit = %d (output %q)", res.Exit, res.Output)
	}
	if !strings.Contains(res.Output, "hello-from-cmd") {
		t.Errorf("output = %q", res.Output)
	}
}

// TestRunShellWindowsReportsTheExitCode: the code is what a caller inspects to
// decide whether the command succeeded, so it must survive.
func TestRunShellWindowsReportsTheExitCode(t *testing.T) {
	res, err := RunShell(context.Background(), "exit 3", t.TempDir(), 10*time.Second, 4096)
	if err != nil {
		t.Fatalf("the command did run, so no error is expected: %v", err)
	}
	if res.Exit != 3 {
		t.Errorf("exit = %d, want 3", res.Exit)
	}
}

// TestRunShellWindowsHonoursTheDeadline: the deadline is applied by the parent, so
// it works with any interpreter.
func TestRunShellWindowsHonoursTheDeadline(t *testing.T) {
	start := time.Now()
	res, err := RunShell(context.Background(), "ping -n 30 127.0.0.1 >NUL", t.TempDir(), time.Second, 4096)
	if err == nil {
		t.Fatal("a timeout was expected")
	}
	if !res.Expired {
		t.Error("the result must mark that it expired")
	}
	if elapsed := time.Since(start); elapsed > 20*time.Second {
		t.Errorf("the deadline was not honoured: %s", elapsed)
	}
}

// TestRunShellWindowsStripsSurroundingQuotes: cmd.exe does not strip one surrounding
// pair of quotes the way `sh -c` does, so a line written that way would arrive with
// them and fail. Checking it here keeps the behaviour from regressing.
func TestRunShellWindowsStripsSurroundingQuotes(t *testing.T) {
	res, err := RunShell(context.Background(), `"echo quoted-line"`, t.TempDir(), 10*time.Second, 4096)
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if !strings.Contains(res.Output, "quoted-line") {
		t.Errorf("output = %q", res.Output)
	}
}

// TestRunShellWindowsTruncatesALongOutput.
func TestRunShellWindowsTruncatesALongOutput(t *testing.T) {
	res, err := RunShell(context.Background(),
		"for /L %i in (1,1,500) do @echo filler-line", t.TempDir(), 20*time.Second, 128)
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if !res.Truncated {
		t.Errorf("the output must be marked truncated (len=%d)", len(res.Output))
	}
}
