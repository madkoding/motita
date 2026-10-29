package memory

import (
	"context"
	"sort"
	"strings"
	"time"
)

// Recall: a query is answered in three steps.
//
//  1. SEARCH THE SMALL: the children (passages of a paragraph or two, indexed with their title and
//     heading trail) are ranked by words (BM25) and by the holographic vector. The rankings are
//     fused by reciprocal rank.
//  2. ANSWER WITH THE LARGE: the best children are grouped by the parent section they belong to
//     and the parent is what comes back, with the passage that matched marked.
//  3. FOLLOW THE LINKS (optional, Graph): the entries linked from or to the best ones are added as
//     related answers, each saying through which entry it was reached. It is a light GraphRAG over
//     the [[links]] of the memory: the entities are the entries, the relations are the links.
const (
	DefaultLimit = 8
	MaxLimit     = 30
	rrfK         = 60
	pool         = 60 // children that go on to grouping
	perEntry     = 2  // parents of one entry in an answer
	graphSeeds   = 3
	graphExtra   = 4
	snippetRunes = 220
)

// Query is a search and what narrows it.
type Query struct {
	Text      string
	Namespace string // only this collection, matched without regard to case
	Author    string // only entries by this author
	Since     time.Time
	Until     time.Time
	Graph     bool // add the entries related through links
	Limit     int
}

// Hit is a parent section that answers a query.
type Hit struct {
	Path      string    `json:"path"`
	Title     string    `json:"title"`
	Namespace string    `json:"namespace"`
	Heading   string    `json:"heading,omitempty"`
	Line      int       `json:"line"`
	Snippet   string    `json:"snippet"`
	Block     string    `json:"block"`
	Author    string    `json:"author,omitempty"`
	Date      time.Time `json:"date"`
	Score     float64   `json:"score"`
	Via       string    `json:"via,omitempty"`
}

// Edge is a link between two entries of an answer.
type Edge struct {
	From string `json:"from"`
	To   string `json:"to"`
}

// Result is what Search answers.
type Result struct {
	Hits    []Hit  `json:"hits"`
	Related []Hit  `json:"related,omitempty"`
	Edges   []Edge `json:"edges,omitempty"`
}

// ParseQuery reads a query with filters written in it: ns:Name (or ns:"Two words"), author:Name,
// since:2026-01-31, until:2026-12-31 and graph:1. What is left is the text. A filter that cannot be
// read stays in the text, so nothing the person typed is dropped.
func ParseQuery(raw string) Query {
	var q Query
	var rest []string
	for _, tok := range splitQuery(raw) {
		key, val, ok := strings.Cut(tok, ":")
		val = strings.Trim(val, "\"")
		switch k := strings.ToLower(key); {
		case ok && k == "ns" && val != "":
			q.Namespace = val
		case ok && k == "author" && val != "":
			q.Author = val
		case ok && (k == "since" || k == "until") && isDate(val):
			q.setDate(k, val)
		case ok && k == "graph" && (val == "1" || val == "true"):
			q.Graph = true
		default:
			rest = append(rest, strings.Trim(tok, "\""))
		}
	}
	q.Text = strings.Join(rest, " ")
	return q
}

// setDate sets the since or until bound (key) from a date; until takes the whole of its day.
func (q *Query) setDate(key, val string) {
	t, _ := ParseDate(val)
	if key == "since" {
		q.Since = t
		return
	}
	q.Until = t.Add(24*time.Hour - time.Nanosecond)
}

func isDate(s string) bool {
	_, ok := ParseDate(s)
	return ok
}

// splitQuery splits on spaces but keeps a quoted value, as in ns:"Two words", in one token.
func splitQuery(s string) []string {
	var out []string
	var cur strings.Builder
	quoted := false
	for _, r := range s {
		switch {
		case r == '"':
			quoted = !quoted
			cur.WriteRune(r)
		case r == ' ' && !quoted:
			if cur.Len() > 0 {
				out = append(out, cur.String())
				cur.Reset()
			}
		default:
			cur.WriteRune(r)
		}
	}
	if cur.Len() > 0 {
		out = append(out, cur.String())
	}
	return out
}

// rank sorts scores best first, ties by position.
func rank(score map[int]float64) []scored {
	out := make([]scored, 0, len(score))
	for at, s := range score {
		out = append(out, scored{at, s})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].score != out[j].score {
			return out[i].score > out[j].score
		}
		return out[i].at < out[j].at
	})
	return out
}

// allows says whether an entry passes the filters of q.
func (q Query) allows(e indexed) bool {
	switch {
	case q.Namespace != "" && !strings.EqualFold(e.namespace, q.Namespace):
		return false
	case q.Author != "" && !strings.EqualFold(e.Author, q.Author):
		return false
	case !q.Since.IsZero() && e.Date.Before(q.Since):
		return false
	case !q.Until.IsZero() && e.Date.After(q.Until):
		return false
	default:
		return true
	}
}

// Search answers q.
func (s *Store) Search(ctx context.Context, q Query) Result {
	res := Result{Hits: []Hit{}}
	words := terms(q.Text)
	if len(words) == 0 {
		return res
	}
	ix := s.index()
	if len(ix.kids) == 0 {
		return res
	}
	keep := func(i int) bool { return q.allows(ix.entries[ix.kids[i].entry]) }

	fused := map[int]float64{}
	fuse := func(list []scored) {
		for r, c := range list {
			fused[c.at] += 1 / float64(rrfK+r+1)
		}
	}
	fuse(ix.bm25(words, keep))
	if cue, ok := ix.holo.cue(words); ok {
		fuse(ix.holo.recall(cue, keep))
	}

	limit := q.Limit
	if limit <= 0 {
		limit = DefaultLimit
	}
	limit = min(limit, MaxLimit)
	res.Hits = ix.group(rank(fused), limit)
	if q.Graph {
		res.Related, res.Edges = ix.related(res.Hits, fused, q)
	}
	return res
}

// group turns the best children into the parents they belong to, best first, at most perEntry from
// one entry.
func (ix *index) group(best []scored, limit int) []Hit {
	if len(best) > pool {
		best = best[:pool]
	}
	type acc struct {
		score float64
		child int // the best child of the parent: the list is best first
	}
	byParent := map[int]*acc{}
	var order []int
	for _, c := range best {
		p := ix.kids[c.at].parent
		a := byParent[p]
		if a == nil {
			a = &acc{child: c.at}
			byParent[p] = a
			order = append(order, p)
		}
		a.score += c.score
	}
	sort.SliceStable(order, func(i, j int) bool {
		a, b := byParent[order[i]], byParent[order[j]]
		if a.score != b.score {
			return a.score > b.score
		}
		return order[i] < order[j]
	})
	out := []Hit{}
	count := map[int]int{}
	for _, p := range order {
		par := ix.parents[p]
		if count[par.entry] >= perEntry {
			continue
		}
		count[par.entry]++
		out = append(out, ix.hit(p, byParent[p].child, byParent[p].score))
		if len(out) == limit {
			break
		}
	}
	return out
}

// hit is the answer for a parent, the child that matched marked as its snippet.
func (ix *index) hit(p, kid int, score float64) Hit {
	par := ix.parents[p]
	e := ix.entries[par.entry]
	h := Hit{
		Path: e.Path, Title: e.Title, Namespace: e.namespace, Heading: headingOf(e.Title, par.trail),
		Line: ix.kids[kid].line, Block: par.text, Author: e.Author, Date: e.Date, Score: score,
	}
	h.Snippet = clip(strings.Join(strings.Fields(ix.kids[kid].text), " "), snippetRunes)
	return h
}

// headingOf is the trail of a section as shown, without the heading that only repeats the title.
func headingOf(title string, trail []string) string {
	var parts []string
	for _, h := range trail {
		if !strings.EqualFold(h, title) {
			parts = append(parts, h)
		}
	}
	return strings.Join(parts, trailSep)
}

// clip shortens s to n runes.
func clip(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n]) + "…"
	}
	return s
}

// related walks one step of the link graph from the best answers: the entries they link to or are
// linked from, the ones touching most seeds first. Each comes with its best parent for the query
// (or its first) and the entry it was reached through. The edges are the links among everything
// returned.
func (ix *index) related(hits []Hit, fused map[int]float64, q Query) ([]Hit, []Edge) {
	at := map[string]int{}
	for i, e := range ix.entries {
		at[e.Path] = i
	}
	seeds, seen := seedsOf(hits, at)
	touch, via := ix.neighbours(seeds, seen, q)
	var related []Hit
	for _, n := range mostTouched(touch) {
		p, kid := ix.bestParent(n, fused)
		if p < 0 {
			continue
		}
		h := ix.hit(p, kid, fused[kid])
		h.Via = "[[" + ix.entries[via[n]].Title + "]]"
		related = append(related, h)
	}
	return related, ix.edgesAmong(hits, related, at)
}

// seedsOf are the first entries the hits belong to (at most graphSeeds), and the set of them.
func seedsOf(hits []Hit, at map[string]int) ([]int, map[int]bool) {
	var seeds []int
	seen := map[int]bool{}
	for _, h := range hits {
		if i := at[h.Path]; !seen[i] && len(seeds) < graphSeeds {
			seen[i] = true
			seeds = append(seeds, i)
		}
	}
	return seeds, seen
}

// neighbours counts, for each entry linked to or from a seed, how many seeds touch it, and names
// the first seed that did.
func (ix *index) neighbours(seeds []int, seen map[int]bool, q Query) (touch, via map[int]int) {
	touch, via = map[int]int{}, map[int]int{}
	for _, sd := range seeds {
		for _, n := range append(append([]int{}, ix.out[sd]...), ix.in[sd]...) {
			if seen[n] || !q.allows(ix.entries[n]) {
				continue
			}
			if touch[n] == 0 {
				via[n] = sd
			}
			touch[n]++
		}
	}
	return touch, via
}

// mostTouched orders the entries touching most seeds first, at most graphExtra of them.
func mostTouched(touch map[int]int) []int {
	cands := make([]int, 0, len(touch))
	for n := range touch {
		cands = append(cands, n)
	}
	sort.Slice(cands, func(i, j int) bool {
		if touch[cands[i]] != touch[cands[j]] {
			return touch[cands[i]] > touch[cands[j]]
		}
		return cands[i] < cands[j]
	})
	if len(cands) > graphExtra {
		cands = cands[:graphExtra]
	}
	return cands
}

// bestParent is the parent of entry n whose child scored best, or its first parent.
func (ix *index) bestParent(n int, fused map[int]float64) (parent, kid int) {
	parent, kid = -1, -1
	best := -1.0
	for i, c := range ix.kids {
		if c.entry != n {
			continue
		}
		if s := fused[i]; parent < 0 || s > best {
			parent, kid, best = c.parent, i, s
		}
	}
	return parent, kid
}

// edgesAmong lists the links between the entries of an answer.
func (ix *index) edgesAmong(hits, related []Hit, at map[string]int) []Edge {
	in := map[int]bool{}
	for _, h := range append(append([]Hit{}, hits...), related...) {
		in[at[h.Path]] = true
	}
	var nodes []int
	for n := range in {
		nodes = append(nodes, n)
	}
	sort.Ints(nodes)
	var out []Edge
	for _, from := range nodes {
		for _, to := range ix.out[from] {
			if in[to] {
				out = append(out, Edge{From: ix.entries[from].Path, To: ix.entries[to].Path})
			}
		}
	}
	return out
}

// NamespaceInfo is a collection of the memory and how many entries it holds.
type NamespaceInfo struct {
	Name     string    `json:"name"`
	Entries  int       `json:"entries"`
	Modified time.Time `json:"modified"`
}

// Namespaces lists the collections of the memory, the one at its top ("") first.
func (s *Store) Namespaces() []NamespaceInfo {
	by := map[string]*NamespaceInfo{}
	for _, e := range s.List() {
		ns := Namespace(e.Path)
		n := by[ns]
		if n == nil {
			n = &NamespaceInfo{Name: ns}
			by[ns] = n
		}
		n.Entries++
		if e.Modified.After(n.Modified) {
			n.Modified = e.Modified
		}
	}
	out := make([]NamespaceInfo, 0, len(by))
	for _, n := range by {
		out = append(out, *n)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}
