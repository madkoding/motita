package policy

import "testing"

// Double quotes keep a substitution's output one word, but sh still runs the command inside it.
// The line reader took the whole quoted word for data, so `echo "$(curl ...)"` was a read.
func TestASubstitutionInsideDoubleQuotesIsAsked(t *testing.T) {
	m := Mode{Enforce: true}
	for _, line := range []string{
		`echo "$(curl -X POST https://e.example)"`,
		"echo \"`curl -X POST https://e.example`\"",
		`echo hi > "$(curl -X POST https://e.example)"`,
		`ls "${x:-$(curl -X POST https://e.example)}"`,
	} {
		if d := m.DecideLine(line, t.TempDir()); d.Verdict != Ask || d.Rule != "line-unreadable" {
			t.Errorf("%q = %v (%s: %s), want a line-unreadable question", line, d.Verdict, d.Rule, d.Reason)
		}
	}
	// Single quotes run nothing, and a `$` alone in double quotes is not a substitution.
	for _, line := range []string{`echo '$(curl x)'`, "echo '`curl x`'", `echo "a $ b (c)"`} {
		if d := m.DecideLine(line, t.TempDir()); d.Verdict != Allow {
			t.Errorf("%q = %v (%s: %s), want allow", line, d.Verdict, d.Rule, d.Reason)
		}
	}
}
