package semantic

import (
	"strings"
	"testing"
)

func TestLintSubject(t *testing.T) {
	good := []string{
		"feat: add the git connection",
		"fix(auth): refresh the token before it expires",
		"feat(gitforge)!: drop the old helper",
		"chore(deps-dev): bump x",
		"revert: feat: x",
		"ci(release/v2): pin the preset",
		"  docs: trimmed  ",
	}
	for _, s := range good {
		if err := LintSubject(s); err != nil {
			t.Errorf("%q: %v", s, err)
		}
	}
	bad := []string{
		"", "   ", "Update README", "feat add x", "feat:no space", "Feat: caps", "feature: x",
		"fix(): empty scope", "fix(Scope): caps scope", "feat: two\nlines", "feat: ",
		"feat: " + strings.Repeat("x", 100),
	}
	for _, s := range bad {
		err := LintSubject(s)
		if err == nil {
			t.Errorf("%q must be refused", s)
			continue
		}
		if !strings.Contains(err.Error(), "feat") && !strings.Contains(err.Error(), "one line") && !strings.Contains(err.Error(), "characters") {
			t.Errorf("%q: the error does not say how to fix it: %v", s, err)
		}
	}
}

func TestCommitSubject(t *testing.T) {
	cases := []struct {
		args []string
		want string
		ok   bool
	}{
		{[]string{"-m", "feat: x"}, "feat: x", true},
		{[]string{"-am", "fix: y"}, "fix: y", true},
		{[]string{"-sam", "fix: y"}, "fix: y", true},
		{[]string{"-mfeat: z"}, "feat: z", true},
		{[]string{"--message", "docs: w"}, "docs: w", true},
		{[]string{"--message=docs: v"}, "docs: v", true},
		{[]string{"-m", "Subject\n\nbody"}, "Subject", true},
		{[]string{"-m", "first", "-m", "second paragraph"}, "first", true},
		{[]string{"--message=first", "-m", "second"}, "first", true},
		{[]string{"-m", "first", "--message", "second"}, "first", true},
		{[]string{"-m", "first", "--message=second"}, "first", true},
		{[]string{"--no-verify", "-S", "src/a.go", "-m", "chore: q"}, "chore: q", true},
		{[]string{"-m", "  padded  "}, "padded", true},
		// The subject cannot be known from the line.
		{nil, "", false},
		{[]string{"--amend"}, "", false},
		{[]string{"--amend", "--no-edit"}, "", false},
		{[]string{"-F", "msg.txt"}, "", false},
		{[]string{"-Fmsg.txt"}, "", false},
		{[]string{"--file", "msg.txt"}, "", false},
		{[]string{"--file=msg.txt"}, "", false},
		{[]string{"-C", "HEAD"}, "", false},
		{[]string{"-c", "HEAD"}, "", false},
		{[]string{"--reuse-message=HEAD"}, "", false},
		{[]string{"--reedit-message", "HEAD"}, "", false},
		{[]string{"--fixup=abc123"}, "", false},
		{[]string{"--squash", "abc123"}, "", false},
		{[]string{"-m", "x", "-F", "f"}, "", false},
		{[]string{"-"}, "", false},
		{[]string{"-1x"}, "", false},
		{[]string{"-Am", "feat: x"}, "", false},
		{[]string{"-m"}, "", false},
		{[]string{"--message"}, "", false},
	}
	for _, c := range cases {
		got, ok := CommitSubject(c.args)
		if got != c.want || ok != c.ok {
			t.Errorf("%q: (%q, %v), want (%q, %v)", c.args, got, ok, c.want, c.ok)
		}
	}
}

func TestCoerce(t *testing.T) {
	cases := map[string]string{
		"":                                "chore(agent): validated changes",
		"   \n ":                          "chore(agent): validated changes",
		"fix(parser): handle empty input": "fix(parser): handle empty input",
		"agent: fix the parser":           "chore(agent): agent: fix the parser",
		"Fix the\nparser   now":           "chore(agent): Fix the parser now",
	}
	for in, want := range cases {
		got := Coerce(in, "agent")
		if err := LintSubject(got); err != nil {
			t.Errorf("%q -> %q is not valid: %v", in, got, err)
		}
		if want != "" && got != want {
			t.Errorf("%q -> %q, want %q", in, got, want)
		}
	}
	long := Coerce("feat: "+strings.Repeat("word ", 30), "agent")
	if len(long) > 100 || !strings.HasPrefix(long, "feat: word") || strings.HasSuffix(long, " ") {
		t.Errorf("long = %q (%d)", long, len(long))
	}
	// One unbreakable word over the limit still ends valid.
	if got := Coerce(strings.Repeat("x", 200), "agent"); LintSubject(got) != nil {
		t.Errorf("got %q", got)
	}
	// A message whose cut leaves nothing usable still ends valid.
	if got := Coerce("feat: "+strings.Repeat("-", 200), "agent"); LintSubject(got) != nil {
		t.Errorf("got %q", got)
	}
}
