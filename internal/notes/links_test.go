package notes

import (
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
	all := []Note{{Path: "a/deep/Japan.md"}, {Path: "Japan.md"}, {Path: "trips/Japan.md"}, {Path: "other/Peru.md"}}
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
	if got, _ := Resolve([]Note{{Path: "b/X.md"}, {Path: "a/X.md"}}, "x"); got != "a/X.md" {
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
	// The note itself is not its own backlink, and a note nobody links to has none.
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
	// A note that grew past the limit cannot be read and is skipped.
	writeRaw(v, "big.md", make([]byte, MaxNoteBytes+1))
	if back := v.Backlinks("b.md"); len(back) != 1 {
		t.Fatalf("%+v", back)
	}
}
