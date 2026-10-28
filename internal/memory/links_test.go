package memory

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestParseLinks(t *testing.T) {
	body := "See [[Japan]] and [[trips/Japan#Food|the food]], [[Japan]] again, ![[img.md]].\n" +
		"`[[inline]]` and\n```\n[[fenced]]\n```\n[[ ]] [[x.md]]"
	got := ParseLinks(body)
	want := []Link{
		{Target: "Japan"},
		{Target: "trips/Japan", Heading: "Food", Alias: "the food"},
		{Target: "img"},
		{Target: "x"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v\nwant %+v", got, want)
	}
}

func TestResolveFollowsObsidian(t *testing.T) {
	all := []Entry{{Path: "a/deep/Japan.md"}, {Path: "Japan.md"}, {Path: "trips/Japan.md"}, {Path: "other/Peru.md"}}
	for target, want := range map[string]string{
		"japan":        "Japan.md",
		"trips/japan":  "trips/Japan.md",
		"deep/Japan":   "a/deep/Japan.md",
		"Peru.md":      "other/Peru.md",
		"/other/peru/": "other/Peru.md",
	} {
		if got, ok := Resolve(all, target); !ok || got != want {
			t.Errorf("Resolve(%q) = %q %v, want %q", target, got, ok, want)
		}
	}
	for _, none := range []string{"", "nothing", "/"} {
		if got, ok := Resolve(all, none); ok {
			t.Errorf("Resolve(%q) = %q", none, got)
		}
	}
	// Same depth: the path order decides.
	if got, _ := Resolve([]Entry{{Path: "b/X.md"}, {Path: "a/X.md"}}, "x"); got != "a/X.md" {
		t.Errorf("tie = %q", got)
	}
}

func TestLinksOfAndBacklinks(t *testing.T) {
	v := Open(t.TempDir())
	write(t, v, "Japan.md", "# Japan\nSee [[Peru]].")
	write(t, v, "Peru.md", "# Peru")
	write(t, v, "trips/plan.md", "Plan:\n"+strings.Repeat("y", 200)+" [[japan|Nippon]] and [[peru]]\n")
	write(t, v, "lonely.md", "no links, [[ghost]]")
	all := v.List()
	body, _, _ := v.Read("Japan.md")
	links := LinksOf(all, body)
	if len(links) != 1 || links[0].Path != "Peru.md" {
		t.Fatalf("links = %+v", links)
	}
	if ghost := LinksOf(all, "[[ghost]]"); ghost[0].Path != "" {
		t.Errorf("ghost = %+v", ghost)
	}
	back := v.Backlinks("Peru.md")
	if len(back) != 2 || back[0].Path != "Japan.md" || back[1].Path != "trips/plan.md" {
		t.Fatalf("backlinks = %+v", back)
	}
	if !strings.HasSuffix(back[1].Context, "…") || back[0].Context != "See [[Peru]]." {
		t.Errorf("context = %q / %q", back[0].Context, back[1].Context)
	}
	// The entry itself is not its own backlink, and an entry nobody links to has none.
	if b := v.Backlinks("lonely.md"); len(b) != 0 {
		t.Errorf("lonely = %+v", b)
	}
}

func TestContextOfWithoutTheLine(t *testing.T) {
	if got := contextOf("nothing here", Link{Target: "x"}); got != "" {
		t.Errorf("%q", got)
	}
}

func TestBacklinksSkipNotesThatCannotBeRead(t *testing.T) {
	v := Open(t.TempDir())
	write(t, v, "a.md", "[[b]]")
	write(t, v, "b.md", "x")
	// An entry that grew past the limit cannot be read and is skipped.
	writeRaw(v, "big.md", make([]byte, MaxEntryBytes+1))
	if back := v.Backlinks("b.md"); len(back) != 1 {
		t.Fatalf("%+v", back)
	}
}

func TestRenameLinkedRewritesTheLinksThatLeadToTheNote(t *testing.T) {
	v := Open(t.TempDir())
	write(t, v, "trips/Japan.md", "# Japan\nSelf [[Japan]]")
	write(t, v, "a.md", "See [[Japan]], [[japan#Food|ramen]], ![[trips/Japan]], [[Peru]] and `[[Japan]]`.\n"+
		"```\n[[Japan]]\n```")
	write(t, v, "Peru.md", "nothing here")
	got, err := v.RenameLinked("trips/Japan.md", "trips/Nippon.md")
	if err != nil || got != 2 {
		t.Fatalf("RenameLinked = %d, %v", got, err)
	}
	a, _, _ := v.Read("a.md")
	want := "See [[Nippon]], [[Nippon#Food|ramen]], ![[trips/Nippon]], [[Peru]] and `[[Japan]]`.\n```\n[[Japan]]\n```"
	if a != want {
		t.Errorf("a.md = %q", a)
	}
	if self, _, _ := v.Read("trips/Nippon.md"); !strings.Contains(self, "[[Nippon]]") {
		t.Errorf("the entry's own link = %q", self)
	}
}

func TestRenameLinkedWritesThePathWhenTheNameWouldBeAmbiguous(t *testing.T) {
	v := Open(t.TempDir())
	write(t, v, "Nippon.md", "the other one")
	write(t, v, "trips/Japan.md", "x")
	write(t, v, "a.md", "[[Japan]]")
	if got, err := v.RenameLinked("trips/Japan.md", "trips/Nippon.md"); err != nil || got != 1 {
		t.Fatalf("RenameLinked = %d, %v", got, err)
	}
	if a, _, _ := v.Read("a.md"); a != "[[trips/Nippon]]" {
		t.Errorf("a.md = %q", a)
	}
}

func TestRenameLinkedReportsWhatItCannotDo(t *testing.T) {
	v := Open(t.TempDir())
	write(t, v, "a.md", "A")
	write(t, v, "b.md", "[[a]]")
	if _, err := v.RenameLinked("../x.md", "c.md"); !errors.Is(err, ErrBadPath) {
		t.Errorf("bad path = %v", err)
	}
	if _, err := v.RenameLinked("zz.md", "c.md"); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing = %v", err)
	}

	old := readFile
	defer func() { readFile = old }()
	readFile = func(p string) ([]byte, error) {
		if strings.HasSuffix(p, "b.md") {
			return nil, errors.New("locked")
		}
		return old(p)
	}
	if got, err := v.RenameLinked("a.md", "c.md"); err != nil || got != 0 {
		t.Errorf("an unreadable entry is skipped: %d, %v", got, err)
	}
	readFile = old

	oldMove := moveFile
	defer func() { moveFile = oldMove }()
	moveFile = func(string, string) error { return errors.New("disk full") }
	if got, err := v.RenameLinked("c.md", "d.md"); err != nil || got != 0 {
		t.Errorf("an entry that cannot be rewritten is not counted: %d, %v", got, err)
	}
}
