package gateway

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
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
