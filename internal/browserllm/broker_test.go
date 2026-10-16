package browserllm

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/madkoding/motita/internal/llm"
)

// A request waits for a tab, which must have asked for work lately.
func TestAskNeedsATab(t *testing.T) {
	b := New()
	if b.Online() {
		t.Fatal("a broker nobody polled is not online")
	}
	if _, err := b.Ask(context.Background(), nil); err != ErrNoTab {
		t.Fatalf("Ask = %v", err)
	}
}

// A tab takes the job, runs it and answers; the text comes back to the asker.
func TestAskAndAnswer(t *testing.T) {
	b := New()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	b.seen()
	done := make(chan string, 1)
	go func() {
		text, err := b.Ask(ctx, []llm.Message{{Role: "user", Content: "name this"}})
		if err != nil {
			text = "error: " + err.Error()
		}
		done <- text
	}()
	job, ok := b.Next(ctx)
	if !ok || len(job.Messages) != 1 || job.Messages[0].Content != "name this" || job.Messages[0].Role != "user" {
		t.Fatalf("Next = %+v, %v", job, ok)
	}
	if !b.Answer(job.ID, "A title", "") {
		t.Fatal("the job is waited for")
	}
	if got := <-done; got != "A title" {
		t.Errorf("answer = %q", got)
	}
	if b.Answer(job.ID, "again", "") {
		t.Error("a job is answered once")
	}
}

// A tab that cannot run the job says why, and the asker gets that as an error.
func TestAnAnswerCanBeAFailure(t *testing.T) {
	b := New()
	b.seen()
	errs := make(chan error, 1)
	go func() {
		_, err := b.Ask(context.Background(), nil)
		errs <- err
	}()
	job, _ := b.Next(context.Background())
	b.Answer(job.ID, "", "out of memory")
	if err := <-errs; err == nil || !strings.Contains(err.Error(), "out of memory") {
		t.Errorf("Ask = %v", err)
	}
}

// The asker's context ends the wait, and so does the time bound.
func TestAskGivesUp(t *testing.T) {
	b := New()
	b.seen()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := b.Ask(ctx, nil); err == nil || !strings.Contains(err.Error(), "did not answer") {
		t.Errorf("Ask = %v", err)
	}
	if len(b.waiting) != 0 {
		t.Error("a request that gave up is not waited for")
	}
}

// Too many requests at once are refused instead of piling up.
func TestAskRefusesAFullQueue(t *testing.T) {
	b := New()
	b.seen()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	for i := 0; i < queueSize; i++ {
		go func() { _, _ = b.Ask(ctx, nil) }()
	}
	for len(b.jobs) < queueSize {
		time.Sleep(time.Millisecond)
	}
	if _, err := b.Ask(ctx, nil); err == nil || !strings.Contains(err.Error(), "too many") {
		t.Errorf("Ask = %v", err)
	}
}

// A tab counts as there for a while after its last request, and stops counting after that.
func TestPresenceFades(t *testing.T) {
	b := New()
	now := time.Now()
	b.now = func() time.Time { return now }
	b.seen()
	if !b.Online() {
		t.Fatal("a tab that just asked is there")
	}
	now = now.Add(presence + time.Second)
	if b.Online() {
		t.Error("a tab that stopped asking is gone")
	}
}

// A request for work with none to give ends when the tab goes away, and a quiet one ends empty.
func TestNextWithoutWork(t *testing.T) {
	b := New()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, ok := b.Next(ctx); ok {
		t.Error("a cancelled request gets no job")
	}
	if !b.Online() {
		t.Error("asking for work marks the tab as there")
	}
}

// With nothing to do for the poll window the request ends empty.
func TestNextTimesOut(t *testing.T) {
	b := New()
	b.poll = 5 * time.Millisecond
	if _, ok := b.Next(context.Background()); ok {
		t.Error("a quiet request ends without a job")
	}
	if PollWait <= 0 || New().poll != PollWait {
		t.Error("a broker polls for PollWait")
	}
}

// A tab that took a job asks for no work while it answers, and is still there until it does.
func TestATabWritingAnAnswerIsStillThere(t *testing.T) {
	b := New()
	now := time.Now()
	b.now = func() time.Time { return now }
	b.seen()
	go func() { _, _ = b.Ask(context.Background(), nil) }()
	job, _ := b.Next(context.Background())
	now = now.Add(presence + time.Minute)
	if !b.Online() {
		t.Error("a tab writing an answer is there")
	}
	b.Answer(job.ID, "done", "")
	if b.Online() {
		t.Error("a tab that answered and stopped asking is gone")
	}
}

// A job's id cannot be guessed, so another client holding the token cannot answer it (F79).
func TestJobIDsCannotBeGuessed(t *testing.T) {
	b := New()
	b.seen()
	go func() { _, _ = b.Ask(context.Background(), nil) }()
	job, _ := b.Next(context.Background())
	if len(job.ID) < 32 || job.ID == "1" {
		t.Errorf("job id %q is guessable", job.ID)
	}
	if b.Answer("1", "injected", "") {
		t.Error("a guessed id must not be accepted")
	}
	if !b.Online() {
		t.Error("a stale answer must not mark the tab that took the job as gone")
	}
	b.Answer(job.ID, "ok", "")
}

// A job whose asker gave up is dropped, not run by the next tab (F78).
func TestAJobNobodyWaitsForIsSkipped(t *testing.T) {
	b := New()
	b.poll = 20 * time.Millisecond
	b.seen()
	ctx, cancel := context.WithCancel(context.Background())
	asked := make(chan struct{})
	go func() {
		_, _ = b.Ask(ctx, nil)
		close(asked)
	}()
	for len(b.jobs) == 0 {
		time.Sleep(time.Millisecond)
	}
	cancel()
	<-asked
	if _, ok := b.Next(context.Background()); ok {
		t.Error("a job whose asker left must not be handed out")
	}
}

// What a tab hands back is bounded.
func TestAnAnswerIsBounded(t *testing.T) {
	b := New()
	b.seen()
	errs := make(chan error, 1)
	go func() {
		_, err := b.Ask(context.Background(), nil)
		errs <- err
	}()
	job, _ := b.Next(context.Background())
	b.Answer(job.ID, strings.Repeat("x", maxAnswer+1), "")
	if err := <-errs; err == nil || !strings.Contains(err.Error(), "too long") {
		t.Errorf("an oversized answer: %v", err)
	}
}
