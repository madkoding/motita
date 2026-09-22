//go:build unix

package tui

import (
	"context"
	"io"
	"os"
	"syscall"
	"testing"
	"time"
)

// chattyRunner reports progress many times, so a resize lands in the middle of the updates.
type chattyRunner struct {
	fakeRunner
	finished chan struct{}
}

func (c *chattyRunner) RunTask(_ context.Context, _ string, progress func(string, ...any)) (string, error) {
	defer close(c.finished)
	for i := 0; i < 100; i++ {
		progress("step %d", i)
		time.Sleep(time.Millisecond)
	}
	return "done", nil
}

// TestResizingDuringARunDoesNotRaceTheConversation: a painter goroutine used to read the
// conversation on SIGWINCH while the run loop appended to it and rewrote it. Run under -race.
func TestResizingDuringARunDoesNotRaceTheConversation(t *testing.T) {
	withEnvStale(t)
	restore := stubTTYSize(80, 24, true)
	defer restore()

	reader, writer := io.Pipe()
	runner := &chattyRunner{fakeRunner: fakeRunner{cfg: configWithKey("k")}, finished: make(chan struct{})}
	var out syncBuffer
	tu := New(runner)
	tu.In, tu.Out = reader, &out

	exited := make(chan int, 1)
	go func() { exited <- tu.Run(context.Background()) }()
	writer.Write([]byte("do it\n"))

	for running := true; running; {
		select {
		case <-runner.finished:
			running = false
		case <-time.After(2 * time.Millisecond):
			if err := syscall.Kill(os.Getpid(), syscall.SIGWINCH); err != nil {
				t.Skipf("cannot raise SIGWINCH here: %v", err)
			}
		}
	}
	waitFor(t, &out, "done")
	writer.Close()
	select {
	case <-exited:
	case <-time.After(3 * time.Second):
		t.Fatal("Run did not return")
	}
}
