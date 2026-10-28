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
