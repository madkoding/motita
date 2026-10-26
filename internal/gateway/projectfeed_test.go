package gateway

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// waitSignal reports whether the listener was told within d.
func waitSignal(ch <-chan struct{}, d time.Duration) bool {
	select {
	case <-ch:
		return true
	case <-time.After(d):
		return false
	}
}

// A page is told when a project changes under it, and only then.
func TestProjectFeedTellsOnlyWhatChanged(t *testing.T) {
	srv := newTestServer(t, &fakeService{})
	ws := t.TempDir()
	withProjectsAndFake(t, srv, ws)
	id := makeProject(t, srv, "feed")
	dir := filepath.Join(ws, "feed")

	ch, unsubscribe := srv.remoteWatch.feed.subscribe(srv)
	defer unsubscribe()

	srv.remoteWatch.feed.kick()
	if waitSignal(ch, 300*time.Millisecond) {
		t.Fatal("nothing changed and the page was told")
	}
	if err := os.WriteFile(filepath.Join(dir, "new.txt"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	srv.remoteWatch.feed.kick()
	if !waitSignal(ch, 5*time.Second) {
		t.Fatalf("an edited file of %s was not told", id)
	}
	srv.remoteWatch.feed.kick()
	if waitSignal(ch, 300*time.Millisecond) {
		t.Fatal("the same state was told twice")
	}
}

// The look stops with the last page and starts again with the next.
func TestProjectFeedStopsWithItsLastListener(t *testing.T) {
	srv := newTestServer(t, &fakeService{})
	withProjectsAndFake(t, srv, t.TempDir())
	f := &srv.remoteWatch.feed
	f.kick() // nobody listens: nothing to wake
	_, off1 := f.subscribe(srv)
	_, off2 := f.subscribe(srv)
	off1()
	if f.cancel == nil {
		t.Fatal("the look stopped while a page still listened")
	}
	off2()
	if f.cancel != nil {
		t.Fatal("the look kept running without a page")
	}
	_, off3 := f.subscribe(srv)
	defer off3()
	if f.cancel == nil {
		t.Fatal("the look did not start again")
	}
}

// Without a project store the signature is of an empty list, and a broken store reads as "".
func TestProjectsSignatureWithoutProjects(t *testing.T) {
	srv := newTestServer(t, &fakeService{})
	srv.projects = nil
	if projectsSignature(srv) == "" {
		t.Fatal("no projects is a state, not a failure")
	}
}

// lockedRecorder is a recorder the test can read while the handler still writes to it.
type lockedRecorder struct {
	*httptest.ResponseRecorder
	mu sync.Mutex
}

func (l *lockedRecorder) Write(b []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.ResponseRecorder.Write(b)
}

func (l *lockedRecorder) WriteString(s string) (int, error) { return l.Write([]byte(s)) }

func (l *lockedRecorder) body() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.Body.String()
}

// The stream answers with event-stream headers, a "projects" event on a change, and ends with the
// request.
func TestProjectEventsStream(t *testing.T) {
	srv := newTestServer(t, &fakeService{})
	ws := t.TempDir()
	withProjectsAndFake(t, srv, ws)
	makeProject(t, srv, "stream")

	ctx, cancel := context.WithCancel(context.Background())
	req := httptest.NewRequest(http.MethodGet, "/v1/projects/events", nil).WithContext(ctx)
	rec := &lockedRecorder{ResponseRecorder: httptest.NewRecorder()}
	done := make(chan struct{})
	go func() {
		srv.handleProjectEvents(rec, req)
		close(done)
	}()
	time.Sleep(200 * time.Millisecond) // the listener is registered
	if err := os.WriteFile(filepath.Join(ws, "stream", "a.txt"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	srv.remoteWatch.feed.kick()
	deadline := time.Now().Add(5 * time.Second)
	for !strings.Contains(rec.body(), "event: projects") && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	cancel()
	<-done
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		t.Errorf("content type = %q", ct)
	}
	if !strings.Contains(rec.body(), "event: projects") {
		t.Errorf("no event in %q", rec.body())
	}
}

// goneWriter accepts the first ok writes and refuses the rest, like a page that went away.
type goneWriter struct {
	header http.Header
	ok     int
}

func (f *goneWriter) Header() http.Header { return f.header }
func (f *goneWriter) WriteHeader(int)     {}
func (f *goneWriter) Flush()              {}
func (f *goneWriter) Write(b []byte) (int, error) {
	if f.ok == 0 {
		return 0, http.ErrHandlerTimeout
	}
	f.ok--
	return len(b), nil
}

// A page that is gone ends the stream: at the first write, at an event, at a keepalive.
func TestProjectEventsEndsWhenThePageIsGone(t *testing.T) {
	srv := newTestServer(t, &fakeService{})
	withProjectsAndFake(t, srv, t.TempDir())
	req := httptest.NewRequest(http.MethodGet, "/v1/projects/events", nil)

	srv.handleProjectEvents(&goneWriter{header: http.Header{}}, req) // the preamble

	oldKeep, oldTick := projectFeedKeepalive, projectFeedInterval
	t.Cleanup(func() { projectFeedKeepalive, projectFeedInterval = oldKeep, oldTick })
	projectFeedKeepalive = 20 * time.Millisecond
	srv.handleProjectEvents(&goneWriter{header: http.Header{}, ok: 1}, req) // the keepalive

	projectFeedKeepalive = time.Hour
	done := make(chan struct{})
	go func() {
		srv.handleProjectEvents(&goneWriter{header: http.Header{}, ok: 1}, req) // the event
		close(done)
	}()
	time.Sleep(200 * time.Millisecond)
	srv.remoteWatch.feed.broadcast()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the stream did not end")
	}
}

// The look also runs on its own clock, not only when nudged.
func TestProjectFeedLooksOnItsOwnClock(t *testing.T) {
	srv := newTestServer(t, &fakeService{})
	ws := t.TempDir()
	withProjectsAndFake(t, srv, ws)
	makeProject(t, srv, "clock")
	old := projectFeedInterval
	t.Cleanup(func() { projectFeedInterval = old })
	projectFeedInterval = 20 * time.Millisecond
	ch, off := srv.remoteWatch.feed.subscribe(srv)
	defer off()
	time.Sleep(100 * time.Millisecond)
	if err := os.WriteFile(filepath.Join(ws, "clock", "a.txt"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !waitSignal(ch, 5*time.Second) {
		t.Fatal("the clock did not notice the change")
	}
}
