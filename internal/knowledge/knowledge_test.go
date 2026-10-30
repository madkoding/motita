package knowledge

import (
	"strings"
	"testing"

	"github.com/madkoding/motita/internal/memory"
)

// fakeLibrary answers like the library does, and records what it was asked.
type fakeLibrary struct{ saved string }

func (f *fakeLibrary) Search(query string) string {
	if strings.Contains(query, "usb") {
		return "1 skill(s) match. Use read_skill with a name.\n- flash-usb: Flash a board"
	}
	return "No skill matches " + query
}

func (f *fakeLibrary) Read(name string) string {
	if name == "flash-usb" {
		return "# skill: flash-usb\n1. plug it in"
	}
	return "No skill named " + name
}

func (f *fakeLibrary) Save(name, body string) string {
	f.saved = name + "|" + body
	return "Saved " + name
}

// base is a session with every place, holding one fact in each.
func base(t *testing.T) (Base, *fakeLibrary) {
	t.Helper()
	mem := memory.Open(t.TempDir())
	mem.WriteText("Deploys", "Deploys go to staging-zeta first.")
	ws := t.TempDir()
	if err := RecordLessons(ws, []string{"the deploy script is make ship"}); err != nil {
		t.Fatal(err)
	}
	lib := &fakeLibrary{}
	return Base{
		Memory: mem, Library: lib, Workspace: ws,
		Conversation: func(q string) string {
			if strings.Contains(q, "deploy") {
				return "Turn 2: deploy on friday"
			}
			return ""
		},
	}, lib
}

// A recall with no place looks everywhere and says where each answer comes from, with the tools
// it replaced named by the new ones.
func TestRecallLooksEverywhere(t *testing.T) {
	b, _ := base(t)
	got := b.Recall("", "deploy usb", "")
	for _, want := range []string{
		"## Earlier in this conversation", "deploy on friday",
		"## This project's lessons", "make ship",
		"## The person's memory", "staging-zeta",
		"## Procedures", "flash-usb",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("recall lacks %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "read_skill") || strings.Contains(got, "read_memory") {
		t.Errorf("an old tool name is still given:\n%s", got)
	}
	if got := b.Recall("", "zzz", ""); !strings.HasPrefix(got, "Nothing is known about") {
		t.Errorf("an empty recall = %q", got)
	}
}

func TestRecallInOnePlace(t *testing.T) {
	b, _ := base(t)
	cases := map[string]string{
		"memoria":      "staging-zeta",
		"skills":       "flash-usb",
		"proyecto":     "make ship",
		"conversation": "deploy on friday",
	}
	for in, want := range cases {
		if got := b.Recall(in, "deploy usb", ""); !strings.Contains(got, want) {
			t.Errorf("recall in %s = %q", in, got)
		}
	}
	if got := b.Recall("memory", "zzz", ""); !strings.HasPrefix(got, "Nothing in the memory matches") {
		t.Errorf("a memory miss = %q", got)
	}
	if got := b.Recall("conversation", "zzz", ""); got != `Nothing in conversation matches "zzz".` {
		t.Errorf("a conversation miss = %q", got)
	}
	if got := b.Recall("attic", "x", ""); !strings.Contains(got, "no place called") {
		t.Errorf("an unknown place = %q", got)
	}
	if got := b.Recall("", " ", ""); !strings.HasPrefix(got, "Error: say what") {
		t.Errorf("an empty query = %q", got)
	}
}

func TestRecallOpensOneEntry(t *testing.T) {
	b, _ := base(t)
	if got := b.Recall("", "", "Deploys"); !strings.Contains(got, "staging-zeta") {
		t.Errorf("open a memory entry = %q", got)
	}
	if got := b.Recall("", "", "flash-usb"); !strings.Contains(got, "plug it in") {
		t.Errorf("open falls back to a procedure: %q", got)
	}
	if got := b.Recall("memory", "", "flash-usb"); !strings.Contains(got, "there is no entry") {
		t.Errorf("open in memory only = %q", got)
	}
	if got := b.Recall("procedures", "", "flash-usb"); !strings.Contains(got, "plug it in") {
		t.Errorf("open a procedure = %q", got)
	}
	if got := b.Recall("project", "", "all"); !strings.Contains(got, "make ship") {
		t.Errorf("open the lessons = %q", got)
	}
	if got := b.Recall("conversation", "", "deploy"); !strings.Contains(got, "friday") {
		t.Errorf("open the conversation = %q", got)
	}
	empty := Base{}
	if got := empty.Recall("project", "", "x"); got != "This project has no lessons yet." {
		t.Errorf("no lessons = %q", got)
	}
	if got := empty.Recall("memory", "", "x"); !strings.Contains(got, "no memory") {
		t.Errorf("no memory = %q", got)
	}
	if got := empty.Recall("", "", "x"); !strings.Contains(got, "no procedure library") {
		t.Errorf("nothing at all = %q", got)
	}
	if got := empty.Recall("", "anything", ""); !strings.HasPrefix(got, "Nothing is known") {
		t.Errorf("an empty session's recall = %q", got)
	}
}

func TestRememberKeepsItInThePlaceNamed(t *testing.T) {
	b, lib := base(t)
	if got := b.Remember("", "Deploys", "Rollbacks need a ticket. token=sk-abcdefghijklmnopqrstuvwxyz123456"); !strings.HasPrefix(got, "Added to [[Deploys]]") {
		t.Fatalf("remember in memory = %q", got)
	}
	if read := b.Memory.ReadText("Deploys"); !strings.Contains(read, "Rollbacks need a ticket") || strings.Contains(read, "sk-abcdefghij") {
		t.Errorf("the memory holds %q", read)
	}
	if got := b.Remember("procedures", " flash-usb ", "# Flash"); got != "Saved flash-usb" || lib.saved != "flash-usb|# Flash" {
		t.Errorf("remember a procedure = %q (%q)", got, lib.saved)
	}
	if got := b.Remember("project", "", "pnpm, not npm\ntests need docker"); !strings.Contains(got, LessonsFile) {
		t.Errorf("remember a lesson = %q", got)
	}
	if n := len(Lessons(b.Workspace)); n != 3 {
		t.Errorf("lessons = %d, want 3", n)
	}
	for _, tc := range []struct{ in, content, want string }{
		{"conversation", "x", "keeps itself"},
		{"attic", "x", "no place called"},
		{"", "  ", "say what to remember"},
	} {
		if got := b.Remember(tc.in, "e", tc.content); !strings.Contains(got, tc.want) {
			t.Errorf("remember(%q) = %q", tc.in, got)
		}
	}
	empty := Base{}
	for in, want := range map[string]string{"project": "no project", "procedures": "no procedure library", "memory": "no memory"} {
		if got := empty.Remember(in, "e", "x"); !strings.Contains(got, want) {
			t.Errorf("remember in %s with nothing = %q", in, got)
		}
	}
}

func TestCutStopsOnARuneBoundary(t *testing.T) {
	if got := cut("abc", 5); got != "abc" {
		t.Errorf("cut = %q", got)
	}
	if got := cut("añb", 2); !strings.HasPrefix(got, "a\n") {
		t.Errorf("cut = %q", got)
	}
}
