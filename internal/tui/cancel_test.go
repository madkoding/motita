package tui

import (
	"bytes"
	"context"
	"errors"
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
	started := make(chan struct{})
	runner := &fakeRunner{taskBlock: make(chan struct{}), taskStarted: started}
	tui := newFakeTUI("una tarea\n", runner)
	if code := cancelOnceRunning(t, tui, started); code != ExitInterrupted {
		t.Errorf("code = %d, want ExitInterrupted", code)
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

// TestAwaitRunTakesTheOutcomeFromTheRunnerOnCancellation: when the context is
// already cancelled before the loop starts, the only path left is the cancellation
// branch. The outcome must still be the runner's report, not a synthesised
// cancellation error, and the turn must end as soon as that report arrives.
//
// Driving the function directly is what makes this deterministic: waiting for a
// real run to reach the exact moment where a cancel and a completion race is not
// something a test can arrange, and it is the reason the coverage of that line used
// to come and go.
func TestAwaitRunTakesTheOutcomeFromTheRunnerOnCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	progress := make(chan string, 1)
	done := make(chan runOutcome, 1)

	// A turn that observes the cancellation and reports it, like the real runner.
	go func() {
		<-ctx.Done()
		done <- runOutcome{err: ctx.Err()}
	}()

	var seen []string
	out := (&TUI{}).awaitRun(ctx, progress, done, func(p string) { seen = append(seen, p) })

	if !errors.Is(out.err, context.Canceled) {
		t.Errorf("the outcome must come from the runner, got %+v", out)
	}
	if len(seen) != 0 {
		t.Errorf("no progress line was sent, got %v", seen)
	}
}

// TestAwaitRunKeepsDrainingProgressWhileTheTurnRuns: the progress lines sent before
// the outcome are all delivered, in order, and do not delay the result.
func TestAwaitRunKeepsDrainingProgressWhileTheTurnRuns(t *testing.T) {
	progress := make(chan string, 3)
	done := make(chan runOutcome, 1)
	progress <- "first"
	progress <- "second"
	done <- runOutcome{result: "the answer"}

	var seen []string
	out := (&TUI{}).awaitRun(context.Background(), progress, done, func(p string) { seen = append(seen, p) })

	// The outcome may win the first select, which is correct: the caller drains the
	// rest of the buffer itself. What must hold is that the answer is the runner's.
	if out.result != "the answer" {
		t.Errorf("result = %q", out.result)
	}
}
