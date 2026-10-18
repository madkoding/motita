package codeindex

import (
	"context"
	"os"
	"path/filepath"
)

// Index states reported by Inspect.
const (
	StateNone  = "none"  // nothing is kept on disk for the folder
	StateStale = "stale" // the folder changed since the index on disk was built
	StateReady = "ready" // the index on disk describes the folder as it is
)

// Status says how much of a folder the index on disk covers.
type Status struct {
	State string `json:"state"`
	// Candidates is how many files the folder offers (at most MaxFiles); Files how many the index
	// on disk holds, so Files/Candidates is the share covered.
	Candidates int `json:"candidates"`
	Files      int `json:"files"`
	// Percent is how much of the folder the index covers: 100 when it is ready, otherwise the share
	// of candidates the (old or missing) index holds.
	Percent  int `json:"percent"`
	Chunks   int `json:"chunks"`
	Terms    int `json:"terms"`
	MaxFiles int `json:"max_files"`
	// Capped says the folder has at least MaxFiles files, so some may be left out.
	Capped     bool  `json:"capped"`
	CacheBytes int64 `json:"cache_bytes"`
	Semantic   bool  `json:"semantic"`
	Embeddings bool  `json:"embeddings"`
}

// Inspect reports the index kept on disk for root without building or changing anything.
func Inspect(ctx context.Context, root string, o Options) (Status, error) {
	files, fp, err := scan(ctx, root, o)
	if err != nil {
		return Status{}, err
	}
	st := Status{State: StateNone, Candidates: len(files), MaxFiles: o.maxFiles()}
	st.Capped = st.Candidates >= st.MaxFiles
	data := readData(o.CacheDir, root)
	if data == nil {
		return st, nil
	}
	st.State = StateStale
	if data.Fingerprint == fp {
		st.State = StateReady
	}
	st.Files, st.Chunks, st.Terms = len(data.Files), len(data.Chunks), len(data.DF)
	st.Percent = 100
	if st.State == StateStale {
		st.Percent = min(99, st.Files*100/max(1, st.Candidates))
	}
	st.Semantic, st.Embeddings = data.Sem != nil, data.Emb != nil
	if info, err := os.Stat(cachePath(o.CacheDir, root)); err == nil {
		st.CacheBytes = info.Size()
	}
	return st, nil
}

// CacheSize counts the index files kept in dir and the bytes they hold.
func CacheSize(dir string) (files int, bytes int64) {
	names, _ := filepath.Glob(filepath.Join(dir, "*.gob"))
	for _, name := range names {
		if info, err := os.Lstat(name); err == nil && info.Mode().IsRegular() {
			files++
			bytes += info.Size()
		}
	}
	return files, bytes
}
