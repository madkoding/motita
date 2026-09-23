package gateway

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/madkoding/starlight/internal/agent"
	"github.com/madkoding/starlight/internal/session"
)

// --- readers -------------------------------------------------------------------------------------

// collectFrom opens a stream and reads it to the end.
//
// It is collect() for a GET: the attach endpoint takes no body, and a client that attaches is
// reading a stream exactly the way the one that started the run does.
func collectFrom(t *testing.T, srv *Server, path string) []event {
	t.Helper()
	resp := openStream(t, srv, path)
	defer resp.Body.Close()
	return readEvents(t, resp.Body)
}

// firstEvents opens a stream and reads only its first n events, then closes it.
//
// It exists because a stream that belongs to a run still in flight does NOT end: a test about the
// PREAMBLE has no business waiting for the run, and reading to the end would block on a run that is
// blocked on the very question the test is about to answer.
func firstEvents(t *testing.T, srv *Server, path string, n int) []event {
	t.Helper()
	resp := openStream(t, srv, path)
	defer resp.Body.Close()

	var out []event
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	name := ""
	for len(out) < n && sc.Scan() {
		line := sc.Text()
		switch {
		case strings.HasPrefix(line, "event: "):
			name = strings.TrimPrefix(line, "event: ")
		case strings.HasPrefix(line, "data: "):
			out = append(out, event{name, strings.TrimPrefix(line, "data: ")})
		}
	}
	return out
}

func openStream(t *testing.T, srv *Server, path string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, srv.BaseURL()+path, nil)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+testToken)
	req.Header.Set("Accept", "text/event-stream")
	resp, err := (&http.Client{}).Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		t.Fatalf("status = %d", resp.StatusCode)
	}
	return resp
}

// streamInBackground reads a stream in a goroutine, so a test can act WHILE it is still live.
//
// The interesting cases here are about a run that has not finished yet, and reading inline would
// block until it did - which is exactly the moment the test is trying to control.
func streamInBackground(t *testing.T, srv *Server, path string) <-chan []event {
	t.Helper()
	out := make(chan []event, 1)
	go func() {
		resp := openStream(t, srv, path)
		defer resp.Body.Close()
		out <- readEvents(t, resp.Body)
	}()
	return out
}

// waitForRunning blocks until the gateway says a run is in flight in this conversation.
//
// It asks the GATEWAY rather than sleeping, so the test is deterministic: a sleep long enough to
// usually work is a test that fails once a week on a loaded machine.
func waitForRunning(t *testing.T, srv *Server, id string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if conv, ok := srv.lookup(id); ok && conv.isRunning() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatal("no run ever started")
}

// currentRunOf reads the run in flight, failing the test if there is none.
func currentRunOf(t *testing.T, srv *Server, id string) *run {
	t.Helper()
	conv, ok := srv.lookup(id)
	if !ok {
		t.Fatalf("no session %q", id)
	}
	rn, ok := conv.currentRun()
	if !ok {
		t.Fatal("no run in flight")
	}
	return rn
}

// waitForLog blocks until the run's log holds at least n events, so a test about the replay does not
// race the lines it expects to be replayed.
func waitForLog(t *testing.T, srv *Server, id string, n int) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if rn, ok := srv.lookup(id); ok {
			if c, ok := rn.currentRun(); ok {
				if replay, _ := c.since(0); len(replay) >= n {
					return
				}
			}
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("the log never reached %d events", n)
}

// startInBackground begins a run and abandons the request, which is what a client that walks away
// looks like from the gateway's side.
func startInBackground(t *testing.T, srv *Server, id, path, body string) context.CancelFunc {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		req, _ := http.NewRequestWithContext(ctx, http.MethodPost, srv.BaseURL()+sessionPath(srv, id, path), strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+testToken)
		req.Header.Set("Accept", "text/event-stream")
		if resp, err := http.DefaultClient.Do(req); err == nil {
			resp.Body.Close()
		}
	}()
	waitForRunning(t, srv, id)
	return cancel
}

// --- the run outlives its client -----------------------------------------------------------------

// TestARunSurvivesItsClientAndCanBeWatchedFromWhereItWas is the property the whole design is for.
//
// The first client abandons the request; a second one attaches, is given what it missed, and sees
// the run reach its own end. Nothing is reconstructed by the client - it asks from a sequence
// number and is told the rest, which is what makes a client a remote control.
func TestARunSurvivesItsClientAndCanBeWatchedFromWhereItWas(t *testing.T) {
	release := make(chan struct{})
	srv := newTestServer(t, &fakeService{task: func(ctx context.Context, _ string, progress func(string, ...any)) (string, error) {
		for i := 0; i < 3; i++ {
			progress("line %d", i)
		}
		select {
		case <-release:
			return "late result", nil
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}})

	cancel := startInBackground(t, srv, DefaultSession, "/task", `{"task":"x"}`)
	waitForLog(t, srv, DefaultSession, 3)
	cancel()

	// The run is STILL in flight after the client that started it left. That is the whole point:
	// a turn bounded by its connection would have nothing left to reconnect to.
	if !srv.sessions[DefaultSession].isRunning() {
		t.Fatal("the run ended with the client that started it, so there is nothing to reattach to")
	}

	live := streamInBackground(t, srv, sessionPath(srv, DefaultSession, "/events?from=0"))
	time.Sleep(100 * time.Millisecond)
	close(release)

	var events []event
	select {
	case events = <-live:
	case <-time.After(10 * time.Second):
		t.Fatal("the attached client's stream never ended, although the run did")
	}

	if len(events) < 4 {
		t.Fatalf("the reattached client received %d events, the preamble and the three lines must be there: %+v", len(events), events)
	}
	if events[0].Event != EventAttached {
		t.Fatalf("the first frame is %q, a joining client must be told what it joined", events[0].Event)
	}
	replayed := 0
	for _, e := range events[1:] {
		if e.Event != EventProgress {
			break
		}
		replayed++
	}
	if replayed < 3 {
		t.Fatalf("the replay carried %d progress lines, the three emitted before it arrived must be there", replayed)
	}
	// And it saw the run END. A client that got the past and then a closed socket would know what
	// happened, not what happens next.
	last := events[len(events)-1]
	if last.Event != EventDone {
		t.Errorf("the last event the reattached client saw is %q, it must see the run finish: %+v", last.Event, events)
	}
	if !strings.Contains(last.Data, "late result") {
		t.Errorf("the done event lost the result: %s", last.Data)
	}
}

// TestTheReattachedClientSeesTheRunReachItsEnd: a client that attaches from where it got to is given
// what comes NEXT rather than the history again, and still sees the end.
func TestTheReattachedClientSeesTheRunReachItsEnd(t *testing.T) {
	release := make(chan struct{})
	srv := newTestServer(t, &fakeService{task: func(ctx context.Context, _ string, progress func(string, ...any)) (string, error) {
		progress("working")
		select {
		case <-release:
			return "the result", nil
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}})

	cancel := startInBackground(t, srv, DefaultSession, "/task", `{"task":"x"}`)
	waitForLog(t, srv, DefaultSession, 1)
	cancel()

	// Attached from 1, the line it already had, so only what follows arrives.
	live := streamInBackground(t, srv, sessionPath(srv, DefaultSession, "/events?from=1"))
	time.Sleep(100 * time.Millisecond)
	close(release)

	var events []event
	select {
	case events = <-live:
	case <-time.After(10 * time.Second):
		t.Fatal("the attached stream never ended")
	}
	for _, e := range events {
		if e.Event == EventProgress && strings.Contains(e.Data, "working") {
			t.Errorf("the line the client said it already had was sent again: %s", e.Data)
		}
	}
	var done *event
	for i := range events {
		if events[i].Event == EventDone {
			done = &events[i]
		}
	}
	if done == nil {
		t.Fatalf("the reattached client never saw the run end: %+v", events)
	}
	if !strings.Contains(done.Data, "the result") {
		t.Errorf("the done event = %s", done.Data)
	}
}

// TestEveryEventCarriesItsSequenceNumber: SSE has an `id:` field for exactly this, and without it a
// client has no way to say where it got to - which turns "resume" into "reconcile", the thing this
// design exists to avoid.
//
// The wire is read RAW rather than parsed: a parser that dropped the `id:` line would hide the very
// field being checked.
func TestEveryEventCarriesItsSequenceNumber(t *testing.T) {
	srv := newTestServer(t, &fakeService{task: func(_ context.Context, _ string, progress func(string, ...any)) (string, error) {
		progress("one")
		return "done", nil
	}})
	raw := rawStream(t, srv, http.MethodPost, sessionPath(srv, DefaultSession, "/task"), `{"task":"x"}`)
	// The preamble is not part of the numbering and carries 0; the run's own events start at 1, so
	// this turn (one progress line and the done) is numbered 1 and 2.
	for _, want := range []string{"id: 0", "id: 1", "id: 2"} {
		if !strings.Contains(raw, want) {
			t.Errorf("the stream carries no %q line, so a client cannot resume from where it stopped:\n%s", want, raw)
		}
	}
}

// rawStream performs a request over a real socket and returns the bytes as written.
func rawStream(t *testing.T, srv *Server, method, path, body string) string {
	t.Helper()
	req, err := http.NewRequest(method, srv.BaseURL()+path, strings.NewReader(body))
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+testToken)
	req.Header.Set("Accept", "text/event-stream")
	resp, err := (&http.Client{}).Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer resp.Body.Close()
	var sb strings.Builder
	buf := make([]byte, 1024)
	for {
		n, err := resp.Body.Read(buf)
		if n > 0 {
			sb.Write(buf[:n])
		}
		if err != nil || strings.Contains(sb.String(), "event: done") {
			break
		}
	}
	return sb.String()
}

// TestTheReattachingClientIsToldWhatItMissed: a bounded log loses events, and a client that is
// silently given a stream with holes cannot tell that from a turn that simply said less. The
// preamble is where the gateway says so.
func TestTheReattachingClientIsToldWhatItMissed(t *testing.T) {
	release := make(chan struct{})
	srv := newTestServer(t, &fakeService{task: func(ctx context.Context, _ string, progress func(string, ...any)) (string, error) {
		// Overfill the log, so there really is a gap to report.
		for i := 0; i < maxLoggedEvents+10; i++ {
			progress("line %d", i)
		}
		select {
		case <-release:
			return "done", nil
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}})

	cancel := startInBackground(t, srv, DefaultSession, "/task", `{"task":"x"}`)
	// Wait for the EVICTION, not merely for the run: the gap is what is being reported.
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if _, info := currentRunOf(t, srv, DefaultSession).since(0); info.Dropped > 0 {
			break
		}
		time.Sleep(2 * time.Millisecond)
	}

	events := firstEvents(t, srv, sessionPath(srv, DefaultSession, "/events?from=0"), 2)
	cancel()

	if len(events) == 0 || events[0].Event != EventAttached {
		t.Fatalf("an attaching client is not greeted with a preamble, so it cannot learn the run's state: %+v", events)
	}
	var got attachedEvent
	if err := json.Unmarshal([]byte(events[0].Data), &got); err != nil {
		t.Fatalf("the preamble is not the JSON a client expects: %v", err)
	}
	if got.Dropped == 0 {
		t.Error("the preamble reports nothing dropped on a log that evicted events, so the client cannot tell it lost lines")
	}
	if got.FirstSeq <= 1 {
		t.Errorf("the preamble says the oldest event is %d, it must say which ones are gone", got.FirstSeq)
	}
	if got.RunID == "" {
		t.Error("the preamble does not name the run, so the client cannot say what it is watching")
	}
	if got.LastSeq < uint64(maxLoggedEvents) {
		t.Errorf("the preamble says the newest event is %d, it must count the ones that were emitted", got.LastSeq)
	}
}

// TestNothingToAttachToIsReported: a client that reconnects after the run ended must be TOLD, so it
// can show the finished turn instead of waiting forever for a stream that will never come.
func TestNothingToAttachToIsReported(t *testing.T) {
	srv := newTestServer(t, &fakeService{})
	resp := get(t, srv, sessionPath(srv, DefaultSession, "/events?from=0"), testToken)
	if resp.Code != http.StatusNotFound {
		t.Fatalf("attaching with nothing running answered %d, it must be 404", resp.Code)
	}
	if !strings.Contains(resp.Body.String(), "no run") {
		t.Errorf("the refusal must say what is happening: %s", resp.Body.String())
	}
}

// TestAttachingToAFinishedRunIsToldSo: the run ended while the client was away, and there is nothing
// in flight to attach to. Saying so is what sends the client to read the session instead of waiting.
func TestAttachingToAFinishedRunIsToldSo(t *testing.T) {
	srv := newTestServer(t, &fakeService{task: func(context.Context, string, func(string, ...any)) (string, error) {
		return "done and dusted", nil
	}})
	collect(t, srv, http.MethodPost, sessionPath(srv, DefaultSession, "/task"), `{"task":"x"}`)

	resp := get(t, srv, sessionPath(srv, DefaultSession, "/events?from=0"), testToken)
	if resp.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.Code)
	}
}

// TestTheOutcomeIsReportedToAReattachingClient: a client that arrives in the instant between the
// last event and the slot being freed gets a preamble saying the run is over, instead of waiting on
// a stream that has nothing left to send.
func TestTheOutcomeIsReportedToAReattachingClient(t *testing.T) {
	srv := newTestServer(t, &fakeService{})

	// A run that has already finished, still registered as current: the window this test is about
	// exists for as long as the goroutine has not yet cleared the slot.
	conv := srv.sessions[DefaultSession]
	rn := newTestRun()
	rn.append(EventDone, doneEvent{Result: "the answer"})
	rn.finish("done", "the answer", "", session.Snapshot{})
	conv.setCurrentRun(rn)
	defer conv.clearCurrentRun()

	events := collectFrom(t, srv, sessionPath(srv, DefaultSession, "/events?from=0"))
	if len(events) < 2 {
		t.Fatalf("events = %+v", events)
	}
	var preamble attachedEvent
	if err := json.Unmarshal([]byte(events[0].Data), &preamble); err != nil {
		t.Fatalf("the preamble is not JSON: %v", err)
	}
	if preamble.Outcome != "done" {
		t.Errorf("the preamble reports outcome %q, it must say the run is over so the client stops waiting", preamble.Outcome)
	}
	if preamble.LastSeq != 1 {
		t.Errorf("the preamble says the newest event is %d, it must count the finished run's log", preamble.LastSeq)
	}
	// And the finished run's events are replayed, so the client sees the whole turn.
	if events[1].Event != EventDone || !strings.Contains(events[1].Data, "the answer") {
		t.Errorf("the replayed event = %+v", events[1])
	}
}

// TestTheSequenceToResumeFromIsReadLeniently: a client that cannot say where it got to is given
// everything the log still holds. An error would leave it with nothing at all, and the preamble
// tells it whether that was all of it.
func TestTheSequenceToResumeFromIsReadLeniently(t *testing.T) {
	srv := newTestServer(t, &fakeService{})
	conv := srv.sessions[DefaultSession]
	rn := newTestRun()
	conv.setCurrentRun(rn)
	defer conv.clearCurrentRun()
	rn.append(EventProgress, progressEvent{Text: "one"})

	for _, q := range []string{"", "?from=", "?from=not-a-number", "?from=-1", "?from=999999999999999999999"} {
		events := firstEvents(t, srv, sessionPath(srv, DefaultSession, "/events"+q), 2)
		if len(events) != 2 {
			t.Errorf("from=%q gave %d events, an unreadable position must mean 'from the beginning'", q, len(events))
			continue
		}
		if !strings.Contains(events[1].Data, "one") {
			t.Errorf("from=%q did not replay the event: %s", q, events[1].Data)
		}
	}
}

// TestASequenceNumberPastTheEndReplaysNothing: a client that is ahead of the log is CURRENT, not
// broken. Handing it the whole log again would make it render the turn twice.
func TestASequenceNumberPastTheEndReplaysNothing(t *testing.T) {
	srv := newTestServer(t, &fakeService{})
	conv := srv.sessions[DefaultSession]
	rn := newTestRun()
	conv.setCurrentRun(rn)
	defer conv.clearCurrentRun()
	rn.append(EventProgress, progressEvent{Text: "one"})

	events := firstEvents(t, srv, sessionPath(srv, DefaultSession, "/events?from=50"), 1)
	if len(events) != 1 {
		t.Fatalf("events = %+v, only the preamble was expected", events)
	}
	if events[0].Event != EventAttached {
		t.Errorf("the frame is %q, want %s", events[0].Event, EventAttached)
	}
	if !strings.Contains(events[0].Data, `"last_seq":1`) {
		t.Errorf("the preamble does not tell the client where the log is: %s", events[0].Data)
	}
}

// --- what is running, without opening a stream ---------------------------------------------------

// TestTheRunStatusReportsWhatIsRunning: a client decides WHETHER to attach by asking a question that
// costs one JSON reply. Opening a stream to find out would be a stream to close immediately.
func TestTheRunStatusReportsWhatIsRunning(t *testing.T) {
	release := make(chan struct{})
	srv := newTestServer(t, &fakeService{task: func(ctx context.Context, _ string, progress func(string, ...any)) (string, error) {
		progress("working")
		select {
		case <-release:
			return "done", nil
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}})

	cancel := startInBackground(t, srv, DefaultSession, "/task", `{"task":"x"}`)
	defer cancel()

	resp := get(t, srv, sessionPath(srv, DefaultSession, "/run"), testToken)
	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", resp.Code, resp.Body.String())
	}
	var got map[string]any
	if err := json.Unmarshal(resp.Body.Bytes(), &got); err != nil {
		t.Fatalf("the status is not JSON: %v", err)
	}
	if got["run_id"] == nil || got["run_id"] == "" {
		t.Errorf("the status does not name the run: %v", got)
	}
	if got["outcome"] != "" {
		t.Errorf("a run in flight reports outcome %v, it must report nothing until it ends", got["outcome"])
	}
	if got["last_seq"] == nil {
		t.Errorf("the status does not say where the log is, so a client cannot tell how far behind it is: %v", got)
	}
	if got["subscribers"] == nil {
		t.Errorf("the status does not report how many readers are attached: %v", got)
	}
	close(release)
}

// TestTheStatusOfANonRunningSessionIsNotFound: a client asking about a run in a quiet conversation
// is answered with a refusal it can act on, not an empty object it has to interpret.
func TestTheStatusOfANonRunningSessionIsNotFound(t *testing.T) {
	srv := newTestServer(t, &fakeService{})
	resp := get(t, srv, sessionPath(srv, DefaultSession, "/run"), testToken)
	if resp.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.Code)
	}
}

// --- stopping a run that is not being watched ----------------------------------------------------

// TestAClientCanStopTheRunItIsNotWatching: the user who cancels is the one at the keyboard, and the
// run may be one they walked away from and came back to. Cancelling is addressed to the
// CONVERSATION, so it never depends on remembering a run id from an earlier connection.
func TestAClientCanStopTheRunItIsNotWatching(t *testing.T) {
	srv := newTestServer(t, &fakeService{task: func(ctx context.Context, _ string, _ func(string, ...any)) (string, error) {
		<-ctx.Done()
		return "", ctx.Err()
	}})

	cancel := startInBackground(t, srv, DefaultSession, "/task", `{"task":"x"}`)
	defer cancel()

	if w := post(t, srv, sessionPath(srv, DefaultSession, "/cancel"), "", testToken); w.Code != http.StatusNoContent {
		t.Fatalf("cancel status = %d, want 204 (body %s)", w.Code, w.Body.String())
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if !srv.sessions[DefaultSession].isRunning() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatal("the run kept going after it was cancelled")
}

// TestCancellingNothingIsReported: an idempotent-looking 204 would tell a client its cancel worked
// when there was nothing to cancel, and the user would wait for a stop nobody asked for.
func TestCancellingNothingIsReported(t *testing.T) {
	srv := newTestServer(t, &fakeService{})
	w := post(t, srv, sessionPath(srv, DefaultSession, "/cancel"), "", testToken)
	if w.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409 (body %s)", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "no run") {
		t.Errorf("the refusal must say there was nothing to stop: %s", w.Body.String())
	}
}

// TestACancelledRunEndsItsStream: the client that was watching has to see the turn end, or it sits
// on a socket the gateway has already finished with. And it is told it was CANCELLED rather than
// that it failed, because cancelling is something the user asked for.
func TestACancelledRunEndsItsStream(t *testing.T) {
	srv := newTestServer(t, &fakeService{task: func(ctx context.Context, _ string, _ func(string, ...any)) (string, error) {
		<-ctx.Done()
		return "", ctx.Err()
	}})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	events := make(chan []event, 1)
	go func() {
		req, _ := http.NewRequestWithContext(ctx, http.MethodPost, srv.BaseURL()+sessionPath(srv, DefaultSession, "/task"), strings.NewReader(`{"task":"x"}`))
		req.Header.Set("Authorization", "Bearer "+testToken)
		req.Header.Set("Accept", "text/event-stream")
		resp, err := (&http.Client{}).Do(req)
		if err != nil {
			events <- nil
			return
		}
		defer resp.Body.Close()
		events <- readEvents(t, resp.Body)
	}()
	waitForRunning(t, srv, DefaultSession)

	if w := post(t, srv, sessionPath(srv, DefaultSession, "/cancel"), "", testToken); w.Code != http.StatusNoContent {
		t.Fatalf("cancel status = %d", w.Code)
	}
	select {
	case got := <-events:
		if len(got) == 0 {
			t.Fatal("the client of a cancelled run received nothing")
		}
		last := got[len(got)-1]
		if last.Event != EventError || !strings.Contains(last.Data, "cancelled") {
			t.Errorf("the last event is %+v, a cancelled turn must say so", last)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the stream of a cancelled run never ended")
	}
}

// --- the question nobody was there to answer -----------------------------------------------------

// TestAQuestionAskedWhileAwayIsAnsweredAgainOnReattach is the case that makes reconnection more than
// a nicety.
//
// The approval BLOCKS the run, so a client that reconnects and is not told about it sits forever
// watching a run that is waiting for the answer it will never give.
func TestAQuestionAskedWhileAwayIsAnsweredAgainOnReattach(t *testing.T) {
	svc := &fakeService{}
	svc.task = func(ctx context.Context, _ string, progress func(string, ...any)) (string, error) {
		// A turn does something before it asks, so the log has real history behind the question -
		// which is what a client that reconnects actually finds waiting for it.
		progress("about to clean the tree")
		if svc.approver == nil {
			return "", fmt.Errorf("no approver was installed")
		}
		ok, err := svc.approver(ctx, agent.ApprovalRequest{
			Command: "rm -rf ./build", Reason: "it deletes a directory", Rule: "destructive",
		})
		if err != nil {
			return "", err
		}
		if !ok {
			return "the command was refused", nil
		}
		return "the command ran", nil
	}
	srv := newTestServer(t, svc)
	conv := srv.sessions[DefaultSession]

	// The first client starts the run and leaves while the question is open.
	cancel := startInBackground(t, srv, DefaultSession, "/task", `{"task":"clean"}`)
	// Wait for the QUESTION, not merely for the run: the run starts before it asks, and reattaching
	// in the gap between the two would test nothing.
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) && conv.pendingApprovalNow() == nil {
		time.Sleep(2 * time.Millisecond)
	}
	if conv.pendingApprovalNow() == nil {
		t.Fatal("the run never asked the question")
	}
	cancel()

	// The second client attaches and must be TOLD the question is open, WITH its id, or it cannot
	// answer the run it is watching. Only the first events are read: the stream belongs to a run
	// that is still blocked, so waiting for its end would wait forever.
	events := firstEvents(t, srv, sessionPath(srv, DefaultSession, "/events?from=0"), 3)
	if len(events) < 2 {
		t.Fatalf("events = %+v", events)
	}
	if events[0].Event != EventAttached {
		t.Fatalf("no preamble: %+v", events)
	}
	var preamble attachedEvent
	if err := json.Unmarshal([]byte(events[0].Data), &preamble); err != nil {
		t.Fatalf("the preamble is not JSON: %v", err)
	}
	if preamble.PendingApproval == nil {
		t.Fatal("the preamble does not carry the pending question, so the reattached client waits forever for an answer it is never asked for")
	}
	if preamble.PendingApproval.Command != "rm -rf ./build" {
		t.Errorf("the pending question lost its command: %+v", preamble.PendingApproval)
	}
	if preamble.PendingApproval.Reason != "it deletes a directory" || preamble.PendingApproval.Rule != "destructive" {
		t.Errorf("the pending question lost its reason or rule: %+v", preamble.PendingApproval)
	}
	if preamble.PendingApproval.ID == "" {
		t.Fatal("the pending question has no id, so it cannot be answered")
	}
	// The line emitted before the question is replayed too, so the client sees what led to it.
	if !strings.Contains(events[1].Data, "about to clean") {
		t.Errorf("the replay does not carry the line before the question: %+v", events)
	}

	// And answering with that id really does release the run, which is the point of carrying it.
	w := post(t, srv, sessionPath(srv, DefaultSession, "/runs/approval"),
		fmt.Sprintf(`{"id":%q,"approve":true}`, preamble.PendingApproval.ID), testToken)
	if w.Code != http.StatusNoContent {
		t.Fatalf("answer status = %d, want 204 (body %s)", w.Code, w.Body.String())
	}
	deadline = time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if !conv.isRunning() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatal("the run never continued after its question was answered")
}

// TestAPreambleWithoutAQuestionSaysNothingAboutOne: the field is OMITTED rather than sent as null,
// so a client switching on its presence does not have to also test for null.
func TestAPreambleWithoutAQuestionSaysNothingAboutOne(t *testing.T) {
	srv := newTestServer(t, &fakeService{})
	conv := srv.sessions[DefaultSession]
	rn := newTestRun()
	conv.setCurrentRun(rn)
	defer conv.clearCurrentRun()

	events := firstEvents(t, srv, sessionPath(srv, DefaultSession, "/events?from=0"), 1)
	if len(events) != 1 {
		t.Fatalf("events = %+v", events)
	}
	if strings.Contains(events[0].Data, "pending_approval") {
		t.Errorf("the preamble mentions an approval although nothing is being asked: %s", events[0].Data)
	}
}

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

// --- writers -------------------------------------------------------------------------------------

// flushableBuffer is a writer that supports Flush, so a ResponseController around it really does
// flush and the handler goes on to the NEXT write rather than stopping at the first one.
type flushableBuffer struct{ b *bytes.Buffer }

func (f *flushableBuffer) Header() http.Header         { return http.Header{} }
func (f *flushableBuffer) WriteHeader(int)             {}
func (f *flushableBuffer) Flush()                      {}
func (f *flushableBuffer) Write(p []byte) (int, error) { return f.b.Write(p) }
