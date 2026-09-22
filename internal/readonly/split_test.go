package readonly

// SplitCommand is the tokeniser the policy and the executor share, so these tests are about
// what it REFUSES as much as what it splits: a line it lets through is a line whose program
// the policy believes it knows.

import (
	"strings"
	"testing"
)

// TestSplitCommandSeparatesProgramFromArguments is the ordinary case, including the quoting
// that has to survive the round trip.
func TestSplitCommandSeparatesProgramFromArguments(t *testing.T) {
	cases := []struct {
		line string
		cmd  string
		args []string
	}{
		{"ls", "ls", nil},
		{"grep -n x f.txt", "grep", []string{"-n", "x", "f.txt"}},
		{"/usr/bin/grep -n x", "/usr/bin/grep", []string{"-n", "x"}},
		{`grep -n "two words" f`, "grep", []string{"-n", "two words", "f"}},
		{`grep -n 'two words' f`, "grep", []string{"-n", "two words", "f"}},
		{`echo a\ b`, "echo", []string{"a b"}},
		{`echo "a\"b"`, "echo", []string{`a"b`}},
		{`echo 'a"b'`, "echo", []string{`a"b`}},
		{`echo ""`, "echo", []string{""}},
		{"  ls   -la  ", "ls", []string{"-la"}},
		{"\tls\t-la\t", "ls", []string{"-la"}},
		{`echo *`, "echo", []string{"*"}},
		{`echo a=b`, "echo", []string{"a=b"}},
	}
	for _, tc := range cases {
		cmd, args, err := SplitCommand(tc.line)
		if err != nil {
			t.Errorf("SplitCommand(%q) failed: %v", tc.line, err)
			continue
		}
		if cmd != tc.cmd {
			t.Errorf("SplitCommand(%q) program = %q, want %q", tc.line, cmd, tc.cmd)
		}
		if len(args) != len(tc.args) {
			t.Errorf("SplitCommand(%q) args = %#v, want %#v", tc.line, args, tc.args)
			continue
		}
		for i := range args {
			if args[i] != tc.args[i] {
				t.Errorf("SplitCommand(%q) arg %d = %q, want %q", tc.line, i, args[i], tc.args[i])
			}
		}
	}
}

// TestSplitCommandRefusesAShellMetacharacter is the guarantee the whole read-only mode rests
// on. The character is reported so a caller can tell one refusal from another, and so the
// policy can split a line on the character that stopped it.
//
// The backslash is NOT in this list: a backslash in the middle of a line is an escape, and it
// turns the character after it into data (`ls \; rm` is a program with one argument, not a
// chain). Only a trailing backslash is refused, which the unfinished-line test covers.
func TestSplitCommandRefusesAShellMetacharacter(t *testing.T) {
	for _, bad := range []rune{'|', '&', ';', '<', '>', '`', '(', ')', '$', '\n', '\r'} {
		line := "ls " + string(bad) + " x"
		_, _, got, err := SplitCommandWithErr(line)
		if err == nil {
			t.Errorf("SplitCommandWithErr(%q) accepted a shell metacharacter", line)
			continue
		}
		if got != bad {
			t.Errorf("SplitCommandWithErr(%q) reported %q, want %q", line, got, bad)
		}
		if !strings.Contains(err.Error(), "needs a shell") {
			t.Errorf("the refusal must say WHY: %q", err)
		}
	}
}

// TestSplitCommandEscapedMetacharacterIsData: the other half of the rule above. An escaped
// metacharacter is an argument, and the line stays readable because nothing will interpret it.
func TestSplitCommandEscapedMetacharacterIsData(t *testing.T) {
	cases := []struct {
		line string
		arg  string
	}{
		{`ls \;`, ";"},
		{`ls \|`, "|"},
		{`ls \>`, ">"},
		{`ls \$`, "$"},
	}
	for _, tc := range cases {
		cmd, args, err := SplitCommand(tc.line)
		if err != nil {
			t.Errorf("%q escapes the metacharacter and must be readable: %v", tc.line, err)
			continue
		}
		if cmd != "ls" || len(args) != 1 || args[0] != tc.arg {
			t.Errorf("SplitCommand(%q) = %q %#v, want ls and %q", tc.line, cmd, args, tc.arg)
		}
	}
}

// TestSplitCommandMetacharacterInsideQuotesIsData: quoting is what makes a metacharacter an
// argument rather than syntax, and the tokeniser has to honour it in both quote styles.
func TestSplitCommandMetacharacterInsideQuotesIsData(t *testing.T) {
	for _, line := range []string{
		`grep -n "a|b" f`,
		`grep -n 'a;b' f`,
		`grep -n "a>b" f`,
		`echo "a\$b"`,
		`echo 'a$b'`,
	} {
		if _, _, err := SplitCommand(line); err != nil {
			t.Errorf("%q is quoted data and must be readable: %v", line, err)
		}
	}
}

// TestSplitCommandRefusesAnUnfinishedLine: a backslash at the end and an unclosed quote both
// mean the line continues somewhere the tokeniser cannot see, so they are refused rather than
// guessed at.
func TestSplitCommandRefusesAnUnfinishedLine(t *testing.T) {
	cases := map[string]string{
		`echo x\`:        "backslash",
		`echo "unclosed`: "quote",
		`echo 'unclosed`: "quote",
		`ls "a b`:        "quote",
	}
	for line, want := range cases {
		_, _, _, err := SplitCommandWithErr(line)
		if err == nil {
			t.Errorf("SplitCommandWithErr(%q) accepted an unfinished line", line)
			continue
		}
		if !strings.Contains(err.Error(), want) {
			t.Errorf("SplitCommandWithErr(%q) = %q, want it to mention %q", line, err, want)
		}
	}
}

// TestSplitCommandRefusesAnEmptyLine: there is nothing to run, which is its own answer.
func TestSplitCommandRefusesAnEmptyLine(t *testing.T) {
	for _, line := range []string{"", "   ", "\t", " \t "} {
		if _, _, err := SplitCommand(line); err == nil {
			t.Errorf("SplitCommand(%q) accepted a line with no command", line)
		}
	}
}

// TestSplitCommandReportsTheFirstMetacharacter: a line with several has to be split on the
// FIRST one, or the caller would divide the line at the wrong place.
func TestSplitCommandReportsTheFirstMetacharacter(t *testing.T) {
	_, _, bad, err := SplitCommandWithErr("ls | grep x ; rm")
	if err == nil || bad != '|' {
		t.Errorf("the first metacharacter is the pipe, got %q (err %v)", bad, err)
	}
}

// TestSplitCommandOnAnEscapedQuote: a quote inside an escape is data, not a delimiter, which
// is what keeps `echo \"` from opening a quote that never closes.
func TestSplitCommandOnAnEscapedQuote(t *testing.T) {
	cmd, args, err := SplitCommand(`echo \"`)
	if err != nil {
		t.Fatalf("an escaped quote is data: %v", err)
	}
	if cmd != "echo" || len(args) != 1 || args[0] != `"` {
		t.Errorf("got %q %#v", cmd, args)
	}
}
