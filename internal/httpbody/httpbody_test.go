package httpbody

import (
	"strings"
	"testing"
)

func TestNoteDropsPagesAndKeepsMessages(t *testing.T) {
	waf := `<!doctype html><html><head><script>var AC_Opt = {appkey: "CF_APP_WAF"}</script></head>` +
		`<body>blocked</body></html>`
	cases := map[string]string{
		waf:                           "",
		"  <?xml version='1.0'?><e/>": "",
		"oops:\n<html>":               "",
		"rate  limited\nretry later":  "rate limited retry later",
		`{"error":"bad key"}`:         `{"error":"bad key"}`,
		"":                            "",
	}
	for in, want := range cases {
		if got := Note([]byte(in), 50); got != want {
			t.Errorf("Note(%q) = %q, want %q", in, got, want)
		}
	}
	if got := Note([]byte(strings.Repeat("é", 20)), 5); got != "ééééé..." {
		t.Errorf("long message = %q", got)
	}
	if got := Note([]byte(strings.Repeat("a", 20)), 0); len(got) != 20 {
		t.Errorf("no limit = %q", got)
	}
}

// Only the start of a long body decides whether it is a page.
func TestIsPageLooksAtTheStartOnly(t *testing.T) {
	if IsPage(strings.Repeat("a", 600) + "<html>") {
		t.Error("a page marker far into plain text does not make a page")
	}
	if !IsPage(strings.Repeat(" ", 10) + "text <HTML>" + strings.Repeat("a", 600)) {
		t.Error("a page marker near the start does")
	}
}
