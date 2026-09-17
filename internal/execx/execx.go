// Package execx holds the process execution primitives shared by the anchor
// (Layer A) and the sandbox (Layer C).
//
// This is where the blood was spilled: really killing a command that expires.
// `exec.CommandContext` only kills the direct process; its children (`sleep`, a
// script with pipes) stay alive holding the output pipe open and `Wait` keeps
// waiting well past the deadline. Verified: a `sleep 30` with a 1 s timeout
// used to take 5 s. The fix is a process group of its own, SIGKILL to the
// group and a bounded WaitDelay.
package execx

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// Request describes a command to run.
type Request struct {
	Command     string
	Args        []string
	Dir         string
	Environment []string // nil = inherit the process' own
	Timeout     time.Duration
	MaxOutput   int64 // bytes of combined output (0 = 256 KiB)
	Stdin       []byte
}

// Result of a run.
type Result struct {
	Output    string
	Exit      int
	Truncated bool
	Duration  time.Duration
	Expired   bool
}

// limitedBuffer accumulates up to max bytes and marks whether anything was cut,
// without making the writing process fail.
type limitedBuffer struct {
	buf       bytes.Buffer
	max       int64
	truncated bool
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	space := b.max - int64(b.buf.Len())
	if space <= 0 {
		b.truncated = true
		return len(p), nil
	}
	if space < int64(len(p)) {
		b.buf.Write(p[:space])
		b.truncated = true
		return len(p), nil
	}
	b.buf.Write(p)
	return len(p), nil
}

// Run executes the command with a process group and a real timeout. It returns
// the combined output (stdout+stderr), the exit code and an explicit error if
// the command could not be launched, expired or finished with a non-zero code.
func Run(ctx context.Context, p Request) (string, bool, int, error) {
	if strings.TrimSpace(p.Command) == "" {
		return "", false, -1, fmt.Errorf("empty command")
	}

	timeout := p.Timeout
	if timeout <= 0 {
		timeout = 120 * time.Second
	}
	max := p.MaxOutput
	if max <= 0 {
		max = 256 << 10
	}

	childCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(childCtx, p.Command, p.Args...)
	cmd.Dir = p.Dir
	if p.Environment != nil {
		cmd.Env = p.Environment
	}
	if p.Stdin != nil {
		cmd.Stdin = bytes.NewReader(p.Stdin)
	}

	configureGroup(cmd)
	cmd.Cancel = func() error { return killGroup(cmd) }
	cmd.WaitDelay = 2 * time.Second

	output := &limitedBuffer{max: max}
	cmd.Stdout = output
	cmd.Stderr = output

	start := time.Now()
	err := cmd.Run()
	duration := time.Since(start)
	text := output.buf.String()

	if childCtx.Err() == context.DeadlineExceeded {
		return text, output.truncated, -1, fmt.Errorf("the command exceeded the limit of %s and was terminated (process group sent SIGKILL)", timeout)
	}

	exit := 0
	if err != nil {
		var ee *exec.ExitError
		if ok := asExitError(err, &ee); ok {
			exit = ee.ExitCode()
			if exit < 0 {
				// Terminated by a signal (e.g. SIGKILL from the sandbox).
				return text, output.truncated, exit, fmt.Errorf("the command was terminated by a signal (exit=%d) after %s", exit, duration.Round(time.Millisecond))
			}
			return text, output.truncated, exit, nil
		}
		return text, output.truncated, -1, fmt.Errorf("could not run %q: %w", p.Command, err)
	}
	_ = duration
	return text, output.truncated, exit, nil
}

// RunShell runs the command through `sh -c`, to support pipes, redirections and
// quotes the way a person would type them in the terminal.
func RunShell(ctx context.Context, command, dir string, timeout time.Duration, max int64) (Result, error) {
	childCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(childCtx, "sh", "-c", command)
	cmd.Dir = dir
	configureGroup(cmd)
	cmd.Cancel = func() error { return killGroup(cmd) }
	cmd.WaitDelay = 2 * time.Second

	if max <= 0 {
		max = 256 << 10
	}
	output := &limitedBuffer{max: max}
	cmd.Stdout = output
	cmd.Stderr = output

	start := time.Now()
	err := cmd.Run()
	res := Result{
		Output:    output.buf.String(),
		Truncated: output.truncated,
		Duration:  time.Since(start),
	}

	if childCtx.Err() == context.DeadlineExceeded {
		res.Expired = true
		res.Exit = -1
		return res, fmt.Errorf("the command exceeded the limit of %s", timeout)
	}

	if err != nil {
		var ee *exec.ExitError
		if asExitError(err, &ee) {
			res.Exit = ee.ExitCode()
			if res.Exit < 0 {
				return res, fmt.Errorf("the command was terminated by a signal (exit=%d)", res.Exit)
			}
			return res, nil
		}
		res.Exit = -1
		return res, fmt.Errorf("could not run the command: %w", err)
	}
	return res, nil
}

// CleanOutput normalizes output for logs and prompts.
func CleanOutput(s string) string {
	return strings.TrimRight(s, "\n")
}
