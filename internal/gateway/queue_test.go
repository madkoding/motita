package gateway

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// queueRec is shared by the services of a test: it records which tasks ran.
type queueRec struct {
	mu        sync.Mutex
	tasks     []string
	started   chan struct{}
	release   chan struct{}
	cancelled atomic.Bool
}

func (q *queueRec) add(t string) {
	q.mu.Lock()
	q.tasks = append(q.tasks, t)
	q.mu.Unlock()
}

func (q *queueRec) ran(t string) bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	for _, x := range q.tasks {
		if x == t {
			return true
		}
	}
	return false
}

// queueProbe blocks on tasks starting with "block" and answers any other at once.
type queueProbe struct {
	fakeService
	rec *queueRec
}

func (p *queueProbe) RunTask(ctx context.Context, task string, progress func(string, ...any)) (string, error) {
	p.rec.add(task)
	progress("step of %s", task)
	if strings.HasPrefix(task, "block") {
		select {
		case p.rec.started <- struct{}{}:
		default:
		}
		select {
		case <-p.rec.release:
			return "released", nil
		case <-ctx.Done():
			p.rec.cancelled.Store(true)
			return "", ctx.Err()
		}
	}
	return "result of " + task, nil
}

func newQueueServer(t *testing.T) (*Server, *queueRec) {
	t.Helper()
	rec := &queueRec{started: make(chan struct{}, 4), release: make(chan struct{})}
	srv := newTestServer(t, &queueProbe{rec: rec}, func(o *Options) {
		o.SessionDir = t.TempDir()
		o.NewService = func() (Service, error) { return &queueProbe{rec: rec}, nil }
	})
	return srv, rec
}

func waitUntil(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func waitBlocked(t *testing.T, rec *queueRec) {
	t.Helper()
	select {
	case <-rec.started:
	case <-time.After(10 * time.Second):
		t.Fatal("the blocking run never started")
	}
}

// A message sent while the agent is busy is queued, and an interruption stops the
// current run and lets the queued message run.
func TestQueueWhileBusyAndInterrupt(t *testing.T) {
	srv, rec := newQueueServer(t)
	a := newSessionFor(t, srv)
	quietRunFor(t, srv, a.ID)
	t.Cleanup(func() { close(rec.release) })

	abandon := startInBackground(t, srv, a.ID, "/task", `{"task":"block-1"}`)
	defer abandon()
	waitBlocked(t, rec)

	base := "/v1/sessions/" + a.ID
	w := post(t, srv, base+"/queue", `{"task":"second"}`, testToken)
	if w.Code != http.StatusAccepted || !strings.Contains(w.Body.String(), `"queued":true`) {
		t.Fatalf("queue while busy: %d %s", w.Code, w.Body.String())
	}
	if rec.ran("second") {
		t.Fatal("the queued message ran while the agent was still busy")
	}
	if w := post(t, srv, base+"/queue", `{"task":"  "}`, testToken); w.Code != http.StatusBadRequest {
		t.Fatalf("empty queue task: %d", w.Code)
	}

	if w := post(t, srv, base+"/interrupt", `{}`, testToken); w.Code != http.StatusNoContent {
		t.Fatalf("interrupt: %d %s", w.Code, w.Body.String())
	}
	waitUntil(t, "the queued message to run", func() bool { return rec.ran("second") })
	if !rec.cancelled.Load() {
		t.Fatal("the interruption did not cancel the run in flight")
	}
	waitUntil(t, "the session to go idle", func() bool { return !srv.conversationOf(a.ID).isRunning() })

	if w := post(t, srv, base+"/interrupt", `{}`, testToken); w.Code != http.StatusConflict {
		t.Fatalf("interrupt when idle: %d", w.Code)
	}
	w = post(t, srv, base+"/queue", `{"task":"idle-start"}`, testToken)
	if w.Code != http.StatusAccepted || !strings.Contains(w.Body.String(), `"started":true`) {
		t.Fatalf("queue when idle: %d %s", w.Code, w.Body.String())
	}
}

// Interrupting with a message runs that message next.
func TestInterruptWithTaskRunsItNext(t *testing.T) {
	srv, rec := newQueueServer(t)
	a := newSessionFor(t, srv)
	quietRunFor(t, srv, a.ID)
	t.Cleanup(func() { close(rec.release) })
	abandon := startInBackground(t, srv, a.ID, "/task", `{"task":"block-1"}`)
	defer abandon()
	waitBlocked(t, rec)
	base := "/v1/sessions/" + a.ID
	post(t, srv, base+"/queue", `{"task":"later"}`, testToken)
	if w := post(t, srv, base+"/interrupt", `{"task":"urgent"}`, testToken); w.Code != http.StatusNoContent {
		t.Fatalf("interrupt: %d", w.Code)
	}
	waitUntil(t, "both messages to run", func() bool { return rec.ran("urgent") && rec.ran("later") })
	rec.mu.Lock()
	defer rec.mu.Unlock()
	if rec.tasks[1] != "urgent" {
		t.Fatalf("urgent must run before the queue, order: %v", rec.tasks)
	}
}

// A message in session B is served while session A's agent runs, and A's streamed
// output never shows up in B.
func TestOtherSessionNotBlockedAndNotMixed(t *testing.T) {
	srv, rec := newQueueServer(t)
	a := newSessionFor(t, srv)
	b := newSessionFor(t, srv)
	quietRunFor(t, srv, a.ID)
	quietRunFor(t, srv, b.ID)
	t.Cleanup(func() { close(rec.release) })

	abandon := startInBackground(t, srv, a.ID, "/task", `{"task":"block-A"}`)
	defer abandon()
	waitBlocked(t, rec)

	w := post(t, srv, "/v1/sessions/"+b.ID+"/task", `{"task":"quick-B"}`, testToken)
	if w.Code != http.StatusOK {
		t.Fatalf("B refused while A runs: %d %s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	if !strings.Contains(body, "result of quick-B") {
		t.Fatalf("B did not get its answer: %s", body)
	}
	if strings.Contains(body, "block-A") {
		t.Fatalf("A's stream leaked into B: %s", body)
	}
	if !srv.conversationOf(a.ID).isRunning() {
		t.Fatal("A should still be running")
	}
}
