package tui

import (
	"bytes"
	"context"
	"io"
	"strings"
	"testing"
	"time"
)

// The last two branches of the interactive loop: the progress callback that fires
// after the run was cancelled, and the read that is abandoned when the context is
// done. Both exist so a Ctrl+C can never leave a goroutine blocked forever.

// TestProgressAfterCancellationDoesNotBlock: the agent's progress callback selects
// between the progress channel and the cancelled context. When the buffer is full
// and the run is over, it must take the second branch: a sender that blocked here
// would keep the agent's goroutine alive after the user asked it to stop.
func TestProgressAfterCancellationDoesNotBlock(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	// A full channel with a cancelled context is exactly the situation the select
	// exists for.
	progress := make(chan string, 1)
	progress <- "occupied"
	send := progressSender(ctx, progress)

	done := make(chan struct{})
	go func() {
		defer close(done)
		send("the next line")
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("the progress sender blocked instead of taking the cancelled branch")
	}
	if got := <-progress; got != "occupied" {
		t.Errorf("the dropped line must not displace what was already there, got %q", got)
	}
}

// TestProgressSenderDeliversWhileRunning: the same callback delivers normally when
// the run is alive, which is what makes the phases visible in the chat.
func TestProgressSenderDeliversWhileRunning(t *testing.T) {
	progress := make(chan string, 1)
	send := progressSender(context.Background(), progress)
	send("running: %s", "ls")
	select {
	case got := <-progress:
		if got != "running: ls" {
			t.Errorf("got %q", got)
		}
	case <-time.After(time.Second):
		t.Fatal("the line was not delivered")
	}
}

// TestTaskCancelledByTheContext: Ctrl+C during a task has to end that task, say so
// in the conversation and hand the prompt back. This is the branch that sets the
// outcome from the context rather than from the runner.
func TestTaskCancelledByTheContext(t *testing.T) {
	runner := &fakeRunner{taskBlock: make(chan struct{})}
	tui := newFakeTUI("una tarea\n", runner)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan int, 1)
	go func() { done <- tui.Run(ctx) }()
	time.Sleep(60 * time.Millisecond)
	cancel()

	select {
	case code := <-done:
		if code != ExitInterrupted {
			t.Errorf("code = %d, want ExitInterrupted", code)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("the task did not stop when the context was cancelled")
	}
	if !strings.Contains(stripANSI(outputOf(tui)), "cancelled") {
		t.Errorf("a cancelled task must say so:\n%s", stripANSI(outputOf(tui)))
	}
}

// TestReadLineAbandonedByCancellation: when the context is done while a line is
// being read, the reader must return immediately and leave a goroutine to drain
// the read so the abandoned io.Reader is still released.
//
// The first byte is sent before the pipe stops: readLine reads that byte on its
// own, and only then blocks waiting for the rest of the line. Cancelling while the
// first byte is still in flight would exercise readKey instead, which is a
// different branch.
func TestReadLineAbandonedByCancellation(t *testing.T) {
	r, w := io.Pipe()
	defer w.Close()
	tui := &TUI{In: r, Out: &bytes.Buffer{}, Err: &bytes.Buffer{}, Runner: &fakeRunner{}, NoColor: true}

	go func() {
		time.Sleep(10 * time.Millisecond)
		_, _ = w.Write([]byte("x")) // the first byte arrives...
		time.Sleep(20 * time.Millisecond)
		// ...and the rest of the line never does.
	}()

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(60 * time.Millisecond)
		cancel()
	}()

	start := time.Now()
	if _, ok := tui.readLine(ctx); ok {
		t.Error("an abandoned read must report false")
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("the read took %s: it must return as soon as the context is done", elapsed)
	}
}
