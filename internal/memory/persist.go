package memory

import (
	"bytes"
	"encoding/gob"
	"errors"
	"fmt"
	"hash/fnv"
	"io"
	"math"
	"os"
	"path/filepath"
	"time"
)

// The index is kept on disk between runs, so the first search after a restart does not build it
// again: on a large memory that takes seconds, nearly all of them learning the vector space. The
// copy lives in a cache folder outside the memory (the memory is the person's folder, often synced
// or opened in Obsidian, and a binary file has no place in it), one file per memory folder. It is
// only a cache: a copy that is missing, unreadable, of another version or of another folder is
// ignored and the index is built from the files, and a copy whose files changed since is brought
// up to date the same way an index in memory is.
//
// Vectors are kept as bfloat16 (the top half of a float32, rounded), which halves the file and
// moves a cosine by about a thousandth.

// diskVersion changes whenever the layout or the meaning of the copy changes.
const diskVersion = 2

// diskIndex is the copy of an index on disk.
type diskIndex struct {
	Version int
	Root    string
	Print   string
	Stale   int
	Docs    []diskDoc
	IDF     map[string]float64
	Rep     map[string][]uint16 // the learned words only; the others are their index vector
}

// diskDoc is what one entry contributed.
type diskDoc struct {
	Path     string
	Title    string
	Size     int64
	Modified time.Time
	Meta     Meta
	Parents  []diskParent
	Kids     []diskKid
	Links    []string
}

// diskParent is a parent section.
type diskParent struct {
	Trail []string
	Line  int
	Text  string
}

// diskKid is a child passage and its vector, empty when it was not encoded (past maxChildren).
type diskKid struct {
	Parent int
	Line   int
	Text   string
	Ctx    string
	Vec    []uint16
}

// WithCache returns the memory with its index kept in dir between runs. An empty dir keeps it in
// memory only.
func (v *Store) WithCache(dir string) *Store {
	c := *v
	c.cacheDir = dir
	return &c
}

// cacheFile is where the copy of this memory's index is kept, "" when there is no cache folder.
func (v *Store) cacheFile() string {
	if v.cacheDir == "" {
		return ""
	}
	h := fnv.New64a()
	_, _ = h.Write([]byte(v.root))
	return filepath.Join(v.cacheDir, fmt.Sprintf("%x.gob", h.Sum64()))
}

// writeCache writes the copy whole or not at all, through the same disk calls an entry is written
// with.
func writeCache(file string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(file), 0o700); err != nil {
		return err
	}
	tmp, err := createTemp(filepath.Dir(file), ".index-*.tmp")
	if err != nil {
		return err
	}
	_, werr := tmp.Write(data)
	if err := errors.Join(werr, tmp.Close(), moveFile(tmp.Name(), file)); err != nil {
		_ = os.Remove(tmp.Name())
		return err
	}
	return nil
}

// encodeIndex writes the copy as gob; a variable so a test can make it fail.
var encodeIndex = func(w io.Writer, d diskIndex) error { return gob.NewEncoder(w).Encode(d) }

// save keeps a copy of ix on disk. A copy that cannot be written is no loss: the next run builds
// the index from the files.
func (v *Store) save(ix *index) {
	file := v.cacheFile()
	if file == "" {
		return
	}
	d := diskIndex{Version: diskVersion, Root: v.root, Print: ix.print, Stale: ix.stale,
		IDF: ix.holo.idf, Rep: map[string][]uint16{}}
	for w := range ix.holo.learned {
		d.Rep[w] = toBF16(ix.holo.rep[w])
	}
	for _, doc := range ix.docs {
		dd := diskDoc{Path: doc.entry.Path, Title: doc.entry.Title, Size: doc.entry.Size,
			Modified: doc.entry.Modified, Meta: doc.entry.Meta, Links: doc.links}
		for _, p := range doc.parents {
			dd.Parents = append(dd.Parents, diskParent{Trail: p.trail, Line: p.line, Text: p.text})
		}
		for j, c := range doc.kids {
			dd.Kids = append(dd.Kids, diskKid{Parent: c.parent, Line: c.line, Text: c.text, Ctx: c.ctx,
				Vec: toBF16(doc.vecs[j])})
		}
		d.Docs = append(d.Docs, dd)
	}
	var buf bytes.Buffer
	if err := encodeIndex(&buf, d); err != nil {
		return
	}
	_ = writeCache(file, buf.Bytes())
}

// load reads the copy of this memory's index, nil when there is none it can use.
func (v *Store) load() *index {
	file := v.cacheFile()
	if file == "" {
		return nil
	}
	data, err := os.ReadFile(filepath.Clean(file))
	if err != nil {
		return nil
	}
	var d diskIndex
	if gob.NewDecoder(bytes.NewReader(data)).Decode(&d) != nil || d.Version != diskVersion || d.Root != v.root {
		return nil
	}
	docs := make([]*doc, len(d.Docs))
	for i, dd := range d.Docs {
		docs[i] = dd.doc()
	}
	ix := assemble(docs, d.Print)
	h := d.space()
	h.vecs = make([]vec, len(ix.kids))
	complete := true
	for i, r := range ix.at {
		h.vecs[i] = docs[r.doc].vecs[r.kid]
		complete = complete && len(h.vecs[i]) == dim
	}
	if complete {
		ix.holo, ix.stale = h, d.Stale
		return ix
	}
	// a child the copy holds no vector for (the cap moved): encode what is missing
	for _, r := range ix.at {
		if dv := docs[r.doc].vecs; len(dv[r.kid]) != dim {
			dv[r.kid] = nil
		}
	}
	ix.encode(&index{holo: h, stale: d.Stale})
	return ix
}

// doc is the entry as the index keeps it, with its words counted again.
func (dd diskDoc) doc() *doc {
	e := Entry{Path: dd.Path, Title: dd.Title, Size: dd.Size, Modified: dd.Modified}
	d := &doc{entry: indexed{Entry: e, Meta: dd.Meta, namespace: Namespace(dd.Path)}, links: dd.Links}
	for _, p := range dd.Parents {
		d.parents = append(d.parents, parent{trail: p.Trail, line: p.Line, text: p.Text})
	}
	d.text = make([][]string, len(dd.Kids))
	d.ctx = make([][]string, len(dd.Kids))
	d.vecs = make([]vec, len(dd.Kids))
	for j, k := range dd.Kids {
		d.kids = append(d.kids, child{parent: k.Parent, line: k.Line, text: k.Text, ctx: k.Ctx})
		d.text[j], d.ctx[j] = terms(k.Text), terms(k.Ctx)
		d.vecs[j] = fromBF16(k.Vec)
	}
	return d
}

// space is the vector space the copy was learned in, without the passages' vectors.
func (d diskIndex) space() *holo {
	h := &holo{idf: d.IDF, index: map[string]vec{}, rep: map[string]vec{}, learned: map[string]bool{}}
	if h.idf == nil {
		h.idf = map[string]float64{}
	}
	for w := range h.idf {
		h.index[w] = wordVector(w)
		h.rep[w] = h.index[w]
	}
	for w, r := range d.Rep {
		if _, ok := h.idf[w]; ok && len(r) == dim {
			h.rep[w] = fromBF16(r)
			h.learned[w] = true
		}
	}
	return h
}

// toBF16 keeps the top half of each float32, rounded to nearest even. nil stays empty.
func toBF16(v vec) []uint16 {
	if v == nil {
		return nil
	}
	out := make([]uint16, len(v))
	for i, x := range v {
		b := math.Float32bits(x)
		b += 0x7fff + (b>>16)&1
		out[i] = uint16(b >> 16)
	}
	return out
}

// fromBF16 widens bfloat16 values back to float32. Empty is nil.
func fromBF16(b []uint16) vec {
	if len(b) == 0 {
		return nil
	}
	out := make(vec, len(b))
	for i, x := range b {
		out[i] = math.Float32frombits(uint32(x) << 16)
	}
	return out
}
