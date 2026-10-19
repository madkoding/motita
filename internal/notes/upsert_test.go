package notes

import (
	"errors"
	"strings"
	"testing"
)

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

func TestUpsertKeepsWhatThePersonWrote(t *testing.T) {
	v := Open(t.TempDir())
	if got, err := v.Upsert("P/a.md", "A", "one"); err != nil || got != Created {
		t.Fatalf("first = %q, %v", got, err)
	}
	body, _, _ := v.Read("P/a.md")
	if !strings.HasPrefix(body, "# A\n\n"+BlockStart+"\none\n"+BlockEnd) {
		t.Errorf("created = %q", body)
	}
	if got, _ := v.Upsert("P/a.md", "A", "one"); got != Unchanged {
		t.Errorf("same block = %q", got)
	}
	write(t, v, "P/a.md", "# A\nmine before\n"+BlockStart+"\none\n"+BlockEnd+"\nmine after\n")
	if got, err := v.Upsert("P/a.md", "A", "two"); err != nil || got != Updated {
		t.Fatalf("update = %q, %v", got, err)
	}
	body, _, _ = v.Read("P/a.md")
	if body != "# A\nmine before\n"+BlockStart+"\ntwo\n"+BlockEnd+"\nmine after\n" {
		t.Errorf("updated = %q", body)
	}

	write(t, v, "plain.md", "just mine")
	if got, _ := v.Upsert("plain.md", "plain", "x"); got != Updated {
		t.Errorf("no markers = %q", got)
	}
	if body, _, _ := v.Read("plain.md"); body != "just mine\n\n"+BlockStart+"\nx\n"+BlockEnd+"\n" {
		t.Errorf("appended = %q", body)
	}
	write(t, v, "cut.md", "mine\n"+BlockStart+"\nold")
	if _, err := v.Upsert("cut.md", "cut", "new"); err != nil {
		t.Fatal(err)
	}
	if body, _, _ := v.Read("cut.md"); body != "mine\n"+BlockStart+"\nnew\n"+BlockEnd {
		t.Errorf("unclosed = %q", body)
	}
}

func TestUpsertReportsWhatItCannotDo(t *testing.T) {
	v := Open(t.TempDir())
	if _, err := v.Upsert("../x.md", "x", "b"); !errors.Is(err, ErrBadPath) {
		t.Errorf("bad path = %v", err)
	}
	write(t, v, "a.md", "mine")
	old := moveFile
	defer func() { moveFile = old }()
	moveFile = func(string, string) error { return errors.New("disk full") }
	if _, err := v.Upsert("a.md", "a", "b"); err == nil {
		t.Error("a write that fails must be reported")
	}
}
