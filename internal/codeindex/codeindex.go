// Package codeindex is the index behind the search_index tool: it reads a folder once, splits each
// text file into overlapping windows of lines, and ranks the windows against a question in plain
// words (BM25, with definitions and file names weighted up). The answer is a few lines with a
// path:line, where grep would bring every match whole.
//
// Everything stays in this process: the index is in memory, rebuilt when a file changes, and
// nothing is sent anywhere. The approach follows XERJ (https://github.com/xerj-org/xerj), cut down
// to what a coding agent needs from one workspace.
package codeindex

import (
	"context"
	"errors"
	"hash/fnv"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"unicode"
)

// Defaults and limits of the index.
const (
	DefaultMaxFileKB = 256
	DefaultMaxFiles  = 5000
	DefaultLimit     = 5
	MaxLimit         = 20

	windowLines   = 40
	windowStep    = 30
	snippetLines  = 12
	snippetRunes  = 160
	defWeight     = 3
	pathWeight    = 2
	fuseK         = 60  // reciprocal rank fusion constant
	semanticShare = 0.5 // weight of the semantic ranking against BM25
	bm25K1        = 1.2
	bm25B         = 0.75
	binarySniffer = 512
)

// Options bound what is indexed. Zero values take the defaults.
type Options struct {
	MaxFileKB int
	MaxFiles  int
	// CacheDir is where the index is kept between runs. Empty keeps it in memory only.
	CacheDir string
	// Semantic adds a second ranking by meaning learned from the folder itself, fused with BM25.
	Semantic bool
	// EmbedURL, EmbedModel and EmbedKey name an OpenAI-compatible embeddings endpoint. When set, the
	// text of every indexed window is sent there once and its vectors replace the learned ones.
	EmbedURL, EmbedModel, EmbedKey string
}

func (o Options) maxBytes() int64 {
	if o.MaxFileKB <= 0 {
		o.MaxFileKB = DefaultMaxFileKB
	}
	return int64(o.MaxFileKB) * 1024
}

func (o Options) maxFiles() int {
	if o.MaxFiles <= 0 {
		return DefaultMaxFiles
	}
	return o.MaxFiles
}

// Hit is one matching passage.
type Hit struct {
	Path    string // relative to the folder searched, with forward slashes
	Line    int    // 1-based line the passage starts at
	Snippet string
	Score   float64
}

// skipDirs are never entered: dependencies and build output are noise for a question about the code.
var skipDirs = map[string]bool{
	"node_modules": true, "vendor": true, "dist": true, "build": true, "target": true,
	"__pycache__": true, "venv": true, "coverage": true,
}

// defRe finds the line that defines a name, in the languages an agent meets most.
var defRe = regexp.MustCompile(
	`^\s*(?:export\s+|public\s+|private\s+|protected\s+|static\s+|async\s+|pub\s+|abstract\s+|final\s+)*` +
		`(?:func|def|class|interface|struct|enum|trait|impl|fn|function|type|const|var|let|module|object)\s+` +
		`(?:\([^)]*\)\s*)?([A-Za-z_][A-Za-z0-9_]*)`)

// stop are words that say nothing about code in a question.
var stop = map[string]bool{
	"the": true, "a": true, "an": true, "of": true, "to": true, "in": true, "is": true, "are": true,
	"how": true, "do": true, "we": true, "does": true, "where": true, "what": true, "and": true,
	"or": true, "for": true, "on": true, "it": true, "this": true, "that": true, "with": true,
}

// tokens splits text into lowercase search terms: every identifier whole and cut at camelCase and
// underscores, so "rotateWalSegment" is found by "rotate", "wal" or "rotatewalsegment".
func tokens(text string) []string {
	var out []string
	for _, word := range strings.FieldsFunc(text, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_'
	}) {
		whole := strings.ToLower(strings.ReplaceAll(word, "_", ""))
		parts := splitIdentifier(word)
		if len(parts) > 1 {
			out = append(out, whole)
		}
		out = append(out, parts...)
	}
	kept := out[:0]
	for _, t := range out {
		if t != "" && !stop[t] {
			kept = append(kept, t)
		}
	}
	return kept
}

// splitIdentifier cuts an identifier at underscores and lower-to-upper changes, lowercased.
func splitIdentifier(word string) []string {
	var parts []string
	var cur []rune
	flush := func() {
		if len(cur) > 0 {
			parts = append(parts, strings.ToLower(string(cur)))
			cur = cur[:0]
		}
	}
	runes := []rune(word)
	for i, r := range runes {
		if r == '_' {
			flush()
			continue
		}
		if i > 0 && unicode.IsUpper(r) && unicode.IsLower(runes[i-1]) {
			flush()
		}
		cur = append(cur, r)
	}
	flush()
	return parts
}

// chunk is one window of lines. It keeps the terms, not the text: the passage is read from the
// file when it is answered, so an index on disk holds no copy of the source.
type chunk struct {
	File int
	Line int // 1-based first line
	N    int // number of lines
	TF   map[string]float64
	Size float64
}

// indexVersion changes whenever the stored form does, so an old index on disk is rebuilt.
const indexVersion = 2

// indexData is everything worth keeping on disk.
type indexData struct {
	Version     int
	Fingerprint uint64
	Files       []string
	Chunks      []chunk
	DF          map[string]int
	AvgSize     float64
	// Sem is the semantic ranking's vectors, kept so a restart does not learn them again.
	Sem *semantic
	// EmbedID names the model Emb came from, and Emb holds one vector per window (nil past the cap).
	EmbedID string
	Emb     [][]float32
}

// Index is a built index of one folder.
type Index struct {
	root string
	indexData
}

// fileInfo is what the walk learns about one candidate file before reading it.
type fileInfo struct {
	rel  string
	size int64
	mod  int64
}

// walker collects the files worth indexing while a folder is walked.
type walker struct {
	ctx      context.Context
	root     string
	limit    int
	maxBytes int64
	files    []fileInfo
	ignore   ignores
}

// visit is the filepath.WalkDir callback.
func (w *walker) visit(path string, d fs.DirEntry, err error) error {
	if cerr := w.ctx.Err(); cerr != nil {
		return cerr
	}
	if err != nil || path == w.root {
		return nil
	}
	name := d.Name()
	rel, _ := filepath.Rel(w.root, path)
	rel = filepath.ToSlash(rel)
	if strings.HasPrefix(name, ".") || d.Type()&fs.ModeSymlink != 0 || w.ignore.matched(rel, d.IsDir()) {
		return skipEntry(d)
	}
	if d.IsDir() {
		if skipDirs[name] {
			return fs.SkipDir
		}
		w.ignore.load(w.root, rel)
		return nil
	}
	info, ierr := d.Info()
	if ierr != nil || !info.Mode().IsRegular() || info.Size() > w.maxBytes || info.Size() == 0 {
		return nil
	}
	if len(w.files) >= w.limit {
		return fs.SkipAll
	}
	w.files = append(w.files, fileInfo{rel, info.Size(), info.ModTime().UnixNano()})
	return nil
}

// scan walks root and lists the files worth indexing, in a stable order, with a fingerprint of
// their names, sizes and times so a later call can tell whether anything changed.
func scan(ctx context.Context, root string, o Options) ([]fileInfo, uint64, error) {
	w := &walker{ctx: ctx, root: root, limit: o.maxFiles(), maxBytes: o.maxBytes(), ignore: ignores{}}
	w.ignore.load(root, "")
	if err := filepath.WalkDir(root, w.visit); err != nil {
		return nil, 0, err
	}
	sort.Slice(w.files, func(i, j int) bool { return w.files[i].rel < w.files[j].rel })
	return w.files, fingerprint(w.files), nil
}

// fingerprint hashes the names, sizes and times of the files.
func fingerprint(files []fileInfo) uint64 {
	h := fnv.New64a()
	for _, f := range files {
		_, _ = h.Write([]byte(f.rel + "\x00" + strconv.FormatInt(f.size, 10) + ":" + strconv.FormatInt(f.mod, 10)))
	}
	return h.Sum64()
}

// skipEntry skips a directory whole and a file alone.
func skipEntry(d fs.DirEntry) error {
	if d.IsDir() {
		return fs.SkipDir
	}
	return nil
}

// Build indexes root.
func Build(ctx context.Context, root string, o Options) (*Index, error) {
	files, fp, err := scan(ctx, root, o)
	if err != nil {
		return nil, err
	}
	return buildFrom(ctx, root, files, fp)
}

func buildFrom(ctx context.Context, root string, files []fileInfo, fp uint64) (*Index, error) {
	ix := &Index{root: root, indexData: indexData{Version: indexVersion, Fingerprint: fp, DF: map[string]int{}}}
	for _, f := range files {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(f.rel)))
		if err != nil || isBinary(data) {
			continue
		}
		ix.Files = append(ix.Files, f.rel)
		ix.addFile(len(ix.Files)-1, f.rel, strings.Split(string(data), "\n"))
	}
	total := 0.0
	for i := range ix.Chunks {
		total += ix.Chunks[i].Size
	}
	if len(ix.Chunks) > 0 {
		ix.AvgSize = total / float64(len(ix.Chunks))
	}
	return ix, nil
}

func isBinary(data []byte) bool {
	head := data
	if len(head) > binarySniffer {
		head = head[:binarySniffer]
	}
	for _, b := range head {
		if b == 0 {
			return true
		}
	}
	return false
}

// addFile splits one file into overlapping windows and counts their terms.
func (ix *Index) addFile(file int, rel string, lines []string) {
	pathTerms := tokens(rel)
	for start := 0; start < len(lines); start += windowStep {
		end := min(start+windowLines, len(lines))
		c := newChunk(file, start+1, lines[start:end], pathTerms)
		for t, n := range c.TF {
			c.Size += n
			ix.DF[t]++
		}
		ix.Chunks = append(ix.Chunks, c)
		if end == len(lines) {
			break
		}
	}
}

// newChunk counts the terms of one window: its lines, the names it defines and its file's path.
func newChunk(file, line int, lines, pathTerms []string) chunk {
	c := chunk{File: file, Line: line, N: len(lines), TF: map[string]float64{}}
	for _, t := range pathTerms {
		c.TF[t] += pathWeight
	}
	for _, l := range lines {
		for _, t := range tokens(l) {
			c.TF[t]++
		}
		if m := defRe.FindStringSubmatch(l); m != nil {
			for _, t := range tokens(m[1]) {
				c.TF[t] += defWeight
			}
		}
	}
	return c
}

// ranked is one window with its score.
type ranked struct {
	i     int
	score float64
}

// Search returns the best passages for a question in plain words.
func (ix *Index) Search(query string, limit int) []Hit { return ix.searchWith(query, limit, nil) }

// searchWith is Search with the embedding of the query, when there is one.
func (ix *Index) searchWith(query string, limit int, queryVec []float32) []Hit {
	if limit < 1 {
		limit = DefaultLimit
	}
	limit = min(limit, MaxLimit)
	terms := uniq(tokens(query))
	if len(terms) == 0 || len(ix.Chunks) == 0 {
		return nil
	}
	second := ix.Sem.rank(terms)
	if queryVec != nil && ix.Emb != nil {
		second = ix.rankEmbedded(queryVec)
	}
	order := ix.fuse(ix.rankLexical(terms), second)
	reader := &lineCache{root: ix.root, byFile: map[string][]string{}}
	var hits []Hit
	seen := map[string]bool{}
	for _, r := range order {
		hit, ok := ix.hit(reader, &ix.Chunks[r.i], terms, r.score)
		key := hit.Path + ":" + strconv.Itoa(hit.Line)
		if !ok || seen[key] {
			continue
		}
		seen[key] = true
		if hits = append(hits, hit); len(hits) == limit {
			break
		}
	}
	return hits
}

// rankLexical orders the windows that hold a query term by BM25.
func (ix *Index) rankLexical(terms []string) []ranked {
	var out []ranked
	for i := range ix.Chunks {
		if s := ix.score(&ix.Chunks[i], terms); s > 0 {
			out = append(out, ranked{i, s})
		}
	}
	sort.SliceStable(out, func(a, b int) bool { return out[a].score > out[b].score })
	return out
}

// fuse merges the lexical and the semantic rankings by reciprocal rank. With no semantic ranking
// the BM25 scores stand as they are.
func (ix *Index) fuse(lexical, semantic []ranked) []ranked {
	if len(semantic) == 0 {
		return lexical
	}
	score := map[int]float64{}
	var order []int
	add := func(list []ranked, share float64) {
		for rank, r := range list {
			if _, seen := score[r.i]; !seen {
				order = append(order, r.i)
			}
			score[r.i] += share / float64(fuseK+rank)
		}
	}
	add(lexical, 1)
	add(semantic, semanticShare)
	out := make([]ranked, 0, len(order))
	for _, i := range order {
		out = append(out, ranked{i, score[i]})
	}
	sort.SliceStable(out, func(a, b int) bool { return out[a].score > out[b].score })
	return out
}

// score is the BM25 of one window for the query terms.
func (ix *Index) score(c *chunk, terms []string) float64 {
	n := float64(len(ix.Chunks))
	total := 0.0
	for _, t := range terms {
		tf := c.TF[t]
		if tf == 0 {
			continue
		}
		df := float64(ix.DF[t])
		idf := math.Log(1 + (n-df+0.5)/(df+0.5))
		total += idf * tf * (bm25K1 + 1) / (tf + bm25K1*(1-bm25B+bm25B*c.Size/ix.AvgSize))
	}
	return total
}

// lineCache reads each file once per search.
type lineCache struct {
	root   string
	byFile map[string][]string
}

func (lc *lineCache) lines(rel string) ([]string, bool) {
	if l, ok := lc.byFile[rel]; ok {
		return l, true
	}
	data, err := os.ReadFile(filepath.Join(lc.root, filepath.FromSlash(rel)))
	if err != nil {
		return nil, false
	}
	l := strings.Split(string(data), "\n")
	lc.byFile[rel] = l
	return l, true
}

// hit cuts the window down to the lines from the one that holds the most query terms. It is not
// ok when the file cannot be read any more.
func (ix *Index) hit(lc *lineCache, c *chunk, terms []string, score float64) (Hit, bool) {
	rel := ix.Files[c.File]
	all, ok := lc.lines(rel)
	if !ok || c.Line-1+c.N > len(all) {
		return Hit{Path: rel, Line: c.Line}, false
	}
	lines := all[c.Line-1 : c.Line-1+c.N]
	want := map[string]bool{}
	for _, t := range terms {
		want[t] = true
	}
	first, best := 0, 0
	for i, line := range lines {
		if n := matches(line, want); n > best {
			first, best = i, n
		}
	}
	last := min(first+snippetLines, len(lines))
	var sb strings.Builder
	for i, line := range lines[first:last] {
		if i > 0 {
			sb.WriteString("\n")
		}
		sb.WriteString(clip(strings.TrimRight(line, " \t\r"), snippetRunes))
	}
	return Hit{Path: rel, Line: c.Line + first, Snippet: strings.TrimRight(sb.String(), "\n"), Score: score}, true
}

// matches counts the distinct query terms a line holds.
func matches(line string, want map[string]bool) int {
	found := map[string]bool{}
	for _, t := range tokens(line) {
		if want[t] {
			found[t] = true
		}
	}
	return len(found)
}

func clip(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

func uniq(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

// Searcher keeps one built index per folder and rebuilds it when the folder changed, so a turn
// that searches twice reads the disk once.
type Searcher struct {
	opts     Options
	embedder Embedder
	mu       sync.Mutex
	byAt     map[string]*Index
	warning  string
}

// NewSearcher builds a searcher that indexes with o.
func NewSearcher(o Options) *Searcher {
	return &Searcher{opts: o, embedder: newEmbedder(o), byAt: map[string]*Index{}}
}

// Warning says why the last search ran without something the settings asked for (the embeddings
// endpoint did not answer), or is empty. A search still answers: it falls back to the rankings
// that need nothing outside.
func (s *Searcher) Warning() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.warning
}

var (
	sharedMu sync.Mutex
	shared   = map[Options]*Searcher{}
)

// Shared returns the searcher every caller with the same options uses, so the index survives the
// planner, which is built again for every turn.
func Shared(o Options) *Searcher {
	sharedMu.Lock()
	defer sharedMu.Unlock()
	if s, ok := shared[o]; ok {
		return s
	}
	s := NewSearcher(o)
	shared[o] = s
	return s
}

// Search returns the best passages under root for a question in plain words.
func (s *Searcher) Search(ctx context.Context, root, query string, limit int) ([]Hit, error) {
	files, fp, err := scan(ctx, root, s.opts)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	ix := s.byAt[root]
	if ix == nil || ix.Fingerprint != fp {
		if ix, err = s.obtain(ctx, root, files, fp); err != nil {
			return nil, err
		}
		s.byAt[root] = ix
	}
	return ix.searchWith(query, limit, s.queryVector(ctx, ix, query)), nil
}

// queryVector embeds the query when the index holds embeddings. A failure is a warning, not an
// error: the other rankings answer.
func (s *Searcher) queryVector(ctx context.Context, ix *Index, query string) []float32 {
	if s.embedder == nil || ix.Emb == nil {
		return nil
	}
	vecs, err := s.embedder.Embed(ctx, []string{query})
	if err != nil || len(vecs) != 1 {
		s.warn(err)
		return nil
	}
	s.warning = ""
	return vecs[0]
}

// warn records that the embeddings could not be used.
func (s *Searcher) warn(err error) {
	if err == nil {
		err = errors.New("the endpoint returned the wrong number of vectors")
	}
	s.warning = "embeddings unavailable (" + err.Error() + "): ranked by words and the vocabulary learned locally"
}

// obtain returns the index of root: the one on disk when it still describes the folder, else a
// new one, which is then kept on disk.
func (s *Searcher) obtain(ctx context.Context, root string, files []fileInfo, fp uint64) (*Index, error) {
	ix := loadIndex(s.opts.CacheDir, root, fp)
	fresh := ix == nil
	if fresh {
		var err error
		if ix, err = buildFrom(ctx, root, files, fp); err != nil {
			return nil, err
		}
	}
	if s.opts.Semantic && ix.Sem == nil {
		ix.Sem = buildSemantic(ix)
		fresh = true
	}
	fresh = s.embed(ctx, ix) || fresh
	if fresh {
		saveIndex(s.opts.CacheDir, ix)
	}
	if !s.opts.Semantic {
		ix.Sem = nil
	}
	return ix, nil
}

// embed gets the vectors of ix from the embeddings endpoint when the settings name one and the index
// does not hold them already, and says whether it changed the index. The vectors of another model
// are dropped. A failure leaves the index without them and is remembered as the warning.
func (s *Searcher) embed(ctx context.Context, ix *Index) bool {
	id := s.opts.embedID()
	if s.embedder == nil {
		ix.EmbedID, ix.Emb = "", nil
		return false
	}
	if ix.EmbedID == id && ix.Emb != nil {
		return false
	}
	vecs, err := embedChunks(ctx, ix, s.embedder)
	if err != nil {
		s.warn(err)
		ix.EmbedID, ix.Emb = "", nil
		return false
	}
	ix.EmbedID, ix.Emb = id, vecs
	s.warning = ""
	return true
}

// Format renders hits as the plain text a model reads: path:line, then the passage.
func Format(query string, hits []Hit) string {
	if len(hits) == 0 {
		return "No indexed passage matches " + quote(query) + ". The index only knows what it read: " +
			"confirm with search_in_files."
	}
	var sb strings.Builder
	for i, h := range hits {
		if i > 0 {
			sb.WriteString("\n---\n")
		}
		sb.WriteString(h.Path + ":" + strconv.Itoa(h.Line) + "\n" + h.Snippet)
	}
	return sb.String()
}

func quote(s string) string { return "\"" + strings.ReplaceAll(s, "\"", "'") + "\"" }
