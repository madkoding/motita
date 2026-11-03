package memory

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestNormalizeKind(t *testing.T) {
	for in, want := range map[string]string{
		"decision": "decision", " Facts ": "fact", "Decisión": "decision", "lección": "lesson",
		`"episodio"`: "episode", "people": "person", "nonsense": "", "": "",
	} {
		if got := NormalizeKind(in); got != want {
			t.Errorf("NormalizeKind(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestRelationsAreReadFromTheFrontMatter(t *testing.T) {
	body := "---\ntype: Decisión\nreplaces: \"[[Old plan]], [[Older]]\"\ndepends-on: Deploys, Staging\n" +
		"project: \"Shop\"\nrepos: acme/shop, acme/api\nmood: calm\n---\nText"
	m, rest, _ := ParseMeta(body, time.Time{})
	if m.Type != KindDecision || rest != "Text" || m.Project != "Shop" ||
		!reflect.DeepEqual(m.Repos, []string{"acme/shop", "acme/api"}) {
		t.Fatalf("meta = %+v rest %q", m, rest)
	}
	want := []Relation{
		{RelReplaces, "Old plan"}, {RelReplaces, "Older"}, {RelDependsOn, "Deploys"}, {RelDependsOn, "Staging"},
	}
	if !reflect.DeepEqual(m.Relations, want) {
		t.Errorf("relations = %+v", m.Relations)
	}
	if relationKey("Part_Of") != RelPartOf || relationKey("tags") != "" {
		t.Error("relationKey")
	}
	if got := relationTargets(`"[]"`); got != nil {
		t.Errorf("an empty property names nothing: %q", got)
	}
}

func TestFrontMatterIsEditedInPlace(t *testing.T) {
	plain := "Just text\n"
	typed := SetKind(plain, KindFact)
	if typed != "---\ntype: fact\n---\n\nJust text\n" || StripFront(typed) != "\nJust text\n" {
		t.Fatalf("typed = %q", typed)
	}
	if SetKind(typed, KindEpisode) != "---\ntype: episode\n---\n\nJust text\n" {
		t.Error("a kind is replaced, not added twice")
	}
	if StripFront(plain) != plain || splitFront(plain).String() != plain {
		t.Error("a body without front matter is kept as it is")
	}
	body := "---\nauthor: Ana\n---\nText"
	one := AddRelation(body, RelRelated, "Deploys")
	if one != "---\nauthor: Ana\nrelated: \"[[Deploys]]\"\n---\nText" {
		t.Fatalf("one = %q", one)
	}
	two := AddRelation(one, RelRelated, "Staging")
	if !strings.Contains(two, `related: "[[Deploys]], [[Staging]]"`) {
		t.Errorf("two = %q", two)
	}
	if AddRelation(two, RelRelated, "deploys") != two {
		t.Error("a relation already stated is not added again")
	}
	empty := AddRelation("---\nrelated:\n---\nx", RelRelated, "A")
	if !strings.Contains(empty, `related: "[[A]]"`) {
		t.Errorf("empty = %q", empty)
	}
}

func TestStampKind(t *testing.T) {
	day := time.Date(2026, 11, 3, 10, 0, 0, 0, time.UTC)
	got := StampKind("x", "motita", "Decisión", day)
	if got != "---\nauthor: motita\ndate: 2026-11-03\ntype: decision\n---\n\nx" {
		t.Errorf("got %q", got)
	}
}

func TestTheGraphNamesKindsAndAssociations(t *testing.T) {
	v := Open(t.TempDir())
	write(t, v, "a.md", "---\ntype: decision\nreplaces: \"[[b]]\"\ncontradicts: \"[[nowhere]]\"\n---\nsee [[b]] and [[c]]")
	write(t, v, "b.md", "old")
	write(t, v, "c.md", "---\nrelated: \"[[a]], [[a]]\"\n---\nback to [[a]]")
	g := v.Graph()
	types := map[string]string{}
	for _, n := range g.Nodes {
		types[n.Path] = n.Type
	}
	if types["a.md"] != KindDecision || types["b.md"] != "" {
		t.Errorf("types = %v", types)
	}
	kinds := map[string]string{}
	for _, e := range g.Edges {
		kinds[e.From+">"+e.To] = e.Kind
	}
	want := map[string]string{"a.md>b.md": RelReplaces, "a.md>c.md": "", "c.md>a.md": RelRelated}
	if !reflect.DeepEqual(kinds, want) || g.Missing != 1 {
		t.Errorf("edges = %v missing %d", kinds, g.Missing)
	}
}

func TestSearchNarrowsByKind(t *testing.T) {
	v := Open(t.TempDir())
	write(t, v, "a.md", "---\ntype: decision\n---\nWe deploy on Fridays")
	write(t, v, "b.md", "We deploy on Mondays")
	q := ParseQuery("deploy type:decision type:nonsense")
	if q.Type != KindDecision || q.Text != "deploy type:nonsense" {
		t.Fatalf("query = %+v", q)
	}
	res := v.Search(context.Background(), Query{Text: "deploy", Type: KindDecision})
	if len(res.Hits) != 1 || res.Hits[0].Path != "a.md" || res.Hits[0].Type != KindDecision {
		t.Fatalf("hits = %+v", res.Hits)
	}
	if out := v.SearchText("deploy type:decision"); !strings.Contains(out, ", a decision,") {
		t.Errorf("search text = %q", out)
	}
}

func TestAProjectViewSaysWhichProjectAndRepos(t *testing.T) {
	v := Open(t.TempDir())
	p := v.ForProject("Shop", "acme/shop", " ", "acme/shop", "acme/api")
	if !reflect.DeepEqual(p.Repos(), []string{"acme/shop", "acme/api"}) {
		t.Fatalf("repos = %v", p.Repos())
	}
	p.Keep("Deploys", "We deploy on Fridays", "decision")
	body, _, _ := v.Read("Shop/Deploys.md")
	for _, want := range []string{"project: Shop", "repos: acme/shop, acme/api", "type: decision"} {
		if !strings.Contains(body, want) {
			t.Errorf("entry lacks %q:\n%s", want, body)
		}
	}
	if got := p.belongs("Shop/x.md", "---\nproject: Other\n---\nx"); strings.Count(got, "project:") != 1 {
		t.Errorf("a project the entry names is kept: %q", got)
	}
	if got := v.ForProject("Shop").belongs("Shop/x.md", "x"); strings.Contains(got, "repos") {
		t.Errorf("no repositories, no repos line: %q", got)
	}
	if got := p.belongs("Other/x.md", "x"); got != "x" {
		t.Errorf("an entry of another collection is left alone: %q", got)
	}
}

func TestSimilarAndHome(t *testing.T) {
	v := Open(t.TempDir())
	write(t, v, "deploy.md", "# Deploy\n\nThe staging server deploys every friday with the release script.")
	write(t, v, "food.md", "# Food\n\nPizza on friday.")
	write(t, v, "other.md", "# Release\n\nThe release script tags the staging server build.")
	if v.Similar("", "", 3) != nil || v.Similar("staging", "", 0) != nil {
		t.Error("nothing to compare")
	}
	near := v.Similar("staging server deploys friday release script", "", 1)
	if len(near) != 1 || near[0].Path != "deploy.md" || near[0].Shared < 4 {
		t.Fatalf("near = %+v", near)
	}
	if got := v.Similar("staging server deploys friday release script", "deploy.md", 3); len(got) != 1 || got[0].Path != "other.md" {
		t.Errorf("skip = %+v", got)
	}
	if home, ok := v.Home("The staging server deploys every friday"); !ok || home != "deploy.md" {
		t.Errorf("home = %q %v", home, ok)
	}
	if _, ok := v.Home("Pizza with the staging people"); ok {
		t.Error("a text an entry barely covers has no home")
	}
	all := []Entry{{Path: "a/x.md", Title: "x"}, {Path: "b/x.md", Title: "x"}, {Path: "a/y.md", Title: "y"}}
	if LinkName(all, "a/y.md") != "y" || LinkName(all, "b/x.md") != "b/x" {
		t.Error("LinkName")
	}
}

func TestKeepAssociatesANewEntry(t *testing.T) {
	v := Open(t.TempDir())
	write(t, v, "deploy.md", "# Deploy\n\nThe staging server deploys every friday with the release script.")
	out := v.Keep("Release day", "The release script deploys the staging server on friday.", "fact")
	if !strings.Contains(out, "Added to [[Release day]]") || !strings.Contains(out, "Associated with [[deploy]].") {
		t.Fatalf("keep = %q", out)
	}
	body, _, _ := v.Read("Release day.md")
	if !strings.Contains(body, `related: "[[deploy]]"`) || !strings.Contains(body, "type: fact") {
		t.Errorf("body = %q", body)
	}
	// Added to an entry without a kind, the kind is written; with one, it stays.
	v.Keep("deploy", "It takes ten minutes.", "episode")
	v.Keep("deploy", "And it is logged.", "decision")
	body, _, _ = v.Read("deploy.md")
	if !strings.Contains(body, "type: episode") || strings.Contains(body, "decision") ||
		!strings.HasSuffix(body, "ten minutes.\n\nAnd it is logged.\n") {
		t.Errorf("body = %q", body)
	}
	if !strings.HasPrefix(v.Keep(" ", "x", ""), errPrefix) {
		t.Error("no title")
	}
	if !strings.HasPrefix(v.Keep("big", strings.Repeat("x", MaxEntryBytes+1), ""), errPrefix) {
		t.Error("too big")
	}
	old := readFile
	defer func() { readFile = old }()
	readFile = func(string) ([]byte, error) { return nil, errors.New("disk") }
	if !strings.HasPrefix(v.Keep("deploy", "x", ""), errPrefix) {
		t.Error("an entry that cannot be read is not added to")
	}
}

func TestAssociateSkipsWeakLikeness(t *testing.T) {
	v := Open(t.TempDir())
	write(t, v, "long.md", "# Long\n\nalpha beta gamma delta and many other words that are not here at all")
	text := "alpha beta gamma one two three four five six seven eight nine ten eleven twelve thirteen"
	body, linked := v.associate("x", text, "new.md")
	if body != "x" || linked != nil {
		t.Errorf("a weak likeness is not an association: %q %v", body, linked)
	}
}
