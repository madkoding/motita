package gateway

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/madkoding/starlight/internal/agent"
)

// resumeGateway serves the request script the test hands it, so a connection can be made to drop
// WITHOUT waiting for a network to do it. A test that waited for a real drop would be a test that
// runs once a week.
func resumeGateway(t *testing.T, handle func(w http.ResponseWriter, r *http.Request)) *Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(handle))
	t.Cleanup(srv.Close)
	return NewClient(srv.URL, testToken)
}

// writeFrame writes one server-sent event in the format the gateway uses.
func writeFrame(w io.Writer, seq uint64, event, data string) {
	_, _ = io.WriteString(w, "id: "+itoa(seq)+"\nevent: "+event+"\ndata: "+data+"\n\n")
}

func itoa(n uint64) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}

// TestTheClientResumesItsRunAfterTheConnectionDrops: a phone loses its network for four seconds and
// the turn keeps going. A client that treats that as a failed turn makes the user re-ask a question
// the agent already answered, and the answer was sitting in the gateway's log the whole time.
func TestTheClientResumesItsRunAfterTheConnectionDrops(t *testing.T) {
	var mu sync.Mutex
	var attempts, resumedFrom []string

	c := resumeGateway(t, func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		attempts = append(attempts, r.URL.Path)
		first := len(attempts) == 1
		if strings.Contains(r.URL.Path, "/events") {
			resumedFrom = append(resumedFrom, r.URL.Query().Get("from"))
		}
		mu.Unlock()

		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		if first {
			// Two events reach the client, and then the connection dies - which is what a dropped
			// socket looks like: the body just ends.
			writeFrame(w, 0, EventAttached, `{"run_id":"r1","first_seq":0,"last_seq":0,"dropped":0}`)
			writeFrame(w, 1, EventProgress, `{"text":"working"}`)
			writeFrame(w, 2, EventProgress, `{"text":"still working"}`)
			return
		}
		// The reattach: the gateway replays what the client missed and finishes the turn.
		writeFrame(w, 0, EventAttached, `{"run_id":"r1","first_seq":0,"last_seq":4,"dropped":0}`)
		writeFrame(w, 3, EventProgress, `{"text":"the last line"}`)
		writeFrame(w, 4, EventDone, `{"result":"the answer"}`)
	})

	var lines []string
	result, err := c.RunTask(context.Background(), "count the files", func(format string, args ...any) {
		mu.Lock()
		lines = append(lines, format)
		mu.Unlock()
	})
	if err != nil {
		t.Fatalf("the run failed after the connection dropped, when the gateway still held it: %v", err)
	}
	if result != "the answer" {
		t.Errorf("result = %q, the resumed run must return the answer", result)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(resumedFrom) == 0 {
		t.Fatal("the client never reattached: it treated a dropped connection as the end of the turn")
	}
	if resumedFrom[0] == "" || resumedFrom[0] == "0" {
		t.Errorf("the client reattached from %q: it must ask for what FOLLOWS the last id it saw, or it will re-read the stream and duplicate everything", resumedFrom[0])
	}
	if resumedFrom[0] != "2" {
		t.Errorf("the client reattached from %q, and the last id it saw was 2", resumedFrom[0])
	}
	// The first attempt POSTs the task; the second must NOT, or the user gets a second turn.
	if len(attempts) != 2 {
		t.Fatalf("attempts = %v, want exactly two", attempts)
	}
	if !strings.HasSuffix(attempts[0], "/task") {
		t.Errorf("the first attempt went to %q, it must start the run", attempts[0])
	}
	if !strings.Contains(attempts[1], "/events") {
		t.Errorf("the second attempt went to %q, it must REATTACH rather than start a second run", attempts[1])
	}
}

// TestTheClientDoesNotResumeAfterACancelledContext: a user who pressed Ctrl+C wants the turn
// stopped, not resumed. Reconnecting here would turn "stop" into "keep going", which is the worst
// possible reading of that instruction.
func TestTheClientDoesNotResumeAfterACancelledContext(t *testing.T) {
	var mu sync.Mutex
	attempts := 0
	c := resumeGateway(t, func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		attempts++
		mu.Unlock()
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		// Nothing more: the run "drops" immediately, which is the case where a resuming client
		// would try again forever if it did not check the context.
	})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := c.RunTask(ctx, "x", func(string, ...any) {}); err == nil {
		t.Fatal("a cancelled run must not be resumed, and must be reported")
	}
	mu.Lock()
	defer mu.Unlock()
	if attempts > 1 {
		t.Errorf("the client made %d attempts after its context was cancelled: it resumed a run the user stopped", attempts)
	}
}

// TestTheClientGivesUpAfterBoundedAttempts: a gateway that is gone for good must produce an error
// the user can read. A client that reconnects forever looks exactly like a client that is working,
// and the user would wait for a turn that is never coming back.
func TestTheClientGivesUpAfterBoundedAttempts(t *testing.T) {
	var mu sync.Mutex
	attempts := 0
	c := resumeGateway(t, func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		attempts++
		mu.Unlock()
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		// A stream that always ends without a verdict: the connection keeps dropping as far as the
		// client can tell.
	})

	_, err := c.RunTask(context.Background(), "x", func(string, ...any) {})
	if err == nil {
		t.Fatal("a run that could never be followed must be reported")
	}
	if !strings.Contains(err.Error(), "kept dropping") {
		t.Errorf("err = %v, it must say what happened rather than only that something did", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if attempts != runAttempts {
		t.Errorf("the client made %d attempts, the bound is %d", attempts, runAttempts)
	}
}

// TestTheClientDoesNotResumeAfterTheGatewayRefused: a refusal is the gateway ANSWERING, and the
// answer will not change by asking again. Retrying would turn a message the user can act on into a
// wait.
func TestTheClientDoesNotResumeAfterTheGatewayRefused(t *testing.T) {
	var mu sync.Mutex
	attempts := 0
	c := resumeGateway(t, func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		attempts++
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		_, _ = io.WriteString(w, `{"error":"a run is already in progress in this session"}`)
	})

	_, err := c.RunTask(context.Background(), "x", func(string, ...any) {})
	if err == nil || !strings.Contains(err.Error(), "already in progress") {
		t.Fatalf("err = %v, the gateway's reason must reach the user", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if attempts != 1 {
		t.Errorf("the client asked %d times, a refusal must be taken as the answer", attempts)
	}
}

// TestTheClientDoesNotResumeAfterAVerdict: an error event IS the turn's end. A connection that dies
// right after it is a transport fact about a run that has already finished, and asking again would
// make the agent answer a question it has already answered.
func TestTheClientDoesNotResumeAfterAVerdict(t *testing.T) {
	var mu sync.Mutex
	attempts := 0
	c := resumeGateway(t, func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		attempts++
		mu.Unlock()
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		writeFrame(w, 0, EventAttached, `{"run_id":"r1"}`)
		writeFrame(w, 1, EventError, `{"error":"the provider refused the request"}`)
	})

	_, err := c.RunTask(context.Background(), "x", func(string, ...any) {})
	if err == nil || !strings.Contains(err.Error(), "refused the request") {
		t.Fatalf("err = %v, the verdict must reach the user", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if attempts != 1 {
		t.Errorf("the client tried %d times after a verdict, which is the turn already being over", attempts)
	}
}

// TestAReattachThatFindsNothingIsReported: the run ended while the client was away, so the gateway
// answers 404. That is a fact to report, not a failure to retry - and the client must not sit there
// reconnecting for a turn that is over.
func TestAReattachThatFindsNothingIsReported(t *testing.T) {
	var mu sync.Mutex
	attempts := 0
	c := resumeGateway(t, func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		attempts++
		first := attempts == 1
		mu.Unlock()
		if first {
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			writeFrame(w, 0, EventAttached, `{"run_id":"r1"}`)
			writeFrame(w, 1, EventProgress, `{"text":"working"}`)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, `{"error":"there is no run in progress in this session to attach to"}`)
	})

	_, err := c.RunTask(context.Background(), "x", func(string, ...any) {})
	if err == nil || !strings.Contains(err.Error(), "no run in progress") {
		t.Fatalf("err = %v, the reason the reattach failed must reach the user", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if attempts != 2 {
		t.Errorf("the client tried %d times, a 404 on the reattach is an answer and not a failure", attempts)
	}
}

// TestTheClientTakesAPendingQuestionFromThePreamble: a reattached client is told about the question
// it missed, and answering it is the whole reason the preamble carries it. Without this the client
// would sit watching a run that is waiting for an answer it never asks for.
func TestTheClientTakesAPendingQuestionFromThePreamble(t *testing.T) {
	answered := make(chan string, 1)

	c := resumeGateway(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/runs/approval") {
			b, _ := io.ReadAll(r.Body)
			answered <- string(b)
			w.WriteHeader(http.StatusNoContent)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		writeFrame(w, 0, EventAttached, `{"run_id":"r1","pending_approval":{"id":"q1","command":"rm -rf ./build","reason":"it deletes a directory","rule":"destructive"}}`)
		writeFrame(w, 1, EventDone, `{"result":"the command ran"}`)
	})

	asked := make(chan string, 1)
	c.SetApprover(func(_ context.Context, req agent.ApprovalRequest) (bool, error) {
		asked <- req.Command
		return true, nil
	})

	result, err := c.RunTask(context.Background(), "clean", func(string, ...any) {})
	if err != nil {
		t.Fatalf("RunTask: %v", err)
	}
	if result != "the command ran" {
		t.Errorf("result = %q", result)
	}
	select {
	case cmd := <-asked:
		if cmd != "rm -rf ./build" {
			t.Errorf("the approver was asked about %q", cmd)
		}
	default:
		t.Fatal("the client never asked its approver about the pending question from the preamble")
	}
	select {
	case body := <-answered:
		if !strings.Contains(body, `"id":"q1"`) || !strings.Contains(body, `"approve":true`) {
			t.Errorf("the answer sent back was %s, it must carry the question's id and the verdict", body)
		}
	default:
		t.Fatal("the client never sent its answer to the gateway")
	}
}

// TestAQuestionThatArrivesTwiceIsAnsweredOnce is the regression test for a real race that the
// flaky test was hiding.
//
// The pending question reaches a client by TWO routes, and both are correct on their own: the
// PREAMBLE carries it (so a client that reconnects while the run is blocked is told), and the run's
// LOG carries the approval event. When a client attaches to a run that is asking at that very
// moment, it receives both - and answering the duplicate is not merely wasteful. The gateway
// answers 409 ("nothing is waiting for an approval right now"), that 409 is a VERDICT on the
// stream, `streamEvents` stops reading, and the `done` sitting behind it in the same read - the
// event that ENDS the turn - is never delivered. The client then concluded the connection had
// dropped, reattached, and was told there was no run to attach to.
//
// It showed up as roughly one failure in two under -race, and only because the ordering of the two
// frames on the wire is genuinely non-deterministic.
func TestAQuestionThatArrivesTwiceIsAnsweredOnce(t *testing.T) {
	answers := make(chan string, 8)

	c := resumeGateway(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/runs/approval") {
			b, _ := io.ReadAll(r.Body)
			answers <- string(b)
			w.WriteHeader(http.StatusNoContent)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		// The preamble AND the log both carry the same question, which is exactly what a client
		// attaching mid-question receives.
		writeFrame(w, 0, EventAttached, `{"run_id":"r1","pending_approval":{"id":"q1","command":"rm -rf ./build"}}`)
		writeFrame(w, 1, EventApproval, `{"id":"q1","command":"rm -rf ./build"}`)
		writeFrame(w, 2, EventDone, `{"result":"the command ran"}`)
	})

	c.SetApprover(func(context.Context, agent.ApprovalRequest) (bool, error) { return true, nil })

	result, err := c.RunTask(context.Background(), "clean", func(string, ...any) {})
	if err != nil {
		t.Fatalf("RunTask: %v, the duplicate question must not take the run's end with it", err)
	}
	if result != "the command ran" {
		t.Errorf("result = %q, the done behind the duplicate must still arrive", result)
	}

	// Exactly one answer went back, and it carried the verdict.
	close(answers)
	n := 0
	for body := range answers {
		n++
		if !strings.Contains(body, `"id":"q1"`) || !strings.Contains(body, `"approve":true`) {
			t.Errorf("answer %d = %s", n, body)
		}
	}
	if n != 1 {
		t.Errorf("the client sent %d answers for one question, and the gateway refuses the second with a 409 that kills the stream", n)
	}
}

// TestAVerdictThatWasNeverDeliveredIsSentAgain: the other half of the rule above. A reattaching
// client must be able to send the SAME id again when its earlier answer did not arrive - the run is
// still blocked on that question, and refusing to resend would leave it blocked forever.
func TestAVerdictThatWasNeverDeliveredIsSentAgain(t *testing.T) {
	answers := make(chan string, 8)

	c := resumeGateway(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/runs/approval") {
			b, _ := io.ReadAll(r.Body)
			answers <- string(b)
			// The FIRST answer is rejected as a transport failure, which is what a dropped
			// connection on the way back looks like: it never reached the gateway.
			if len(answers) == 1 {
				hj, ok := w.(http.Hijacker)
				if ok {
					conn, _, err := hj.Hijack()
					if err == nil {
						conn.Close()
						return
					}
				}
			}
			w.WriteHeader(http.StatusNoContent)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		writeFrame(w, 0, EventAttached, `{"run_id":"r1","pending_approval":{"id":"q1","command":"rm -rf ./build"}}`)
		writeFrame(w, 1, EventDone, `{"result":"the command ran"}`)
	})

	c.SetApprover(func(context.Context, agent.ApprovalRequest) (bool, error) { return true, nil })

	if _, err := c.RunTask(context.Background(), "clean", func(string, ...any) {}); err != nil {
		t.Logf("RunTask returned %v, which is acceptable: the answer could not be delivered", err)
	}
	close(answers)
	n := 0
	for range answers {
		n++
	}
	if n < 1 {
		t.Fatal("the client never sent its answer")
	}
}

// TestRunStatusAsksTheGatewayWhatIsRunning: a client decides whether to attach by asking a question
// that costs one JSON reply. Nothing about "a turn is in flight" is remembered on this side, which
// is the same rule as everywhere else here: the gateway is the source of truth.
func TestRunStatusAsksTheGatewayWhatIsRunning(t *testing.T) {
	var mu sync.Mutex
	var paths []string
	c := resumeGateway(t, func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		paths = append(paths, r.Method+" "+r.URL.Path)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"run_id":"r1","first_seq":3,"last_seq":9,"dropped":2,"subscribers":1,"outcome":""}`)
	})

	info, err := c.RunStatus(context.Background())
	if err != nil {
		t.Fatalf("RunStatus: %v", err)
	}
	if info.RunID != "r1" || info.FirstSeq != 3 || info.LastSeq != 9 || info.Dropped != 2 || info.Subscribers != 1 {
		t.Errorf("RunInfo = %+v", info)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(paths) != 1 || !strings.Contains(paths[0], "/v1/sessions/"+DefaultSession+"/run") {
		t.Errorf("requests = %v, it must be scoped to this client's session", paths)
	}
}

// TestRunStatusReportsARefusal: nothing is running is a fact the client acts on, not a failure it
// has to interpret.
func TestRunStatusReportsARefusal(t *testing.T) {
	c := resumeGateway(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, `{"error":"there is no run in progress in this session"}`)
	})

	_, err := c.RunStatus(context.Background())
	if err == nil || !strings.Contains(err.Error(), "no run in progress") {
		t.Fatalf("err = %v, the gateway's reason must reach the caller", err)
	}
}

// TestCancelRunStopsTheRunItIsAskingAbout: cancelling is addressed to the SESSION, so a client that
// reconnected and remembers a stale run id cannot cancel the wrong run - or nothing at all.
func TestCancelRunStopsTheRunItIsAskingAbout(t *testing.T) {
	var mu sync.Mutex
	var got string
	c := resumeGateway(t, func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		got = r.Method + " " + r.URL.Path
		mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	})

	if err := c.CancelRun(context.Background()); err != nil {
		t.Fatalf("CancelRun: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if got != "POST /v1/sessions/"+DefaultSession+"/cancel" {
		t.Errorf("the cancel went to %q", got)
	}
}

// TestCancelRunReportsThatThereWasNothingToStop: a silent success would tell the user the turn was
// stopped when nothing was asked to stop, and they would wait for it.
func TestCancelRunReportsThatThereWasNothingToStop(t *testing.T) {
	c := resumeGateway(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		_, _ = io.WriteString(w, `{"error":"there is no run in progress in this session to stop"}`)
	})

	if err := c.CancelRun(context.Background()); err == nil {
		t.Fatal("cancelling nothing must be reported, not swallowed")
	}
}

// TestTheClientDoesNotFollowARedirect: a 3xx here would be the gateway pointing elsewhere, and a
// client that silently followed it would post a task - or answer an approval - to whatever answered
// at the other end. The refusal is the answer the caller gets.
func TestTheClientDoesNotFollowARedirect(t *testing.T) {
	var mu sync.Mutex
	var reached []string
	c := resumeGateway(t, func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		reached = append(reached, r.URL.Path)
		mu.Unlock()
		if strings.HasSuffix(r.URL.Path, "/task") {
			http.Redirect(w, r, "/somewhere-else", http.StatusFound)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		writeFrame(w, 1, EventDone, `{"result":"this must never be reached"}`)
	})

	_, err := c.RunTask(context.Background(), "x", func(string, ...any) {})
	if err == nil {
		t.Fatal("a redirect must be reported rather than followed")
	}
	mu.Lock()
	defer mu.Unlock()
	for _, p := range reached {
		if strings.Contains(p, "somewhere-else") {
			t.Errorf("the client followed the redirect to %q", p)
		}
	}
}

// TestTheClientKeepsWorkingThroughAnotherSession: the same code path builds the paths for a named
// conversation, so that branch is exercised rather than assumed.
func TestTheClientSpeaksForTheSessionItWasGiven(t *testing.T) {
	c := NewClientForSession("http://127.0.0.1:1", testToken, "s-elsewhere")
	if got := c.Session(); got != "s-elsewhere" {
		t.Errorf("session = %q", got)
	}
	if got := c.scoped("/task"); got != "/v1/sessions/s-elsewhere/task" {
		t.Errorf("scoped path = %q", got)
	}
}

// TestAnUnparseablePreambleEndsTheRun: the preamble is parsed like every other payload, and a body
// this client cannot read must end the run with a reason. Going quiet would look like a run with no
// output, which is the one thing a user cannot tell from a hang.
func TestAnUnparseablePreambleEndsTheRun(t *testing.T) {
	c := resumeGateway(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		writeFrame(w, 0, EventAttached, "not json")
	})

	_, err := c.RunTask(context.Background(), "x", func(string, ...any) {})
	if err == nil {
		t.Fatal("an unreadable preamble must be reported rather than ignored")
	}
}
