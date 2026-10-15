package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/madkoding/motita/internal/session"
)

// lastRunOf reads the run that ended last in a conversation.
func lastRunOf(t *testing.T, c *conversation) *run {
	t.Helper()
	c.stateMu.Lock()
	defer c.stateMu.Unlock()
	if c.lastRun == nil {
		t.Fatal("no run has ended in this conversation")
	}
	return c.lastRun
}

// A client whose connection dropped just before its run ended reattaches by the run's id and is
// given the run's outcome, not a 404 that would leave it without the answer the agent gave.
func TestAReattachByIDAfterTheRunEndedGetsItsOutcome(t *testing.T) {
	srv := newTestServer(t, &fakeService{task: func(context.Context, string, func(string, ...any)) (string, error) {
		return "the answer", nil
	}})
	collect(t, srv, http.MethodPost, sessionPath(srv, DefaultSession, "/task"), `{"task":"x"}`)
	rn := lastRunOf(t, srv.sessions[DefaultSession])

	events := collectFrom(t, srv, sessionPath(srv, DefaultSession, "/events?from=0&run="+rn.id))
	var pre attachedEvent
	if len(events) == 0 || json.Unmarshal([]byte(events[0].Data), &pre) != nil || pre.RunID != rn.id {
		t.Fatalf("the preamble must name the run asked for: %+v", events)
	}
	last := events[len(events)-1]
	if last.Event != EventDone || !strings.Contains(last.Data, "the answer") {
		t.Fatalf("the reattach did not deliver the outcome: %+v", events)
	}
}

// The run named is the one in flight: the client is attached to it as before.
func TestAReattachByIDToTheRunInFlightAttaches(t *testing.T) {
	srv := newTestServer(t, &fakeService{})
	conv := srv.sessions[DefaultSession]
	rn := newTestRun()
	rn.append(EventDone, doneEvent{Result: "the answer"})
	rn.finish("done", "the answer", "", session.Snapshot{})
	conv.setCurrentRun(rn)
	defer conv.clearCurrentRun()

	events := collectFrom(t, srv, sessionPath(srv, DefaultSession, "/events?from=0&run="+rn.id))
	if len(events) < 2 || events[1].Event != EventDone {
		t.Fatalf("events = %+v", events)
	}
}

// Another run took the place of the one named: attaching to it would show this client a turn it
// did not start, so the reattach is refused with 409.
func TestAReattachToARunThatWasReplacedIsAConflict(t *testing.T) {
	srv := newTestServer(t, &fakeService{})
	conv := srv.sessions[DefaultSession]
	conv.setCurrentRun(newTestRun())
	defer conv.clearCurrentRun()

	w := get(t, srv, sessionPath(srv, DefaultSession, "/events?from=0&run=an-older-run"), testToken)
	if w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "an-older-run") {
		t.Fatalf("status = %d %s, want 409 naming the run", w.Code, w.Body.String())
	}
}

// A run that is gone, with nothing in flight, is 410: the client stops trying.
func TestAReattachToAGoneRunIsGone(t *testing.T) {
	srv := newTestServer(t, &fakeService{})
	w := get(t, srv, sessionPath(srv, DefaultSession, "/events?from=0&run=never-was"), testToken)
	if w.Code != http.StatusGone {
		t.Fatalf("status = %d %s, want 410", w.Code, w.Body.String())
	}
}

// The question being asked belongs to the run in flight: a client attached to the run before it
// is not handed it, or it would answer a question it was never asked.
func TestThePendingQuestionIsOnlyForTheRunInFlight(t *testing.T) {
	c := newConversation("s", &fakeService{})
	live, old := newTestRun(), newTestRun()
	p := &pendingApproval{id: "q1"}
	c.setCurrentRun(live)
	c.setPendingApproval(p)
	if got := c.pendingFor(live); got != p {
		t.Errorf("the run in flight must be given its question, got %+v", got)
	}
	if got := c.pendingFor(old); got != nil {
		t.Errorf("another run was handed the question: %+v", got)
	}
}

// endRun keeps the run that ended, and runByID finds each run by its own id.
func TestRunByID(t *testing.T) {
	c := newConversation("s", &fakeService{})
	first := newRun("first", context.Background(), func() {}, "")
	if !c.takeRunSlot() {
		t.Fatal("slot")
	}
	c.setCurrentRun(first)
	if rn, st := c.runByID("first"); rn != first || st != http.StatusOK {
		t.Fatalf("the run in flight: %v %d", rn, st)
	}
	c.endRun(first)
	if rn, st := c.runByID("first"); rn != first || st != http.StatusOK {
		t.Fatalf("the run that ended: %v %d", rn, st)
	}
	if _, st := c.runByID("other"); st != http.StatusGone {
		t.Fatalf("an unknown run with nothing in flight: %d", st)
	}
	c.setCurrentRun(newRun("second", context.Background(), func() {}, ""))
	if _, st := c.runByID("other"); st != http.StatusConflict {
		t.Fatalf("an unknown run with another in flight: %d", st)
	}
}
