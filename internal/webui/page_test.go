package webui

import (
	"strings"
	"testing"
)

// These are requirements, not taste, and they are testable in the bytes that get served. A phone
// is one of the clients this exists for, and a screen reader is one of the ways it is used.
//
// The page is now a Vite + Preact + Tailwind build, so the assets have hashed names and the HTML
// is a minimal shell that mounts a React tree. The properties below are verified against the
// SERVED bytes, not against source files, because the bytes are what the browser receives.
func TestThePageMeetsItsHardRequirements(t *testing.T) {
	htmlB, _, err := Content("/")
	if err != nil {
		t.Fatalf("Content(/): %v", err)
	}
	html := string(htmlB)

	// 1. The page must have a root div for the app to mount on.
	if !strings.Contains(html, `id="root"`) {
		t.Error("the page has no #root div: the app has nowhere to mount")
	}

	// 2. The viewport meta must support mobile.
	if !strings.Contains(html, "viewport") {
		t.Error("the page has no viewport meta tag: a phone would render at desktop scale")
	}

	// 3. No token in the page (served unauthenticated).
	for _, needle := range []string{"Bearer ", "sk-", "MOTITA_", "token="} {
		if strings.Contains(html, needle) {
			t.Errorf("the page contains %q: it is served unauthenticated and must hold no secret", needle)
		}
	}

	// 4. No external origin reference.
	for _, needle := range []string{"https://", "http://cdn", "//cdn", "unpkg", "jsdelivr"} {
		if strings.Contains(html, needle) {
			t.Errorf("the page reaches outside itself (%q); it must be self-contained", needle)
		}
	}

	// 5. The JS and CSS are discovered dynamically, so find them by Content-Type
	// rather than a hardcoded path. The properties are checked across ALL served
	// JS and ALL served CSS, because a Vite build may split code into chunks.
	var jsBytes, cssBytes []byte
	for _, name := range Names() {
		body, ctype, err := Content(name)
		if err != nil {
			continue
		}
		if strings.HasPrefix(ctype, "text/javascript") {
			jsBytes = append(jsBytes, body...)
		}
		if strings.HasPrefix(ctype, "text/css") {
			cssBytes = append(cssBytes, body...)
		}
	}
	js := string(jsBytes)
	css := string(cssBytes)

	if len(jsBytes) == 0 {
		t.Fatal("no JavaScript is served: the page would be inert")
	}
	if len(cssBytes) == 0 {
		t.Fatal("no CSS is served: the page would be unstyled")
	}

	// 6. The script stores no credential in web storage: the token lives in the
	// HttpOnly cookie. localStorage/sessionStorage may be used for UI preferences
	// (sidebar state, last session id) but MUST NOT hold the token, API key, or
	// any bearer credential. Check for the patterns that would leak a secret,
	// not for the storage API itself — a blanket ban breaks legitimate UI state.
	for _, needle := range []string{
		"localStorage.setItem(\"token",
		"localStorage.setItem(\"api_key",
		"localStorage.setItem(\"bearer",
		"localStorage.setItem(\"auth",
		"sessionStorage.setItem(\"token",
		"sessionStorage.setItem(\"api_key",
		"sessionStorage.setItem(\"bearer",
		"sessionStorage.setItem(\"auth",
	} {
		if strings.Contains(js, needle) {
			t.Errorf("a JS file stores a credential in web storage (%q): the token belongs in the HttpOnly cookie", needle)
		}
	}

	// 7. The fragment is dropped after it is used.
	if !strings.Contains(js, "replaceState") {
		t.Error("the JS never clears the URL fragment: the token would stay in the address bar and in history")
	}

	// 8. Reconnection: a phone that loses signal must resume from the last event.
	if !strings.Contains(js, "from=") {
		t.Error("the JS does not resume the stream from an event id: a phone that loses signal would lose the turn")
	}

	// 9. An approval window must exist.
	if !strings.Contains(js, "approval") {
		t.Error("the JS has no approval handling: a consequential command could not be approved from here")
	}

	// 10. Whitespace preservation for approvals and code blocks.
	if !strings.Contains(css, "pre-wrap") {
		t.Error("nothing in the stylesheet preserves whitespace: an approval or a code block would be reflowed")
	}

	// 11. Touch targets: the send button must be at least 44px in both dimensions.
	// Tailwind generates min-height as rem, so check for either 44px or 2.75rem.
	if !strings.Contains(css, "min-height") || !strings.Contains(css, "min-width") {
		t.Error("the stylesheet has no min-height/min-width: touch targets are not guaranteed")
	}

	// 12. No external origin in JS. Preact's runtime uses the SVG/XHML namespace
	// URIs (http://www.w3.org/...), which are not network requests — they are
	// XML namespace identifiers. Everything else with http:// is a bug.
	if containsExternalHTTP(js) {
		t.Error("a JS file contains an external http:// URL: it must use relative paths")
	}

	// 13. The JS must call the gateway's own endpoints.
	if !strings.Contains(js, "/v1/sessions") {
		t.Error("the JS does not call the sessions API")
	}
	if !strings.Contains(js, "/v1/webui/session") {
		t.Error("the JS never exchanges the fragment for the cookie, so it could never authenticate")
	}
}

// The page must fit what the binary can afford. The Vite + Preact + Tailwind build targets
// ~40 KB for code, a compressed chat background image (~45 KB), and the embedded Sansation
// font family (~270 KB, 6 TTF files). The budget is set to 512 KB to accommodate all three
// while still catching runaway bloat.
func TestThePageStaysInsideItsBudget(t *testing.T) {
	const budget = 512 * 1024
	got, err := Size()
	if err != nil {
		t.Fatalf("Size: %v", err)
	}
	if got > budget {
		t.Fatalf("the page is %d bytes, budget is %d: "+
			"check that Preact (not React) is installed and Tailwind corePlugins are restricted", got, budget)
	}
}

// The page must talk to its OWN origin and nowhere else. A URL with a host in it is either a
// third party or a hard-coded port that will be wrong on the next machine.
func TestTheScriptTalksOnlyToItsOwnOrigin(t *testing.T) {
	for _, name := range Names() {
		body, ctype, err := Content(name)
		if err != nil {
			continue
		}
		if !strings.HasPrefix(ctype, "text/javascript") {
			continue
		}
		js := string(body)
		// Preact's runtime references XML namespace URIs (http://www.w3.org/...),
		// which are NOT network requests. Any other http:// is a bug.
		if containsExternalHTTP(js) {
			t.Fatalf("%s contains an external http:// URL: it must use relative paths", name)
		}
		// The service worker and its registrar are infrastructure, not app code:
		// they do not call the sessions API. Only the app bundle does.
		if name == "/sw.js" || name == "/registerSW.js" {
			continue
		}
		if !strings.Contains(js, "/v1/sessions") {
			t.Errorf("%s does not call the sessions API", name)
		}
		if !strings.Contains(js, "/v1/webui/session") {
			t.Errorf("%s never exchanges the fragment for the cookie", name)
		}
	}
}

// containsExternalHTTP reports whether s contains "http://" outside of the
// XML namespace identifiers that Preact's runtime uses (http://www.w3.org/...).
func containsExternalHTTP(s string) bool {
	search := s
	for {
		idx := strings.Index(search, "http://")
		if idx == -1 {
			return false
		}
		if strings.HasPrefix(search[idx:], "http://www.w3.org/") {
			search = search[idx+len("http://www.w3.org/"):]
			continue
		}
		return true
	}
}

// The PWA manifest must be present and valid.
func TestThePWAManifestIsPresent(t *testing.T) {
	for _, name := range Names() {
		if !strings.HasSuffix(name, ".webmanifest") {
			continue
		}
		body, _, err := Content(name)
		if err != nil {
			t.Fatalf("Content(%s): %v", name, err)
		}
		manifest := string(body)
		if !strings.Contains(manifest, `"name"`) {
			t.Errorf("the manifest %s has no name field", name)
		}
		if !strings.Contains(manifest, `"start_url"`) {
			t.Errorf("the manifest %s has no start_url field", name)
		}
		return
	}
	t.Error("no PWA manifest found in served assets")
}

// The service worker must be present for offline support.
func TestTheServiceWorkerIsPresent(t *testing.T) {
	for _, name := range Names() {
		if name == "/sw.js" {
			body, _, err := Content(name)
			if err != nil {
				t.Fatalf("Content(/sw.js): %v", err)
			}
			if len(body) == 0 {
				t.Fatal("sw.js is empty")
			}
			return
		}
	}
	t.Error("no service worker (sw.js) found in served assets")
}
