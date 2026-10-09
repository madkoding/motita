package gateway

import (
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The cookie is not isolated by port, so a page on another port of this host is same-site and its
// browser attaches the cookie. These tests drive the requests such a page can send and the ones the
// web interface sends, through the real handler chain.

// renameWithCookie builds the request a page would send to rename the default session, carrying
// the browser's cookie, and lets the caller add what a particular page could add.
func renameWithCookie(body string, edit func(*http.Request)) *http.Request {
	req := httptest.NewRequest(http.MethodPatch, "/v1/sessions/"+DefaultSession, strings.NewReader(body))
	req.AddCookie(&http.Cookie{Name: webuiCookie, Value: cookieValue(testToken)})
	if edit != nil {
		edit(req)
	}
	return req
}

func serve(srv *Server, req *http.Request) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	return w
}

// A same-site page can send a text/plain request with the cookie and no custom header: no
// preflight is needed for it. It must not change anything.
func TestACookieRequestWithoutTheBrowserHeaderIsRefused(t *testing.T) {
	srv := newTestServer(t, &fakeService{})
	w := serve(srv, renameWithCookie(`{"title":"pwned"}`, func(r *http.Request) {
		r.Header.Set("Content-Type", "text/plain")
		r.Header.Set("Origin", "http://example.com:3000")
	}))
	if w.Code != http.StatusForbidden {
		t.Fatalf("a cookie request with no %s header answered %d, want 403: %s", browserHeader, w.Code, w.Body)
	}
	if c, _ := srv.lookup(DefaultSession); c.status().Title == "pwned" {
		t.Fatal("the refused request renamed the session")
	}
}

// The web interface's own request - the header, its own origin, a JSON body - goes through.
func TestTheWebInterfacesOwnRequestIsServed(t *testing.T) {
	srv := newTestServer(t, &fakeService{})
	w := serve(srv, renameWithCookie(`{"title":"renamed"}`, func(r *http.Request) {
		r.Header.Set(browserHeader, "1")
		r.Header.Set("Origin", "http://example.com")
		r.Header.Set("Content-Type", "application/json; charset=utf-8")
	}))
	if w.Code != http.StatusOK {
		t.Fatalf("the interface's own rename answered %d, want 200: %s", w.Code, w.Body)
	}
}

// A page on another origin that somehow sends the header is still refused by its Origin.
func TestACookieRequestFromAnotherOriginIsRefused(t *testing.T) {
	srv := newTestServer(t, &fakeService{})
	w := serve(srv, renameWithCookie(`{"title":"pwned"}`, func(r *http.Request) {
		r.Header.Set(browserHeader, "1")
		r.Header.Set("Origin", "http://example.com:3000")
		r.Header.Set("Content-Type", "application/json")
	}))
	if w.Code != http.StatusForbidden || !strings.Contains(w.Body.String(), "another origin") {
		t.Fatalf("a foreign Origin answered %d, want 403 naming the origin: %s", w.Code, w.Body)
	}
}

// A cookie request with a body that is not declared JSON is refused: a form or a text/plain
// fetch is exactly what a page on another origin can send without asking.
func TestACookieRequestWithANonJSONBodyIsRefused(t *testing.T) {
	srv := newTestServer(t, &fakeService{})
	w := serve(srv, renameWithCookie(`{"title":"pwned"}`, func(r *http.Request) {
		r.Header.Set(browserHeader, "1")
		r.Header.Set("Content-Type", "text/plain")
	}))
	if w.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("a text/plain body with the cookie answered %d, want 415: %s", w.Code, w.Body)
	}
}

// A bearer client - the CLI, a script - needs none of it: no page can attach the token.
func TestABearerClientNeedsNoBrowserHeader(t *testing.T) {
	srv := newTestServer(t, &fakeService{})
	req := httptest.NewRequest(http.MethodPatch, "/v1/sessions/"+DefaultSession, strings.NewReader(`{"title":"from the cli"}`))
	req.Header.Set("Authorization", "Bearer "+testToken)
	if w := serve(srv, req); w.Code != http.StatusOK {
		t.Fatalf("a bearer rename with no extra header answered %d, want 200: %s", w.Code, w.Body)
	}
}

// A read is left alone (another origin cannot read the answer), but a WebSocket upgrade is not
// subject to CORS and is judged by its Origin.
func TestAWebSocketUpgradeIsJudgedByItsOrigin(t *testing.T) {
	read := httptest.NewRequest(http.MethodGet, "/", nil)
	read.Header.Set("Origin", "http://evil.example")
	if msg := crossSiteRefusal(read); msg != "" {
		t.Errorf("a plain read was refused: %s", msg)
	}
	upgrade := httptest.NewRequest(http.MethodGet, "/", nil)
	upgrade.Header.Set("Upgrade", "websocket")
	upgrade.Header.Set("Origin", "http://evil.example")
	if crossSiteRefusal(upgrade) == "" {
		t.Error("a WebSocket upgrade from another origin was let through")
	}
	upgrade.Header.Set("Origin", "http://example.com")
	if msg := crossSiteRefusal(upgrade); msg != "" {
		t.Errorf("a WebSocket upgrade from this origin was refused: %s", msg)
	}
}

// Behind TLS the origin a browser writes is https.
func TestRequestOriginFollowsTheScheme(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	if got := requestOrigin(r); got != "http://example.com" {
		t.Errorf("plain origin = %q", got)
	}
	r.TLS = &tls.ConnectionState{}
	if got := requestOrigin(r); got != "https://example.com" {
		t.Errorf("TLS origin = %q", got)
	}
}
