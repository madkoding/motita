package webui

import (
	"errors"
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
	if !strings.Contains(string(body), "<!doctype html>") {
		t.Fatalf("the page does not start with a doctype: %q", first(body, 80))
	}
	for _, n := range []string{"/app.css", "/app.js"} {
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

func first(b []byte, n int) string {
	if len(b) < n {
		n = len(b)
	}
	return string(b[:n])
}
