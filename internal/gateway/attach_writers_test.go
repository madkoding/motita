package gateway

// Attach tests for the gateway package: the writers behind an attached stream - what the replay
// already carried, the keepalive of a long stream and the writes and flushes that fail.

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/madkoding/motita/internal/agent"
)

// --- writers -------------------------------------------------------------------------------------

// flushableBuffer is a writer that supports Flush, so a ResponseController around it really does
// flush and the handler goes on to the NEXT write rather than stopping at the first one.
type flushableBuffer struct{ b *bytes.Buffer }

func (f *flushableBuffer) Header() http.Header         { return http.Header{} }
func (f *flushableBuffer) WriteHeader(int)             {}
func (f *flushableBuffer) Flush()                      {}
func (f *flushableBuffer) Write(p []byte) (int, error) { return f.b.Write(p) }

// TestWriteIfNewSkipsWhatTheReplayAlreadyCarried: the event can be in the replay AND in the queue,
// and sending it twice would make the client render one line as two. The sequence number is what
// tells the two cases apart - and this window is only a few nanoseconds wide on a real run, so it is
// driven here rather than waited for.
func TestWriteIfNewSkipsWhatTheReplayAlreadyCarried(t *testing.T) {
	rn := newTestRun()

	var out bytes.Buffer
	fw := &flushableBuffer{&out}
	rc := http.NewResponseController(fw)

	// At or below the replay's last: already written, so it is skipped.
	if err := rn.writeIfNew(fw, rc, loggedEvent{Seq: 3, Event: EventProgress, Data: []byte(`{"text":"old"}`)}, 3); err != nil {
		t.Fatalf("writeIfNew: %v", err)
	}
	if out.Len() != 0 {
		t.Errorf("an event the replay already carried was written again: %q", out.String())
	}

	// Beyond it: this is the only copy the client will get, so it must be written.
	if err := rn.writeIfNew(fw, rc, loggedEvent{Seq: 4, Event: EventProgress, Data: []byte(`{"text":"new"}`)}, 3); err != nil {
		t.Fatalf("writeIfNew: %v", err)
	}
	if !strings.Contains(out.String(), "new") || !strings.Contains(out.String(), "id: 4") {
		t.Errorf("the queued event was not written: %q", out.String())
	}
}

// A long turn must not be cut by a middlebox that decided an idle connection was dead.
//
// The failure this prevents is silent and misread as the RUN dying: a mobile carrier NAT commonly
// drops a connection idle for 30 to 60 seconds, and a turn can easily run longer than that with
// nothing to report. The client sees the stream close, reattaches, and the user loses the tail of
// work that was still going - so this is asserted on the wire rather than trusted.
func TestALongStreamIsKeptAlive(t *testing.T) {
	release := make(chan struct{})
	srv := newTestServer(t, &fakeService{task: func(ctx context.Context, _ string, progress func(string, ...any)) (string, error) {
		progress("working")
		select {
		case <-release:
			return "the result", nil
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}}, func(o *Options) { o.Heartbeat = 20 * time.Millisecond })
	defer close(release)

	// A run has to be IN FLIGHT: /events has nothing to stream otherwise, and a stream that
	// never opens cannot show a keepalive.
	startInBackground(t, srv, DefaultSession, "/task", `{"task":"x"}`)
	waitForLog(t, srv, DefaultSession, 1)

	// Read until a keepalive arrives, or give up. The deadline is generous compared with the
	// 20ms interval so a loaded machine does not make this fail for the wrong reason.
	got := readUntil(t, srv, sessionPath(srv, DefaultSession, "/events"), ": keepalive", 5*time.Second)
	if !strings.Contains(got, ": keepalive") {
		t.Fatalf("a stream that outlives the heartbeat must carry keepalives, or a phone's "+
			"connection is dropped silently. Got:\n%s", got)
	}
	// And the keepalive is a COMMENT: it must not consume a sequence number or appear as an
	// event, because the numbering belongs to the run's log and a keepalive is not in it.
	if strings.Contains(got, "event: ") && strings.Contains(got, "id: ") {
		if idx := strings.Index(got, ": keepalive"); idx >= 0 && strings.Contains(got[:idx], "id: ") {
			// A preamble may legitimately precede it; what must not happen is a keepalive being
			// framed as an event.
			if strings.Contains(got[:idx], "event: keepalive") {
				t.Errorf("the keepalive was framed as an event: %s", got)
			}
		}
	}
	if strings.Contains(got, "event: keepalive") {
		t.Errorf("the keepalive must be a comment, not an event: %s", got)
	}
}

// readUntil opens a stream and reads it until the marker appears or the deadline passes, returning
// what it read. The reading is done on another goroutine so the deadline actually bounds the wait:
// a blocking Read in the test goroutine would make the test hang instead of fail.
func readUntil(t *testing.T, srv *Server, path, marker string, within time.Duration) string {
	t.Helper()
	type chunk struct{ data string }
	ch := make(chan chunk, 1)
	go func() {
		req, err := http.NewRequest(http.MethodGet, srv.BaseURL()+path, nil)
		if err != nil {
			ch <- chunk{}
			return
		}
		req.Header.Set("Authorization", "Bearer "+testToken)
		req.Header.Set("Accept", "text/event-stream")
		resp, err := (&http.Client{}).Do(req)
		if err != nil {
			ch <- chunk{}
			return
		}
		defer resp.Body.Close()
		var sb strings.Builder
		buf := make([]byte, 512)
		for {
			n, err := resp.Body.Read(buf)
			if n > 0 {
				sb.Write(buf[:n])
				if strings.Contains(sb.String(), marker) {
					ch <- chunk{data: sb.String()}
					return
				}
			}
			if err != nil {
				ch <- chunk{data: sb.String()}
				return
			}
		}
	}()
	select {
	case c := <-ch:
		return c.data
	case <-time.After(within):
		return ""
	}
}

// TestTheStreamStopsWhenTheKeepaliveCannotBeWritten: a connection that died during a quiet stretch
// is exactly when the keepalive is the next thing written, so this is the commonest way the
// heartbeat path ends. The handler must return rather than keep writing into nothing.
//
// The write count matters: 1 is the connection line and 2 is the preamble, so 3 is the keepalive,
// because nothing else has been written - the run is deliberately silent.
//
// The body is asserted AFTER the failure to prove the count landed on the keepalive: a test that
// fails on the wrong write passes for the wrong reason. The partial write is NOT recorded by the
// failing writer, which is what makes the check meaningful rather than a tautology.
func TestTheStreamStopsWhenTheKeepaliveCannotBeWritten(t *testing.T) {
	srv := newTestServer(t, &fakeService{}, func(o *Options) { o.Heartbeat = 10 * time.Millisecond })
	conv := srv.sessions[DefaultSession]
	rn := newTestRun()

	w := &scriptedWriter{failAt: 3}
	done := attachInBackground(t, srv, conv, rn, w)

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("attach never returned after its keepalive could not be written")
	}
	if strings.Contains(w.body(), ": keepalive") {
		t.Fatalf("the keepalive was recorded, so the write did not fail where this test says: %q", w.body())
	}
	if !strings.Contains(w.body(), "event: attached") {
		t.Fatalf("the preamble is missing, so the write count is off: %q", w.body())
	}
}

// TestTheStreamStopsWhenTheKeepaliveCannotBeFlushed: writing the comment succeeded but pushing it
// out did not, which is the same dead connection seen one layer lower. The handler must return
// rather than keep writing into a socket that is not carrying anything.
//
// noFlushWriter is the existing stand-in for it: it accepts writes and cannot flush, which is how
// every other flush failure in this package is produced.
func TestTheStreamStopsWhenTheKeepaliveCannotBeFlushed(t *testing.T) {
	srv := newTestServer(t, &fakeService{}, func(o *Options) { o.Heartbeat = 10 * time.Millisecond })
	conv := srv.sessions[DefaultSession]
	rn := newTestRun()

	// A LATE flush failure, not an immediate one: noFlushWriter cannot flush at all, so startStream
	// fails on the very first flush and the keepalive is never reached. The flush that must fail
	// here is the third - 1 is the connection line, 2 is the preamble, 3 is the keepalive.
	w := &lateFlushFailure{failAt: 3}
	done := attachInBackground(t, srv, conv, rn, w)
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("attach never returned after its keepalive could not be flushed")
	}
	if w.flushes != 3 {
		t.Fatalf("the failure landed on flush %d, not on the keepalive, so this test does not "+
			"assert what it says", w.flushes)
	}
}

// lateFlushFailure accepts writes and fails on one specific FLUSH, which is the shape a connection
// dying during a quiet stretch has: everything was written, and now nothing gets out.
//
// It implements FlushError rather than Flusher, because that is the method the standard library
// consults first and the only one through which a flush can report an error at all.
type lateFlushFailure struct {
	flushes int
	failAt  int
}

func (w *lateFlushFailure) Header() http.Header         { return http.Header{} }
func (w *lateFlushFailure) WriteHeader(int)             {}
func (w *lateFlushFailure) Write(p []byte) (int, error) { return len(p), nil }

func (w *lateFlushFailure) FlushError() error {
	w.flushes++
	if w.flushes == w.failAt {
		return fmt.Errorf("the connection is gone")
	}
	return nil
}

// TestAHeartbeatOfZeroIsOff: a negative interval is how a caller asks for a quiet stream, and it
// must not be read as "use the default" - the two are one comparison apart and the difference is a
// stream that keeps writing when the caller asked it not to.
func TestAHeartbeatOfZeroIsOff(t *testing.T) {
	srv := newTestServer(t, &fakeService{}, func(o *Options) { o.Heartbeat = -1 })
	if srv.heartbeat != 0 {
		t.Fatalf("a negative heartbeat must mean OFF, got %s", srv.heartbeat)
	}
	// A caller who says nothing gets the default, and it is a real interval.
	other := newTestServer(t, &fakeService{})
	if other.heartbeat != defaultHeartbeat {
		t.Fatalf("an unset heartbeat = %s, want %s", other.heartbeat, defaultHeartbeat)
	}
}

// TestTheDoneEventCarriesTheStructuredReport: a front end lays the answer out from FIELDS, so the
// done event has to carry them next to the flat text, and a plan (which has no report) must not
// borrow the task's.
func TestTheDoneEventCarriesTheStructuredReport(t *testing.T) {
	rep := &agent.Report{Version: agent.ReportVersion, Status: "done", Summary: "added Foo",
		Verification: []agent.ReportCheck{{Check: "go test", Result: "pass", Evidence: "3 passed"}}}
	svc := &fakeService{structured: rep, task: func(context.Context, string, func(string, ...any)) (string, error) {
		return "added Foo", nil
	}, plan: func(context.Context, string, func(string, ...any)) (string, error) { return "a plan", nil }}
	srv := newTestServer(t, svc)

	raw := rawStream(t, srv, http.MethodPost, sessionPath(srv, DefaultSession, "/task"), `{"task":"x"}`)
	for _, want := range []string{`"report":{"version":1,"status":"done"`, `"check":"go test"`, `"evidence":"3 passed"`} {
		if !strings.Contains(raw, want) {
			t.Errorf("the done event lacks %s:\n%s", want, raw)
		}
	}
	raw = rawStream(t, srv, http.MethodPost, sessionPath(srv, DefaultSession, "/plan"), `{"prompt":"x"}`)
	if strings.Contains(raw, `"report"`) {
		t.Errorf("a plan must not carry a task report:\n%s", raw)
	}
}
