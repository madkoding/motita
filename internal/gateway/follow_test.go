package gateway

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// followGateway serves one canned SSE body, and records the paths it was asked for.
//
// A canned body is what makes following a run testable without a real agent: the client's half is
// the READING of the stream, and the wire format is the contract it must not disagree with.
func followGateway(t *testing.T, body string) (*Client, *[]string) {
	t.Helper()
	var mu sync.Mutex
	var seen []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen = append(seen, r.Method+" "+r.URL.Path+"?"+r.URL.RawQuery)
		mu.Unlock()
		if strings.HasSuffix(r.URL.Path, "/events") {
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = io.WriteString(w, body)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(srv.Close)
	return NewClient(srv.URL, testToken), &seen
}

// frame builds one server-sent event exactly as the gateway writes it, using the package's own
// sequence-number rendering so a test can never disagree with the wire format about a number.
func frame(seq uint64, event, data string) string {
	return "id: " + itoa(seq) + "\nevent: " + event + "\ndata: " + data + "\n\n"
}

// TestTheClientFollowsARunItDidNotStart: the turn outlives its client, so a client that arrives
// later has to be able to join it. It reads the SAME stream the first client read, from the
// beginning, because it has seen none of it - which is only possible because the run kept a log.
func TestTheClientFollowsARunItDidNotStart(t *testing.T) {
	body := frame(0, EventAttached, `{"run_id":"r1","first_seq":1,"last_seq":3,"dropped":0}`) +
		frame(1, EventProgress, `{"text":"reading the tree"}`) +
		frame(2, EventProgress, `{"text":"running the check"}`) +
		frame(3, EventDone, `{"result":"twelve files"}`)
	c, seen := followGateway(t, body)

	var lines []string
	result, err := c.FollowRun(context.Background(), func(format string, args ...any) {
		lines = append(lines, strings.TrimSpace(sprintf(format, args...)))
	})
	if err != nil {
		t.Fatalf("FollowRun: %v", err)
	}
	if result != "twelve files" {
		t.Errorf("result = %q, the run's own answer must come back", result)
	}
	if len(lines) != 2 || lines[0] != "reading the tree" || lines[1] != "running the check" {
		t.Errorf("the progress lines are %v, both must be reported in order", lines)
	}
	if len(*seen) != 1 || !strings.Contains((*seen)[0], "/v1/sessions/"+DefaultSession+"/events?from=0") {
		t.Errorf("the follow asked for %v, it must attach to this session's stream from the start", *seen)
	}
}

// TestTheFollowIsScopedToTheSession: a follow addressed to the wrong conversation would show the
// user another session's turn, which is the one thing the routing rule exists to prevent.
func TestTheFollowIsScopedToTheSession(t *testing.T) {
	body := frame(1, EventDone, `{"result":"ok"}`)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, body)
	}))
	defer srv.Close()

	c := NewClientForSession(srv.URL, testToken, "sother")
	if _, err := c.FollowRun(context.Background(), func(string, ...any) {}); err != nil {
		t.Fatalf("FollowRun: %v", err)
	}
}

// TestAFollowThatCannotAttachIsReported: the run ended between the question and the attach, so the
// gateway answers 404. That is a fact to report - the user is told the turn is not there instead of
// being shown one that is not.
func TestAFollowThatCannotAttachIsReported(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeError(w, http.StatusNotFound, "there is no run in progress in this session to attach to")
	}))
	defer srv.Close()

	c := NewClient(srv.URL, testToken)
	_, err := c.FollowRun(context.Background(), func(string, ...any) {})
	if err == nil || !strings.Contains(err.Error(), "no run in progress") {
		t.Fatalf("err = %v, the gateway's reason must reach the caller", err)
	}
}

// TestAFollowThatEndsWithoutAnAnswerIsReported: a stream that simply stops is a dropped connection,
// whatever the transport reported, and reporting it as a finished turn would give the user an empty
// answer for a question the agent may still be working on.
func TestAFollowThatEndsWithoutAnAnswerIsReported(t *testing.T) {
	c, _ := followGateway(t, frame(1, EventProgress, `{"text":"working"}`))
	_, err := c.FollowRun(context.Background(), func(string, ...any) {})
	if err == nil {
		t.Fatal("a follow whose stream ended without a verdict must be reported")
	}
}

// TestAFollowedErrorEndsTheFollowWithTheReason: the run failing is how it ended, and the reason is
// what the user can act on.
func TestAFollowedErrorEndsTheFollowWithTheReason(t *testing.T) {
	c, _ := followGateway(t, frame(1, EventError, `{"error":"the model refused"}`))
	_, err := c.FollowRun(context.Background(), func(string, ...any) {})
	if err == nil || !strings.Contains(err.Error(), "the model refused") {
		t.Fatalf("err = %v, the run's failure must reach the caller", err)
	}
}

// TestCancellingAFollowOnlyStopsReading: a turn OUTLIVES the client that was watching it, so
// closing the window - or the process going away - must leave the gateway working. Stopping the run
// is a separate, explicit act by the user (CancelRun), and conflating the two would make Ctrl+C
// kill a turn the user only meant to stop watching.
func TestCancellingAFollowOnlyStopsReading(t *testing.T) {
	// The stream stays open until the test says so, which is what makes the cancellation happen
	// while the follow is genuinely in flight.
	release := make(chan struct{})
	var mu sync.Mutex
	var cancelled bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/cancel") {
			mu.Lock()
			cancelled = true
			mu.Unlock()
			w.WriteHeader(http.StatusNoContent)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, frame(0, EventAttached, `{"run_id":"r1"}`))
		w.(http.Flusher).Flush()
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	c := NewClient(srv.URL, testToken)
	go func() {
		_, err := c.FollowRun(ctx, func(string, ...any) {})
		done <- err
	}()

	// Wait until the stream is actually being read before cancelling: a cancel that arrives before
	// the request is even made would exercise nothing.
	time.Sleep(20 * time.Millisecond)
	cancel()

	// The stop request is made on its own goroutine - it is a request, not a local act - so waiting
	// for it is the only honest way to measure it. Waiting for the follow to return is not enough:
	// the follow comes back the moment the context is cancelled, which is BEFORE the request has
	// necessarily been sent.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		got := cancelled
		mu.Unlock()
		if got {
			break
		}
		time.Sleep(2 * time.Millisecond)
	}
	close(release)

	mu.Lock()
	got := cancelled
	mu.Unlock()
	if got {
		t.Fatal("cancelling the follow asked the gateway to stop the run: a turn must outlive the client watching it")
	}
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the follow did not return after being cancelled")
	}
}

// TestCancellingAFollowWithNothingToStopIsNotAnError: the run may have ended on its own a moment
// before Escape was pressed, which is the common case. The gateway answers 409, and that answer is
// "nothing to stop" - a success for a user who wanted it stopped.
func TestCancellingAFollowWithNothingToStopIsNotAnError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeError(w, http.StatusConflict, "there is no run in progress in this session to stop")
	}))
	defer srv.Close()

	c := NewClient(srv.URL, testToken)
	stopped, err := c.StopRun(context.Background())
	if err != nil {
		t.Fatalf("stopping a run that had already ended must not be an error: %v", err)
	}
	if stopped {
		t.Error("there was nothing to stop, so it must not be reported as stopped")
	}
}

// TestStoppingARealRunReportsThatItWasStopped: the happy path, without which the "nothing to stop"
// case above could pass with a method that never reports anything.
func TestStoppingARealRunReportsThatItWasStopped(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	c := NewClient(srv.URL, testToken)
	stopped, err := c.StopRun(context.Background())
	if err != nil {
		t.Fatalf("StopRun: %v", err)
	}
	if !stopped {
		t.Error("a 204 means the run was stopped, and the caller acts on that")
	}
}

// TestStoppingReportsAFailureThatIsNotNothingToStop: a gateway that could not be reached is a
// different statement from a run that had already finished, and collapsing the two would hide a
// broken connection behind a silent success.
func TestStoppingReportsAFailureThatIsNotNothingToStop(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeError(w, http.StatusInternalServerError, "the gateway fell over")
	}))
	defer srv.Close()

	c := NewClient(srv.URL, testToken)
	if _, err := c.StopRun(context.Background()); err == nil {
		t.Fatal("a gateway that failed must be reported, not treated as nothing to stop")
	}
}

// TestLiveRunAnswersNothingIsRunningWithoutAnError: a 404 is the honest answer to "is anything
// running", and it is the COMMON one. Reporting it as a failure would put an error in the chat
// every time a user returns to a quiet conversation.
func TestLiveRunAnswersNothingIsRunningWithoutAnError(t *testing.T) {
	c := resumeGateway(t, func(w http.ResponseWriter, r *http.Request) {
		writeError(w, http.StatusNotFound, "there is no run in progress in this session")
	})

	info, running, err := c.LiveRun(context.Background())
	if err != nil {
		t.Fatalf("nothing running must not be an error: %v", err)
	}
	if running {
		t.Errorf("running = true with nothing in flight, info = %+v", info)
	}
}

// TestLiveRunReportsTheRunInFlight: the answer a client acts on before following anything.
func TestLiveRunReportsTheRunInFlight(t *testing.T) {
	c := resumeGateway(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"run_id":"r1","first_seq":1,"last_seq":7,"dropped":0,"subscribers":1}`)
	})

	info, running, err := c.LiveRun(context.Background())
	if err != nil {
		t.Fatalf("LiveRun: %v", err)
	}
	if !running || info.RunID != "r1" || info.LastSeq != 7 {
		t.Errorf("running = %v, info = %+v", running, info)
	}
}

// TestLiveRunReportsAGatewayThatFailed: a failure that is not "nothing is running" must reach the
// caller, or a broken gateway would look like a quiet conversation.
func TestLiveRunReportsAGatewayThatFailed(t *testing.T) {
	c := resumeGateway(t, func(w http.ResponseWriter, r *http.Request) {
		writeError(w, http.StatusInternalServerError, "the gateway fell over")
	})

	if _, _, err := c.LiveRun(context.Background()); err == nil {
		t.Fatal("a gateway that failed must be reported, not answered with \"nothing running\"")
	}
}

// TestAFollowWithAnUnusableAddressIsReported: the request itself cannot be built - a client handed
// an address that is not a URL - and that is a failure to report rather than a follow that silently
// does nothing.
func TestAFollowWithAnUnusableAddressIsReported(t *testing.T) {
	c := NewClient("http://[::1]:namedport", testToken)
	_, err := c.FollowRun(context.Background(), func(string, ...any) {})
	if err == nil {
		t.Fatal("a follow to an address that is not a URL must be reported")
	}
}

// TestAFollowAgainstADeadGatewayIsReported: the address is a URL, so the request is built, and
// nothing is listening - which is what a gateway that has stopped looks like. The follow must say
// so rather than returning as though the turn had ended.
func TestAFollowAgainstADeadGatewayIsReported(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	addr := srv.URL
	srv.Close()

	c := NewClient(addr, testToken)
	if _, err := c.FollowRun(context.Background(), func(string, ...any) {}); err == nil {
		t.Fatal("following a run on a gateway that is not there must be reported")
	}
}

// TestARefusalCarriesTheStatusItAnsweredWith: the message is what a user reads, and the status is
// what the code branches on. Losing the status is what forced callers to parse prose.
func TestARefusalCarriesTheStatusItAnsweredWith(t *testing.T) {
	resp := &http.Response{
		StatusCode: http.StatusConflict,
		Body:       io.NopCloser(strings.NewReader(`{"error":"nothing is waiting for an approval right now"}`)),
	}
	err := refusalError(resp)
	if !statusIs(err, http.StatusConflict) {
		t.Errorf("the refusal lost its status: %v", err)
	}
	if !strings.Contains(err.Error(), "nothing is waiting") {
		t.Errorf("the refusal lost its reason: %v", err)
	}
	if statusIs(err, http.StatusNotFound) {
		t.Error("a 409 was reported as a 404")
	}
	// And an error that is not a refusal is never any status.
	if statusIs(io.EOF, http.StatusNotFound) {
		t.Error("a plain error must not be reported as a refusal")
	}
}

// waitForCondition is a tiny poll used to make the timing in a test explicit rather than a bare
// sleep scattered through the body.
func waitForCondition(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(time.Millisecond)
	}
}

// sprintf joins a format and its arguments the way the client's progress callback does.
func sprintf(format string, args ...any) string {
	if len(args) == 0 {
		return format
	}
	out := format
	for _, a := range args {
		if s, ok := a.(string); ok {
			out = strings.Replace(out, "%s", s, 1)
			continue
		}
		out += "?"
	}
	return out
}
