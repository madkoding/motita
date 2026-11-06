package memory

import (
	"fmt"
	"hash/fnv"
	"math"
	"strings"
	"sync"
)

// The index is derived from the files: it is built the first time the memory is searched and
// brought up to date whenever a file was added, removed or changed, so a person who edits an entry
// in Obsidian is searched the next second. A copy is kept outside the memory's folder (see
// persist.go), so a restart does not have to build it again.

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
	docs    []*doc  // what each entry contributed, by entry
	at      []kidRef
	stale   int // children encoded in a space learned without them
}

var (
	cacheMu sync.Mutex
	cache   = map[string]*index{}
)

// Forget drops the index kept in memory for the folder root. A folder that is deleted (a
// conversation's archive) would otherwise keep its vectors until the process ends.
func Forget(root string) {
	cacheMu.Lock()
	defer cacheMu.Unlock()
	delete(cache, root)
}

// Indexed says whether an index is kept in memory for the folder root.
func Indexed(root string) bool {
	cacheMu.Lock()
	defer cacheMu.Unlock()
	return cache[root] != nil
}

// A rebuild after a change re-reads only the entries that changed: the others keep what was cut
// and encoded from them. The vector space is the expensive part (learning it takes seconds on a
// large memory), so a large memory keeps the space it learned and only encodes the new passages
// in it, until the passages encoded that way reach a share of the whole; then it learns again.
// A small memory always learns again, it costs nothing.
var (
	// incrementalMin is how many passages a memory has before it keeps its vector space.
	incrementalMin = 2000
	// staleShare is the share of passages encoded in a kept space that makes it learn again.
	staleShare = 0.1
)

// doc is what one entry contributes to the index, kept between rebuilds while the file does not
// change.
type doc struct {
	entry   indexed
	parents []parent // entry is 0 here; the index sets it
	kids    []child  // parent counts from the entry's first parent
	links   []string // the targets of its [[links]]
	text    [][]string
	ctx     [][]string
	vecs    []vec // one per child, nil until it is encoded
}

// same says whether d was made from the file e describes now.
func (d *doc) same(e Entry) bool {
	return d.entry.Path == e.Path && d.entry.Size == e.Size && d.entry.Modified.Equal(e.Modified)
}

// fingerprint says whether the files changed.
func fingerprint(all []Entry) string {
	h := fnv.New64a()
	for _, e := range all {
		fmt.Fprintf(h, "%s|%d|%d\n", e.Path, e.Size, e.Modified.UnixNano())
	}
	return fmt.Sprintf("%d:%x", len(all), h.Sum64())
}

// index returns the index of the memory, built or brought up to date when the files changed. The
// first time a memory is searched in a run it starts from the index kept on disk, if any.
func (s *Store) index() *index {
	all := s.List()
	fp := fingerprint(all)
	cacheMu.Lock()
	defer cacheMu.Unlock()
	old := cache[s.root]
	if old == nil {
		old = s.load()
	}
	if old != nil && old.print == fp {
		cache[s.root] = old
		return old
	}
	ix := s.build(all, fp, old)
	cache[s.root] = ix
	s.save(ix)
	return ix
}

// build makes the index of all, reusing what old already holds of the entries that did not change.
func (s *Store) build(all []Entry, fp string, old *index) *index {
	kept := map[string]*doc{}
	if old != nil {
		for _, d := range old.docs {
			kept[d.entry.Path] = d
		}
	}
	docs := make([]*doc, len(all))
	for i, e := range all {
		if d := kept[e.Path]; d != nil && d.same(e) {
			docs[i] = d.fork()
			continue
		}
		docs[i] = s.parse(e)
	}
	ix := assemble(docs, fp)
	ix.encode(old)
	return ix
}

// fork is a copy of d whose vectors can be replaced without touching the index d came from.
func (d *doc) fork() *doc {
	c := *d
	c.vecs = append([]vec(nil), d.vecs...)
	return &c
}

// parse reads an entry and cuts it.
func (s *Store) parse(e Entry) *doc {
	body, _, err := s.Read(e.Path)
	if err != nil {
		body = ""
	}
	meta, rest, skipped := ParseMeta(body, e.Modified)
	d := &doc{entry: indexed{Entry: e, Meta: meta, namespace: Namespace(e.Path)}}
	d.parents, d.kids = cut(0, e.Title, rest, skipped)
	for _, l := range ParseLinks(body) {
		d.links = append(d.links, l.Target)
	}
	d.text = make([][]string, len(d.kids))
	d.ctx = make([][]string, len(d.kids))
	for i, c := range d.kids {
		d.text[i], d.ctx[i] = terms(c.text), terms(c.ctx)
	}
	d.vecs = make([]vec, len(d.kids))
	return d
}

// assemble lays the docs out as one index: entries, parents and children numbered across the whole
// memory, the word postings and the link graph. The vectors are encode's business.
func assemble(docs []*doc, fp string) *index {
	ix := &index{print: fp, post: map[string][]posting{}, docs: docs}
	all := make([]Entry, len(docs))
	for i, d := range docs {
		all[i] = d.entry.Entry
		ix.entries = append(ix.entries, d.entry)
		if len(ix.kids) >= maxChildren {
			continue
		}
		base := len(ix.parents)
		for _, p := range d.parents {
			p.entry = i
			ix.parents = append(ix.parents, p)
		}
		for j, c := range d.kids {
			c.entry, c.parent = i, c.parent+base
			ix.kids = append(ix.kids, c)
			ix.at = append(ix.at, kidRef{i, j})
		}
	}
	ix.links(all, docs)
	ix.words()
	return ix
}

// kidRef is where a child of the index comes from: its doc and its place there.
type kidRef struct{ doc, kid int }

// words indexes the words of every child for the word search.
func (ix *index) words() {
	ix.kidLen = make([]int, len(ix.kids))
	var total int
	for i, r := range ix.at {
		d := ix.docs[r.doc]
		ctx, text := d.ctx[r.kid], d.text[r.kid]
		ix.kidLen[i] = len(ctx) + len(text)
		total += ix.kidLen[i]
		tf := map[string]int{}
		for _, w := range ctx {
			tf[w]++
		}
		for _, w := range text {
			tf[w]++
		}
		for w, n := range tf {
			ix.post[w] = append(ix.post[w], posting{i, n})
		}
	}
	if len(ix.kids) > 0 {
		ix.avgLen = float64(total) / float64(len(ix.kids))
	}
}

// encode gives every child its vector. A memory that is small, has no earlier space, or whose
// space has encoded too much since it was learned, learns its space again; otherwise the space of
// old is kept and only the children without a vector are encoded in it.
func (ix *index) encode(old *index) {
	missing := 0
	for _, r := range ix.at {
		if ix.docs[r.doc].vecs[r.kid] == nil {
			missing++
		}
	}
	n := len(ix.kids)
	relearn := old == nil || old.holo == nil || n < incrementalMin ||
		float64(old.stale+missing) > staleShare*float64(n)
	if relearn {
		text := make([][]string, n)
		ctx := make([][]string, n)
		for i, r := range ix.at {
			d := ix.docs[r.doc]
			text[i], ctx[i] = d.text[r.kid], d.ctx[r.kid]
		}
		ix.holo = buildHolo(text, ctx)
		for i, r := range ix.at {
			ix.docs[r.doc].vecs[r.kid] = ix.holo.vecs[i]
		}
		return
	}
	h := &holo{idf: old.holo.idf, index: old.holo.index, rep: old.holo.rep, learned: old.holo.learned,
		vecs: make([]vec, n)}
	for i, r := range ix.at {
		d := ix.docs[r.doc]
		if d.vecs[r.kid] == nil {
			d.vecs[r.kid] = h.passage(d.text[r.kid], d.ctx[r.kid])
		}
		h.vecs[i] = d.vecs[r.kid]
	}
	ix.holo = h
	ix.stale = old.stale + missing
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
func (ix *index) links(all []Entry, docs []*doc) {
	ix.out = make([][]int, len(all))
	ix.in = make([][]int, len(all))
	at := map[string]int{}
	byName := map[string][]int{}
	for i, e := range all {
		at[e.Path] = i
		name := strings.ToLower(Title(e.Path))
		byName[name] = append(byName[name], i)
	}
	for i, d := range docs {
		seen := map[int]bool{}
		for _, target := range d.links {
			to, ok := resolveFast(all, byName, at, target)
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
