package memory

import (
	"fmt"
	"hash/fnv"
	"math"
	"strings"
	"sync"
)

// The index is derived from the files and never stored: it is built the first time the memory is
// searched and again whenever a file was added, removed or changed. A person who edits an entry in
// Obsidian is searched the next second.

// maxChildren bounds the index so a huge memory cannot take the machine's RAM.
var maxChildren = 20000

// indexed is an entry as the index knows it.
type indexed struct {
	Entry
	Meta
	namespace string
}

// posting is a word's count in a child.
type posting struct {
	child int
	tf    int
}

// index is the searchable form of a whole memory.
type index struct {
	print   string
	entries []indexed
	parents []parent
	kids    []child
	post    map[string][]posting
	kidLen  []int
	avgLen  float64
	holo    *holo
	out     [][]int // the entries each entry links to
	in      [][]int // the entries that link to each entry
}

var (
	cacheMu sync.Mutex
	cache   = map[string]*index{}
)

// fingerprint says whether the files changed.
func fingerprint(all []Entry) string {
	h := fnv.New64a()
	for _, e := range all {
		fmt.Fprintf(h, "%s|%d|%d\n", e.Path, e.Size, e.Modified.UnixNano())
	}
	return fmt.Sprintf("%d:%x", len(all), h.Sum64())
}

// index returns the index of the memory, built or rebuilt when the files changed.
func (s *Store) index() *index {
	all := s.List()
	fp := fingerprint(all)
	cacheMu.Lock()
	defer cacheMu.Unlock()
	old := cache[s.root]
	if old != nil && old.print == fp {
		return old
	}
	ix := s.build(all, fp)
	cache[s.root] = ix
	return ix
}

// build reads every entry and cuts, indexes and encodes it.
func (s *Store) build(all []Entry, fp string) *index {
	ix := &index{print: fp, post: map[string][]posting{}}
	bodies := make([]string, len(all))
	for i, e := range all {
		body, _, err := s.Read(e.Path)
		if err != nil {
			body = ""
		}
		bodies[i] = body
		meta, rest, skipped := ParseMeta(body, e.Modified)
		ix.entries = append(ix.entries, indexed{Entry: e, Meta: meta, namespace: Namespace(e.Path)})
		if len(ix.kids) >= maxChildren {
			continue
		}
		ps, cs := cut(i, e.Title, rest, skipped)
		base := len(ix.parents)
		for j := range cs {
			cs[j].parent += base
		}
		ix.parents = append(ix.parents, ps...)
		ix.kids = append(ix.kids, cs...)
	}
	ix.links(all, bodies)
	ix.words()
	return ix
}

// words indexes the words of every child: the postings for the word search and the vectors.
func (ix *index) words() {
	text := make([][]string, len(ix.kids))
	ctx := make([][]string, len(ix.kids))
	ix.kidLen = make([]int, len(ix.kids))
	var total int
	for i, c := range ix.kids {
		text[i], ctx[i] = terms(c.text), terms(c.ctx)
		all := append(append([]string{}, ctx[i]...), text[i]...)
		ix.kidLen[i] = len(all)
		total += len(all)
		tf := map[string]int{}
		for _, w := range all {
			tf[w]++
		}
		for w, n := range tf {
			ix.post[w] = append(ix.post[w], posting{i, n})
		}
	}
	if len(ix.kids) > 0 {
		ix.avgLen = float64(total) / float64(len(ix.kids))
	}
	ix.holo = buildHolo(text, ctx)
}

// bm25 ranks the children allowed by keep against the words of a query by BM25, best first.
func (ix *index) bm25(words []string, keep func(int) bool) []scored {
	const k1, b = 1.2, 0.75
	n := float64(len(ix.kids))
	score := map[int]float64{}
	seen := map[string]bool{}
	for _, w := range words {
		if seen[w] {
			continue
		}
		seen[w] = true
		posts := ix.post[w]
		idf := math.Log(1 + (n-float64(len(posts))+0.5)/(float64(len(posts))+0.5))
		for _, p := range posts {
			if !keep(p.child) {
				continue
			}
			tf := float64(p.tf)
			norm := 1 - b + b*float64(ix.kidLen[p.child])/ix.avgLen
			score[p.child] += idf * tf * (k1 + 1) / (tf + k1*norm)
		}
	}
	return rank(score)
}

// links resolves the [[links]] of every entry into the graph the related search walks.
func (ix *index) links(all []Entry, bodies []string) {
	ix.out = make([][]int, len(all))
	ix.in = make([][]int, len(all))
	at := map[string]int{}
	byName := map[string][]int{}
	for i, e := range all {
		at[e.Path] = i
		name := strings.ToLower(Title(e.Path))
		byName[name] = append(byName[name], i)
	}
	for i, body := range bodies {
		seen := map[int]bool{}
		for _, l := range ParseLinks(body) {
			to, ok := resolveFast(all, byName, at, l.Target)
			if !ok || to == i || seen[to] {
				continue
			}
			seen[to] = true
			ix.out[i] = append(ix.out[i], to)
			ix.in[to] = append(ix.in[to], i)
		}
	}
}

// resolveFast is Resolve with the bare names looked up in a map: a memory of thousands of entries
// is not walked once per link.
func resolveFast(all []Entry, byName map[string][]int, at map[string]int, target string) (int, bool) {
	t := strings.ToLower(strings.Trim(strings.ReplaceAll(target, "\\", "/"), "/"))
	t = strings.TrimSuffix(t, ext)
	if t != "" && !strings.Contains(t, "/") {
		hits := byName[t]
		if len(hits) == 1 {
			return hits[0], true
		}
	}
	p, ok := Resolve(all, target)
	if !ok {
		return 0, false
	}
	return at[p], true
}
