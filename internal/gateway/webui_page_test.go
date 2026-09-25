package gateway

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/madkoding/motita/internal/webui"
)

// The page is reachable WITHOUT a token, and that is deliberate: it is the only way a browser
// can obtain one, exactly like a login form. It must therefore be safe to serve to anybody who
// can reach the port, which is a property internal/webui tests on its own bytes.
func TestThePageIsServedWithoutAToken(t *testing.T) {
	srv := newTestServer(t, &fakeService{}, func(o *Options) { o.WebUI = true })

	for _, path := range webui.Names() {
		resp, err := http.Get(srv.BaseURL() + path)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		body, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("GET %s answered %d without a token, want 200", path, resp.StatusCode)
		}
		if len(body) == 0 {
			t.Fatalf("GET %s answered 200 with an empty body", path)
		}
	}
}

// The page must not be cached. It is part of the binary, so a client holding yesterday's copy
// after an upgrade is running code that no longer exists.
func TestThePageIsNotCached(t *testing.T) {
	srv := newTestServer(t, &fakeService{}, func(o *Options) { o.WebUI = true })
	resp, err := http.Get(srv.BaseURL() + "/")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if got := resp.Header.Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control = %q, want no-store", got)
	}
}

// Every name the package exposes must be routed: this is what stops the two lists from drifting,
// and it is why Names() exists at all.
func TestEveryPageNameIsRegistered(t *testing.T) {
	srv := newTestServer(t, &fakeService{}, func(o *Options) { o.WebUI = true })

	// Asking the server itself, rather than a table in this file: a path is registered if a
	// real request for it comes back with the page instead of a 404. That is the property that
	// matters, and it cannot drift from the router the way a hand-kept list can.
	for _, name := range webui.Names() {
		resp, err := http.Get(srv.BaseURL() + name)
		if err != nil {
			t.Fatalf("GET %s: %v", name, err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("%s is exposed by internal/webui but answered %d: it is not routed", name, resp.StatusCode)
		}
	}
}

// An unknown path must still be a 404. Registering "GET /" without {$} would answer every typo
// with the page, which looks like it worked and hides the mistake.
func TestAnUnknownPathIsStillNotFound(t *testing.T) {
	srv := newTestServer(t, &fakeService{}, func(o *Options) { o.WebUI = true })
	resp, err := http.Get(srv.BaseURL() + "/nope.js")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("GET /nope.js answered %d, want 404: a catch-all route would hide typos", resp.StatusCode)
	}
}

// With the interface off, the root is not served at all: an operator who does not want a web
// front end must not have one, and that includes the page.
func TestThePageIsAbsentWhenTheInterfaceIsOff(t *testing.T) {
	srv := newTestServer(t, &fakeService{}) // WebUI left false

	resp, err := http.Get(srv.BaseURL() + "/")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("GET / answered %d with the interface off, want 404", resp.StatusCode)
	}
	// And the exchange endpoint goes with it: no page means nothing to exchange for.
	req, err := http.NewRequest(http.MethodPost, srv.BaseURL()+"/v1/webui/session", nil)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+testToken)
	resp2, err := (&http.Client{}).Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer func() { _ = resp2.Body.Close() }()
	if resp2.StatusCode != http.StatusNotFound {
		t.Fatalf("the exchange answered %d with the interface off, want 404", resp2.StatusCode)
	}
}

// Exchanging the token for the cookie requires the token: this is the one endpoint where the
// fragment is handed over, and it must not become a way in.
func TestTheCookieEndpointRequiresTheToken(t *testing.T) {
	srv := newTestServer(t, &fakeService{}, func(o *Options) { o.WebUI = true })

	resp, err := http.Post(srv.BaseURL()+"/v1/webui/session", "application/json", nil)
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("POST /v1/webui/session without a token answered %d, want 401", resp.StatusCode)
	}
}

// With the token it sets a cookie, and every attribute on it is a decision worth pinning.
func TestTheCookieEndpointSetsAProtectedCookie(t *testing.T) {
	srv := newTestServer(t, &fakeService{}, func(o *Options) { o.WebUI = true })

	req, err := http.NewRequest(http.MethodPost, srv.BaseURL()+"/v1/webui/session", nil)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+testToken)
	resp, err := (&http.Client{}).Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("answered %d, want 204", resp.StatusCode)
	}

	var got *http.Cookie
	for _, c := range resp.Cookies() {
		if c.Name == webuiCookie {
			got = c
		}
	}
	if got == nil {
		t.Fatal("no cookie was set")
	}
	if !got.HttpOnly {
		t.Error("the cookie is not HttpOnly: an injected script could read it")
	}
	if got.SameSite != http.SameSiteStrictMode {
		t.Errorf("SameSite = %v, want Strict: another origin must never send it", got.SameSite)
	}
	if got.Value != cookieValue(testToken) {
		t.Error("the cookie does not carry the derived value")
	}
	if got.Path != "/" {
		t.Errorf("Path = %q, want /", got.Path)
	}
	// It must NOT be Secure: the gateway speaks plain http, and a Secure cookie over http is one
	// the browser silently refuses to send - the interface would just never stay connected.
	if got.Secure {
		t.Error("the cookie is Secure, but the gateway speaks plain http: no browser would send it")
	}
}

// The exchange is authorised by the token, so a cookie is not a way to mint another one: a
// browser that already has the cookie cannot extend its own life without the token.
func TestTheExchangeCannotBeDrivenByACookie(t *testing.T) {
	srv := newTestServer(t, &fakeService{}, func(o *Options) { o.WebUI = true })

	req, err := http.NewRequest(http.MethodPost, srv.BaseURL()+"/v1/webui/session", nil)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	req.AddCookie(&http.Cookie{Name: webuiCookie, Value: cookieValue(testToken)})
	resp, err := (&http.Client{}).Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("the exchange accepted a cookie alone (%d), want 401: only the token may mint one", resp.StatusCode)
	}
}

// The page handler's failure branch is unreachable through the real page: the names come from
// this package, which is tested against them, so the branch exists for a binary whose page and
// router disagree. Driving it is what keeps it a branch the suite has run.
func TestAPageWhoseFileIsMissingAnswersNotFound(t *testing.T) {
	srv := newTestServer(t, &fakeService{}, func(o *Options) { o.WebUI = true })

	// A route registered at a name the package does not have: the same thing that would happen
	// if a name were added to the router and forgotten in internal/webui.
	mux := http.NewServeMux()
	srv.page(mux, "GET /gone", "/gone")

	req := httptest.NewRequest(http.MethodGet, "/gone", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("a page file that does not exist answered %d, want 404", w.Code)
	}
}

// And with an empty token nothing is authorised on either path, including a cookie derived from
// the empty string - which would otherwise match the empty token and let everyone in.
func TestEmptyTokenAuthorisesNothingOnEitherPath(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/v1/sessions", nil)
	if authorized("", req) {
		t.Fatal("an empty token authorised a request with no credential")
	}
	req.AddCookie(&http.Cookie{Name: webuiCookie, Value: cookieValue("")})
	if authorized("", req) {
		t.Fatal("an empty token authorised a cookie derived from it: that is an open gateway")
	}
	// The same on the bearer-only path that guards the exchange.
	if requireBearerAllows("", httptest.NewRequest(http.MethodPost, "/v1/webui/session", nil)) {
		t.Fatal("requireBearer let the empty token through")
	}
	// And the cookie is never a substitute there, even with a real token configured.
	withCookie := httptest.NewRequest(http.MethodPost, "/v1/webui/session", nil)
	withCookie.AddCookie(&http.Cookie{Name: webuiCookie, Value: cookieValue(testToken)})
	if requireBearerAllows(testToken, withCookie) {
		t.Fatal("requireBearer accepted a cookie: the exchange would be self-renewing")
	}
}

// The logout endpoint clears the cookie so the blocking auth modal reappears.
// It requires no credential — clearing a credential is not a privilege, and the
// reason it is called is that the credential the browser holds is no longer
// valid.
func TestLogoutClearsTheCookie(t *testing.T) {
	srv := newTestServer(t, &fakeService{}, func(o *Options) { o.WebUI = true })

	req, err := http.NewRequest(http.MethodDelete, srv.BaseURL()+"/v1/webui/session", nil)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	resp, err := (&http.Client{}).Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("DELETE /v1/webui/session answered %d, want 204", resp.StatusCode)
	}

	var found bool
	for _, c := range resp.Cookies() {
		if c.Name == webuiCookie {
			found = true
			if c.MaxAge != -1 {
				t.Errorf("cookie MaxAge = %d, want -1 (delete immediately)", c.MaxAge)
			}
		}
	}
	if !found {
		t.Fatal("no Set-Cookie was sent on logout: the browser would keep the stale credential")
	}
}

// requireBearerAllows drives requireBearer and reports whether it let the request through.
func requireBearerAllows(token string, r *http.Request) bool {
	passed := false
	h := requireBearer(token, http.HandlerFunc(func(http.ResponseWriter, *http.Request) { passed = true }))
	h.ServeHTTP(httptest.NewRecorder(), r)
	return passed
}

// The page and the API share an origin, which is why no CORS header is needed anywhere. This
// pins that the interface did not become the reason to add one.
func TestThePageSendsNoCorsHeaderEither(t *testing.T) {
	srv := newTestServer(t, &fakeService{}, func(o *Options) { o.WebUI = true })

	req, err := http.NewRequest(http.MethodGet, srv.BaseURL()+"/", nil)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	req.Header.Set("Origin", "https://an-evil-page.example")
	resp, err := (&http.Client{}).Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	for _, h := range []string{"Access-Control-Allow-Origin", "Access-Control-Allow-Credentials", "Access-Control-Allow-Methods", "Access-Control-Allow-Headers"} {
		if got := resp.Header.Get(h); got != "" {
			t.Fatalf("%s: %q on the page. The browser is same-origin with the API; nothing needs this", h, got)
		}
	}
}
