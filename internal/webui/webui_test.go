package webui

import (
	"errors"
	"io/fs"
	"strings"
	"testing"
)

// Every name the router will register must resolve. This is the test that keeps the router and
// this package from disagreeing about what the page is made of.
func TestEveryNameResolvesToContent(t *testing.T) {
	names := Names()
	if len(names) == 0 {
		t.Fatal("Names() is empty: the page has no files")
	}
	for _, n := range names {
		body, ctype, err := Content(n)
		if err != nil {
			t.Fatalf("Content(%q): %v", n, err)
		}
		if len(body) == 0 {
			t.Fatalf("Content(%q) is empty", n)
		}
		if ctype == "" {
			t.Fatalf("Content(%q) has no Content-Type", n)
		}
	}
}

// A name nobody serves must be an ERROR, not a fallback. Falling back to index.html turns every
// typo into a page that renders, which is how a broken link ships unnoticed.
func TestAnUnknownNameIsAnError(t *testing.T) {
	if _, _, err := Content("/nope.js"); err == nil {
		t.Fatal("Content(\"/nope.js\") succeeded; an unknown path must be an error")
	}
}

func TestThePageIsHTMLAndTheAssetsAreNot(t *testing.T) {
	body, ctype, err := Content("/")
	if err != nil {
		t.Fatalf("Content(/): %v", err)
	}
	if !strings.HasPrefix(ctype, "text/html") {
		t.Fatalf("the page's Content-Type is %q, want text/html", ctype)
	}
	if !strings.Contains(strings.ToLower(string(body)), "<!doctype html>") {
		t.Fatalf("the page does not start with a doctype: %q", first(body, 80))
	}
	// Every other served file must NOT be HTML: a JS or CSS file served as HTML
	// would be parsed by the browser as a page.
	for _, n := range Names() {
		if n == "/" {
			continue
		}
		_, ctype, err := Content(n)
		if err != nil {
			t.Fatalf("Content(%q): %v", n, err)
		}
		if strings.HasPrefix(ctype, "text/html") {
			t.Fatalf("%s is served as html (%q): the browser would parse it as a page", n, ctype)
		}
	}
}

// The token is the operator's, handed over once in the URL fragment. It must never be written
// into the page, because the page is served WITHOUT authentication: anything inside it is
// readable by anyone who can reach the port.
func TestThePageCarriesNoTokenAndNoOtherOrigin(t *testing.T) {
	body, _, err := Content("/")
	if err != nil {
		t.Fatalf("Content(/): %v", err)
	}
	html := string(body)
	for _, needle := range []string{"Bearer ", "sk-", "MOTITA_", "token="} {
		if strings.Contains(html, needle) {
			t.Fatalf("the page contains %q: it is served unauthenticated and must hold no secret", needle)
		}
	}
	for _, needle := range []string{"https://", "http://cdn", "//cdn", "unpkg", "jsdelivr"} {
		if strings.Contains(html, needle) {
			t.Fatalf("the page reaches outside itself (%q); it must be self-contained", needle)
		}
	}
}

// Size reports what the page costs, because every one of these bytes becomes a byte of the
// executable and the size gate has 1.2 MB of margin on windows/amd64.
func TestSizeCountsTheWholePage(t *testing.T) {
	total, err := Size()
	if err != nil {
		t.Fatalf("Size: %v", err)
	}
	if total <= 0 {
		t.Fatal("Size() reported nothing: the page would cost nothing, which cannot be true")
	}
	// The sum of the parts must be the whole, or the budget is measuring something else.
	sum := 0
	for _, n := range Names() {
		body, _, err := Content(n)
		if err != nil {
			t.Fatalf("Content(%q): %v", n, err)
		}
		sum += len(body)
	}
	if total != sum {
		t.Fatalf("Size() = %d but the files add up to %d", total, sum)
	}
}

func TestIsPageTellsTheDocumentFromItsAssets(t *testing.T) {
	_, pageType, _ := Content("/")
	if !IsPage(pageType) {
		t.Fatalf("IsPage(%q) = false for the page itself", pageType)
	}
	if IsPage("text/css; charset=utf-8") {
		t.Fatal("IsPage said a stylesheet is the page")
	}
}

// The read failure is unreachable through the embedded filesystem in a correct build, so the
// seam is what makes the branch a branch the suite has actually run. Without it the failure
// shape would be whatever the first person to hit it discovered.
func TestAReadFailureIsReportedAndNotPanicked(t *testing.T) {
	restore := readAsset
	readAsset = func(string) ([]byte, error) { return nil, errors.New("the disk went away") }
	defer func() { readAsset = restore }()

	if _, _, err := Content("/"); err == nil {
		t.Fatal("Content returned no error when the read failed")
	}
	if _, err := Size(); err == nil {
		t.Fatal("Size returned no error when the read failed")
	}
}

// The discover failure is unreachable through the embedded filesystem in a correct build, so the
// seam is what makes the mustDiscover panic branch a branch the suite has actually run.
func TestADiscoverFailurePanics(t *testing.T) {
	restore := discover
	discover = func() ([]file, error) { return nil, errors.New("the embed broke") }
	defer func() { discover = restore; files = mustDiscover() }()

	defer func() {
		if r := recover(); r == nil {
			t.Fatal("mustDiscover did not panic when discover failed")
		}
	}()
	files = mustDiscover()
}

// The walk-callback error path inside discover is unreachable on an embed.FS, so the walkDir
// seam drives it: a walk that calls the callback with an error must propagate it through
// discover's callback return and its outer return.
func TestADiscoverWalkErrorIsPropagated(t *testing.T) {
	restore := walkDir
	walkDir = func(_ fs.FS, _ string, fn fs.WalkDirFunc) error {
		// Call the callback with an error, which makes discover's callback
		// hit its `if err != nil { return err }` branch.
		return fn("assets/broken", nil, errors.New("walk: permission denied"))
	}
	defer func() { walkDir = restore; files = mustDiscover() }()

	entries, err := discover()
	if err == nil {
		t.Fatal("discover returned no error when the walk callback failed")
	}
	if entries != nil {
		t.Fatal("discover returned entries alongside an error")
	}
}

// The outer walk error (fs.WalkDir itself failing, not the callback) is also unreachable on an
// embed.FS, so the walkDir seam drives it too.
func TestADiscoverWalkOuterErrorIsPropagated(t *testing.T) {
	restore := walkDir
	walkDir = func(fs.FS, string, fs.WalkDirFunc) error {
		return errors.New("walk: root does not exist")
	}
	defer func() { walkDir = restore; files = mustDiscover() }()

	_, err := discover()
	if err == nil {
		t.Fatal("discover returned no error when the walk itself failed")
	}
}

// contentType must return a fallback for extensions it does not know. A file the build adds with
// a new extension must not arrive with an empty Content-Type.
func TestContentTypeForUnknownExtension(t *testing.T) {
	got := contentType("file.xyz")
	if got == "" {
		t.Fatal("contentType returned empty for an unknown extension")
	}
	if got != "application/octet-stream" {
		t.Fatalf("contentType(%q) = %q, want application/octet-stream", "file.xyz", got)
	}
}

// contentType must return the right type for each extension the build actually uses.
//
// Every branch of the switch is exercised, and the list is a SUPERSET of the extensions in
// assets/ rather than a copy of it: the point of the function is to serve a file the build
// adds later without a code change, so pinning only what happens to be there today would let
// a branch rot until the day it is first needed. The extensions that assets/ does NOT carry
// yet (.jpg, .png, .ttf, .woff) are named here on purpose.
func TestContentTypeForKnownExtensions(t *testing.T) {
	cases := map[string]string{
		"index.html":           "text/html; charset=utf-8",
		"style.css":            "text/css; charset=utf-8",
		"app.js":               "text/javascript; charset=utf-8",
		"manifest.webmanifest": "application/manifest+json",
		"icon.svg":             "image/svg+xml",
		"photo.jpg":            "image/jpeg",
		"photo.jpeg":           "image/jpeg",
		"icon.png":             "image/png",
		"background.webp":      "image/webp",
		"font.ttf":             "font/ttf",
		"font.woff":            "font/woff",
		"font.woff2":           "font/woff2",
	}
	for name, want := range cases {
		got := contentType(name)
		if got != want {
			t.Errorf("contentType(%q) = %q, want %q", name, got, want)
		}
	}
}

// contentType decides on the EXTENSION, not on the name, and it is the last dot that does:
// an asset with dots in its name (a Vite hash is not the only shape a name can take) must
// still resolve. A path with no dot at all is the unknown case, not a panic.
func TestContentTypeLooksAtTheLastExtension(t *testing.T) {
	cases := map[string]string{
		"index-B39OGPPm.min.js": "text/javascript; charset=utf-8",
		"archive.tar.gz":        "application/octet-stream", // gz is not a served type
		"noextension":           "application/octet-stream",
		"":                      "application/octet-stream",
	}
	for name, want := range cases {
		if got := contentType(name); got != want {
			t.Errorf("contentType(%q) = %q, want %q", name, got, want)
		}
	}
}

func first(b []byte, n int) string {
	if len(b) < n {
		n = len(b)
	}
	return string(b[:n])
}
