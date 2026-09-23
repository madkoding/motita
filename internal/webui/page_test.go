package webui

import (
	"strings"
	"testing"
)

// These are requirements, not taste, and they are testable in the bytes that get served. A phone
// is one of the clients this exists for, and a screen reader is one of the ways it is used.
func TestThePageMeetsItsHardRequirements(t *testing.T) {
	htmlB, _, err := Content("/")
	if err != nil {
		t.Fatalf("Content(/): %v", err)
	}
	cssB, _, err := Content("/app.css")
	if err != nil {
		t.Fatalf("Content(/app.css): %v", err)
	}
	jsB, _, err := Content("/app.js")
	if err != nil {
		t.Fatalf("Content(/app.js): %v", err)
	}
	html, css, js := string(htmlB), string(cssB), string(jsB)

	// 1. Usable on a phone: the send control must be a real touch target, which means BOTH
	// dimensions, in the rule that styles it. Grepping the stylesheet for "44px" would pass on a
	// leftover from any other rule - which is exactly what the negative control showed - so the
	// assertion is on the button's own block.
	block := ruleFor(css, "#composer button")
	if block == "" {
		t.Fatal("the stylesheet has no rule for the send button")
	}
	if !strings.Contains(block, "min-height: 44px") || !strings.Contains(block, "min-width: 44px") {
		t.Errorf("the send button is not a 44px touch target in both dimensions:\n%s", block)
	}
	// 2. A live turn must be ANNOUNCED, not only drawn.
	for _, n := range []string{`role="log"`, "aria-live"} {
		if !strings.Contains(html, n) {
			t.Errorf("the conversation region is missing %s: a screen reader gets nothing from a live turn", n)
		}
	}
	// 3. Keyboard: Enter must send, which means a form with a submit control and not a div.
	if !strings.Contains(html, "<form") || !strings.Contains(html, `type="submit"`) {
		t.Error("the composer is not a form with a submit control: Enter would not send")
	}
	// 4. The fragment is dropped after it is used, because a token left in the address bar ends
	// up in the history, in a screenshot and in a pasted bug report.
	if !strings.Contains(js, "replaceState") {
		t.Error("app.js never clears the URL fragment: the token would stay in the address bar and in history")
	}
	// 5. And the script stores NOTHING: the credential lives in the cookie the server set, and
	// the token lives nowhere once it has been traded in.
	if strings.Contains(js, "localStorage") || strings.Contains(js, "sessionStorage") {
		t.Error("app.js stores something: the credential belongs in the HttpOnly cookie and the token nowhere")
	}
	// 6. Reconnection: a phone that loses signal must resume from the last event it saw, and the
	// gateway already numbers them for exactly this.
	if !strings.Contains(js, "from=") {
		t.Error("app.js does not resume the stream from an event id: a phone that loses signal would lose the turn")
	}
	// 7. An approval window must exist and must show the WHOLE command: approving is approving
	// THAT text, and a truncated command is a different command.
	if !strings.Contains(js, "approval") {
		t.Error("app.js has no approval handling: a consequential command could not be approved from here")
	}
	if !strings.Contains(css, "pre-wrap") {
		t.Error("nothing in the stylesheet preserves whitespace: an approval or a code block would be reflowed into something else")
	}
	// 8. No build step, no module system, no third party: three files, and the browser loads them.
	if strings.Contains(html, "node_modules") || strings.Contains(js, "import ") {
		t.Error("the page pulls in a module system; it is three files and no build")
	}
}

// The page must fit what the binary can afford. Measured: about one binary byte per asset byte,
// so this budget IS a budget on the executable, and the gate has 1.16 MB of margin on
// windows/amd64.
func TestThePageStaysInsideItsBudget(t *testing.T) {
	const budget = 64 * 1024
	got, err := Size()
	if err != nil {
		t.Fatalf("Size: %v", err)
	}
	if got > budget {
		t.Fatalf("the page is %d bytes, budget is %d: every asset byte becomes a binary byte, "+
			"and the size gate has 1.16 MB of margin on windows/amd64. A webfont is the one item "+
			"that can spend it - use the system font.", got, budget)
	}
}

// The page must talk to its OWN origin and nowhere else. A URL with a host in it is either a
// third party or a hard-coded port that will be wrong on the next machine.
func TestTheScriptTalksOnlyToItsOwnOrigin(t *testing.T) {
	jsB, _, err := Content("/app.js")
	if err != nil {
		t.Fatalf("Content(/app.js): %v", err)
	}
	js := string(jsB)
	for _, needle := range []string{"http://", "https://"} {
		if strings.Contains(js, needle) {
			t.Fatalf("app.js contains %q: it must use relative paths so it works on any port and host", needle)
		}
	}
	// It must call the gateway's own endpoints, which are the API it shares an origin with.
	if !strings.Contains(js, "/v1/sessions") {
		t.Error("app.js does not call the sessions API")
	}
	if !strings.Contains(js, "/v1/webui/session") {
		t.Error("app.js never exchanges the fragment for the cookie, so it could never authenticate")
	}
}

// ruleFor returns the body of one CSS rule, so an assertion can be about the rule that styles the
// thing being asserted and not about the file as a whole. A whole-file search passes on a
// coincidence, and a test that passes for the wrong reason is worse than no test.
func ruleFor(css, selector string) string {
	start := strings.Index(css, selector+" {")
	if start == -1 {
		return ""
	}
	rest := css[start:]
	end := strings.Index(rest, "}")
	if end == -1 {
		return ""
	}
	return rest[:end+1]
}
