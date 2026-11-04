package steer

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// A closed inbox refuses messages and leaves calls alone; an open one takes them, cuts the calls
// in flight, and keeps what the turn did not read once it ends.
func TestTheInboxCutsTheCallInFlight(t *testing.T) {
	var b Inbox
	if b.Push("early") || b.Open() {
		t.Fatal("a closed inbox takes nothing")
	}
	ctx := context.Background()
	if c, done := b.Call(ctx); c != ctx {
		t.Fatal("a closed inbox hands the context back as it is")
	} else {
		done()
	}

	b.Begin()
	call, done := b.Call(ctx)
	if b.Push("   ") || !b.Open() {
		t.Fatal("a blank message is not taken")
	}
	if !b.Push(" turn left ") || call.Err() == nil || !Interrupted(ctx, call) {
		t.Fatal("a message cuts the call in flight")
	}
	done()
	if !b.Pending() {
		t.Fatal("the message waits to be read")
	}

	// A call made while a message waits is cut at once.
	again, done2 := b.Call(ctx)
	if !Interrupted(ctx, again) {
		t.Fatal("a call made with a message waiting is cut")
	}
	done2()
	if got := b.Take(); len(got) != 1 || got[0] != "turn left" || b.Pending() {
		t.Fatalf("take: %q", got)
	}

	b.Push("late")
	b.End()
	if b.Push("after") {
		t.Fatal("an ended turn takes nothing")
	}
	if got := b.Unread(); len(got) != 1 || got[0] != "late" || b.Unread() != nil {
		t.Fatalf("unread: %q", got)
	}
}

// A stopped turn is not a steered one, and the note says what the person wrote.
func TestAStopIsNotASteer(t *testing.T) {
	var b Inbox
	b.Begin()
	parent, stop := context.WithCancel(context.Background())
	call, done := b.Call(parent)
	stop()
	if Interrupted(parent, call) || !errors.Is(call.Err(), context.Canceled) {
		t.Fatal("a stop is not a message")
	}
	done()
	if n := Note([]string{"a", "b"}); !strings.Contains(n, "WHILE YOU WORKED") || !strings.Contains(n, "- a\n- b") {
		t.Fatalf("note: %q", n)
	}
}
