//go:build windows

package execx

// The Windows counterpart of execx_test.go, which describes `sh` and only builds on
// Unix. The surface under test is the same — Run executes, captures both streams,
// truncates, propagates the exit code, honours the deadline and the environment —
// with the command processor as the interpreter.

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

// shell returns the interpreter and the arguments for a line, so the tests below
// look like their Unix counterparts.
func shell(line string) (string, []string) {
	cmd := os.Getenv("ComSpec")
	if cmd == "" {
		cmd = "cmd.exe"
	}
	return cmd, []string{"/c", line}
}

// TestRunSimpleCommand: the full path of Run.
func TestRunSimpleCommand(t *testing.T) {
	command, args := shell("echo hello")
	output, truncated, exit, err := Run(context.Background(), Request{
		Command: command, Args: args, Timeout: 10 * time.Second,
	})
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if exit != 0 || !strings.Contains(output, "hello") || truncated {
		t.Errorf("output=%q exit=%d truncated=%v", output, exit, truncated)
	}
}

// TestRunEmptyCommand is covered by the portable tests; repeated here only if the
// platform changed the meaning, which it does not. It is deliberately absent.

func TestRunMissingCommand(t *testing.T) {
	_, _, exit, err := Run(context.Background(), Request{
		Command: "no-such-binary-anywhere.exe",
		Timeout: 5 * time.Second,
	})
	if err == nil {
		t.Fatal("an error was expected")
	}
	if exit != -1 {
		t.Errorf("exit = %d", exit)
	}
}

func TestRunPropagatesTheExitCode(t *testing.T) {
	command, args := shell("exit 7")
	_, _, exit, err := Run(context.Background(), Request{
		Command: command, Args: args, Timeout: 5 * time.Second,
	})
	if err != nil {
		t.Fatalf("a non-zero exit is not a run error: %v", err)
	}
	if exit != 7 {
		t.Errorf("exit = %d", exit)
	}
}

// TestRunCombinesStdoutAndStderr: the log must include both streams.
func TestRunCombinesStdoutAndStderr(t *testing.T) {
	command, args := shell("echo out & echo err 1>&2")
	output, _, _, err := Run(context.Background(), Request{
		Command: command, Args: args, Timeout: 5 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output, "out") || !strings.Contains(output, "err") {
		t.Errorf("output = %q", output)
	}
}

// TestRunTruncatesTheOutput.
func TestRunTruncatesTheOutput(t *testing.T) {
	command, args := shell("for /L %i in (1,1,500) do @echo a-filler-line")
	output, truncated, exit, err := Run(context.Background(), Request{
		Command: command, Args: args, Timeout: 20 * time.Second, MaxOutput: 128,
	})
	if err != nil {
		t.Fatalf("error: %v (exit %d)", err, exit)
	}
	if !truncated {
		t.Errorf("the output must be truncated (len %d)", len(output))
	}
}

// TestRunTimeout: the deadline is enforced by the parent, so it is the same on every
// platform; the sleeping command is the platform's own.
func TestRunTimeout(t *testing.T) {
	command, args := shell("ping -n 30 127.0.0.1 >NUL")
	start := time.Now()
	_, _, exit, err := Run(context.Background(), Request{
		Command: command, Args: args, Timeout: time.Second,
	})
	if err == nil {
		t.Error("a timeout was expected")
	}
	if exit != -1 {
		t.Errorf("exit = %d, want -1", exit)
	}
	if elapsed := time.Since(start); elapsed > 20*time.Second {
		t.Errorf("the deadline was not honoured: %s", elapsed)
	}
}

// TestRunCancelledContext.
func TestRunCancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	command, args := shell("echo never")
	if _, _, _, err := Run(ctx, Request{Command: command, Args: args, Timeout: 5 * time.Second}); err == nil {
		t.Error("a cancelled context must be reported")
	}
}

// TestRunWithDirectory: the command runs where it is told.
func TestRunWithDirectory(t *testing.T) {
	dir := t.TempDir()
	command, args := shell("cd")
	output, _, exit, err := Run(context.Background(), Request{
		Command: command, Args: args, Dir: dir, Timeout: 5 * time.Second,
	})
	if err != nil {
		t.Fatalf("error: %v (exit %d)", err, exit)
	}
	// cmd's `cd` prints the current directory; Windows paths are case-insensitive.
	if !strings.Contains(strings.ToLower(output), strings.ToLower(dir)) &&
		!strings.Contains(strings.ToLower(output), strings.ToLower(filepathBase(dir))) {
		t.Errorf("the command did not run in the given directory: %q", output)
	}
}

// TestRunWithOwnEnvironment: the environment handed in is the one the command sees,
// which is how the sandbox restricts PATH.
func TestRunWithOwnEnvironment(t *testing.T) {
	command, args := shell("echo VAR=%SECRET_THAT_MUST_NOT_LEAK%")
	output, _, _, err := Run(context.Background(), Request{
		Command:     command,
		Args:        args,
		Environment: []string{"SECRET_THAT_MUST_NOT_LEAK=visible"},
		Timeout:     5 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output, "visible") {
		t.Errorf("the given environment was not used: %q", output)
	}
}

// TestRunWithStdin: what is handed in reaches the command.
func TestRunWithStdin(t *testing.T) {
	command, args := shell("more")
	output, _, _, err := Run(context.Background(), Request{
		Command: command, Args: args, Stdin: []byte("from-stdin\r\n"), Timeout: 5 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output, "from-stdin") {
		t.Errorf("stdin did not reach the command: %q", output)
	}
}

// filepathBase avoids importing path/filepath for one call.
func filepathBase(p string) string {
	if i := strings.LastIndexAny(p, `\/`); i >= 0 {
		return p[i+1:]
	}
	return p
}
