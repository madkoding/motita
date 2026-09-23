package gateway

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/madkoding/starlight/internal/session"
)

// contextWithCancelForTest gives a test its own context without pulling context into every call
// site of the test file.
func contextWithCancelForTest() (context.Context, context.CancelFunc) {
	return context.WithCancel(context.Background())
}

// newTestRun builds a run with no conversation behind it, which is all the log needs: this file
// tests the LOG, and a run that needed a conversation to be constructed would drag HTTP into a test
// about numbering.
func newTestRun() *run {
	ctx, cancel := contextWithCancelForTest()
	return newRun("r1", ctx, cancel)
}

// TestTheLogReplaysFromTheBeginning: a client that connects to a run already in flight asks for
// everything it missed. Without that, "reconnect and resume" has nothing to resume from - the
// progress lines went to a socket and were gone.
func TestTheLogReplaysFromTheBeginning(t *testing.T) {
	r := newTestRun()
	for i := 0; i < 3; i++ {
		r.append(EventProgress, progressEvent{Text: "line"})
	}

	replay, info := r.since(0)
	if len(replay) != 3 {
		t.Fatalf("replaying from the beginning returned %d events, all three must be there", len(replay))
	}
	for i, e := range replay {
		if e.Seq != uint64(i+1) {
			t.Errorf("event %d has seq %d, the sequence must start at one and increase", i, e.Seq)
		}
		if e.Event != EventProgress {
			t.Errorf("event %d lost its name: %q", i, e.Event)
		}
	}
	if info.Dropped != 0 {
		t.Errorf("nothing was evicted, but the log reports %d dropped", info.Dropped)
	}
}

// TestTheLogReplaysFromWhereTheClientWas: the whole point of numbering the events. A client says
// "I saw up to 2", and gets 3 onwards - not the whole log again, and not a gap.
func TestTheLogReplaysFromWhereTheClientWas(t *testing.T) {
	r := newTestRun()
	for i := 0; i < 5; i++ {
		r.append(EventProgress, progressEvent{Text: "line"})
	}

	replay, _ := r.since(2)
	if len(replay) != 3 {
		t.Fatalf("replaying from 2 returned %d events, it must return 3, 4 and 5", len(replay))
	}
	if replay[0].Seq != 3 {
		t.Errorf("the replay starts at seq %d, it must start at 3", replay[0].Seq)
	}
}

// TestReplayingPastTheEndIsEmptyAndNotAnError: a client that reconnects after the run finished, and
// says "I saw up to 99", must be told there is nothing more - not handed the whole log again, which
// would make it render the turn twice.
func TestReplayingPastTheEndIsEmptyAndNotAnError(t *testing.T) {
	r := newTestRun()
	r.append(EventProgress, progressEvent{Text: "line"})

	replay, info := r.since(50)
	if len(replay) != 0 {
		t.Errorf("replaying from 50 on a one-event log returned %d events, it must return none", len(replay))
	}
	if info.LastSeq != 1 {
		t.Errorf("the log says its newest event is %d, it must say 1 so the client can see it is current", info.LastSeq)
	}
}

// TestAGapIsReportedRatherThanHidden: the log is bounded, so a client that was away long enough
// cannot be given what it missed. The honest answer is to SAY so - a client that silently renders a
// conversation with a hole in it is worse than one that says "I lost some lines".
func TestAGapIsReportedRatherThanHidden(t *testing.T) {
	r := newTestRun()
	total := maxLoggedEvents + 10
	for i := 0; i < total; i++ {
		r.append(EventProgress, progressEvent{Text: "line"})
	}

	if _, info := r.since(0); info.Dropped != 10 {
		t.Errorf("the log reports %d dropped events, it must report the 10 that were evicted", info.Dropped)
	}
	replay, info := r.since(0)
	if info.FirstSeq != uint64(11) {
		t.Errorf("the oldest event the log still holds has seq %d, it must be 11", info.FirstSeq)
	}
	if replay[0].Seq != info.FirstSeq {
		t.Errorf("the replay starts at %d but the log says %d is the oldest, so a client cannot tell what it missed",
			replay[0].Seq, info.FirstSeq)
	}
}

// TestTheLogDoesNotGrowWithoutBound: the reason the cap exists. A turn can emit thousands of lines,
// and an unbounded log is a memory leak with a nice name.
func TestTheLogDoesNotGrowWithoutBound(t *testing.T) {
	r := newTestRun()
	for i := 0; i < maxLoggedEvents*3; i++ {
		r.append(EventProgress, progressEvent{Text: "line"})
	}

	r.mu.Lock()
	held := len(r.log)
	r.mu.Unlock()
	if held != maxLoggedEvents {
		t.Errorf("the log holds %d events, the cap is %d", held, maxLoggedEvents)
	}
}

// TestThePayloadIsKeptVerbatim: replaying must not re-encode. A payload that went out once and is
// re-encoded for a reconnecting client is a second encoding that can disagree with the first.
func TestThePayloadIsKeptVerbatim(t *testing.T) {
	r := newTestRun()
	// A newline inside the text is the case that matters: it is escaped in JSON, and a
	// re-encoding that got it wrong would split the frame.
	r.append(EventProgress, progressEvent{Text: "a line\nwith a newline in it"})

	replay, _ := r.since(0)
	if len(replay) != 1 {
		t.Fatalf("the log returned %d events, want 1", len(replay))
	}
	var back progressEvent
	if err := json.Unmarshal(replay[0].Data, &back); err != nil {
		t.Fatalf("the stored payload is not decodable: %v", err)
	}
	if back.Text != "a line\nwith a newline in it" {
		t.Errorf("the payload came back as %q, it must be byte-for-byte what went out", back.Text)
	}
	if got := string(replay[0].Data); !json.Valid([]byte(got)) {
		t.Errorf("the stored payload is not valid JSON: %s", got)
	}
}

// TestASubscriberReceivesWhatIsAppendedAfterIt: it is the live half. The replay covers what already
// happened and this covers what happens next, and a client needs both or it stops mid-turn.
func TestASubscriberReceivesWhatIsAppendedAfterIt(t *testing.T) {
	r := newTestRun()
	sub := r.subscribe()
	defer r.unsubscribe(sub)

	r.append(EventProgress, progressEvent{Text: "hello"})
	r.append(EventDone, doneEvent{Result: "finished"})

	for _, want := range []string{EventProgress, EventDone} {
		select {
		case got := <-sub.ch:
			if got.Event != want {
				t.Fatalf("the subscriber received %q, it must receive %q", got.Event, want)
			}
		case <-sub.lost:
			t.Fatalf("the subscriber was dropped before it received %q", want)
		}
	}
}

// TestASlowSubscriberIsCutOffRatherThanSilentlySkipped: when a subscriber's buffer fills, dropping
// events would leave it rendering a stream with invisible holes. Cutting it off is self-healing -
// the client reconnects and replays from its last sequence number - and it is a fact the client can
// act on instead of a lie it cannot see.
func TestASlowSubscriberIsCutOffRatherThanSilentlySkipped(t *testing.T) {
	r := newTestRun()
	sub := r.subscribe()
	defer r.unsubscribe(sub)

	// Overfill it: nobody is reading the channel.
	for i := 0; i < subscriberBuffer+1; i++ {
		r.append(EventProgress, progressEvent{Text: "line"})
	}

	select {
	case <-sub.lost:
		// Cut off, which is the contract. Its stream ends and the client re-attaches.
	case <-sub.ch:
		// Reading one is fine; the signal is what matters, so drain and look again.
		select {
		case <-sub.lost:
		default:
			t.Fatal("a subscriber that could not keep up was left attached, so it would render a stream with holes in it")
		}
	default:
		t.Fatal("a subscriber that could not keep up was neither fed nor cut off")
	}
}

// TestUnsubscribingStopsDelivery: a disconnected client must not keep a channel alive, or every
// reconnection leaks one until the process runs out.
func TestUnsubscribingStopsDelivery(t *testing.T) {
	r := newTestRun()
	sub := r.subscribe()
	r.unsubscribe(sub)
	r.append(EventProgress, progressEvent{Text: "line"})

	// Nothing must panic and nothing must block. The channel may hold a buffered event, so the
	// assertion is that the subscriber is no longer registered.
	if n := r.subscriberCount(); n != 0 {
		t.Fatalf("the run still holds %d subscribers after one unsubscribed", n)
	}
}

// TestUnsubscribingTwiceIsSafe: a handler whose client vanished can reach the unsubscribe path from
// two places (the defer and the cut-off branch), and the second one must be a no-op rather than a
// panic that takes the process with it.
func TestUnsubscribingTwiceIsSafe(t *testing.T) {
	r := newTestRun()
	sub := r.subscribe()
	r.unsubscribe(sub)
	r.unsubscribe(sub)

	if n := r.subscriberCount(); n != 0 {
		t.Errorf("the run holds %d subscribers, want 0", n)
	}
}

// TestFinishingTellsEverySubscriberToStop: a reader waiting on an empty log has to wake up when the
// run ends, or it hangs on its socket until the client gives up - and a finished run with no events
// is exactly the case where there is nothing to wake it.
func TestFinishingTellsEverySubscriberToStop(t *testing.T) {
	r := newTestRun()
	sub := r.subscribe()
	defer r.unsubscribe(sub)

	r.finish("done", "the result", "", session.Snapshot{})

	select {
	case <-sub.lost:
		// The reader was told, which is what lets it close its stream.
	default:
		t.Fatal("finishing the run left a subscriber attached with nothing to read")
	}
	if _, _, _, finished := r.outcomeOf(); !finished {
		t.Error("the run reports itself unfinished after finishing")
	}
}

// TestARunFinishesExactlyOnce: the outcome is a statement about the turn, and a second one would
// overwrite the first - which is how a cancelled run would end up reported as a success.
func TestARunFinishesExactlyOnce(t *testing.T) {
	r := newTestRun()
	r.finish("cancelled", "", "the user stopped it", session.Snapshot{})
	r.finish("done", "a result", "", session.Snapshot{})

	outcome, result, errText, _ := r.outcomeOf()
	if outcome != "cancelled" {
		t.Errorf("the run reports %q, the first outcome must stand", outcome)
	}
	if result != "" || errText != "the user stopped it" {
		t.Errorf("the second finish overwrote the first: result=%q err=%q", result, errText)
	}
	// And closing done twice would panic, so reaching here is part of the assertion.
	select {
	case <-r.done:
	default:
		t.Error("the run is finished but done is not closed")
	}
}

// TestAnUnencodableEventIsDroppedRatherThanKillingTheRun: the payloads this package emits are
// always encodable, so the branch is reached with a value that genuinely cannot be - a channel.
// Dropping the event is right: a turn that dies because one progress line could not be encoded
// would lose the whole turn over a line nobody needed.
func TestAnUnencodableEventIsDroppedRatherThanKillingTheRun(t *testing.T) {
	r := newTestRun()
	r.append(EventProgress, progressEvent{Text: "before"})

	// A channel cannot be marshalled, so append takes its error branch and returns 0.
	if seq := r.append(EventProgress, make(chan int)); seq != 0 {
		t.Errorf("an unencodable event was given sequence %d, it must be dropped with 0", seq)
	}

	// And the run is intact: the good event is still there and the sequence did not advance.
	replay, info := r.since(0)
	if len(replay) != 1 {
		t.Fatalf("the log holds %d events, the encodable one must survive", len(replay))
	}
	if info.LastSeq != 1 {
		t.Errorf("the sequence is at %d, a dropped event must not consume a number", info.LastSeq)
	}
}

// TestARunReadsWellInAFailureMessage: runs end up in logs and test output, and the default Go
// formatting of a struct with a mutex and channels is unreadable.
func TestARunReadsWellInAFailureMessage(t *testing.T) {
	r := newTestRun()
	if got := r.String(); !strings.Contains(got, "r1") || !strings.Contains(got, "in flight") {
		t.Errorf("String() = %q, it must name the run and its state", got)
	}
	r.finish("done", "a result", "", session.Snapshot{})
	if got := r.String(); !strings.Contains(got, "done") {
		t.Errorf("String() = %q, it must report the outcome once there is one", got)
	}
}
