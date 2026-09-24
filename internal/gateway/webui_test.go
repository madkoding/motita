package gateway

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// The cookie is a DERIVATION of the token, so what a browser holds is not a bearer token. If
// this ever becomes the token itself, every browser session is a reusable credential for an
// agent that runs commands on this machine.
func TestTheBrowserCookieIsNotTheToken(t *testing.T) {
	got := cookieValue(testToken)
	if got == testToken {
		t.Fatal("the cookie value IS the token: a cookie taken from a browser would be a reusable bearer token")
	}
	if len(got) != 64 {
		t.Fatalf("the cookie value is %d chars, want 64 hex chars: %q", len(got), got)
	}
}

// Rotation is the whole reason for deriving instead of storing: nothing has to be invalidated
// when the token changes, because the expected value is recomputed from the new one.
func TestRotatingTheTokenInvalidatesOldCookies(t *testing.T) {
	if cookieValue("token-a") == cookieValue("token-b") {
		t.Fatal("two different tokens derive the same cookie")
	}
}

// The derivation must be stable: a value that changed between requests would log the browser out
// on every call.
func TestTheDerivationIsStable(t *testing.T) {
	a := cookieValue(testToken)
	b := cookieValue(testToken)
	if a != b {
		t.Fatal("the derivation is not deterministic")
	}
}

// A request with the cookie is authorised, which is the whole point of the exchange.
func TestACookieAuthorisesTheApi(t *testing.T) {
	srv := newTestServer(t, &fakeService{})

	req, err := http.NewRequest(http.MethodGet, srv.BaseURL()+"/v1/sessions/default/config", nil)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	req.AddCookie(&http.Cookie{Name: webuiCookie, Value: cookieValue(testToken)})
	resp, err := (&http.Client{}).Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("a request carrying the derived cookie answered %d, want 200", resp.StatusCode)
	}
}

// A wrong cookie is refused exactly like no cookie: nothing was opened by adding the path.
func TestAWrongCookieIsRefused(t *testing.T) {
	srv := newTestServer(t, &fakeService{})

	req, err := http.NewRequest(http.MethodGet, srv.BaseURL()+"/v1/sessions/default/config", nil)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	req.AddCookie(&http.Cookie{Name: webuiCookie, Value: "not-the-derived-value"})
	resp, err := (&http.Client{}).Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("a wrong cookie answered %d, want 401", resp.StatusCode)
	}
}

// And a cookie derived from a DIFFERENT token is refused too: that is what makes rotation work.
func TestACookieFromAnOldTokenIsRefused(t *testing.T) {
	srv := newTestServer(t, &fakeService{})

	req, err := http.NewRequest(http.MethodGet, srv.BaseURL()+"/v1/sessions/default/config", nil)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	req.AddCookie(&http.Cookie{Name: webuiCookie, Value: cookieValue("the-previous-token")})
	resp, err := (&http.Client{}).Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("a cookie from a rotated token answered %d, want 401", resp.StatusCode)
	}
}

// An empty configured token refuses EVERYTHING, including a cookie derived from "".
func TestAnEmptyTokenRefusesEvenADerivedCookie(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/v1/sessions", nil)
	req.AddCookie(&http.Cookie{Name: webuiCookie, Value: cookieValue("")})
	if authorized("", req) {
		t.Fatal("an empty token authorised a request")
	}
}

// The bearer path still works, and a bearer header that is merely present is not enough.
func TestTheBearerPathStillNeedsTheRightToken(t *testing.T) {
	ok := httptest.NewRequest(http.MethodGet, "/v1/sessions", nil)
	ok.Header.Set("Authorization", "Bearer "+testToken)
	if !authorized(testToken, ok) {
		t.Fatal("the bearer token was not accepted")
	}
	bad := httptest.NewRequest(http.MethodGet, "/v1/sessions", nil)
	bad.Header.Set("Authorization", "Bearer "+testToken+"x")
	if authorized(testToken, bad) {
		t.Fatal("a wrong bearer token was accepted")
	}
}
