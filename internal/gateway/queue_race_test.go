package gateway

import (
	"net/http"
	"strings"
	"testing"
)

// queueOf is a copy of a conversation's queue, read under its lock.
func queueOf(c *conversation) []string {
	c.stateMu.Lock()
	defer c.stateMu.Unlock()
	return append([]string(nil), c.queue...)
}

// waitIdle waits until nothing holds the conversation's slot and nothing is left in its queue.
func waitIdle(t *testing.T, c *conversation) {
	t.Helper()
	waitUntil(t, "the conversation to go idle", func() bool { return !c.isRunning() && len(queueOf(c)) == 0 })
}

// The lost wakeup, reproduced: the run holding the slot ends - and drains - right after the
// message was refused a start. The old code queued the message only after that, so the drain had
// already found the queue empty and the message stayed there with nobody left to start it. Now the
// refusal and the enqueue are one step, and the drain of the ending run starts the message.
func TestQueuedMessageIsNotStrandedByARunEndingInBetween(t *testing.T) {
	srv, rec := newQueueServer(t)
	a := newSessionFor(t, srv)
	quietRunFor(t, srv, a.ID)
	c := srv.conversationOf(a.ID)
	afterQueued = func() { srv.releaseAndDrain(c) }
	t.Cleanup(func() { afterQueued = func() {} })

	c.takeRunSlot()
	w := post(t, srv, "/v1/sessions/"+a.ID+"/queue", `{"task":"late"}`, testToken)
	if w.Code != http.StatusAccepted || !strings.Contains(w.Body.String(), `"queued":true`) {
		t.Fatalf("queue while held: %d %s", w.Code, w.Body.String())
	}
	waitUntil(t, "the queued message to run", func() bool { return rec.ran("late") })
	waitIdle(t, c)
}

// The deterministic halves of the race above: queued while held, then drained by the release;
// and offered after the release's drain, then started at once.
func TestQueueBothOrdersOfReleaseAndOffer(t *testing.T) {
	srv, rec := newQueueServer(t)
	a := newSessionFor(t, srv)
	quietRunFor(t, srv, a.ID)
	c := srv.conversationOf(a.ID)

	c.takeRunSlot()
	w := post(t, srv, "/v1/sessions/"+a.ID+"/queue", `{"task":"held"}`, testToken)
	if w.Code != http.StatusAccepted || !strings.Contains(w.Body.String(), `"position":1`) {
		t.Fatalf("queue while held: %d %s", w.Code, w.Body.String())
	}
	srv.releaseAndDrain(c)
	waitUntil(t, "the held message to run", func() bool { return rec.ran("held") })
	waitIdle(t, c)

	c.takeRunSlot()
	srv.releaseAndDrain(c)
	if started, _ := srv.sendToAgent(c, "after"); !started {
		t.Fatal("a message offered to an idle conversation must start")
	}
	waitUntil(t, "the late message to run", func() bool { return rec.ran("after") })
	waitIdle(t, c)
}

// An interruption cancels the run it found, never the one that started after it: when the run ends
// on its own between the interruption's check and its cancel, the queued message starts as the
// next run, and cancelling "whatever is current" (the old code) stopped exactly that message.
func TestInterruptCancelsTheRunItFoundNotTheNextOne(t *testing.T) {
	srv, rec := newQueueServer(t)
	a := newSessionFor(t, srv)
	quietRunFor(t, srv, a.ID)
	t.Cleanup(func() { close(rec.release) })
	abandon := startInBackground(t, srv, a.ID, "/task", `{"task":"block-1"}`)
	defer abandon()
	waitBlocked(t, rec)
	c := srv.conversationOf(a.ID)
	first, _ := c.currentRun()
	afterQueuedBehindRun = func() {
		// The run ends by itself in the window, and its drain starts the message.
		first.cancel()
		waitBlocked(t, rec)
	}
	t.Cleanup(func() { afterQueuedBehindRun = func() {} })

	w := post(t, srv, "/v1/sessions/"+a.ID+"/interrupt", `{"task":"block-2"}`, testToken)
	if w.Code != http.StatusNoContent {
		t.Fatalf("interrupt: %d %s", w.Code, w.Body.String())
	}
	next, ok := c.currentRun()
	if !ok || next == first || !rec.ran("block-2") {
		t.Fatal("the queued message did not start as a run of its own")
	}
	if next.ctx.Err() != nil {
		t.Fatal("the interruption cancelled the run that started after the one it found")
	}
}

// A slot held by something that is not a run (a WebSocket query) still accepts an interruption
// with a message: there is nothing to cancel, and the message runs when the holder lets go.
func TestInterruptWhileTheSlotIsHeldWithoutARun(t *testing.T) {
	srv, rec := newQueueServer(t)
	a := newSessionFor(t, srv)
	quietRunFor(t, srv, a.ID)
	c := srv.conversationOf(a.ID)

	c.takeRunSlot()
	w := post(t, srv, "/v1/sessions/"+a.ID+"/interrupt", `{"task":"after-query"}`, testToken)
	if w.Code != http.StatusNoContent {
		t.Fatalf("interrupt: %d %s", w.Code, w.Body.String())
	}
	srv.releaseAndDrain(c)
	waitUntil(t, "the message to run", func() bool { return rec.ran("after-query") })
	waitIdle(t, c)
}

// A conversation that cannot run keeps the message offered to it and starts nothing, and its
// drain leaves the queue alone.
func TestStartOrQueueKeepsTheMessageOfAnIntegratedSession(t *testing.T) {
	srv, rec := newQueueServer(t)
	a := newSessionFor(t, srv)
	c := srv.conversationOf(a.ID)
	c.stateMu.Lock()
	c.merged = true
	c.stateMu.Unlock()

	if started, pos := srv.startOrQueue(c, "kept"); started || pos != 1 {
		t.Fatalf("an integrated session started a message: %v %d", started, pos)
	}
	srv.drainQueue(c)
	if got := queueOf(c); len(got) != 1 || got[0] != "kept" {
		t.Fatalf("the message must stay queued: %q", got)
	}
	if rec.ran("kept") || c.isRunning() {
		t.Fatal("an integrated session ran a queued message")
	}
}

// A message sent while the slot is free but older messages are still queued (a run ended and has
// not drained yet) waits behind them instead of jumping ahead, and the queue is drained at once.
func TestANewMessageDoesNotJumpTheQueue(t *testing.T) {
	srv, rec := newQueueServer(t)
	a := newSessionFor(t, srv)
	quietRunFor(t, srv, a.ID)
	c := srv.conversationOf(a.ID)
	c.pushQueue("older")

	if started, pos := srv.startOrQueue(c, "newer"); started || pos != 2 {
		t.Fatalf("the new message must queue behind the older one: %v %d", started, pos)
	}
	waitUntil(t, "both messages to run", func() bool { return rec.ran("older") && rec.ran("newer") })
	waitIdle(t, c)
	rec.mu.Lock()
	defer rec.mu.Unlock()
	if rec.tasks[0] != "older" {
		t.Fatalf("the older message must run first, order: %v", rec.tasks)
	}
}
