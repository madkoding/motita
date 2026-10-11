package gateway

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/madkoding/motita/internal/agent"
	"github.com/madkoding/motita/internal/session"
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
		default:
			// Blank separators and comment lines carry nothing to collect.
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

// askBeforeCleaning is the run of the reattach test: it logs a line and then blocks on the
// approval of a destructive command.
func askBeforeCleaning(svc *fakeService) func(context.Context, string, func(string, ...any)) (string, error) {
	return func(ctx context.Context, _ string, progress func(string, ...any)) (string, error) {
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
}

// pendingPreamble decodes the attach preamble at the head of events and checks that it carries
// the open question whole: its command, its reason and rule, and the id that answers it.
func pendingPreamble(t *testing.T, events []event) attachedEvent {
	t.Helper()
	if events[0].Event != EventAttached {
		t.Fatalf("no preamble: %+v", events)
	}
	var preamble attachedEvent
	if err := json.Unmarshal([]byte(events[0].Data), &preamble); err != nil {
		t.Fatalf("the preamble is not JSON: %v", err)
	}
	if preamble.PendingApproval == nil {
		t.Fatal("the preamble does not carry the pending question, " +
			"so the reattached client waits forever for an answer it is never asked for")
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
	return preamble
}

// TestAQuestionAskedWhileAwayIsAnsweredAgainOnReattach is the case that makes reconnection more than
// a nicety.
//
// The approval BLOCKS the run, so a client that reconnects and is not told about it sits forever
// watching a run that is waiting for the answer it will never give.
func TestAQuestionAskedWhileAwayIsAnsweredAgainOnReattach(t *testing.T) {
	svc := &fakeService{}
	svc.task = askBeforeCleaning(svc)
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
	preamble := pendingPreamble(t, events)
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
