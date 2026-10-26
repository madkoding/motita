package readonly

import (
	"strings"
	"testing"
)

func mustFilter(t *testing.T, command string) Filter {
	t.Helper()
	f, err := ParseFilter(command)
	if err != nil {
		t.Fatalf("ParseFilter(%q): %v", command, err)
	}
	return f
}

const sample = "alpha\nBeta\ngamma\nbeta two\n"

func TestFilters(t *testing.T) {
	cases := []struct {
		command string
		in      string
		out     string
		exit    int
	}{
		{"head", "1\n2\n3\n4\n5\n6\n7\n8\n9\n10\n11\n12\n", "1\n2\n3\n4\n5\n6\n7\n8\n9\n10\n", 0},
		{"head -n 2", sample, "alpha\nBeta\n", 0},
		{"head -n2", sample, "alpha\nBeta\n", 0},
		{"head --lines=1", sample, "alpha\n", 0},
		{"head --lines 1", sample, "alpha\n", 0},
		{"head -3", sample, "alpha\nBeta\ngamma\n", 0},
		{"head -n 99", sample, sample, 0},
		{"head -n 0", sample, "", 0},
		{"head", "", "", 0},
		{"tail -n 2", sample, "gamma\nbeta two\n", 0},
		{"tail -1", "x\ny", "y\n", 0},
		{"tail -n 9", sample, sample, 0},
		{"wc -l", sample, "4\n", 0},
		{"wc -w", sample, "5\n", 0},
		{"wc -c", "abc", "3\n", 0},
		{"wc", "a b\nc\n", "2 3 6\n", 0},
		{"wc -l", "", "0\n", 0},
		{"grep beta", sample, "beta two\n", 0},
		{"grep -i beta", sample, "Beta\nbeta two\n", 0},
		{"grep -v a", "x\nab\n", "x\n", 0},
		{"grep -c a", "a\nb\na\n", "2\n", 0},
		{"grep -n b", "a\nb\n", "2:b\n", 0},
		{"grep -nE '^(a|b)$'", "a\nc\nb\n", "1:a\n3:b\n", 0},
		{"grep -F a.b", "a.b\naxb\n", "a.b\n", 0},
		{"grep -- x", "x\n", "x\n", 0},
		{"grep zzz", sample, "", 1},
		{"grep -c zzz", sample, "0\n", 1},
		{"/usr/bin/grep alpha", sample, "alpha\n", 0},
	}
	for _, tc := range cases {
		out, exit := mustFilter(t, tc.command)(tc.in)
		if out != tc.out || exit != tc.exit {
			t.Errorf("%s over %q = %q, %d; want %q, %d", tc.command, tc.in, out, exit, tc.out, tc.exit)
		}
	}
}

func TestFilterRefusals(t *testing.T) {
	for command, want := range map[string]string{
		"sort":           "only head, tail, wc and grep",
		"cat":            "only head, tail, wc and grep",
		"head -n":        "needs a number",
		"head --lines":   "needs a number",
		"head -n x":      "not a line count",
		"head -n -1":     "not a line count",
		"head -c 5":      "not an option",
		"tail +3":        "not an option",
		"head file":      "not an option",
		"wc -m":          "use -l, -w or -c",
		"grep":           "needs a pattern",
		"grep -z x":      "not supported",
		"grep x file":    "reads the pipe",
		"grep '('":       "not valid",
		"head 'unclosed": "unclosed quote",
	} {
		if _, err := ParseFilter(command); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("ParseFilter(%q) error = %v, want %q", command, err, want)
		}
	}
}
