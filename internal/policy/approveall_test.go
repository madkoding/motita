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

// A segment the policy cannot read decides the line on its own, and that answer is relaxed too.
// Reported from real use: with every command allowed in the settings, `ls $(pwd)` and a line with
// an unclosed quote still asked, because that path returned before the mode had its say.
func TestApproveAllReachesALineThatCannotBeRead(t *testing.T) {
	dir := t.TempDir()
	lines := []string{"ls $(pwd)", "eval $X", "echo 'a", "cat \"$HOME/notes.txt\" | head"}
	ask := Mode{Enforce: true}
	all := Mode{Enforce: true, ApproveAll: true}
	off := Mode{}
	for _, line := range lines {
		if d := ask.DecideLine(line, dir); d.Verdict == Deny {
			t.Errorf("%q: asking mode must not refuse it: %s/%s", line, d.Verdict, d.Rule)
		}
		if d := all.DecideLine(line, dir); d.Verdict != Allow {
			t.Errorf("%q with every command allowed: got %s/%s (%s)", line, d.Verdict, d.Rule, d.Reason)
		}
		if d := off.DecideLine(line, dir); d.Verdict != Allow {
			t.Errorf("%q with the policy off: got %s/%s (%s)", line, d.Verdict, d.Rule, d.Reason)
		}
	}
	if d := ask.DecideLine("ls $(pwd)", dir); d.Verdict != Ask {
		t.Errorf("without the setting the unreadable line still asks: %s/%s", d.Verdict, d.Rule)
	}
	strict := Mode{Enforce: true, Strict: true, ApproveAll: true}
	if d := strict.DecideLine("ls $(pwd)", dir); d.Verdict != Deny {
		t.Errorf("strict still refuses what it cannot read: %s/%s", d.Verdict, d.Rule)
	}
	if d := all.DecideLine("echo $(mkfs.ext4 /dev/sda1)", dir); d.Verdict != Deny || !d.Mandatory {
		t.Errorf("the mandatory floor holds inside an unreadable line: %s/%s", d.Verdict, d.Rule)
	}
}
