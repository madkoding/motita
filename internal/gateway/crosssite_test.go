package gateway

import (
	"crypto/tls"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
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
	req := httptest.NewRequest(
		http.MethodPatch,
		"/v1/sessions/"+DefaultSession,
		strings.NewReader(`{"title":"from the cli"}`),
	)
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

// Behind a proxy that terminates TLS, the page the browser loaded was https even though this
// connection is not, so a same-origin upgrade from it must pass and another origin still must not.
func TestAProxyThatTerminatesTLSKeepsTheOriginCheck(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.Header.Set("X-Forwarded-Proto", "https")
	if got := requestOrigin(r); got != "https://example.com" {
		t.Errorf("proxied origin = %q", got)
	}
	r.Header.Set("Upgrade", "websocket")
	r.Header.Set("Origin", "https://example.com")
	if msg := crossSiteRefusal(r); msg != "" {
		t.Errorf("an upgrade from the proxied page was refused: %s", msg)
	}
	r.Header.Set("Origin", "https://evil.example")
	if crossSiteRefusal(r) == "" {
		t.Error("an upgrade from another origin was let through behind the proxy")
	}
}

// The page and the API both carry the security headers: no framing by another site, no type
// sniffing, no Referer, and a script policy with neither inline code nor eval: WebAssembly may be
// compiled ('wasm-unsafe-eval', for the local voices), JavaScript may not.
func TestEveryResponseCarriesTheSecurityHeaders(t *testing.T) {
	srv := newTestServer(t, &fakeService{}, func(o *Options) { o.WebUI = true })
	// The whole policy, so a widening is a change to this line and not a quiet edit elsewhere.
	const want = "default-src 'self'; script-src 'self' 'wasm-unsafe-eval'; style-src 'self' 'unsafe-inline'; " +
		"img-src 'self' data: blob:; media-src 'self' blob:; font-src 'self' data:; connect-src 'self'; worker-src 'self'; " +
		"object-src 'none'; base-uri 'none'; form-action 'self'; frame-ancestors 'none'"
	for _, path := range []string{"/", "/v1/health", "/v1/sessions"} {
		w := serve(srv, httptest.NewRequest(http.MethodGet, path, nil))
		if csp := w.Header().Get("Content-Security-Policy"); csp != want {
			t.Errorf("%s: the CSP is %q, want %q", path, csp, want)
		}
		if got := w.Header().Get("X-Content-Type-Options"); got != "nosniff" {
			t.Errorf("%s: X-Content-Type-Options = %q", path, got)
		}
		if got := w.Header().Get("Referrer-Policy"); got != "no-referrer" {
			t.Errorf("%s: Referrer-Policy = %q", path, got)
		}
	}
}

// A DNS-rebinding page reaches the gateway under ITS OWN name, and that name is refused - on the
// public routes too, the page and /v1/health. IP addresses, localhost, this machine's name, the
// listen host and the names in gateway.hosts are all accepted.
func TestTheHostHeaderMustNameThisGateway(t *testing.T) {
	osHostname = func() (string, error) { return "MyBox.lan", nil }
	t.Cleanup(func() { osHostname = os.Hostname })
	srv := newTestServer(t, &fakeService{}, func(o *Options) {
		o.WebUI = true
		o.Listen = "127.0.0.1:0"
		o.Hosts = []string{"Motita.Example.ORG."}
	})
	for host, want := range map[string]int{
		"attacker.example:8321":  http.StatusMisdirectedRequest,
		"evil.localhost.example": http.StatusMisdirectedRequest,
		"127.0.0.1:7477":         http.StatusOK,
		"[::1]:7477":             http.StatusOK,
		"192.168.1.20:7477":      http.StatusOK,
		"localhost:7477":         http.StatusOK,
		"app.localhost":          http.StatusOK,
		"mybox.lan":              http.StatusOK,
		"mybox.local:7477":       http.StatusOK,
		"motita.example.org":     http.StatusOK,
		"":                       http.StatusOK,
	} {
		for _, path := range []string{"/", "/v1/health"} {
			req := httptest.NewRequest(http.MethodGet, path, nil)
			req.Host = host
			if w := serve(srv, req); w.Code != want {
				t.Errorf("Host %q %s answered %d, want %d", host, path, w.Code, want)
			}
		}
	}
}

// A machine whose name cannot be read still knows its listen host and gateway.hosts.
func TestKnownHostsWithoutAMachineName(t *testing.T) {
	osHostname = func() (string, error) { return "", errors.New("no name") }
	t.Cleanup(func() { osHostname = os.Hostname })
	got := knownHosts("gw.internal:7477", []string{"proxy.example", " "})
	if len(got) != 2 || !got["gw.internal"] || !got["proxy.example"] {
		t.Errorf("knownHosts = %v", got)
	}
	if len(knownHosts("not-host-port", nil)) != 0 {
		t.Error("an unsplittable listen added a host")
	}
}

// A proxy or port forward on port 80 makes the browser write "http://127.0.0.1" while the gateway
// sees Host "127.0.0.1:8787": that is the same page, and must not be refused. A different port, or
// another host, still is (DNS rebinding and a dev server on a sibling port stay blocked).
func TestOriginWithoutThePortBehindAProxy(t *testing.T) {
	post := func(host, origin string, hdr map[string]string) string {
		r := httptest.NewRequest(http.MethodPost, "/v1/sessions", nil)
		r.Host = host
		r.Header.Set(browserHeader, "1")
		r.Header.Set("Origin", origin)
		for k, v := range hdr {
			r.Header.Set(k, v)
		}
		return crossSiteRefusal(r)
	}
	fwd := map[string]string{"X-Forwarded-Host": "127.0.0.1"}
	fwdPort := map[string]string{"X-Forwarded-Host": "127.0.0.1:9000"}
	fwdTLS := map[string]string{"X-Forwarded-Host": "motita.example", "X-Forwarded-Proto": "https"}
	for _, c := range []struct {
		name, host, origin string
		hdr                map[string]string
		refused            bool
	}{
		{"direct, same port", "127.0.0.1:8787", "http://127.0.0.1:8787", nil, false},
		{"direct, default port spelled out", "127.0.0.1:80", "http://127.0.0.1", nil, false},
		{"direct, trailing slash", "127.0.0.1:8787", "http://127.0.0.1:8787/", nil, false},
		{"ipv6 loopback", "[::1]:8787", "http://[::1]:8787", nil, false},
		{"localhost", "localhost:8787", "http://localhost:8787", nil, false},
		{"proxy on 80 forwarding the host", "127.0.0.1:8787", "http://127.0.0.1", fwd, false},
		{"proxy forwarding host with port", "127.0.0.1:8787", "http://127.0.0.1:9000", fwdPort, false},
		{"https proxy", "127.0.0.1:8787", "https://motita.example", fwdTLS, false},
		{"proxy without the header", "127.0.0.1:8787", "http://127.0.0.1", nil, true},
		{"sibling port on the same host", "127.0.0.1:8787", "http://127.0.0.1:3000", nil, true},
		{"sibling port behind a proxy", "127.0.0.1:8787", "http://127.0.0.1:3000", fwd, true},
		{"rebinding name", "127.0.0.1:8787", "http://evil.example", nil, true},
		{"rebinding name behind a proxy", "127.0.0.1:8787", "http://evil.example", fwd, true},
		{"other scheme", "127.0.0.1:80", "https://127.0.0.1", nil, true},
		{"opaque origin", "127.0.0.1:8787", "null", nil, true},
	} {
		msg := post(c.host, c.origin, c.hdr)
		if (msg != "") != c.refused {
			t.Errorf("%s: refusal = %q, want refused=%v", c.name, msg, c.refused)
		}
	}
	msg := post("127.0.0.1:8787", "http://127.0.0.1", nil)
	if !strings.Contains(msg, "X-Forwarded-Host") || !strings.Contains(msg, "http://127.0.0.1:8787") {
		t.Errorf("the refusal does not say what to do next: %q", msg)
	}
}

// gateway.origins trusts one named origin and nothing around it.
func TestAListedOriginIsTrustedExactly(t *testing.T) {
	trusted := []string{"http://127.0.0.1", "https://motita.example/"}
	for origin, refused := range map[string]bool{
		"http://127.0.0.1":         false,
		"http://127.0.0.1:80":      false,
		"https://motita.example":   false,
		"http://127.0.0.1:3000":    true,
		"http://motita.example":    true,
		"https://evil.example":     true,
		"http://127.0.0.1.evil.io": true,
	} {
		r := httptest.NewRequest(http.MethodPost, "/v1/sessions", nil)
		r.Host = "127.0.0.1:8787"
		r.Header.Set(browserHeader, "1")
		r.Header.Set("Origin", origin)
		if got := crossSiteRefusal(r, trusted...) != ""; got != refused {
			t.Errorf("origin %q: refused = %v, want %v", origin, got, refused)
		}
	}
}

// The listed origins reach the check through the server's options.
func TestOptionsOriginsReachTheOriginCheck(t *testing.T) {
	srv := newTestServer(t, &fakeService{}, func(o *Options) { o.Origins = []string{"http://127.0.0.1"} })
	req := httptest.NewRequest(http.MethodPost, "/v1/sessions", strings.NewReader(`{}`))
	req.Host = "127.0.0.1:8787"
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(browserHeader, "1")
	req.Header.Set("Origin", "http://127.0.0.1")
	req.AddCookie(&http.Cookie{Name: webuiCookie, Value: cookieValue(testToken)})
	if w := serve(srv, req); w.Code == http.StatusForbidden {
		t.Fatalf("a listed origin was refused: %s", w.Body)
	}
}
