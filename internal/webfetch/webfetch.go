// Package webfetch lets the read-only plan mode read a web page.
//
// The one thing it does is a GET. It sends no body, no cookies and no credentials, follows a
// bounded number of redirects, reads a bounded amount of text and refuses every address that is
// not on the public internet. It is the plan mode's answer to "browse the web" without handing it
// curl, which writes files and can POST.
//
// It obeys Settings → Security like a command does: while the commands run without a network
// (sandbox.isolate_network, the default), a page is not fetched either, because a URL is also a
// way to send out what the session has read.
package webfetch

import (
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"regexp"
	"strings"
	"syscall"
	"time"
)

const (
	// MaxBytes is how much of a page is read; the rest is dropped.
	MaxBytes = 1 << 20
	// maxRedirects bounds the redirects followed.
	maxRedirects = 5
	// timeout bounds the whole request.
	timeout = 30 * time.Second
)

// Reader fetches pages for the plan mode.
type Reader struct {
	// Isolated says the commands run without a network, which refuses every fetch. Nil means a
	// reader that is never isolated.
	Isolated func() bool
	// client is replaced by the tests; nil builds the guarded one.
	client *http.Client
}

// New builds a Reader that follows isolated (see Reader.Isolated).
func New(isolated func() bool) *Reader { return &Reader{Isolated: isolated} }

// ErrIsolated is why a fetch was refused while the network is closed to commands.
var ErrIsolated = errors.New("the network is closed to commands (Settings → Security → Network for commands); " +
	"allow the network there to read web pages")

// Fetch GETs rawURL and returns the page as text: markup is removed from HTML, other text is
// returned as is. Anything that is not text, or is not provably a plain GET of a public page, is
// an error whose message says why.
func (r *Reader) Fetch(ctx context.Context, rawURL string) (string, error) {
	if r.Isolated != nil && r.Isolated() {
		return "", ErrIsolated
	}
	u, err := validate(rawURL)
	if err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	// Built by hand: validate has already parsed the URL, so the constructor has nothing left to refuse.
	req := (&http.Request{Method: http.MethodGet, URL: u, Header: http.Header{}, Host: u.Host}).WithContext(ctx)
	req.Header.Set("User-Agent", "motita-plan/1 (read-only GET)")
	req.Header.Set("Accept", "text/html,text/plain,application/json,application/xml;q=0.9,*/*;q=0.1")
	resp, err := r.http().Do(req)
	if err != nil {
		return "", fmt.Errorf("fetching %s: %w", u.Redacted(), err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return "", fmt.Errorf("%s answered %s", u.Redacted(), resp.Status)
	}
	kind, _, _ := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	if !textual(kind) {
		return "", fmt.Errorf("%s is %q, and only text pages are read", u.Redacted(), kind)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, MaxBytes+1))
	if err != nil {
		return "", fmt.Errorf("reading %s: %w", u.Redacted(), err)
	}
	truncated := len(data) > MaxBytes
	if truncated {
		data = data[:MaxBytes]
	}
	text := string(data)
	if kind == "text/html" || kind == "application/xhtml+xml" {
		text = htmlText(text)
	}
	if truncated {
		text += fmt.Sprintf("\n[... page cut at %d KiB ...]", MaxBytes>>10)
	}
	return text, nil
}

// validate accepts only an http(s) URL with a host and no credentials.
func validate(rawURL string) (*url.URL, error) {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return nil, fmt.Errorf("not a valid URL: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, fmt.Errorf("only http and https pages are read, not %q", u.Scheme)
	}
	if u.Hostname() == "" {
		return nil, errors.New("the URL has no host")
	}
	if u.User != nil {
		return nil, errors.New("a URL with a user name or password is refused")
	}
	return u, nil
}

// http returns the client: the one the tests set, or the guarded one.
func (r *Reader) http() *http.Client {
	if r.client != nil {
		return r.client
	}
	dialer := &net.Dialer{Timeout: 10 * time.Second, Control: publicOnly}
	return &http.Client{
		Transport: &http.Transport{
			DialContext: dialer.DialContext,
			// A proxy named by the environment would be asked to reach the address instead of
			// this process, which is the check being skipped.
			Proxy: nil,
		},
		CheckRedirect: func(_ *http.Request, via []*http.Request) error {
			if len(via) > maxRedirects {
				return fmt.Errorf("more than %d redirects", maxRedirects)
			}
			return nil
		},
	}
}

// publicOnly refuses to connect to an address that is not on the public internet. It runs on the
// address actually dialled, after the name was resolved, so a name that points at a private
// address (or is rebound to one between two lookups) is stopped as well as a literal IP.
func publicOnly(_, address string, _ syscall.RawConn) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return err
	}
	ip, err := netip.ParseAddr(host)
	if err != nil {
		return err
	}
	if !public(ip) {
		return fmt.Errorf("%s is not a public address; only pages on the internet are read", ip)
	}
	return nil
}

// public reports whether ip is on the public internet.
func public(ip netip.Addr) bool {
	ip = ip.Unmap()
	if !ip.IsValid() || ip.IsUnspecified() || ip.IsMulticast() || cgnat.Contains(ip) {
		return false
	}
	return !ip.IsLoopback() && !ip.IsPrivate() && !ip.IsLinkLocalUnicast()
}

// cgnat is the shared address space of carrier-grade NAT (RFC 6598), which IsPrivate leaves out.
var cgnat = netip.MustParsePrefix("100.64.0.0/10")

// textual reports whether a media type is text a model can read.
func textual(kind string) bool {
	switch kind {
	case "application/json", "application/xml", "application/xhtml+xml", "application/javascript":
		return true
	}
	return strings.HasPrefix(kind, "text/") || strings.HasSuffix(kind, "+json") || strings.HasSuffix(kind, "+xml")
}

var (
	reBlocks = regexp.MustCompile(
		`(?is)<(script|style|noscript|template|svg|head)\b.*?</(script|style|noscript|template|svg|head)\s*>`)
	reBreaks = regexp.MustCompile(`(?i)</?(p|div|br|li|tr|h[1-6]|section|article|pre|table|ul|ol)\b[^>]*>`)
	reTags   = regexp.MustCompile(`(?s)<[^>]*>`)
	reBlank  = regexp.MustCompile(`\n[ \t]*(\n[ \t]*)+`)
	reSpaces = regexp.MustCompile(`[ \t]+`)
)

// htmlText turns a page into the text a reader would see: scripts, styles and tags go, block
// elements become line breaks and entities are decoded.
func htmlText(page string) string {
	page = reBlocks.ReplaceAllString(page, "")
	page = reBreaks.ReplaceAllString(page, "\n")
	page = reTags.ReplaceAllString(page, "")
	page = htmlEntities.Replace(page)
	page = reSpaces.ReplaceAllString(page, " ")
	page = reBlank.ReplaceAllString(page, "\n\n")
	return strings.TrimSpace(page)
}

var htmlEntities = strings.NewReplacer("&nbsp;", " ", "&lt;", "<", "&gt;", ">", "&quot;", `"`,
	"&#39;", "'", "&apos;", "'", "&amp;", "&")
