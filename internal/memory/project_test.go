package memory

import (
	"context"
	"strings"
	"testing"
)

func TestForProjectNamesAFolder(t *testing.T) {
	var none *Store
	if none.ForProject("x") != nil {
		t.Error("nil memory")
	}
	v := Open(t.TempDir())
	for in, want := range map[string]string{
		"motita":                "motita",
		" a/b\\c ":              "a-b-c",
		"..hidden":              "hidden",
		strings.Repeat("x", 90): strings.Repeat("x", 80),
		"":                      "",
	} {
		if got := v.ForProject(in).Project(); got != want {
			t.Errorf("ForProject(%q) = %q, want %q", in, got, want)
		}
	}
	if v.Project() != "" {
		t.Error("the memory itself has no project")
	}
}

func TestAProjectWritesToItsCollection(t *testing.T) {
	v := Open(t.TempDir())
	p := v.ForProject("motita")
	out := p.WriteText("Decisions", "We use Go.")
	if !strings.Contains(out, "(motita/Decisions.md)") || !strings.Contains(out, `collection "motita"`) {
		t.Errorf("write = %q", out)
	}
	if _, ok := SavedLine(out); !ok {
		t.Error("the saved line must still read the reply")
	}
	// "/" at the start writes at the top, for every project.
	if out := p.WriteText("/Preferences", "Short answers."); !strings.Contains(out, "(Preferences.md)") ||
		strings.Contains(out, "collection") {
		t.Errorf("top write = %q", out)
	}
	// A path with a folder is written as given.
	if out := p.WriteText("HR/Policy", "x"); !strings.Contains(out, "(HR/Policy.md)") {
		t.Errorf("folder write = %q", out)
	}
	// Outside a project nothing changes.
	if out := v.WriteText("Decisions", "x"); !strings.Contains(out, "(Decisions.md)") {
		t.Errorf("plain write = %q", out)
	}
}

func TestAProjectReadsAndAddsToItsOwnEntryFirst(t *testing.T) {
	v := Open(t.TempDir())
	write(t, v, "Decisions.md", "top level\n")
	write(t, v, "other/Notes.md", "the other project's notes\n")
	write(t, v, "motita/Decisions.md", "motita decisions\n")
	p := v.ForProject("motita")
	// The project's entry wins over the shallower one of the same name.
	if out := p.ReadText("Decisions"); !strings.Contains(out, "motita decisions") {
		t.Errorf("read = %q", out)
	}
	if out := p.ReadText("/Decisions"); !strings.Contains(out, "top level") {
		t.Errorf("top read = %q", out)
	}
	if out := p.ReadText("/Missing"); !strings.HasPrefix(out, errPrefix) {
		t.Errorf("missing top read = %q", out)
	}
	// Found elsewhere when the project has none.
	if out := p.ReadText("Notes"); !strings.Contains(out, "other project's notes") {
		t.Errorf("read elsewhere = %q", out)
	}
	// An append to a bare title found only in another project makes the project's own entry.
	if out := p.AppendText("Notes", "mine"); !strings.Contains(out, "(motita/Notes.md)") {
		t.Errorf("append = %q", out)
	}
	// One found at the top or in the project is added to.
	if out := p.AppendText("Decisions", "more"); !strings.Contains(out, "(motita/Decisions.md)") {
		t.Errorf("append to the project's = %q", out)
	}
	write(t, v, "Glossary.md", "words\n")
	if out := p.AppendText("Glossary", "more"); !strings.Contains(out, "(Glossary.md)") ||
		strings.Contains(out, "collection") {
		t.Errorf("append to the top = %q", out)
	}
	// A title with a folder goes where it says.
	if out := p.AppendText("other/Notes", "x"); !strings.Contains(out, "(other/Notes.md)") {
		t.Errorf("append with a folder = %q", out)
	}
	if out := v.AppendText("Notes", "x"); !strings.Contains(out, "(motita/Notes.md)") &&
		!strings.Contains(out, "(other/Notes.md)") {
		t.Errorf("plain append = %q", out)
	}
}

func TestAProjectSearchRanksItsCollectionFirst(t *testing.T) {
	v := Open(t.TempDir())
	write(t, v, "alpha/Deploy.md", "# Deploy\n\nDeploy with the release script and check the dashboards twice.\n")
	write(t, v, "motita/Deploy.md", "# Deploy\n\nDeploy with the release script.\n")
	plain := v.Search(context.Background(), Query{Text: "deploy release script dashboards"})
	if len(plain.Hits) < 2 || plain.Hits[0].Path != "alpha/Deploy.md" {
		t.Fatalf("plain = %s", paths(plain.Hits))
	}
	p := v.ForProject("motita")
	mine := p.Search(context.Background(), Query{Text: "deploy release script dashboards"})
	if len(mine.Hits) < 2 || mine.Hits[0].Path != "motita/Deploy.md" {
		t.Errorf("in the project = %s", paths(mine.Hits))
	}
	// A search that names a collection is answered as asked.
	asked := p.Search(context.Background(), Query{Text: "deploy", Namespace: "alpha"})
	if paths(asked.Hits) != "alpha/Deploy.md" {
		t.Errorf("ns:alpha = %s", paths(asked.Hits))
	}
	// A query of words the vector space does not know still ranks by words.
	if res := p.Search(context.Background(), Query{Text: "dashboards"}); len(res.Hits) == 0 {
		t.Error("no hit")
	}
}
