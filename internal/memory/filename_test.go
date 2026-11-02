package memory

import "testing"

func TestFileNameStripsWhatAFileCannotHold(t *testing.T) {
	cases := map[[2]string]string{
		{"", "Idea"}:                    "Idea.md",
		{"Proj", "Idea.MD"}:             "Proj/Idea.md",
		{"/Proj/", "sub / A:b?"}:        "Proj/sub/Ab.md",
		{"", "../.hid/x"}:               "x.md",
		{"Sessions", "Fix: a|b [x] #1"}: "Sessions/Fix ab x 1.md",
		{"", "  "}:                      "",
		{"", ".md"}:                     "",
	}
	for in, want := range cases {
		if got := FileName(in[0], in[1]); got != want {
			t.Errorf("FileName(%q, %q) = %q, want %q", in[0], in[1], got, want)
		}
	}
}

func TestNameForTakesTheFirstLineOfTheText(t *testing.T) {
	cases := map[string]string{
		"## Mad prefers Spanish\nmore":                 "Mad prefers Spanish",
		"\n\n- a/b: c [x] #1":                          "a b c x 1",
		"one two three four five six seven eight nine": "one two three four five six seven eight",
		"..hidden start":                               "hidden start",
		"#  \n***\n":                                   "Notes",
		"":                                             "Notes",
	}
	long := ""
	for range 20 {
		long += "abcdefghij"
	}
	cases[long] = long[:60]
	for in, want := range cases {
		if got := NameFor(in); got != want {
			t.Errorf("NameFor(%q) = %q, want %q", in, got, want)
		}
	}
}
