package gateway

// Attach tests for the gateway package: the run's log at the level the run owns it, and a
// connection that fails part-way through an attached stream.

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/madkoding/motita/internal/session"
)

// --- the log, at the level the run owns it -------------------------------------------------------

// TestAQueuedEventIsHandedOverWhenTheRunEnds: an event can reach the subscriber's channel without
// being in the replay - it is appended after `since` read the log, or it is dropped from the replay
// because the client already had it. If nothing drained the queue, the run's `done` would be
// selected first and the event would be stranded: the client would be told the turn finished
// without ever being shown its last line.
//
// This drives the branch attach takes when it sees `done`, which is the only place the queue is
// drained for an event the replay did not carry.
func TestAQueuedEventIsHandedOverWhenTheRunEnds(t *testing.T) {
	rn := newTestRun()
	sub := rn.subscribe()
	defer rn.unsubscribe(sub)
	rn.append(EventProgress, progressEvent{Text: "queued"})
	rn.finish("done", "the result", "", session.Snapshot{})

	// The client says it already had seq 1, so the replay is empty and the queue holds the only
	// copy of that event.
	replay, _ := rn.since(1)
	if len(replay) != 0 {
		t.Fatalf("the replay should be empty, got %+v", replay)
	}
	var out bytes.Buffer
	fw := &flushableBuffer{&out}
	// `after` is where the replay stopped as far as this client is concerned: 1.
	if err := rn.drainFrom(fw, http.NewResponseController(fw), sub, 0); err != nil {
		t.Fatalf("drainFrom: %v", err)
	}
	if !strings.Contains(out.String(), "queued") {
		t.Errorf("the queued event was not handed over: %q", out.String())
	}
}

// TestDrainingSkipsWhatTheReplayAlreadyCarried: the same event can be in the replay AND in the
// queue, and sending it twice would make the client render one line as two. The sequence number is
// what tells them apart.
func TestDrainingSkipsWhatTheReplayAlreadyCarried(t *testing.T) {
	rn := newTestRun()
	sub := rn.subscribe()
	defer rn.unsubscribe(sub)
	rn.append(EventProgress, progressEvent{Text: "already replayed"})

	_, info := rn.since(0)
	var out bytes.Buffer
	fw := &flushableBuffer{&out}
	if err := rn.drainFrom(fw, http.NewResponseController(fw), sub, info.LastSeq); err != nil {
		t.Fatalf("drainFrom: %v", err)
	}
	if out.Len() != 0 {
		t.Errorf("an event the replay already carried was sent again: %q", out.String())
	}
}

// TestDrainingStopsOnAFailedWrite: draining writes what is left, and a write that fails ends it.
// Retrying into a socket that is gone would spin for a client that is never coming back.
func TestDrainingStopsOnAFailedWrite(t *testing.T) {
	rn := newTestRun()
	sub := rn.subscribe()
	defer rn.unsubscribe(sub)
	rn.append(EventProgress, progressEvent{Text: "one"})

	rc := http.NewResponseController(noFlushWriter{})
	if err := rn.drainFrom(noFlushWriter{}, rc, sub, 0); err == nil {
		t.Error("a failed write while draining must be reported, so the caller stops")
	}
}

// TestAReaderThatCannotKeepUpIsToldAndDetached: the subscriber's buffer fills when the run outruns
// the reader, and the honest answer is to CUT IT OFF and say so. Dropping events would leave the
// client rendering a stream with invisible holes; being told lets it reconnect from its last
// sequence number and be given the rest.
func TestAReaderThatCannotKeepUpIsToldAndDetached(t *testing.T) {
	rn := newTestRun()
	sub := rn.subscribe()
	defer rn.unsubscribe(sub)

	// Nobody is reading the channel, so the run outruns it.
	for i := 0; i < subscriberBuffer+1; i++ {
		rn.append(EventProgress, progressEvent{Text: "line"})
	}

	select {
	case <-sub.lost:
	case <-time.After(5 * time.Second):
		t.Fatal("a reader that could not keep up was never told, so it would render a stream with holes")
	}
	// And nothing was lost: the events are in the log for whoever attaches next.
	if replay, _ := rn.since(0); len(replay) != subscriberBuffer+1 {
		t.Errorf("the log holds %d events, none may be lost when a reader is cut off", len(replay))
	}
}

// --- a connection that fails part-way ------------------------------------------------------------
//
// The write order inside attach is: 1 the ": connected" line, 2 the preamble, then the replay, then
// whatever was queued, then the live events.
//
// Every test below drives a WRITE that fails, and a real socket cannot be asked to die on demand.
// scriptedWriter is a connection told which write to fail or block, which is the only way to reach
// these paths - and an error path nothing can provoke is an error path that rots.

// scriptedWriter is a connection whose body writes can be made to fail or to block.
//
// It implements Flush, so http.ResponseController accepts it and the handler goes on to the NEXT
// write: without that, the stream would stop at the headers and everything below would be
// unreachable.
type scriptedWriter struct {
	mu  sync.Mutex
	n   int
	buf bytes.Buffer
	// failAt is the write that fails, counted from 1. Zero never fails.
	failAt int
	// blockAt is the write after which to wait for release. Zero never blocks.
	blockAt int
	release chan struct{}
}

func (s *scriptedWriter) Header() http.Header { return http.Header{} }

func (s *scriptedWriter) WriteHeader(int) {}

func (s *scriptedWriter) Flush() {}

func (s *scriptedWriter) Write(p []byte) (int, error) {
	s.mu.Lock()
	s.n++
	n := s.n
	s.mu.Unlock()

	// The lock is NOT held while blocking: the test has to be able to look at the writer, and a
	// lock held across a wait is a deadlock waiting for a slow machine.
	if s.blockAt != 0 && n >= s.blockAt && s.release != nil {
		<-s.release
	}
	if s.failAt != 0 && n == s.failAt {
		return 0, fmt.Errorf("the connection is gone")
	}
	s.mu.Lock()
	s.buf.Write(p)
	s.mu.Unlock()
	return len(p), nil
}

func (s *scriptedWriter) body() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.String()
}

// writeCount is how many writes have been made, for a test waiting for the handler to get somewhere.
func (s *scriptedWriter) writeCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.n
}

// attachInBackground runs attach against a scripted writer and reports when it returned.
func attachInBackground(t *testing.T, srv *Server, conv *conversation, rn *run, w http.ResponseWriter) <-chan struct{} {
	t.Helper()
	done := make(chan struct{})
	go func() {
		defer close(done)
		req := httptest.NewRequest(http.MethodGet, sessionPath(srv, DefaultSession, "/events"), nil)
		srv.attach(w, req, conv, rn, 0)
	}()
	return done
}

// waitForWrites blocks until the writer has been written to at least n times.
func waitForWrites(t *testing.T, w *scriptedWriter, n int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if w.writeCount() >= n {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("the handler only made %d writes, it never reached %d", w.writeCount(), n)
}

// TestTheStreamStopsWhenTheClientIsGoneAtThePreamble: a client that vanished between the headers and
// the first event is a reader that failed, and the handler must let it go. The RUN is untouched -
// that is the property the whole design is for.
func TestTheStreamStopsWhenTheClientIsGoneAtThePreamble(t *testing.T) {
	srv := newTestServer(t, &fakeService{})
	conv := srv.sessions[DefaultSession]
	rn := newTestRun()
	rn.append(EventProgress, progressEvent{Text: "one"})

	done := attachInBackground(t, srv, conv, rn, &scriptedWriter{failAt: 2})
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("attach never returned after its preamble could not be written")
	}
	if _, _, _, finished := rn.outcomeOf(); finished {
		t.Error("a failed write finished the run, which is a statement about the reader and not the turn")
	}
	// The event is in the log, which is what makes the failed reader recoverable.
	if replay, _ := rn.since(0); len(replay) != 1 {
		t.Errorf("the log holds %d events, the one nobody could be sent must stay", len(replay))
	}
}

// TestTheStreamStopsWhenAReplayedEventCannotBeWritten: the replay is a loop of writes and the
// connection can die in the middle of it. The handler stops at the first failure instead of pushing
// the rest into a socket that is gone.
func TestTheStreamStopsWhenAReplayedEventCannotBeWritten(t *testing.T) {
	srv := newTestServer(t, &fakeService{})
	conv := srv.sessions[DefaultSession]
	rn := newTestRun()
	rn.append(EventProgress, progressEvent{Text: "one"})

	// 3 is the first replayed event: 1 the connection line, 2 the preamble.
	done := attachInBackground(t, srv, conv, rn, &scriptedWriter{failAt: 3})
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("attach never returned after a replayed event could not be written")
	}
}

// TestTheStreamStopsWhenALiveEventCannotBeWritten: the steady state of a run is events arriving and
// being written out. A connection that dies there is the commonest way a client goes away, and the
// handler must return rather than keep writing into nothing.
func TestTheStreamStopsWhenALiveEventCannotBeWritten(t *testing.T) {
	srv := newTestServer(t, &fakeService{})
	conv := srv.sessions[DefaultSession]
	rn := newTestRun()

	// Nothing to replay and nothing queued, so 3 is the first LIVE event. The test waits for the
	// preamble before appending, so the handler really is in its read loop.
	w := &scriptedWriter{failAt: 3}
	done := attachInBackground(t, srv, conv, rn, w)
	waitForWrites(t, w, 2)
	rn.append(EventProgress, progressEvent{Text: "live"})

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("attach never returned after a live event could not be written")
	}
}

// TestAReaderCutOffWhileAttachedIsToldAndReleased: the run cuts a slow reader off, and the handler
// has to act on it - write the refusal that tells the client it can reconnect, and return. Left
// alone, the reader would sit on a socket for a run it will never be told about again.
func TestAReaderCutOffWhileAttachedIsToldAndReleased(t *testing.T) {
	srv := newTestServer(t, &fakeService{})
	conv := srv.sessions[DefaultSession]
	rn := newTestRun()

	// The writer blocks on the preamble, so the handler is NOT reading its channel while the run
	// fills it - which is what a stalled socket looks like, and the only way to make the run decide
	// the reader cannot keep up.
	w := &scriptedWriter{blockAt: 2, release: make(chan struct{})}
	done := attachInBackground(t, srv, conv, rn, w)
	waitForWrites(t, w, 2)

	// The handler has subscribed by now; overfilling the queue is what triggers the cut-off.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && rn.subscriberCount() == 0 {
		time.Sleep(time.Millisecond)
	}
	for i := 0; i < subscriberBuffer+1; i++ {
		rn.append(EventProgress, progressEvent{Text: "line"})
	}
	close(w.release)

	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("a reader that was cut off was never released from its socket")
	}
	if !strings.Contains(w.body(), "fell behind") {
		t.Errorf("the cut-off reader was not told why, so the client cannot reconnect knowingly: %q", w.body())
	}
	if n := rn.subscriberCount(); n != 0 {
		t.Errorf("the run still holds %d subscribers after the handler returned", n)
	}
}

// --- questions the run already had answered --------------------------------------------------------

// replayFor attaches a client to a finished run and returns what it was sent.
func replayFor(t *testing.T, srv *Server, conv *conversation, rn *run) string {
	t.Helper()
	rn.finish("done", "the result", "", session.Snapshot{})
	w := &scriptedWriter{}
	select {
	case <-attachInBackground(t, srv, conv, rn, w):
	case <-time.After(5 * time.Second):
		t.Fatal("attach never returned for a finished run")
	}
	return w.body()
}

// TestAReloadIsNotAskedAQuestionAlreadyAnswered: the log keeps every approval the turn asked, and a
// client that reattaches (a reload) draws the last one it reads as open. An answered approval came
// back on screen as if it had never been answered, and answering it again was refused.
func TestAReloadIsNotAskedAQuestionAlreadyAnswered(t *testing.T) {
	srv := newTestServer(t, &fakeService{})
	conv := srv.sessions[DefaultSession]
	rn := newTestRun()
	conv.setCurrentRun(rn)
	rn.append(EventApproval, approvalEvent{ID: "answered-1", Command: "rm -rf ./build"})
	rn.append(EventProgress, progressEvent{Text: "after the answer"})

	body := replayFor(t, srv, conv, rn)
	if strings.Contains(body, "event: approval") || strings.Contains(body, "answered-1") {
		t.Errorf("an answered approval was replayed as open:\n%s", body)
	}
	if !strings.Contains(body, "after the answer") {
		t.Errorf("the rest of the log was not replayed:\n%s", body)
	}
}

// TestAReloadIsStillAskedTheQuestionThatIsOpen: the approval the run is blocked on right now is
// replayed, so a client that comes back can still answer it; the ones before it are not.
func TestAReloadIsStillAskedTheQuestionThatIsOpen(t *testing.T) {
	srv := newTestServer(t, &fakeService{})
	conv := srv.sessions[DefaultSession]
	rn := newTestRun()
	conv.setCurrentRun(rn)
	rn.append(EventApproval, approvalEvent{ID: "answered-1", Command: "rm -rf ./build"})
	rn.append(EventApproval, approvalEvent{ID: "open-2", Command: "rm -rf ./dist"})
	conv.setPendingApproval(&pendingApproval{id: "open-2", ch: make(chan bool, 1), command: "rm -rf ./dist"})

	body := replayFor(t, srv, conv, rn)
	if strings.Contains(body, "answered-1") {
		t.Errorf("an answered approval was replayed beside the open one:\n%s", body)
	}
	if strings.Count(body, "event: approval\n") != 1 || !strings.Contains(body, "rm -rf ./dist") {
		t.Errorf("the open approval must be replayed once:\n%s", body)
	}
}

// An approval whose payload cannot be read names no question, so it cannot be the open one.
func TestAnUnreadableApprovalIsNotReplayed(t *testing.T) {
	e := loggedEvent{Seq: 1, Event: EventApproval, Data: []byte("{")}
	if !answeredApproval(e, &approvalEvent{ID: "open-2"}) {
		t.Error("an approval that cannot be read was replayed as the open one")
	}
	if answeredApproval(loggedEvent{Seq: 2, Event: EventProgress, Data: []byte(`{}`)}, nil) {
		t.Error("an event that is not an approval was dropped from the replay")
	}
}
