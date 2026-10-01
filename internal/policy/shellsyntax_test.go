package policy

import "testing"

// Reported from a real session: `for f in server/test/*.mjs; do head -70 "$f"; done` was put to
// the user as a question - twice, 85 s and 14 s - on a run with nobody at the keyboard, because
// `for`, `do` and `done` were taken for unknown programs. The control words are syntax; what they
// wrap is judged on its own, and that judgement must not get weaker for being inside a loop.
func TestAReadOnlyLoopIsNotAQuestion(t *testing.T) {
	m := Mode{Enforce: true}
	for _, line := range []string{
		`ls -la server/test/ && for f in server/test/*.mjs; do echo "=== $f ==="; head -70 "$f"; done`,
		`for f in a b; do cat "$f"; done`,
		`if [ -f a ]; then cat a; fi`,
		`if [ -f a ]; then cat a; else echo none; fi`,
		`case x in *) echo hi;; esac`,
		`! grep -q x f`,
		`time ls`,
	} {
		if d := m.DecideLine(line, "/tmp/w"); d.Verdict != Allow {
			t.Errorf("%q = %v (%s), want allow", line, d.Verdict, d.Reason)
		}
	}
}

func TestWhatALoopWrapsIsStillJudged(t *testing.T) {
	m := Mode{Enforce: true}
	cases := []struct {
		line string
		want Verdict
	}{
		{`for f in a b; do echo x > /etc/passwd; done`, Deny},
		{`while true; do curl -X POST http://example.com; done`, Ask},
		{`for f in $(ls); do echo $f; done`, Ask}, // a substitution is not read, it is offered whole
		{`for f in a; do sh -c "$f"; done`, Ask},
	}
	for _, c := range cases {
		if d := m.DecideLine(c.line, "/tmp/w"); d.Verdict != c.want {
			t.Errorf("%q = %v (%s), want %v", c.line, d.Verdict, d.Reason, c.want)
		}
	}
}

func TestStripShellKeyword(t *testing.T) {
	cases := []struct {
		in     []string
		rest   []string
		syntax bool
	}{
		{[]string{"done"}, nil, true},
		{[]string{"fi"}, nil, true},
		{[]string{"for", "f", "in", "a"}, nil, true},
		{[]string{"do", "cat", "f"}, []string{"cat", "f"}, false},
		{[]string{"then", "do", "ls"}, []string{"ls"}, false},
		{[]string{"do"}, nil, true},
		{[]string{"ls", "-la"}, []string{"ls", "-la"}, false},
		{nil, nil, true},
	}
	for _, c := range cases {
		rest, syntax := stripShellKeyword(c.in)
		if syntax != c.syntax || len(rest) != len(c.rest) {
			t.Errorf("stripShellKeyword(%v) = %v, %v; want %v, %v", c.in, rest, syntax, c.rest, c.syntax)
			continue
		}
		for i := range rest {
			if rest[i] != c.rest[i] {
				t.Errorf("stripShellKeyword(%v) = %v, want %v", c.in, rest, c.rest)
			}
		}
	}
}
