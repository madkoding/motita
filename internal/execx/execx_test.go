//go:build unix

// These tests describe the behaviour of Run with commands that exist on Unix
// (/bin/sh, kill, $$). The same surface on Windows is covered by execx_windows_test.go,
// which uses the command processor instead.

package execx

import (
	"context"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// TestRunSimpleCommand: the full path of Run.
func TestRunSimpleCommand(t *testing.T) {
	output, truncated, exit, err := Run(context.Background(), Request{
		Command: "/bin/sh",
		Args:    []string{"-c", "echo hello; exit 0"},
		Timeout: 10 * time.Second,
	})
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if exit != 0 || strings.TrimSpace(output) != "hello" || truncated {
		t.Errorf("output=%q exit=%d truncated=%v", output, exit, truncated)
	}
}

func TestRunEmptyCommand(t *testing.T) {
	if _, _, _, err := Run(context.Background(), Request{Command: "   "}); err == nil {
		t.Fatal("an empty command must return an error")
	}
}

func TestRunMissingCommand(t *testing.T) {
	_, _, exit, err := Run(context.Background(), Request{
		Command: "/no/such/binary",
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
	_, _, exit, err := Run(context.Background(), Request{
		Command: "/bin/sh", Args: []string{"-c", "exit 7"}, Timeout: 5 * time.Second,
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
	output, _, _, err := Run(context.Background(), Request{
		Command: "/bin/sh",
		Args:    []string{"-c", "echo out; echo err >&2"},
		Timeout: 5 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output, "out") || !strings.Contains(output, "err") {
		t.Errorf("output = %q", output)
	}
}

// TestRunTruncatesTheOutput: the limited buffer must mark the cut and must NOT
// make the command fail (if Write returned an error, the process would die).
func TestRunTruncatesTheOutput(t *testing.T) {
	output, truncated, exit, err := Run(context.Background(), Request{
		Command:   "/bin/sh",
		Args:      []string{"-c", "i=0; while [ $i -lt 500 ]; do echo long-filler-line; i=$((i+1)); done"},
		Timeout:   10 * time.Second,
		MaxOutput: 100,
	})
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if !truncated {
		t.Error("it should mark the truncation")
	}
	if len(output) > 200 {
		t.Errorf("the output was not truncated: %d bytes", len(output))
	}
	if exit != 0 {
		t.Errorf("truncating must not make the command fail: exit=%d", exit)
	}
}

// TestRunTimeout: the deadline must really be met, killing the group.
func TestRunTimeout(t *testing.T) {
	start := time.Now()
	_, _, _, err := Run(context.Background(), Request{
		Command: "/bin/sh", Args: []string{"-c", "sleep 30"}, Timeout: 1 * time.Second,
	})
	elapsed := time.Since(start)
	if err == nil || !strings.Contains(err.Error(), "exceeded the limit") {
		t.Fatalf("error = %v", err)
	}
	if elapsed > 5*time.Second {
		t.Errorf("the deadline was not met: %s (was the group not killed?)", elapsed)
	}
}

// TestRunCancelledContext: cancelling the parent context also cuts it short.
func TestRunCancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()
	start := time.Now()
	_, _, _, err := Run(ctx, Request{
		Command: "/bin/sh", Args: []string{"-c", "sleep 30"}, Timeout: 20 * time.Second,
	})
	if err == nil {
		t.Fatal("an error was expected from the cancellation")
	}
	if time.Since(start) > 5*time.Second {
		t.Errorf("the cancellation took too long: %s", time.Since(start))
	}
}

func TestRunWithDirectory(t *testing.T) {
	dir := t.TempDir()
	output, _, _, err := Run(context.Background(), Request{
		Command: "/bin/sh", Args: []string{"-c", "pwd"}, Dir: dir, Timeout: 5 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output, dir) {
		t.Errorf("output = %q, expected %q", output, dir)
	}
}

// TestRunWithOwnEnvironment: an explicit environment replaces the inherited one.
func TestRunWithOwnEnvironment(t *testing.T) {
	t.Setenv("SECRET_THAT_MUST_NOT_LEAK", "value")
	output, _, _, err := Run(context.Background(), Request{
		Command:     "/bin/sh",
		Args:        []string{"-c", "echo VAR=$SECRET_THAT_MUST_NOT_LEAK PATH=$PATH"},
		Environment: []string{"PATH=/usr/bin:/bin"},
		Timeout:     5 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(output, "value") {
		t.Errorf("the parent environment leaked: %q", output)
	}
	if !strings.Contains(output, "PATH=/usr/bin:/bin") {
		t.Errorf("the explicit environment was not applied: %q", output)
	}
}

func TestRunWithStdin(t *testing.T) {
	output, _, _, err := Run(context.Background(), Request{
		Command: "/bin/sh",
		Args:    []string{"-c", "cat"},
		Stdin:   []byte("input data"),
		Timeout: 5 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output, "input data") {
		t.Errorf("output = %q", output)
	}
}

// TestRunTerminatedBySignal: a process that destroys itself with a signal must
// be reported as such, not as a normal exit.
func TestRunTerminatedBySignal(t *testing.T) {
	_, _, exit, err := Run(context.Background(), Request{
		Command: "/bin/sh", Args: []string{"-c", "kill -TERM $$"}, Timeout: 5 * time.Second,
	})
	if err == nil {
		t.Fatal("a death by signal must be an error")
	}
	if !strings.Contains(err.Error(), "signal") {
		t.Errorf("the error must mention the signal: %v", err)
	}
	if exit >= 0 {
		t.Errorf("exit = %d, expected a negative one", exit)
	}
}

// --- limitedBuffer ----------------------------------------------------------

func TestLimitedBuffer(t *testing.T) {
	// With a wide limit everything accumulates without marking a cut.
	b := limitedBuffer{max: 1024}
	n, err := b.Write([]byte("first"))
	if err != nil || n != 5 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	if b.buf.String() != "first" {
		t.Errorf("content = %q", b.buf.String())
	}
	if b.truncated {
		t.Error("it should not be truncated")
	}

	// With the limit reached, the excess is discarded but nothing fails:
	// returning an error here would kill the writing command.
	limited := &limitedBuffer{max: 4}
	n, err = limited.Write([]byte("1234567890"))
	if err != nil {
		t.Fatalf("truncating must not return an error: %v", err)
	}
	if n != 10 {
		t.Errorf("n = %d: it must report all the bytes written", n)
	}
	if limited.buf.String() != "1234" {
		t.Errorf("content = %q", limited.buf.String())
	}
	if !limited.truncated {
		t.Error("it should mark the truncation")
	}
}

func TestLimitedBufferAlreadyFull(t *testing.T) {
	b := &limitedBuffer{max: 2}
	b.buf.WriteString("ab")
	n, err := b.Write([]byte("more"))
	if err != nil || n != 4 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	if b.buf.String() != "ab" {
		t.Errorf("content = %q", b.buf.String())
	}
	if !b.truncated {
		t.Error("it should mark the truncation")
	}
}

// TestRunLeavingABackgroundProcessSucceedsAndKillsIt: a command whose leader
// exits 0 while a background child still holds the output pipe must be
// reported as a success, and the child must not outlive the run.
func TestRunLeavingABackgroundProcessSucceedsAndKillsIt(t *testing.T) {
	out, _, exit, err := Run(context.Background(), Request{
		Command: "/bin/sh", Args: []string{"-c", "sleep 30 & echo $!"}, Timeout: 10 * time.Second,
	})
	if err != nil || exit != 0 {
		t.Fatalf("exit=%d err=%v, expected a success", exit, err)
	}
	pid, convErr := strconv.Atoi(strings.TrimSpace(out))
	if convErr != nil {
		t.Fatalf("output = %q, expected the background pid", out)
	}
	deadline := time.Now().Add(2 * time.Second)
	for syscall.Kill(pid, 0) == nil {
		if time.Now().After(deadline) {
			_ = syscall.Kill(pid, syscall.SIGKILL)
			t.Fatalf("the background process %d outlived the run", pid)
		}
		time.Sleep(20 * time.Millisecond)
	}
}
