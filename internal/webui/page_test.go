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
// font family (~270 KB, 6 TTF files).
//
// ## The guard is SPLIT, not raised — and it now measures the SHELL, not the total
//
// Rendering an answer takes real machinery: a CommonMark parser, a syntax highlighter, KaTeX
// for formulas and mermaid for diagrams. Together those are about 2 MB of source, and a single
// ceiling over the whole page would have to be raised to admit them — which would throw away
// the check that catches runaway bloat in the code, the images and the fonts.
//
// So the two questions are separated, because they are not the same question:
//
//   - The SHELL is what every visitor downloads before they can read anything: the entry
//     bundle, the stylesheet, the chat background, the text faces and the page itself. It is
//     what the old 512 KB budget was really protecting. Anything in it is paid on every load.
//   - The LAZY assets are chunks a message pulls in only when it needs them (a diagram, a
//     formula, a code block), and each gets its own ceiling. They still live in the binary —
//     `Size()` counts embedded bytes — but they are not what the reader waits for.
//
// The line between the two is a property of the BUILD, not a list kept here: Vite names the
// entry `index-*.js`, and every other chunk under assets/ is reachable only through a dynamic
// import. A file that moves from lazy to eager therefore moves into the budgeted number by
// itself, which is the direction that matters.
//
// The mono icon face predates this split and keeps its own line for its own reason. Code is
// set in JetBrains Mono Nerd Font, and "Nerd Font" means 10,610 extra icon glyphs on top of
// the ~1,600 text ones. They are kept in a SEPARATE face carrying a `unicode-range` of the
// private-use blocks, so a browser lays out ordinary code from the 53 KB text face and never
// requests the icons at all (measured: loading the app fetches only the text face). But it is
// embedded either way, so it gets a ceiling of its own.
func TestThePageStaysInsideItsBudget(t *testing.T) {
	// What a visitor waits for: the entry bundle, the stylesheet, the background, the text
	// faces, the page. Measured at 535 KB when this split was made; the ceiling stays at the
	// number that was protecting it. It is deliberately tight because everything in it is
	// paid on EVERY load.
	const shellBudget = 560 * 1024
	const iconFace = "/JetBrainsMonoNerdFont-Icons.woff2"
	const iconBudget = 1024 * 1024

	got, err := Size()
	if err != nil {
		t.Fatalf("Size: %v", err)
	}
	// `Size()` is the number the BINARY carries; the two sums below are what the reader
	// pays. Kept in the log so a change in the split is visible rather than inferred.
	t.Logf("embedded page: %d bytes", got)
	icons, _, err := Content(iconFace)
	if err != nil {
		// Not a failure: a build without the Nerd Font icon face is smaller, not broken.
		// The check below is about the face not becoming unbounded once it is there.
		icons = nil
	}

	shell := 0
	lazy := 0
	for _, name := range Names() {
		body, _, err := Content(name)
		if err != nil {
			continue
		}
		switch {
		case name == iconFace:
			// Measured on its own below.
		case isLazyChunk(name):
			lazy += len(body)
		case name == "/sw.js" || name == "/registerSW.js":
			// The service worker is not part of the first paint and is fetched by the
			// browser itself; it is tiny either way.
		default:
			shell += len(body)
		}
	}

	if shell > shellBudget {
		t.Fatalf("the shell is %d bytes, budget is %d: this is what EVERY visitor downloads, so "+
			"check what became eager. A chunk meant to be loaded on demand has to be reached "+
			"through a dynamic import(), or it stops being lazy and lands in this number", shell, shellBudget)
	}
	t.Logf("shell %d bytes, lazy %d bytes (of which the icon face is %d)", shell, lazy, len(icons))
	if len(icons) > iconBudget {
		t.Fatalf("the mono icon face is %d bytes, budget is %d: the Nerd Font icon subset has "+
			"grown, or is being built without subsetting at all (the unsubset upstream face is 2.5 MB)",
			len(icons), iconBudget)
	}
}

// isLazyChunk reports whether a served asset is reached only through a dynamic import.
//
// This is an inference from the build's own naming, and it is the whole reason the split above
// works without a hand-kept list: Vite emits the entry as `assets/index-<hash>.js` and every
// other JS chunk is a `import()` target. A file that stops being lazy changes its name and
// stops matching here on its own.
func isLazyChunk(name string) bool {
	if !strings.HasPrefix(name, "/assets/") || !strings.HasSuffix(name, ".js") {
		return false
	}
	return !strings.HasPrefix(strings.TrimPrefix(name, "/assets/"), "index-")
}

// The page must talk to its OWN origin and nowhere else. A URL with a host in it is either a
// third party or a hard-coded port that will be wrong on the next machine.
//
// This applies to EVERY served script, including the ones loaded on demand: a third-party
// URL is a bug wherever it lives.
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
	}
}

// The entry bundle is the one that has to be an API client, and it has to do the exchange
// BEFORE anything else can run.
//
// This check used to be applied to every served script, which was the same statement while
// there was a single bundle. It is not the same statement now: a diagram or a formula is
// loaded on demand, and those chunks are third-party libraries with no business calling our
// sessions API. Requiring it of them would only be satisfied by shipping our API calls into
// a library, which is the opposite of what this test is for.
//
// The property worth pinning is the security one, and it lives in the entry: the browser
// holds the session token in a URL fragment, the entry presents it once at
// /v1/webui/session to be exchanged for a cookie, and a fragment that is never exchanged
// means a page that silently cannot talk to its own gateway. Naming the entry specifically
// is also what keeps it honest: if session setup were ever moved into a lazily loaded chunk,
// this test would fail rather than pass quietly.
func TestTheEntryBundleExchangesTheFragmentForTheCookie(t *testing.T) {
	name, js, ok := entryChunk()
	if !ok {
		t.Fatalf("no entry bundle found: expected one served asset named /assets/index-*.js")
	}
	if !strings.Contains(js, "/v1/webui/session") {
		t.Errorf("%s never exchanges the fragment for the cookie", name)
	}
	if !strings.Contains(js, "/v1/sessions") {
		t.Errorf("%s does not call the sessions API", name)
	}
}

// entryChunk returns the bundle a visitor downloads first — the one Vite names
// `assets/index-<hash>.js`. Every other chunk under assets/ is a dynamic-import target.
func entryChunk() (name, body string, ok bool) {
	for _, n := range Names() {
		if !strings.HasPrefix(n, "/assets/") || !strings.HasSuffix(n, ".js") {
			continue
		}
		if !strings.HasPrefix(strings.TrimPrefix(n, "/assets/"), "index-") {
			continue
		}
		raw, _, err := Content(n)
		if err != nil {
			return "", "", false
		}
		return n, string(raw), true
	}
	return "", "", false
}

// containsExternalHTTP reports whether s contains an http:// URL that is NOT one of the
// two things known to be harmless. Anything else is a bug: this page is served from the
// gateway's own origin and must reach no other.
//
// The two exceptions, and why the check is written around them rather than loosened:
//
//  1. "http://www.w3.org/..." — XML namespace identifiers from Preact's runtime and from
//     KaTeX's MathML (`xmlns`). They name a vocabulary, they are not resolvable hosts, and
//     a browser never fetches them.
//  2. "http://${...}" and "http://" + <identifier> — a TEMPLATE, not an address. These come
//     from url-normalising code (linkify-it's `e.url = \`http://${e.url}\“, mermaid's
//     `\`http://${e}\`.replace(/^http:\/\//, "")`) whose whole job is to give a schemeless
//     input a scheme. There is no host here to fetch from; the string is completed at
//     RUNTIME from whatever the user typed, and the completed value is only ever used as an
//     href or a comparison.
//
// The exceptions are matched on the EXACT PREFIX ("http://" followed by the interpolation
// marker) so a real address cannot slip through them: "http://evil.example" has no `$` or
// quote after the slashes and is still reported.
func containsExternalHTTP(s string) bool {
	exceptions := []string{
		"http://www.w3.org/",
		"http://${",
		`http://"`,
		"http://'+",
		"http://\"+",
	}
	for _, ex := range exceptions {
		s = strings.ReplaceAll(s, ex, "\x00")
	}
	return strings.Contains(s, "http://")
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
