package webfetch

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
)

// local is a Reader that may reach the test server (the real one refuses loopback).
func local() *Reader { return &Reader{client: http.DefaultClient} }

func serve(t *testing.T, kind, body string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("method = %s", r.Method)
		}
		w.Header().Set("Content-Type", kind)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestFetchHTMLBecomesText(t *testing.T) {
	srv := serve(t, "text/html; charset=utf-8",
		`<html><head><title>x</title></head><body><script>evil()</script><h1>Hi &amp; bye</h1><p>one</p><p>two</p></body></html>`)
	got, err := local().Fetch(context.Background(), srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(got, "evil") || strings.Contains(got, "<") || !strings.Contains(got, "Hi & bye") ||
		!strings.Contains(got, "one\n") {
		t.Errorf("text = %q", got)
	}
}

func TestFetchPlainAndTruncated(t *testing.T) {
	srv := serve(t, "text/plain", strings.Repeat("a", MaxBytes+10))
	got, err := local().Fetch(context.Background(), srv.URL)
	if err != nil || !strings.Contains(got, "page cut at 1024 KiB") {
		t.Errorf("got %d bytes, err %v", len(got), err)
	}
}

func TestFetchRefusals(t *testing.T) {
	bin := serve(t, "image/png", "x")
	bad := httptest.NewServer(http.NotFoundHandler())
	t.Cleanup(bad.Close)
	loop := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, r.URL.Path, http.StatusFound)
	}))
	t.Cleanup(loop.Close)
	for name, tc := range map[string]struct{ url, want string }{
		"binary":     {bin.URL, "only text pages"},
		"status":     {bad.URL, "404"},
		"redirects":  {loop.URL, "redirects"},
		"scheme":     {"file:///etc/passwd", "only http and https"},
		"no host":    {"http://", "no host"},
		"userinfo":   {"http://a:b@example.com/", "user name"},
		"bad url":    {"http://[::1", "not a valid URL"},
		"connection": {"http://127.0.0.1:1/", "fetching"},
	} {
		t.Run(name, func(t *testing.T) {
			r := local()
			r.client = &http.Client{CheckRedirect: New(nil).http().CheckRedirect}
			_, err := r.Fetch(context.Background(), tc.url)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("err = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestFetchRequestError(t *testing.T) {
	if _, err := local().Fetch(context.Background(), "http://exa mple.com/"); err == nil {
		t.Error("a host with a space was fetched")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := local().Fetch(ctx, "http://example.com/"); err == nil {
		t.Error("a cancelled context was fetched")
	}
}

func TestFetchBodyReadError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.Header().Set("Content-Length", "100")
		_, _ = w.Write([]byte("short"))
	}))
	t.Cleanup(srv.Close)
	if _, err := local().Fetch(context.Background(), srv.URL); err == nil || !strings.Contains(err.Error(), "reading") {
		t.Errorf("err = %v", err)
	}
}

func TestIsolatedRefuses(t *testing.T) {
	srv := serve(t, "text/plain", "x")
	r := New(func() bool { return true })
	r.client = http.DefaultClient
	if _, err := r.Fetch(context.Background(), srv.URL); err != ErrIsolated {
		t.Errorf("err = %v", err)
	}
	r = New(func() bool { return false })
	r.client = http.DefaultClient
	if _, err := r.Fetch(context.Background(), srv.URL); err != nil {
		t.Errorf("open network: %v", err)
	}
}

// The real client must refuse private destinations, including through a name.
func TestGuardedClientRefusesPrivateAddresses(t *testing.T) {
	srv := serve(t, "text/plain", "secret")
	for _, u := range []string{srv.URL, "http://localhost:1/", "http://169.254.169.254/latest/meta-data/"} {
		_, err := New(nil).Fetch(context.Background(), u)
		if err == nil || !strings.Contains(err.Error(), "not a public address") {
			t.Errorf("%s: err = %v", u, err)
		}
	}
}

func TestRedirectLimit(t *testing.T) {
	c := New(nil).http()
	via := make([]*http.Request, maxRedirects+1)
	if err := c.CheckRedirect(nil, via); err == nil {
		t.Error("too many redirects allowed")
	}
	if err := c.CheckRedirect(nil, via[:1]); err != nil {
		t.Error(err)
	}
}

func TestPublicOnlyAddresses(t *testing.T) {
	for addr, want := range map[string]bool{
		"8.8.8.8": true, "2606:4700::1111": true, "127.0.0.1": false, "10.0.0.1": false,
		"192.168.1.1": false, "172.16.0.1": false, "100.64.0.1": false, "169.254.1.1": false,
		"0.0.0.0": false, "224.0.0.1": false, "::1": false, "fe80::1": false, "::ffff:10.0.0.1": false,
	} {
		if got := public(netip.MustParseAddr(addr)); got != want {
			t.Errorf("public(%s) = %v", addr, got)
		}
	}
	if public(netip.Addr{}) {
		t.Error("the zero address is public")
	}
	if publicOnly("tcp", "8.8.8.8:80", nil) != nil {
		t.Error("a public address was refused")
	}
	if publicOnly("tcp", "no-port", nil) == nil || publicOnly("tcp", "name:80", nil) == nil {
		t.Error("a malformed address was accepted")
	}
}

func TestTextual(t *testing.T) {
	for kind, want := range map[string]bool{
		"text/html": true, "application/json": true, "application/ld+json": true, "application/atom+xml": true,
		"application/xml": true, "application/xhtml+xml": true, "application/javascript": true,
		"image/png": false, "application/pdf": false, "": false,
	} {
		if textual(kind) != want {
			t.Errorf("textual(%q) != %v", kind, want)
		}
	}
}
