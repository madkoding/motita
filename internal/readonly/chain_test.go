package readonly

import (
	"reflect"
	"strings"
	"testing"
)

func TestSplitChain(t *testing.T) {
	cases := []struct {
		line string
		want []Step
	}{
		{"ls", []Step{{"", "ls"}}},
		{"git status; git log", []Step{{"", "git status"}, {";", "git log"}}},
		{"a && b || c", []Step{{"", "a"}, {"&&", "b"}, {"||", "c"}}},
		{"git log | head -5", []Step{{"", "git log"}, {"|", "head -5"}}},
		{"grep 'a|b;c&&d' f", []Step{{"", "grep 'a|b;c&&d' f"}}},
		{`grep "a|b" f`, []Step{{"", `grep "a|b" f`}}},
		{`echo a\;b`, []Step{{"", `echo a\;b`}}},
		{`echo 'a\' ; ls`, []Step{{"", `echo 'a\'`}, {";", "ls"}}},
		{"git log 2>&1 | head", []Step{{"", "git log"}, {"|", "head"}}},
		{"git log 2>&1", []Step{{"", "git log"}}},
		{"git log 2>&1; ls", []Step{{"", "git log"}, {";", "ls"}}},
		{"2>&1 ls", []Step{{"", "ls"}}},
		{"echo a2>&1", []Step{{"", "echo a2>&1"}}},
		{"echo 2>&1x", []Step{{"", "echo 2>&1x"}}},
		{"echo 2>&", []Step{{"", "echo 2>&"}}},
		{"echo a & b", []Step{{"", "echo a & b"}}},
		{"echo a &", []Step{{"", "echo a &"}}},
	}
	for _, tc := range cases {
		got, err := SplitChain(tc.line)
		if err != nil || !reflect.DeepEqual(got, tc.want) {
			t.Errorf("SplitChain(%q) = %v, %v; want %v", tc.line, got, err, tc.want)
		}
	}
}

func TestSplitChainErrors(t *testing.T) {
	for line, want := range map[string]string{
		"":                  "no command to run",
		"   ":               "no command to run",
		"ls ;":              `ends with ";"`,
		"ls &&":             `ends with "&&"`,
		"ls |":              `ends with "|"`,
		"; ls":              `no command before ";"`,
		"ls ; ; pwd":        `no command before ";"`,
		"a | | b":           `no command before "|"`,
		"1;2;3;4;5;6;7;8;9": "at most 8",
		"a && b && c && d && e && f && g && h && i": "at most 8",
	} {
		if _, err := SplitChain(line); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("SplitChain(%q) error = %v, want %q", line, err, want)
		}
	}
}

func TestStepsStillGoThroughTheTokeniser(t *testing.T) {
	// The chain splitter is not a shell: what is left in a step is judged by SplitCommand.
	for _, line := range []string{"ls > f", "echo $(pwd)", "echo `pwd`", "ls & pwd", "cat <f"} {
		steps, err := SplitChain(line)
		if err != nil {
			t.Fatalf("%q: %v", line, err)
		}
		if _, _, err := SplitCommand(steps[0].Command); err == nil {
			t.Errorf("%q must still be refused by the tokeniser", line)
		}
	}
}
