package memory

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// corpus is a small memory of two collections, with links, headings and front matter.
func corpus(t *testing.T) *Store {
	t.Helper()
	v := Open(t.TempDir())
	write(t, v, "HR/Hiring.md", "---\nauthor: Ana\ndate: 2026-03-10\ntags: salary, offers\n---\n"+
		"# Hiring\n\nHow we hire.\n\n## Offers\n\n### Salary bands\n\nThe band for senior engineers tops out at 90k. "+
		"Payroll closes on the 25th and the salary review happens in March.\n\n## Interviews\n\n"+
		"Two rounds. Ask about teamwork. See [[Onboarding]].\n")
	write(t, v, "HR/Onboarding.md", "---\nauthor: Luis\ndate: 2025-01-05\n---\n# Onboarding\n\n"+
		"The first week: laptop, accounts, buddy. Payroll forms are signed on day one. Back to [[Hiring]].\n")
	write(t, v, "Engineering/Deploys.md", "# Deploys\n\n## Rollback\n\nTo roll back a release run the rollback script "+
		"and check the dashboards. Incident reviews are written in [[Postmortems]].\n")
	write(t, v, "Engineering/Postmortems.md",
		"# Postmortems\n\nAfter every outage we write what failed. The rollback of 2026-02 took ten minutes.\n")
	write(t, v, "Inbox.md", // spanish-fixture: accent folding
		"Pick up the política de vacaciones from the office. Café con Ana.\n") // spanish-fixture: accent folding
	return v
}

func paths(hits []Hit) string {
	var out []string
	for _, h := range hits {
		out = append(out, h.Path)
	}
	return strings.Join(out, ",")
}

func TestSearchAnswersWithTheParentSectionOfTheBestPassage(t *testing.T) {
	v := corpus(t)
	res := v.Search(context.Background(), Query{Text: "senior engineers salary band"})
	if len(res.Hits) == 0 || res.Hits[0].Path != "HR/Hiring.md" {
		t.Fatalf("hits = %+v", res.Hits)
	}
	h := res.Hits[0]
	if h.Heading != "Offers › Salary bands" || h.Namespace != "HR" || h.Author != "Ana" {
		t.Errorf("hit = %+v", h)
	}
	if !strings.HasPrefix(h.Block, "### Salary bands") || !strings.Contains(h.Block, "Payroll closes") {
		t.Errorf("the parent block must come back whole, got %q", h.Block)
	}
	if h.Date.Format("2006-01-02") != "2026-03-10" || h.Line < 9 || h.Snippet == "" {
		t.Errorf("metadata or position = %+v", h)
	}
}

func TestContextualChunkingFindsAPassageThroughItsTitle(t *testing.T) {
	v := Open(t.TempDir())
	write(t, v, "Engineering/Database.md", "# Database\n\n## Backups\n\nRun it nightly at three and keep seven copies.\n")
	write(t, v, "Engineering/Cache.md", "# Cache\n\n## Eviction\n\nRun it nightly at three and keep seven copies.\n")
	res := v.Search(context.Background(), Query{Text: "database backups nightly"})
	if len(res.Hits) == 0 || res.Hits[0].Path != "Engineering/Database.md" {
		t.Errorf("the title and heading in front of a passage decide which of two equal passages wins: %+v", res.Hits)
	}
}

func TestTheHolographicVectorRecallsWhatSharesNoWord(t *testing.T) {
	v := Open(t.TempDir())
	for i, body := range []string{
		"payroll salary wages bonus pay", "payroll salary wages overtime pay", "salary wages bonus raise pay",
		"payroll wages bonus overtime pay", "garden tomato soil water seeds", "garden soil seeds compost water",
		"tomato garden compost seeds soil", "garden water compost tomato soil",
	} {
		write(t, v, "n"+string(rune('a'+i))+".md", body)
	}
	write(t, v, "target.md", "# Target\n\nsalary wages bonus pay\n")
	write(t, v, "plain.md", "# Plain\n\nwages\n")
	// "payroll" is in no word of target.md or plain.md, but it is used with the same words the
	// target is made of, so its vector resembles it.
	ix := v.index()
	cue, ok := ix.holo.cue(terms("payroll"))
	if !ok {
		t.Fatal("no cue")
	}
	keep := func(int) bool { return true }
	found := false
	for _, s := range ix.holo.recall(cue, keep) {
		if ix.entries[ix.kids[s.at].entry].Path == "target.md" {
			found = true
		}
	}
	if !found {
		t.Error("a cue that shares no word with a passage should still recall it through shared context")
	}
}

func TestFiltersNarrowTheSearch(t *testing.T) {
	v := corpus(t)
	ctx := context.Background()
	if got := paths(v.Search(ctx, Query{Text: "payroll", Namespace: "engineering"}).Hits); got != "" {
		t.Errorf("namespace filter: %q", got)
	}
	if got := paths(v.Search(ctx, Query{Text: "payroll", Namespace: "hr"}).Hits); !strings.Contains(got, "HR/") {
		t.Errorf("namespace filter keeps its own: %q", got)
	}
	if got := paths(v.Search(ctx, Query{Text: "payroll", Author: "luis"}).Hits); got != "HR/Onboarding.md" {
		t.Errorf("author filter: %q", got)
	}
	since, _ := ParseDate("2026-01-01")
	if got := paths(v.Search(ctx, Query{Text: "payroll", Since: since}).Hits); got != "HR/Hiring.md" {
		t.Errorf("since filter: %q", got)
	}
	until, _ := ParseDate("2025-12-31")
	if got := paths(v.Search(ctx, Query{Text: "payroll", Until: until}).Hits); got != "HR/Onboarding.md" {
		t.Errorf("until filter: %q", got)
	}
}

func TestParseQueryReadsFiltersAndKeepsTheRest(t *testing.T) {
	q := ParseQuery(`salary ns:"Human Resources" author:Ana since:2026-01-02 until:2026-02-03 graph:1 until:soon ns: x:y`)
	if q.Text != "salary until:soon ns: x:y" || q.Namespace != "Human Resources" || q.Author != "Ana" || !q.Graph {
		t.Errorf("q = %+v", q)
	}
	if q.Since.Format("2006-01-02") != "2026-01-02" || q.Until.Format("2006-01-02 15") != "2026-02-03 23" {
		t.Errorf("dates = %v %v", q.Since, q.Until)
	}
	if q := ParseQuery(`"a b" graph:no ns:A`); q.Text != "a b graph:no" || q.Graph || q.Namespace != "A" {
		t.Errorf("q = %+v", q)
	}
	if q := ParseQuery("  "); q.Text != "" {
		t.Errorf("empty = %+v", q)
	}
}

func TestGraphAddsTheEntriesLinkedToTheBestOnes(t *testing.T) {
	v := corpus(t)
	ctx := context.Background()
	plain := v.Search(ctx, Query{Text: "rollback script dashboards", Namespace: "Engineering", Limit: 1})
	if len(plain.Related) != 0 {
		t.Fatalf("related without graph: %+v", plain.Related)
	}
	res := v.Search(ctx, Query{Text: "rollback script dashboards", Limit: 1, Graph: true})
	if len(res.Hits) != 1 || res.Hits[0].Path != "Engineering/Deploys.md" {
		t.Fatalf("hits = %+v", res.Hits)
	}
	if len(res.Related) != 1 {
		t.Fatalf("related = %+v", res.Related)
	}
	if r := res.Related[0]; r.Path != "Engineering/Postmortems.md" || r.Via != "[[Deploys]]" {
		t.Fatalf("related = %+v", res.Related)
	}
	if len(res.Edges) != 1 || res.Edges[0] != (Edge{"Engineering/Deploys.md", "Engineering/Postmortems.md"}) {
		t.Errorf("edges = %+v", res.Edges)
	}
	// the filters hold for the related entries too
	res = v.Search(ctx, Query{Text: "rollback script dashboards", Limit: 1, Graph: true, Author: "nobody"})
	if len(res.Hits) != 0 || len(res.Related) != 0 {
		t.Errorf("filtered graph = %+v", res)
	}
}

func TestSearchHandlesWhatHasNothingToMatch(t *testing.T) {
	ctx := context.Background()
	empty := Open(t.TempDir())
	if res := empty.Search(ctx, Query{Text: "anything"}); len(res.Hits) != 0 {
		t.Errorf("empty memory = %+v", res)
	}
	v := corpus(t)
	for _, text := range []string{"", "the of and", "zzzzqqq"} {
		if res := v.Search(ctx, Query{Text: text}); len(res.Hits) != 0 || res.Hits == nil {
			t.Errorf("%q = %+v", text, res)
		}
	}
}

func TestAccentsAndPluralsAreFolded(t *testing.T) {
	v := corpus(t)
	for _, q := range []string{"politica vacaciones", "política de vacación", "cafe"} { // spanish-fixture: accent folding
		if res := v.Search(context.Background(), Query{Text: q}); len(res.Hits) == 0 || res.Hits[0].Path != "Inbox.md" {
			t.Errorf("%q = %+v", q, res.Hits)
		}
	}
}

func TestLimitAndPerEntryCap(t *testing.T) {
	v := Open(t.TempDir())
	var long strings.Builder
	for i := 0; i < 8; i++ {
		long.WriteString("## Part " + string(rune('A'+i)) + "\n\nwidget gadget details here\n\n")
	}
	write(t, v, "Big.md", "# Big\n\n"+long.String())
	for _, name := range []string{"a", "b", "c"} {
		write(t, v, name+".md", "widget gadget in "+name)
	}
	res := v.Search(context.Background(), Query{Text: "widget gadget"})
	big := 0
	for _, h := range res.Hits {
		if h.Path == "Big.md" {
			big++
		}
	}
	if big != perEntry || len(res.Hits) != perEntry+3 {
		t.Errorf("one entry may answer %d times: %d of %d hits", perEntry, big, len(res.Hits))
	}
	if res := v.Search(context.Background(), Query{Text: "widget gadget", Limit: 2}); len(res.Hits) != 2 {
		t.Errorf("limit: %d", len(res.Hits))
	}
	if res := v.Search(context.Background(), Query{Text: "widget gadget", Limit: 1000}); len(res.Hits) > MaxLimit {
		t.Errorf("max limit: %d", len(res.Hits))
	}
}

func TestIndexFollowsTheFiles(t *testing.T) {
	v := Open(t.TempDir())
	write(t, v, "a.md", "alpha beta gamma")
	ctx := context.Background()
	if len(v.Search(ctx, Query{Text: "delta"}).Hits) != 0 {
		t.Fatal("not there yet")
	}
	write(t, v, "b.md", "delta epsilon")
	if got := paths(v.Search(ctx, Query{Text: "delta"}).Hits); got != "b.md" {
		t.Errorf("a new file is found: %q", got)
	}
	if _, err := v.Write("b.md", "something else entirely", false); err != nil {
		t.Fatal(err)
	}
	if got := paths(v.Search(ctx, Query{Text: "delta"}).Hits); got != "" {
		t.Errorf("a changed file is searched as it is now: %q", got)
	}
	first := v.index()
	if first != v.index() {
		t.Error("an unchanged memory keeps its index")
	}
}

type fakeEmbedder struct {
	calls int
	fail  error
	short bool
	text  []string
}

// Embed makes one-hot-ish vectors: dimension 0 for "ocean" texts, 1 for "garden" ones.
func (f *fakeEmbedder) Embed(_ context.Context, texts []string) ([][]float32, error) {
	f.calls++
	f.text = append(f.text, texts...)
	if f.fail != nil {
		return nil, f.fail
	}
	if f.short {
		return nil, nil
	}
	out := make([][]float32, len(texts))
	for i, s := range texts {
		v := []float32{0.01, 0.01}
		if strings.Contains(s, "sea") || strings.Contains(s, "ocean") || strings.Contains(s, "waves") {
			v[0] = 1
		}
		if strings.Contains(s, "garden") {
			v[1] = 1
		}
		out[i] = v
	}
	return out, nil
}

func TestEmbeddingsAddRecallAndAreSentOnce(t *testing.T) {
	v := Open(t.TempDir())
	write(t, v, "Sea.md", "# Sea\n\nThe sea was calm, waves everywhere.")
	write(t, v, "Garden.md", "# Garden\n\nThe garden needs water.")
	f := &fakeEmbedder{}
	e := v.WithEmbedder(fixed(f, "m1"))
	// "ocean" shares no word with Sea.md: only the embeddings connect them
	res := e.Search(context.Background(), Query{Text: "ocean"})
	if len(res.Hits) == 0 || res.Hits[0].Path != "Sea.md" || res.Warning != "" {
		t.Fatalf("res = %+v", res)
	}
	first := f.calls
	e.Search(context.Background(), Query{Text: "ocean"})
	if sent := len(f.text); sent != 2+1+1 {
		t.Errorf("two passages and the query twice, got %d texts", sent)
	}
	if f.calls != first+1 {
		t.Errorf("the second search sends only the query: %d calls after %d", f.calls, first)
	}
	// a rebuilt index keeps the vectors already paid for; another model starts over
	write(t, v, "Garden.md", "# Garden\n\nThe garden needs water.\nAnd compost.")
	f.text = nil
	e.Search(context.Background(), Query{Text: "ocean"})
	if len(f.text) != 2 {
		t.Errorf("only the changed passage and the query: %v", f.text)
	}
	f.text = nil
	v.WithEmbedder(fixed(f, "m2")).Search(context.Background(), Query{Text: "ocean"})
	if len(f.text) != 3 {
		t.Errorf("a new model re-embeds everything: %v", f.text)
	}
}

func TestEmbeddingFailuresDegradeToWordsAndVectors(t *testing.T) {
	v := Open(t.TempDir())
	write(t, v, "Sea.md", "# Sea\n\nThe sea was calm.")
	for name, f := range map[string]*fakeEmbedder{
		"error": {fail: errors.New("offline")}, "short": {short: true},
	} {
		res := v.WithEmbedder(fixed(f, name)).Search(context.Background(), Query{Text: "calm sea"})
		if res.Warning == "" || len(res.Hits) != 1 {
			t.Errorf("%s: %+v", name, res)
		}
	}
	// the query is the call that fails
	f := &queryFails{}
	v.WithEmbedder(fixed(f, "q")).Search(context.Background(), Query{Text: "calm sea"}) // fills the cache
	for _, mode := range []int{1, 2} {
		f.mode = mode
		if res := v.WithEmbedder(fixed(f, "q")).Search(context.Background(), Query{Text: "calm sea"}); res.Warning == "" {
			t.Errorf("mode %d: %+v", mode, res)
		}
	}
}

// queryFails embeds the passages and then fails (mode 1) or answers nothing (mode 2) for the query.
type queryFails struct{ mode int }

func (q *queryFails) Embed(_ context.Context, texts []string) ([][]float32, error) {
	if len(texts) == 1 && !strings.Contains(texts[0], "\n") {
		switch q.mode {
		case 1:
			return nil, errors.New("down")
		case 2:
			return nil, nil
		default:
		}
	}
	out := make([][]float32, len(texts))
	for i := range out {
		out[i] = []float32{1, 0}
	}
	return out, nil
}

func TestEmbeddingCapAndMismatchedVectors(t *testing.T) {
	old := maxEmbedded
	defer func() { maxEmbedded = old }()
	maxEmbedded = 1
	v := Open(t.TempDir())
	write(t, v, "a.md", "# A\n\nsea one")
	write(t, v, "b.md", "# B\n\nsea two")
	f := &fakeEmbedder{}
	v.WithEmbedder(fixed(f, "cap")).Search(context.Background(), Query{Text: "sea"})
	if len(f.text) != 2 { // one passage under the cap, plus the query
		t.Errorf("sent %v", f.text)
	}
	if cosine(vec{1}, vec{1, 0}) != 0 || cosine(vec{1}, vec{1}) != 1 {
		t.Error("cosine")
	}
	if got := unitOf([]float32{0, 0}); got[0] != 0 {
		t.Errorf("zero = %v", got)
	}
}

func TestNamespacesListTheCollections(t *testing.T) {
	v := corpus(t)
	got := v.Namespaces()
	if len(got) != 3 {
		t.Fatalf("namespaces = %+v", got)
	}
	if got[0].Name != "" || got[1].Name != "Engineering" || got[2].Name != "HR" || got[1].Entries != 2 {
		t.Errorf("namespaces = %+v", got)
	}
	if got[2].Modified.IsZero() || got[2].Modified.After(time.Now().Add(time.Minute)) {
		t.Errorf("modified = %v", got[2].Modified)
	}
	if len(Open(t.TempDir()).Namespaces()) != 0 {
		t.Error("an empty memory has none")
	}
}

// letters is a short distinct name for the i-th generated entry or section.
func letters(i int) string {
	return string(rune('a'+i%26)) + string(rune('a'+i/26))
}

func TestGroupingUsesTheBestChildAndThePool(t *testing.T) {
	v := Open(t.TempDir())
	var b strings.Builder
	for i := 0; i < pool+20; i++ {
		b.WriteString("## S" + letters(i) + "\n\nshared token alpha " + strings.Repeat("filler ", i%7) + "\n\n")
	}
	write(t, v, "Many.md", b.String())
	for i := 0; i < 30; i++ {
		write(t, v, "e"+letters(i)+".md", "shared token alpha beta"+strings.Repeat(" more", i))
	}
	res := v.Search(context.Background(), Query{Text: "shared token alpha beta", Limit: MaxLimit})
	if len(res.Hits) == 0 || len(res.Hits) > MaxLimit {
		t.Fatalf("hits = %d", len(res.Hits))
	}
	// two passages of one parent: the parent answers once, from its best child
	w := Open(t.TempDir())
	write(t, w, "Two.md", "# Two\n\n"+strings.Repeat("alpha beta gamma ", 20)+"\n\nalpha beta gamma delta epsilon\n")
	if r := w.Search(context.Background(), Query{Text: "alpha beta gamma delta epsilon"}); len(r.Hits) != 1 {
		t.Errorf("one parent, one answer: %+v", r.Hits)
	}
}

func TestSnippetsAreClipped(t *testing.T) {
	v := Open(t.TempDir())
	write(t, v, "Long.md", "# Long\n\n"+strings.Repeat("needle ", 100))
	res := v.Search(context.Background(), Query{Text: "needle"})
	if len(res.Hits) != 1 {
		t.Fatalf("hits = %+v", res.Hits)
	}
	snippet := res.Hits[0].Snippet
	if !strings.HasSuffix(snippet, "…") || len([]rune(snippet)) > snippetRunes+1 {
		t.Errorf("snippet = %q", snippet)
	}
}

func TestRepeatedQueryWordsCountOnce(t *testing.T) {
	v := corpus(t)
	a := v.Search(context.Background(), Query{Text: "rollback"})
	b := v.Search(context.Background(), Query{Text: "rollback rollback rollback"})
	if len(a.Hits) == 0 || paths(a.Hits) != paths(b.Hits) {
		t.Errorf("%q vs %q", paths(a.Hits), paths(b.Hits))
	}
}

func TestLinksResolveByPathAndAmbiguousNames(t *testing.T) {
	v := Open(t.TempDir())
	write(t, v, "A/Plan.md", "Plan A about widgets. See [[B/Plan]] and [[Plan]] and [[ghost/none]].")
	write(t, v, "B/Plan.md", "Plan B about gadgets.")
	write(t, v, "Empty.md", "")
	write(t, v, "Hub.md", "Hub with [[Empty]] and [[A/Plan]]. widgets everywhere")
	res := v.Search(context.Background(), Query{Text: "widgets hub", Graph: true, Limit: 1})
	related := paths(res.Related)
	if !strings.Contains(related, "B/Plan.md") && !strings.Contains(related, "A/Plan.md") {
		t.Errorf("linked by path: %+v", res)
	}
	for _, h := range res.Related {
		if h.Path == "Empty.md" {
			t.Errorf("an entry with no text has nothing to answer with: %+v", h)
		}
	}
}

func TestUnreadableEntriesAndTheChildCapDoNotBreakTheIndex(t *testing.T) {
	v := Open(t.TempDir())
	write(t, v, "ok.md", "alpha content")
	writeRaw(v, "huge.md", make([]byte, MaxEntryBytes+1))
	if got := paths(v.Search(context.Background(), Query{Text: "alpha"}).Hits); got != "ok.md" {
		t.Errorf("hits = %q", got)
	}
	old := maxChildren
	defer func() { maxChildren = old }()
	maxChildren = 1
	w := Open(t.TempDir())
	write(t, w, "a.md", "alpha")
	write(t, w, "b.md", "alpha")
	if got := paths(w.Search(context.Background(), Query{Text: "alpha"}).Hits); got != "a.md" {
		t.Errorf("past the cap an entry is listed but not searched: %q", got)
	}
}

func TestGraphRanksNeighboursByHowManyBestAnswersTouchThem(t *testing.T) {
	v := Open(t.TempDir())
	write(t, v, "S1.md", "---\nauthor: Ana\n---\nzebra stripes. Links: [[N1]] [[N2]] [[N3]] [[N4]] [[N5]]")
	write(t, v, "S2.md", "---\nauthor: Ana\n---\nzebra stripes too. Links: [[N1]]")
	for _, n := range []string{"N1", "N2", "N3", "N4", "N5"} {
		write(t, v, n+".md", "---\nauthor: Luis\n---\nneighbour "+n)
	}
	res := v.Search(context.Background(), Query{Text: "zebra stripes", Graph: true, Limit: 2})
	if len(res.Related) != graphExtra || res.Related[0].Path != "N1.md" {
		t.Fatalf("related = %v", paths(res.Related))
	}
	// neighbours that fail the filters are not related either
	res = v.Search(context.Background(), Query{Text: "zebra stripes", Graph: true, Author: "Ana", Limit: 2})
	if len(res.Hits) != 2 || len(res.Related) != 0 {
		t.Errorf("filtered = %v / %v", paths(res.Hits), paths(res.Related))
	}
}

func TestVeryLongPassagesKeepTheirRarestWords(t *testing.T) {
	v := Open(t.TempDir())
	var words []string
	for i := 0; i < 3*maxUnique; i++ {
		words = append(words, "w"+string(rune('a'+i%26))+string(rune('a'+i/26)))
	}
	write(t, v, "long.md", strings.Join(words, " "))
	if got := paths(v.Search(context.Background(), Query{Text: words[0] + " " + words[1]}).Hits); got != "long.md" {
		t.Errorf("hits = %q", got)
	}
}

// fixed is a Source that always answers the same embedder.
func fixed(e Embedder, id string) Source {
	return func(context.Context) (Embedder, string) { return e, id }
}

func TestASourceWithNothingToOfferIsSkipped(t *testing.T) {
	v := Open(t.TempDir())
	write(t, v, "Sea.md", "# Sea\n\nThe sea was calm.")
	none := v.WithEmbedder(func(context.Context) (Embedder, string) { return nil, "" })
	if res := none.Search(context.Background(), Query{Text: "calm"}); len(res.Hits) != 1 || res.Warning != "" {
		t.Errorf("res = %+v", res)
	}
}
