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
	bm25K1        = 1.2
	bm25B         = 0.75
	binarySniffer = 512
)

// Options bound what is indexed. Zero values take the defaults.
type Options struct {
	MaxFileKB int
	MaxFiles  int
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

// chunk is one window of lines.
type chunk struct {
	file  int
	line  int // 1-based first line
	lines []string
	tf    map[string]float64
	size  float64
}

// Index is a built index of one folder.
type Index struct {
	root        string
	files       []string
	chunks      []chunk
	df          map[string]int
	avgSize     float64
	fingerprint uint64
}

// fileInfo is what the walk learns about one candidate file before reading it.
type fileInfo struct {
	rel  string
	size int64
	mod  int64
}

// scan walks root and lists the files worth indexing, in a stable order, with a fingerprint of
// their names, sizes and times so a later call can tell whether anything changed.
func scan(ctx context.Context, root string, o Options) ([]fileInfo, uint64, error) {
	var files []fileInfo
	limit, maxBytes := o.maxFiles(), o.maxBytes()
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if cerr := ctx.Err(); cerr != nil {
			return cerr
		}
		if err != nil || path == root {
			return nil
		}
		name := d.Name()
		if strings.HasPrefix(name, ".") || d.Type()&fs.ModeSymlink != 0 {
			return skipEntry(d)
		}
		if d.IsDir() {
			if skipDirs[name] {
				return fs.SkipDir
			}
			return nil
		}
		info, ierr := d.Info()
		if ierr != nil || !info.Mode().IsRegular() || info.Size() > maxBytes || info.Size() == 0 {
			return nil
		}
		if len(files) >= limit {
			return fs.SkipAll
		}
		rel, _ := filepath.Rel(root, path)
		files = append(files, fileInfo{filepath.ToSlash(rel), info.Size(), info.ModTime().UnixNano()})
		return nil
	})
	if err != nil {
		return nil, 0, err
	}
	sort.Slice(files, func(i, j int) bool { return files[i].rel < files[j].rel })
	h := fnv.New64a()
	for _, f := range files {
		_, _ = h.Write([]byte(f.rel))
		_, _ = h.Write([]byte{0})
		_, _ = h.Write([]byte(strings.Join([]string{strconv.FormatInt(f.size, 10), strconv.FormatInt(f.mod, 10)}, ":")))
	}
	return files, h.Sum64(), nil
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
	ix := &Index{root: root, df: map[string]int{}, fingerprint: fp}
	for _, f := range files {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(f.rel)))
		if err != nil || isBinary(data) {
			continue
		}
		ix.files = append(ix.files, f.rel)
		ix.addFile(len(ix.files)-1, f.rel, strings.Split(string(data), "\n"))
	}
	total := 0.0
	for i := range ix.chunks {
		total += ix.chunks[i].size
	}
	if len(ix.chunks) > 0 {
		ix.avgSize = total / float64(len(ix.chunks))
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
		c := chunk{file: file, line: start + 1, lines: lines[start:end], tf: map[string]float64{}}
		for _, t := range pathTerms {
			c.tf[t] += pathWeight
		}
		for _, line := range c.lines {
			for _, t := range tokens(line) {
				c.tf[t]++
			}
			if m := defRe.FindStringSubmatch(line); m != nil {
				for _, t := range tokens(m[1]) {
					c.tf[t] += defWeight
				}
			}
		}
		for t, n := range c.tf {
			c.size += n
			ix.df[t]++
		}
		ix.chunks = append(ix.chunks, c)
		if end == len(lines) {
			break
		}
	}
}

// Search returns the best passages for a question in plain words.
func (ix *Index) Search(query string, limit int) []Hit {
	if limit < 1 {
		limit = DefaultLimit
	}
	limit = min(limit, MaxLimit)
	terms := uniq(tokens(query))
	if len(terms) == 0 || len(ix.chunks) == 0 {
		return nil
	}
	type scored struct {
		i     int
		score float64
	}
	var ranked []scored
	for i := range ix.chunks {
		if s := ix.score(&ix.chunks[i], terms); s > 0 {
			ranked = append(ranked, scored{i, s})
		}
	}
	sort.SliceStable(ranked, func(a, b int) bool { return ranked[a].score > ranked[b].score })
	var hits []Hit
	seen := map[string]bool{}
	for _, r := range ranked {
		c := &ix.chunks[r.i]
		hit := ix.hit(c, terms, r.score)
		key := hit.Path + ":" + strconv.Itoa(hit.Line)
		if seen[key] {
			continue
		}
		seen[key] = true
		hits = append(hits, hit)
		if len(hits) == limit {
			break
		}
	}
	return hits
}

// score is the BM25 of one window for the query terms.
func (ix *Index) score(c *chunk, terms []string) float64 {
	n := float64(len(ix.chunks))
	total := 0.0
	for _, t := range terms {
		tf := c.tf[t]
		if tf == 0 {
			continue
		}
		idf := math.Log(1 + (n-float64(ix.df[t])+0.5)/(float64(ix.df[t])+0.5))
		total += idf * tf * (bm25K1 + 1) / (tf + bm25K1*(1-bm25B+bm25B*c.size/ix.avgSize))
	}
	return total
}

// hit cuts the window down to the lines from the one that holds the most query terms.
func (ix *Index) hit(c *chunk, terms []string, score float64) Hit {
	want := map[string]bool{}
	for _, t := range terms {
		want[t] = true
	}
	first, best := 0, 0
	for i, line := range c.lines {
		if n := matches(line, want); n > best {
			first, best = i, n
		}
	}
	last := min(first+snippetLines, len(c.lines))
	var sb strings.Builder
	for i, line := range c.lines[first:last] {
		if i > 0 {
			sb.WriteString("\n")
		}
		sb.WriteString(clip(strings.TrimRight(line, " \t\r"), snippetRunes))
	}
	return Hit{Path: ix.files[c.file], Line: c.line + first, Snippet: strings.TrimRight(sb.String(), "\n"), Score: score}
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
	opts Options
	mu   sync.Mutex
	byAt map[string]*Index
}

// NewSearcher builds a searcher that indexes with o.
func NewSearcher(o Options) *Searcher { return &Searcher{opts: o, byAt: map[string]*Index{}} }

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
	if ix == nil || ix.fingerprint != fp {
		if ix, err = buildFrom(ctx, root, files, fp); err != nil {
			return nil, err
		}
		s.byAt[root] = ix
	}
	return ix.Search(query, limit), nil
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
