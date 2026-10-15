package gateway

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"testing"
)

// A client that reconnects to its run names it with the run_id of the attached event, so the
// gateway attaches it to ITS run and not to whichever one is current.
func TestTheClientReattachesToItsOwnRun(t *testing.T) {
	var mu sync.Mutex
	var runs []string
	c := resumeGateway(t, func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		first := len(runs) == 0
		runs = append(runs, r.URL.Query().Get("run"))
		mu.Unlock()
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		if first {
			writeFrame(w, 0, EventAttached, `{"run_id":"r-mine"}`)
			writeFrame(w, 1, EventProgress, `{"text":"working"}`)
			return // the connection drops
		}
		writeFrame(w, 0, EventAttached, `{"run_id":"r-mine"}`)
		writeFrame(w, 2, EventDone, `{"result":"the answer"}`)
	})

	result, err := c.RunTask(context.Background(), "x", nil)
	if err != nil || result != "the answer" {
		t.Fatalf("RunTask = (%q, %v)", result, err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(runs) != 2 || runs[0] != "" || runs[1] != "r-mine" {
		t.Fatalf("the reattach must name the run it follows: %q", runs)
	}
}

// A run that is no longer there is a refusal the client reports, not one it retries.
func TestTheClientReportsThatItsRunIsGone(t *testing.T) {
	var mu sync.Mutex
	calls := 0
	c := resumeGateway(t, func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		calls++
		n := calls
		mu.Unlock()
		if n > 1 {
			writeError(w, http.StatusGone, `the run "r-mine" is no longer available in this session`)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		writeFrame(w, 0, EventAttached, `{"run_id":"r-mine"}`)
	})

	_, err := c.RunTask(context.Background(), "x", nil)
	if err == nil || !strings.Contains(err.Error(), "no longer available") {
		t.Fatalf("err = %v, want the gateway's reason", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if calls != 2 {
		t.Fatalf("calls = %d, want the refusal taken as final", calls)
	}
}

// dialOnce fails the first request as a connection that could not be opened, and sends the rest.
type dialOnce struct {
	mu      sync.Mutex
	failed  bool
	methods []string
}

func (d *dialOnce) RoundTrip(r *http.Request) (*http.Response, error) {
	d.mu.Lock()
	d.methods = append(d.methods, r.Method)
	fail := !d.failed
	d.failed = true
	d.mu.Unlock()
	if fail {
		return nil, &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("connection refused")}
	}
	return http.DefaultTransport.RoundTrip(r)
}

// A POST that never reached the gateway started nothing: the next attempt POSTs again instead of
// reattaching to whatever run is in flight.
func TestAPostThatCouldNotConnectIsSentAgain(t *testing.T) {
	c := resumeGateway(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeError(w, http.StatusNotFound, "there is no run in progress in this session to attach to")
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		writeFrame(w, 0, EventAttached, `{"run_id":"r1"}`)
		writeFrame(w, 1, EventDone, `{"result":"the answer"}`)
	})
	tr := &dialOnce{}
	c.http.Transport = tr

	result, err := c.RunTask(context.Background(), "x", nil)
	if err != nil || result != "the answer" {
		t.Fatalf("RunTask = (%q, %v)", result, err)
	}
	if len(tr.methods) != 2 || tr.methods[1] != http.MethodPost {
		t.Fatalf("requests = %v, want the POST sent again", tr.methods)
	}
}

func TestNotConnected(t *testing.T) {
	dial := &net.OpError{Op: "dial", Err: errors.New("refused")}
	cases := []struct {
		err  error
		want bool
	}{
		{dial, true},
		{fmt.Errorf("post: %w", dial), true},
		{&net.OpError{Op: "read", Err: errors.New("reset")}, false},
		{errors.New("the connection ended"), false},
		{nil, false},
	}
	for _, tc := range cases {
		if got := notConnected(tc.err); got != tc.want {
			t.Errorf("notConnected(%v) = %v, want %v", tc.err, got, tc.want)
		}
	}
}

func TestEventsURLNamesTheRunWhenKnown(t *testing.T) {
	from := uint64(7)
	f := &runFollow{sid: "s1", from: &from}
	if got := f.eventsURL("http://g"); got != "http://g/v1/sessions/s1/events?from=7" {
		t.Errorf("no run id: %s", got)
	}
	id := ""
	f.runID = &id
	if got := f.eventsURL("http://g"); strings.Contains(got, "run=") {
		t.Errorf("an empty run id must not be sent: %s", got)
	}
	id = "r 1"
	if got := f.eventsURL("http://g"); !strings.HasSuffix(got, "?from=7&run=r+1") {
		t.Errorf("the run id must be sent escaped: %s", got)
	}
}
