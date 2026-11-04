// Package steer is the inbox of a turn in flight: what the person writes while the agent works.
//
// A message sent while a turn runs used to wait for the turn to end (a queue) or for its next
// round. The inbox makes it an interruption instead: the model call in flight is cancelled the
// moment the message arrives, the run adds the message to its context, and the round is asked
// again. Work already done stays: a command that is running is left to finish, its output is in
// the context, and only the model's unfinished reply is dropped.
package steer

import (
	"context"
	"errors"
	"strings"
	"sync"
)

// ErrSteered is the cause of a model call cancelled because the person wrote.
var ErrSteered = errors.New("the person sent a new message")

// Inbox holds the person's messages for the turn in flight. The zero value is closed: nothing
// reads it, and Push refuses so the caller queues the message instead.
type Inbox struct {
	mu      sync.Mutex
	open    bool
	notes   []string
	left    []string
	next    int
	cancels map[int]context.CancelCauseFunc
}

// Begin opens the inbox for a starting turn.
func (b *Inbox) Begin() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.open, b.notes = true, nil
}

// End closes it, so a message sent after the turn is not swallowed by it. The messages the turn
// took but never read are kept for Unread: they were accepted, so they must not be lost.
func (b *Inbox) End() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.left = append(b.left, b.notes...)
	b.open, b.notes = false, nil
}

// Unread returns, and forgets, the messages a finished turn accepted and never read, for the
// caller to send as the next turn.
func (b *Inbox) Unread() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := b.left
	b.left = nil
	return out
}

// Open says whether a turn is reading the inbox.
func (b *Inbox) Open() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.open
}

// Push hands the turn a message and interrupts the model calls in flight. It reports whether a
// turn was there to read it.
func (b *Inbox) Push(text string) bool {
	text = strings.TrimSpace(text)
	b.mu.Lock()
	defer b.mu.Unlock()
	if text == "" || !b.open {
		return false
	}
	b.notes = append(b.notes, text)
	for _, cancel := range b.cancels {
		cancel(ErrSteered)
	}
	return true
}

// Take returns the messages written since the last Take, and empties the inbox.
func (b *Inbox) Take() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := b.notes
	b.notes = nil
	return out
}

// Pending says whether messages are waiting to be read.
func (b *Inbox) Pending() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.notes) > 0
}

// Call returns the context for ONE model call: cancelled, with ErrSteered as its cause, when a
// message arrives (or already cancelled when one is waiting). done must be called when the call
// returns. A closed inbox hands back ctx as it is.
func (b *Inbox) Call(ctx context.Context) (context.Context, func()) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if !b.open {
		return ctx, func() {
			// Nothing was registered for a closed inbox: there is nothing to release.
		}
	}
	cctx, cancel := context.WithCancelCause(ctx)
	if len(b.notes) > 0 {
		cancel(ErrSteered)
	}
	if b.cancels == nil {
		b.cancels = map[int]context.CancelCauseFunc{}
	}
	id := b.next
	b.next++
	b.cancels[id] = cancel
	return cctx, func() {
		b.mu.Lock()
		delete(b.cancels, id)
		b.mu.Unlock()
		cancel(nil)
	}
}

// Interrupted says whether a call made with ctx (from Call) was cut by a message rather than
// failing or being stopped: the parent is still live and the cause is ErrSteered.
func Interrupted(parent, call context.Context) bool {
	return parent.Err() == nil && errors.Is(context.Cause(call), ErrSteered)
}

// Note is how a message the person sent mid-turn is put in front of the model.
func Note(notes []string) string {
	return "THE PERSON WROTE THIS WHILE YOU WORKED. It is part of the request from now on, and it " +
		"wins over what you were doing where they differ:\n- " + strings.Join(notes, "\n- ")
}
