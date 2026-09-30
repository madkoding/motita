package readonly

import (
	"strings"
	"testing"
)

// THE TABLES ARE TWO-SIDED, and these tests hold both sides of each pair.
//
// The defect they were written for: `sed` and `awk` were in the writers table, so
// `sed -n '1,300p' file` — the way a coding agent looks at a file, measured 197 times in one
// real session — was classified as a writer, refused in plan mode and put in front of the
// user as a question in every other mode. The table was answering a question about the NAME
// when the line was asking one about the ARGUMENTS.
//
// The sweep that followed found the same table wrong in the other direction, which is the
// direction with no question to catch it: `sort -o out.txt in.txt`, `uniq f out.txt`,
// `xxd f out.bin`, `yq -i`, `xmllint --output` and `base64 -o` were all listed as "only
// reads" with no rule, so they ran in SILENCE while writing a file.
//
// So each program below is asserted twice: the reading form must be allowed, and the
// writing form must be refused. A test that only checked one direction is what let both
// defects live here at once.

// TestTheLineEditorsReadInTheirPrintingForm: the reading use of sed and awk, which is what
// the table was refusing.
func TestTheLineEditorsReadInTheirPrintingForm(t *testing.T) {
	reads := []struct {
		command string
		args    []string
	}{
		{"sed", []string{"-n", "1,300p", "app/admin/page.tsx"}},
		{"sed", []string{"-n", "200,404p", "server/src/routes.mjs"}},
		{"sed", []string{"-n", "1,50p", "README.md"}},
		{"sed", []string{"-n", "1,20p", "a", "b"}},
		{"sed", []string{"'s/a/b/'", "f"}},
		{"sed", []string{"-e", "s/a/b/", "f"}},
		{"/usr/bin/sed", []string{"-n", "1,5p", "f"}},
		{"awk", []string{"'/CREATE TABLE/,/^\\);/'", "schema.sql"}},
		{"awk", []string{"'{print $1}'", "f"}},
		{"awk", []string{"-F:", "'{print $1}'", "/etc/passwd"}},
		{"awk", []string{"'{print $1, $2}'", "f"}},
		{"awk", []string{"'$1 > 5'", "f"}},
		{"awk", []string{"'a || b'", "f"}},
		{"awk", []string{"-v", "n=1", "'{print}'", "f"}},
		{"gawk", []string{"'{print}'", "f"}},
		{"mawk", []string{"'{print}'", "f"}},
	}
	for _, tc := range reads {
		d := Check(tc.command, tc.args)
		if !d.Allowed {
			t.Errorf("%s %v only reads and must be allowed, got refused: %s", tc.command, tc.args, d.Reason)
		}
	}
}

// TestTheLineEditorsAreRefusedWhenTheyWrite: the other half of the same pair. Every one of
// these writes a file, and a reader that let it through would be worse than the question the
// old table asked.
func TestTheLineEditorsAreRefusedWhenTheyWrite(t *testing.T) {
	writes := []struct {
		command string
		args    []string
		reason  string
	}{
		{"sed", []string{"-i", "s/a/b/", "f"}, "sed -i rewrites the files"},
		{"sed", []string{"-i.bak", "s/a/b/", "f"}, "sed -i.bak rewrites the files"},
		{"sed", []string{"--in-place", "s/a/b/", "f"}, "sed --in-place rewrites the files"},
		{"sed", []string{"--in-place=.bak", "s/a/b/", "f"}, "sed --in-place rewrites the files"},
		// A short-option CLUSTER, which GNU sed really accepts: measured on GNU sed 4.9,
		// `sed -ni '1p' f` rewrites f in place. A rule looking only for "-i" missed it.
		{"sed", []string{"-ni", "1p", "f"}, "sed -ni rewrites the files"},
		{"sed", []string{"-n", "-i", "1p", "f"}, "sed -i rewrites the files"},
		{"awk", []string{"'{print > \"out\"}'", "f"}, "awk writes a file"},
		{"awk", []string{"'{print >> \"out\"}'", "f"}, "awk writes a file"},
		{"awk", []string{"'{print | \"sort\"}'", "f"}, "awk writes a file"},
		{"awk", []string{"'BEGIN{system(\"rm f\")}'"}, "awk with system() runs an arbitrary command"},
		{"gawk", []string{"-i", "inplace", "'{print}'", "f"}, "awk -i inplace rewrites the files"},
		{"gawk", []string{"-iinplace", "'{print}'", "f"}, "awk -i inplace rewrites the files"},
		{"mawk", []string{"'BEGIN{system(\"ls\")}'"}, "awk with system() runs an arbitrary command"},
	}
	for _, tc := range writes {
		d := Check(tc.command, tc.args)
		if d.Allowed {
			t.Errorf("%s %v writes and must be refused", tc.command, tc.args)
			continue
		}
		if !strings.Contains(d.Reason, tc.reason) {
			t.Errorf("%s %v: the refusal must name what writes it; got %q, want it to mention %q",
				tc.command, tc.args, d.Reason, tc.reason)
		}
	}
}

// TestTheReadersWhoseFlagsWrite: the mirror defect. Each of these is a READER in the table
// below, and each has an argument list that makes it write. Before the rules existed, every
// one of them was reported as "only reads" — which is a silent write, the one outcome this
// package exists to prevent.
func TestTheReadersWhoseFlagsWrite(t *testing.T) {
	cases := []struct {
		command string
		args    []string
		writes  bool
	}{
		{"sort", []string{"in.txt"}, false},
		{"sort", []string{"-n", "in.txt"}, false},
		{"sort", []string{"-o", "out.txt", "in.txt"}, true},
		{"sort", []string{"-oout.txt", "in.txt"}, true},
		{"sort", []string{"--output", "out.txt", "in.txt"}, true},
		{"sort", []string{"--output=out.txt", "in.txt"}, true},

		{"uniq", []string{"in.txt"}, false},
		{"uniq", []string{"-c", "in.txt"}, false},
		{"uniq", []string{"in.txt", "out.txt"}, true},

		{"xxd", []string{"in.bin"}, false},
		{"xxd", []string{"-l", "16", "in.bin"}, false},
		// The synopsis is `xxd [options] [infile [outfile]]`: the second operand is
		// written whatever the flags say.
		{"xxd", []string{"in.bin", "out.hex"}, true},
		{"xxd", []string{"-r", "dump.hex", "out.bin"}, true},

		{"xmllint", []string{"in.xml"}, false},
		{"xmllint", []string{"--noout", "in.xml"}, false},
		{"xmllint", []string{"--output", "out.xml", "in.xml"}, true},
		{"xmllint", []string{"-o", "out.xml", "in.xml"}, true},

		{"yq", []string{".a", "in.yaml"}, false},
		{"yq", []string{"-i", ".a=1", "in.yaml"}, true},
		{"yq", []string{"--inplace", ".a=1", "in.yaml"}, true},

		{"base64", []string{"in.bin"}, false},
		{"base64", []string{"-w", "0", "in.bin"}, false},
		{"base64", []string{"-o", "out.txt", "in.bin"}, true},
	}
	for _, tc := range cases {
		d := Check(tc.command, tc.args)
		switch {
		case tc.writes && d.Allowed:
			t.Errorf("%s %v writes and must be refused, got allowed: %s", tc.command, tc.args, d.Reason)
		case !tc.writes && !d.Allowed:
			t.Errorf("%s %v only reads and must be allowed, got: %s", tc.command, tc.args, d.Reason)
		}
	}
}

// TestTheWritingFormIsCheckedBeforeTheReaderList is the ORDER, asserted on its own because
// the order is the fix. A rule that ran after the reader test would be unreachable for any
// name that is also a reader — the reader branch would have returned Allow already — and
// that is exactly how `sort -o` came to be reported as "only reads".
func TestTheWritingFormIsCheckedBeforeTheReaderList(t *testing.T) {
	for _, name := range []string{"sort", "uniq", "xxd", "xmllint", "yq", "sed", "awk", "find"} {
		if !readers[name] {
			t.Fatalf("%q must be a reader for this test to mean anything", name)
		}
		if _, ok := argumentRules[name]; !ok {
			t.Errorf("%q is a reader with a writing form but has no rule: Classify would allow it to write", name)
		}
	}
}

// TestEveryReaderWithAWritingFormHasARule is the sweep, as a standing check rather than a
// one-off: it names the readers that CAN write, so that adding one to the table without a
// rule is a test failure rather than a silent write discovered later.
//
// The list is deliberately narrow. A reader whose flags cannot write does not need a rule,
// and inventing one would refuse reads for nothing: `cat -n f`, `ls -la`, `wc -l f`,
// `head -n 5 f`, `tail -f log`, `cut -f1 -d, f`, `tr a b`, `column -t f`, `tree -L 2` and
// `jq '.a' f` all read whatever they are given.
func TestEveryReaderWithAWritingFormHasARule(t *testing.T) {
	canWrite := map[string]string{
		"sed":     "-i rewrites the file",
		"awk":     "a redirection in the program writes",
		"gawk":    "-i inplace rewrites the file",
		"mawk":    "a redirection in the program writes",
		"find":    "-delete and -exec write",
		"sort":    "-o writes a file",
		"uniq":    "a second operand is an output file",
		"xxd":     "a second operand is an output file",
		"xmllint": "--output writes a file",
		"yq":      "-i writes the file back",
		"base64":  "-o writes a file",
	}
	for name, why := range canWrite {
		if !readers[name] {
			t.Errorf("%q can write (%s) but is not in the readers table; if it belongs in writers, say why", name, why)
		}
		if _, ok := argumentRules[name]; !ok {
			t.Errorf("%q can write (%s) and has no argument rule: it would be allowed to write in silence", name, why)
		}
	}
	// And the rule is reachable: with the writing form it refuses, with none of it it reads.
	for name := range canWrite {
		if d, _ := argumentRules[name](nil); d != "" {
			t.Errorf("%q is refused with no arguments at all (%q); the rule must answer about the arguments", name, d)
		}
	}
}

// TestTheKnownGapsAreRecorded: two forms write and are NOT refused, and they are here as
// failures waiting to be written rather than as surprises. Both need a parser to see, and a
// rule that guessed would refuse the programs people actually write — see the notes on the
// awk and sed rules.
func TestTheKnownGapsAreRecorded(t *testing.T) {
	// `awk '{print > out}'` — an UNQUOTED redirection target, which is lexically
	// indistinguishable from the comparison `awk '$1 > out'`.
	if d := Check("awk", []string{"'{print > out}'", "f"}); !d.Allowed {
		t.Errorf("this gap has been closed: an unquoted awk redirection is now refused (%s). "+
			"Update this test, it is here to record the gap rather than to defend it", d.Reason)
	}
	// `sed 'w out' f` — a `w` command inside the script writes a file with no `-i` at all.
	if d := Check("sed", []string{"-n", "'1w out'", "f"}); !d.Allowed {
		t.Errorf("this gap has been closed: a sed w command is now refused (%s). "+
			"Update this test, it is here to record the gap rather than to defend it", d.Reason)
	}
}

// TestTheDirectionEachRuleLeans: the two shapes of rule fail in opposite directions, and
// both directions are asserted so that "fixing" one by flipping it is caught here.
//
// A flag-scanning rule must treat an unrecognised flag as a READ: refusing `sed --unbuffered`
// or `sort --random-source=f` would break the ordinary work of looking at a file, and the
// writing flags are the ones it names. An operand-counting rule must treat a separate value
// as an OPERAND, because it cannot know better — which is why those rules carry the options
// that take a value, and why the false alarm on `xxd -l 16` was a real defect and not a
// preference.
func TestTheDirectionEachRuleLeans(t *testing.T) {
	// Flag scanning: an unknown flag reads.
	for _, tc := range []struct {
		command string
		args    []string
	}{
		{"sed", []string{"--unbuffered", "-n", "1,5p", "f"}},
		{"sed", []string{"-u", "-n", "1,5p", "f"}},
		{"awk", []string{"--posix", "'{print}'", "f"}},
		{"sort", []string{"--random-source=f", "in.txt"}},
		{"yq", []string{"--verbose", ".a", "in.yaml"}},
	} {
		if d := Check(tc.command, tc.args); !d.Allowed {
			t.Errorf("%s %v: an unrecognised flag is a read for a flag-scanning rule, got: %s",
				tc.command, tc.args, d.Reason)
		}
	}
	// Operand counting: a separate value is an operand, so the option table is what keeps the
	// common invocations from being refused.
	if d := Check("xxd", []string{"-l", "16", "in.bin"}); !d.Allowed {
		t.Errorf("xxd -l 16 in.bin names ONE operand and must read, got: %s", d.Reason)
	}
	if d := Check("xxd", []string{"-s", "10", "in.bin"}); !d.Allowed {
		t.Errorf("xxd -s takes a value and must read, got: %s", d.Reason)
	}
	if d := Check("uniq", []string{"-f", "2", "in.txt"}); !d.Allowed {
		t.Errorf("uniq -f takes a value and must read, got: %s", d.Reason)
	}
}

// TestTheHelpersBehindTheRules: the flag and operand extraction the rules are built on, so
// that a change in the extraction cannot silently change what the rules refuse.
func TestTheHelpersBehindTheRules(t *testing.T) {
	if _, ok := writesTo([]string{"-n", "in"}, "-o", "--output"); ok {
		t.Error("a flag that is not an output flag must not be reported as one")
	}
	if _, ok := writesTo([]string{"-n", "in"}, "-o"); ok {
		t.Error("a flag that is not present must not be reported")
	}
	if _, ok := writesTo([]string{"--output"}, "--output"); !ok {
		t.Error("the long form must be recognised")
	}
	if _, ok := writesTo([]string{"--output=x"}, "--output"); !ok {
		t.Error("the glued long form must be recognised")
	}
	if _, ok := writesTo([]string{"-ox"}, "-o"); !ok {
		t.Error("the glued short form must be recognised")
	}
	// A separate value after the flag is not reported as a glued one, so `-o` alone is what
	// the rule quotes back to the user.
	if got, _ := writesTo([]string{"-o", "out"}, "-o"); got != "-o" {
		t.Errorf("the separate form must report the flag itself, got %q", got)
	}

	if got := positionals("xxd", []string{"-p", "in", "out"}); len(got) != 2 {
		t.Errorf("positionals must skip options: %v", got)
	}
	// The per-program table is what makes `-l` behave, and it is the case that caught a real
	// false alarm: xxd takes a value for -l, so `xxd -l 16 in.bin` names ONE operand, and an
	// operand-counting rule that missed it would refuse one of xxd's commonest invocations.
	if got := positionals("xxd", []string{"-l", "16", "in.bin"}); len(got) != 1 || got[0] != "in.bin" {
		t.Errorf("xxd -l takes a value, so 16 is not an operand: %v", got)
	}
	if got := positionals("xxd", []string{"-l16", "in.bin"}); len(got) != 1 {
		t.Errorf("a glued value is a single token: %v", got)
	}
	if got := positionals("uniq", []string{"-f", "2", "in.txt"}); len(got) != 1 {
		t.Errorf("uniq -f takes a value: %v", got)
	}
	// A program with NO entry counts an option's value as an operand, because the helper
	// cannot know otherwise. That is why a rule that counts operands must have an entry, and
	// why `sort`'s rule looks for its flag instead of counting: it has no such rule to make.
	if got := positionals("sort", []string{"-o", "out", "in"}); len(got) != 2 {
		t.Errorf("with no table entry the value counts as an operand, and the rule must not rely on counting: %v", got)
	}
	// `-` is stdin or stdout, and counts: `xxd - out` names an output file.
	if got := positionals("xxd", []string{"-", "out"}); len(got) != 2 {
		t.Errorf("- is an operand for these rules: %v", got)
	}
	// `--` ends the options, so what follows is an operand even when it starts with a dash.
	if got := positionals("xxd", []string{"--", "-weird"}); len(got) != 1 || got[0] != "-weird" {
		t.Errorf("after -- everything is an operand: %v", got)
	}

	if prog, ok := awkProgram([]string{"-F:", "'{print $1}'", "f"}); !ok || prog != "'{print $1}'" {
		t.Errorf("the program is the first non-option argument: %q ok=%v", prog, ok)
	}
	if prog, ok := awkProgram([]string{"-v", "n=1", "'{print}'"}); !ok || prog != "'{print}'" {
		t.Errorf("an option's VALUE is not the program: %q ok=%v", prog, ok)
	}
	if prog, ok := awkProgram([]string{"-f", "prog.awk", "f"}); !ok || prog != "f" {
		t.Errorf("-f names the program's file, so the next operand is the input: %q ok=%v", prog, ok)
	}
	// `--` ends the options, and the token right after it is the program even when it starts
	// with a dash. Without a token after it there is no program to read.
	if prog, ok := awkProgram([]string{"--", "-weird"}); !ok || prog != "-weird" {
		t.Errorf("after -- the next token is the program: %q ok=%v", prog, ok)
	}
	if _, ok := awkProgram([]string{"--"}); ok {
		t.Error("-- with nothing after it names no program")
	}
	if _, ok := awkProgram([]string{"-F:"}); ok {
		t.Error("a program composed only of options has no program text")
	}

	if awkRedirects("'{print}'") {
		t.Error("a program with no redirection must not be reported as one")
	}
	if !awkRedirects("'{print > \"out\"}'") {
		t.Error("a quoted redirection must be found")
	}
	if !awkRedirects("'{print >> \"out\"}'") {
		t.Error("an appending redirection must be found")
	}
	if !awkRedirects("'{print | \"sort\"}'") {
		t.Error("a pipe to a quoted command must be found")
	}
	if awkRedirects("'$1 > out'") {
		t.Error("a comparison of two fields is not a redirection, and refusing it would break real awk")
	}
	if awkRedirects("'$1>5'") {
		t.Error("a comparison of a field and a number is not a redirection")
	}
}
