package policy

import "testing"

// ApproveAll turns a question into a yes, and only a question: the mandatory floor and the
// strict refusals stay refused, and the enforcing mode is otherwise untouched.
func TestApproveAllAnswersQuestionsOnly(t *testing.T) {
	dir := t.TempDir()
	ask := Mode{Enforce: true}
	if d := ask.DecideLine("sudo ls", dir); d.Verdict != Ask {
		t.Fatalf("without it sudo asks: %s/%s", d.Verdict, d.Rule)
	}
	all := Mode{Enforce: true, ApproveAll: true}
	if d := all.DecideLine("sudo ls", dir); d.Verdict != Allow {
		t.Errorf("with it sudo runs: %s/%s (%s)", d.Verdict, d.Rule, d.Reason)
	}
	if d := all.DecideLine("mkfs.ext4 /dev/sda1", dir); d.Verdict != Deny || !d.Mandatory {
		t.Errorf("the mandatory floor holds: %s/%s", d.Verdict, d.Rule)
	}
	strict := Mode{Enforce: true, Strict: true, ApproveAll: true}
	if d := strict.DecideLine("some-unknown-tool --x", dir); d.Verdict != Deny {
		t.Errorf("strict still refuses what it cannot classify: %s/%s", d.Verdict, d.Rule)
	}
}
