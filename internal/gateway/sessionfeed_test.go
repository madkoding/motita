package gateway

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// A turn that starts or ends moves the session's rev, and with it the digest a page is told about;
// a read does not.
func TestSessionFeedSeesATurnStartAndEnd(t *testing.T) {
	srv := newTestServer(t, &fakeService{})
	c := namedDefault(t, srv)
	before := sessionsSignature(srv)
	if get(t, srv, "/v1/sessions/"+DefaultSession+"/messages", testToken).Code != http.StatusOK {
		t.Fatal("the transcript did not answer")
	}
	if sessionsSignature(srv) != before {
		t.Fatal("reading the conversation changed what pages are told")
	}
	if !c.takeRunSlot() {
		t.Fatal("the slot was taken")
	}
	started := sessionsSignature(srv)
	if started == before {
		t.Fatal("a turn that started was not seen")
	}
	srv.releaseAndDrain(c)
	if sessionsSignature(srv) == started {
		t.Fatal("a turn that ended was not seen")
	}
	if c.status().Rev != 2 {
		t.Fatalf("rev = %d, want 2", c.status().Rev)
	}
}

// The stream sends a "sessions" event when a session changes, on its own clock.
func TestSessionEventsStream(t *testing.T) {
	old := sessionFeedInterval
	t.Cleanup(func() { sessionFeedInterval = old })
	sessionFeedInterval = 20 * time.Millisecond
	srv := newTestServer(t, &fakeService{})
	c := namedDefault(t, srv)

	ctx, cancel := context.WithCancel(context.Background())
	req := httptest.NewRequest(http.MethodGet, "/v1/sessions/events", nil).WithContext(ctx)
	rec := &lockedRecorder{ResponseRecorder: httptest.NewRecorder()}
	done := make(chan struct{})
	go func() {
		srv.handleSessionEvents(rec, req)
		close(done)
	}()
	time.Sleep(200 * time.Millisecond) // the listener is registered
	c.takeRunSlot()
	deadline := time.Now().Add(5 * time.Second)
	for !strings.Contains(rec.body(), "event: sessions") && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	cancel()
	<-done
	if !strings.Contains(rec.body(), "event: sessions") {
		t.Errorf("no event in %q", rec.body())
	}
}

// The route is served behind the token, and is not mistaken for a session called "events".
func TestSessionEventsRoute(t *testing.T) {
	srv := newTestServer(t, &fakeService{})
	if w := get(t, srv, "/v1/sessions/events", "wrong"); w.Code != http.StatusUnauthorized {
		t.Fatalf("without the token: %d", w.Code)
	}
	// The default session nobody spoke to is not listed, so it is not in the digest either.
	empty := sessionsSignature(srv)
	namedDefault(t, srv)
	if sessionsSignature(srv) == empty {
		t.Fatal("a session that talked is not in the digest")
	}
}

// namedDefault is the default session with a title, so the list (and the feed) counts it.
func namedDefault(t *testing.T, srv *Server) *conversation {
	t.Helper()
	c, ok := srv.lookup(DefaultSession)
	if !ok {
		t.Fatal("no default session")
	}
	c.stateMu.Lock()
	c.title = "talked"
	c.stateMu.Unlock()
	return c
}

// One stream carries both feeds: a session that moved is a "sessions" event and a project that
// moved a "projects" event, on the same connection.
func TestEventsStreamCarriesEveryFeed(t *testing.T) {
	srv := newTestServer(t, &fakeService{})
	withProjectsAndFake(t, srv, t.TempDir())
	c := namedDefault(t, srv)
	if w := get(t, srv, "/v1/events", "wrong"); w.Code != http.StatusUnauthorized {
		t.Fatalf("without the token: %d", w.Code)
	}

	ctx, cancel := context.WithCancel(context.Background())
	req := httptest.NewRequest(http.MethodGet, "/v1/events", nil).WithContext(ctx)
	rec := &lockedRecorder{ResponseRecorder: httptest.NewRecorder()}
	done := make(chan struct{})
	go func() {
		srv.handleEvents(rec, req)
		close(done)
	}()
	time.Sleep(200 * time.Millisecond) // the listeners are registered
	c.takeRunSlot()
	srv.sessionFeed.kick()
	srv.remoteWatch.feed.broadcast()
	deadline := time.Now().Add(5 * time.Second)
	both := func() bool {
		b := rec.body()
		return strings.Contains(b, "event: sessions") && strings.Contains(b, "event: projects")
	}
	for !both() && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	cancel()
	<-done
	if !both() {
		t.Errorf("not both events in %q", rec.body())
	}
}

// Two changes of one feed before the stream writes are one event, and the names come out sorted.
func TestFeedChangesCoalesce(t *testing.T) {
	c := &feedChanges{signal: make(chan struct{}, 1), names: map[string]bool{}}
	ctx, cancel := context.WithCancel(context.Background())
	from := make(chan struct{})
	other := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(2)
	forward := func(ch chan struct{}, name string) {
		defer wg.Done()
		c.forward(ctx, ch, name)
	}
	go forward(from, "sessions")
	go forward(other, "projects")
	from <- struct{}{}
	from <- struct{}{}
	other <- struct{}{}
	cancel()
	wg.Wait() // each forwarder marked what it received before it saw the end
	<-c.signal
	if got := strings.Join(c.take(), ","); got != "projects,sessions" {
		t.Fatalf("take = %q", got)
	}
	if len(c.take()) != 0 {
		t.Fatal("take did not forget")
	}
}

// A page that is gone ends the combined stream at an event.
func TestEventsStreamEndsWhenThePageIsGone(t *testing.T) {
	srv := newTestServer(t, &fakeService{})
	req := httptest.NewRequest(http.MethodGet, "/v1/events", nil)
	done := make(chan struct{})
	go func() {
		srv.handleEvents(&goneWriter{header: http.Header{}, ok: 1}, req)
		close(done)
	}()
	time.Sleep(200 * time.Millisecond)
	srv.sessionFeed.broadcast()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the stream did not end")
	}
}
