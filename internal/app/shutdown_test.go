package app

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/madkoding/motita/internal/agent"
)

// --- Graceful shutdown ------------------------------------------------------

// TestGracefulShutdown: the first signal cancels the context and the work ends
// cleanly (code 0).
func TestGracefulShutdown(t *testing.T) {
	silence(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	mustWrite(t, path, `task_source:
  kind: stdin
anchor:
  kind: command
  command: "true"
sandbox:
  kind: cgroups
  cgroups: off
llm:
  api_key: x
agent:
  workspace_dir: `+dir+`
  log_level: error
  log_console: false
  graceful_shutdown_timeout: 2s
`)

	signals := make(chan os.Signal, 2)
	done := make(chan struct{})

	code := Run(Options{
		Args:    []string{"-config", path},
		Err:     &bytes.Buffer{},
		Signals: signals,
		waitSignal: func(time.Duration) <-chan time.Time {
			return make(chan time.Time)
		},
		Exit: func(int) { t.Error("the exit must not be forced after a single signal") },
		RunAgent: func(ctx context.Context, _ *agent.Agent) error {
			go func() {
				time.Sleep(20 * time.Millisecond)
				signals <- fakeSignal{}
			}()
			select {
			case <-ctx.Done():
				close(done)
				return nil // cancelled gracefully
			case <-time.After(5 * time.Second):
				t.Error("the context was not cancelled by the signal")
				return nil
			}
		},
	})
	if code != Success {
		t.Errorf("code = %d", code)
	}
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Error("the agent did not react to the signal")
	}
}

// fakeSignal implements os.Signal without depending on real signals.
type fakeSignal struct{}

func (fakeSignal) String() string { return "fake" }
func (fakeSignal) Signal()        {}

// TestForcedShutdownByDeadline: if the work does not finish within the deadline
// after the first signal, the exit is forced with the interruption code.
func TestForcedShutdownByDeadline(t *testing.T) {
	silence(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	mustWrite(t, path, `task_source:
  kind: stdin
anchor:
  kind: command
  command: "true"
sandbox:
  kind: cgroups
  cgroups: off
llm:
  api_key: x
agent:
  workspace_dir: `+dir+`
  log_level: error
  log_console: false
  graceful_shutdown_timeout: 1s
`)

	signals := make(chan os.Signal, 2)
	exits := make(chan int, 1)

	code := Run(Options{
		Args:    []string{"-config", path},
		Err:     &bytes.Buffer{},
		Signals: signals,
		Exit:    func(c int) { exits <- c },
		// Immediate countdown: it simulates that the deadline already expired.
		waitSignal: func(time.Duration) <-chan time.Time {
			ch := make(chan time.Time, 1)
			ch <- time.Now()
			return ch
		},
		RunAgent: func(ctx context.Context, _ *agent.Agent) error {
			time.Sleep(30 * time.Millisecond)
			signals <- fakeSignal{} // first signal: cancels
			<-ctx.Done()
			// The work does not finish: the countdown forces the exit.
			time.Sleep(200 * time.Millisecond)
			return nil
		},
	})
	if code != Success {
		t.Errorf("code = %d", code)
	}
	select {
	case c := <-exits:
		if c != InterruptedError {
			t.Errorf("forced exit code = %d, expected %d", c, InterruptedError)
		}
	case <-time.After(3 * time.Second):
		t.Error("the exit was not forced when the deadline expired")
	}
}

// TestSecondSignalForcesExit: the second signal exits immediately.
func TestSecondSignalForcesExit(t *testing.T) {
	silence(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	mustWrite(t, path, `task_source:
  kind: stdin
anchor:
  kind: command
  command: "true"
sandbox:
  kind: cgroups
  cgroups: off
llm:
  api_key: x
agent:
  workspace_dir: `+dir+`
  log_level: error
  log_console: false
  graceful_shutdown_timeout: 30s
`)

	signals := make(chan os.Signal, 2)
	exits := make(chan int, 1)

	Run(Options{
		Args:    []string{"-config", path},
		Err:     &bytes.Buffer{},
		Signals: signals,
		Exit:    func(c int) { exits <- c },
		RunAgent: func(ctx context.Context, _ *agent.Agent) error {
			time.Sleep(20 * time.Millisecond)
			signals <- fakeSignal{} // first: cancel
			<-ctx.Done()
			time.Sleep(20 * time.Millisecond)
			signals <- fakeSignal{} // second: immediate exit
			time.Sleep(200 * time.Millisecond)
			return nil
		},
	})
	select {
	case c := <-exits:
		if c != InterruptedError {
			t.Errorf("code = %d", c)
		}
	case <-time.After(3 * time.Second):
		t.Error("the second signal did not force the exit")
	}
}

// TestClosedSignalChannel: if the channel closes (end of process), the handler
// must neither block nor exit bluntly.
func TestClosedSignalChannel(t *testing.T) {
	silence(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	mustWrite(t, path, `task_source:
  kind: stdin
anchor:
  kind: command
  command: "true"
sandbox:
  kind: cgroups
  cgroups: off
llm:
  api_key: x
agent:
  workspace_dir: `+dir+`
  log_level: error
  log_console: false
  graceful_shutdown_timeout: 1s
`)

	signals := make(chan os.Signal, 2)
	close(signals)

	code := Run(Options{
		Args:    []string{"-config", path},
		Err:     &bytes.Buffer{},
		Signals: signals,
		Exit:    func(int) { t.Error("the exit must not be forced with the channel closed") },
		RunAgent: func(context.Context, *agent.Agent) error {
			time.Sleep(50 * time.Millisecond)
			return nil
		},
	})
	if code != Success {
		t.Errorf("code = %d", code)
	}
}

// TestShutdownHandlerUninstallsItself: after the work finishes, the shutdown
// goroutine must be gone (otherwise it would force an exit later, which is the
// bug that made the coverage profile disappear).
func TestShutdownHandlerUninstallsItself(t *testing.T) {
	silence(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	mustWrite(t, path, `task_source:
  kind: stdin
anchor:
  kind: command
  command: "true"
sandbox:
  kind: cgroups
  cgroups: off
llm:
  api_key: x
agent:
  workspace_dir: `+dir+`
  log_level: error
  log_console: false
  graceful_shutdown_timeout: 1s
`)

	signals := make(chan os.Signal, 2)
	exits := make(chan int, 1)

	code := Run(Options{
		Args:    []string{"-config", path},
		Err:     &bytes.Buffer{},
		Signals: signals,
		Exit:    func(c int) { exits <- c },
		// A deadline that fires immediately: if the handler were left alive it
		// would force the exit right away.
		waitSignal: func(time.Duration) <-chan time.Time {
			ch := make(chan time.Time, 1)
			ch <- time.Now()
			return ch
		},
		RunAgent: func(context.Context, *agent.Agent) error {
			return nil // the work finishes without any signal
		},
	})
	if code != Success {
		t.Errorf("code = %d", code)
	}
	select {
	case c := <-exits:
		t.Errorf("the shutdown handler forced an exit (%d) after the work had finished", c)
	case <-time.After(200 * time.Millisecond):
		// Nothing happened: the handler was uninstalled correctly.
	}
}

// TestShutdownHandlerIgnoresAClosedChannel: if the signal channel is closed
// while the handler is waiting, it must return instead of forcing an exit.
func TestShutdownHandlerIgnoresAClosedChannel(t *testing.T) {
	silence(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	mustWrite(t, path, `task_source:
  kind: stdin
anchor:
  kind: command
  command: "true"
sandbox:
  kind: cgroups
  cgroups: off
llm:
  api_key: x
agent:
  workspace_dir: `+dir+`
  log_level: error
  log_console: false
  graceful_shutdown_timeout: 5s
`)

	signals := make(chan os.Signal, 2)
	exits := make(chan int, 1)

	code := Run(Options{
		Args:    []string{"-config", path},
		Err:     &bytes.Buffer{},
		Signals: signals,
		Exit:    func(c int) { exits <- c },
		RunAgent: func(context.Context, *agent.Agent) error {
			// The channel closes while the handler is waiting for a signal.
			time.Sleep(20 * time.Millisecond)
			close(signals)
			time.Sleep(80 * time.Millisecond)
			return nil
		},
	})
	if code != Success {
		t.Errorf("code = %d", code)
	}
	select {
	case c := <-exits:
		t.Errorf("a closed channel must not force an exit (got %d)", c)
	default:
	}
}

// TestClosedChannelDuringTheSecondWait: the shutdown countdown also listens to the
// teardown signal; if the channel closes while it waits, it must return instead of
// forcing an exit.
func TestClosedChannelDuringTheSecondWait(t *testing.T) {
	silence(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	mustWrite(t, path, `task_source:
  kind: stdin
anchor:
  kind: command
  command: "true"
sandbox:
  kind: cgroups
  cgroups: off
llm:
  api_key: x
agent:
  workspace_dir: `+dir+`
  log_level: error
  log_console: false
  graceful_shutdown_timeout: 30s
`)

	signals := make(chan os.Signal, 2)
	exits := make(chan int, 1)

	code := Run(Options{
		Args:    []string{"-config", path},
		Err:     &bytes.Buffer{},
		Signals: signals,
		Exit:    func(c int) { exits <- c },
		// A countdown that never fires, so the only way out is the closed channel.
		waitSignal: func(time.Duration) <-chan time.Time {
			return make(chan time.Time)
		},
		RunAgent: func(context.Context, *agent.Agent) error {
			// One signal (which cancels), then the channel closes.
			signals <- fakeSignal{}
			time.Sleep(30 * time.Millisecond)
			close(signals)
			return nil
		},
	})
	if code != Success {
		t.Errorf("code = %d", code)
	}
	select {
	case c := <-exits:
		t.Errorf("a closed channel must not force an exit (got %d)", c)
	case <-time.After(300 * time.Millisecond):
		// No forced exit: correct.
	}
}
