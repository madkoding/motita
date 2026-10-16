// Package browserllm is the go-between of the "browser" provider: the model runs in an open tab of
// the web UI (WebGPU), the agent runs here. A tab asks for work (Next), runs it and hands back the
// text (Answer); the agent's side (Ask) queues a request and waits for that text.
//
// A request is only taken when a tab was asking recently, so a person who closed the tab gets an
// immediate, readable failure instead of a wait: the caller falls back to its other model.
package browserllm

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"sync"
	"time"

	"github.com/madkoding/motita/internal/llm"
)

// The timings of the exchange.
const (
	// PollWait is how long a tab's request for work stays open without any.
	PollWait = 25 * time.Second
	// presence is how long after its last request for work a tab counts as there: a poll, a
	// reload's gap and a slow answer fit in it.
	presence = 2 * PollWait
	// AnswerWait bounds a request: a 0.5B model on a laptop card answers a title in seconds, and a
	// card that emulates WebGPU on the processor in minutes.
	AnswerWait = 5 * time.Minute
	// queueSize is how many requests wait for a tab at once.
	queueSize = 16
	// maxAnswer bounds what a tab may hand back: the text becomes a title or a summary the agent
	// reads, so a tab cannot flood it.
	maxAnswer = 64 << 10
)

// ErrNoTab is the failure when no tab with the browser model on is open.
var ErrNoTab = errors.New(
	"no browser tab is running the browser model: open the web UI and turn it on in Settings → Models")

// Message is one turn of the conversation, as the tab reads it.
type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// Job is a request a tab runs.
type Job struct {
	ID       string    `json:"id"`
	Messages []Message `json:"messages"`
}

// result is what a tab answered.
type result struct {
	text string
	err  error
}

// Broker queues requests for the tabs and matches their answers.
type Broker struct {
	jobs chan Job
	now  func() time.Time
	// poll is how long Next holds a request open; a test shortens it.
	poll time.Duration

	mu       sync.Mutex
	lastSeen time.Time
	// busyUntil is when a tab that took a job is given up on: while it writes the answer it asks
	// for no work, and is still there.
	busyUntil time.Time
	waiting   map[string]chan result
}

// New makes a broker.
func New() *Broker {
	return &Broker{jobs: make(chan Job, queueSize), now: time.Now, poll: PollWait, waiting: map[string]chan result{}}
}

// Online says a tab asked for work lately.
func (b *Broker) Online() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	now := b.now()
	return (!b.lastSeen.IsZero() && now.Sub(b.lastSeen) < presence) || now.Before(b.busyUntil)
}

// seen records that a tab is there.
func (b *Broker) seen() {
	b.mu.Lock()
	b.lastSeen = b.now()
	b.mu.Unlock()
}

// Ask has the model in a tab answer the conversation.
func (b *Broker) Ask(ctx context.Context, messages []llm.Message) (string, error) {
	if !b.Online() {
		return "", ErrNoTab
	}
	job := Job{Messages: make([]Message, 0, len(messages))}
	for _, m := range messages {
		job.Messages = append(job.Messages, Message{Role: m.Role, Content: m.Content})
	}
	ch := make(chan result, 1)
	job.ID = newJobID()
	b.mu.Lock()
	b.waiting[job.ID] = ch
	b.mu.Unlock()
	defer func() {
		b.mu.Lock()
		delete(b.waiting, job.ID)
		b.mu.Unlock()
	}()

	select {
	case b.jobs <- job:
	default:
		return "", errors.New("the browser model has too many requests waiting")
	}
	ctx, cancel := context.WithTimeout(ctx, AnswerWait)
	defer cancel()
	select {
	case r := <-ch:
		return r.text, r.err
	case <-ctx.Done():
		return "", errors.New("the browser model did not answer in time: " + ctx.Err().Error())
	}
}

// newJobID is a name nobody can guess: only the tab that was handed the job knows it, and that is
// what lets that tab, and no other client holding the token, answer it.
func newJobID() string {
	var raw [16]byte
	_, _ = rand.Read(raw[:])
	return hex.EncodeToString(raw[:])
}

// Next is the work a tab asked for, held open for up to PollWait; false when there was none.
func (b *Broker) Next(ctx context.Context) (Job, bool) {
	b.seen()
	t := time.NewTimer(b.poll)
	defer t.Stop()
	for {
		select {
		case j := <-b.jobs:
			if !b.claim(j.ID) {
				// Its asker gave up (timed out or cancelled): running it would waste the card.
				continue
			}
			return j, true
		case <-t.C:
			return Job{}, false
		case <-ctx.Done():
			return Job{}, false
		}
	}
}

// claim marks the tab that took job id as busy, and reports whether anyone still waits for it.
func (b *Broker) claim(id string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if _, ok := b.waiting[id]; !ok {
		return false
	}
	b.lastSeen = b.now()
	b.busyUntil = b.lastSeen.Add(AnswerWait)
	return true
}

// Answer delivers what a tab wrote for a job: its text, or the reason it could not. False when
// nobody waits for that job any more (it timed out, or another tab answered first).
func (b *Broker) Answer(id, text, failure string) bool {
	b.mu.Lock()
	ch, ok := b.waiting[id]
	delete(b.waiting, id)
	if ok {
		b.busyUntil = time.Time{}
	}
	b.mu.Unlock()
	if !ok {
		return false
	}
	r := result{text: text}
	if len(text) > maxAnswer {
		r.text, failure = "", "the answer is too long"
	}
	if failure != "" {
		r.err = errors.New("the browser model failed: " + failure)
	}
	ch <- r
	return true
}
