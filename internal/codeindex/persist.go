package codeindex

import (
	"encoding/gob"
	"encoding/hex"
	"hash/fnv"
	"os"
	"path/filepath"
)

// createTemp is os.CreateTemp, a variable so a test can make the write fail on a disk that allows it.
var createTemp = os.CreateTemp

// cachePath is where the index of root lives inside dir.
func cachePath(dir, root string) string {
	h := fnv.New64a()
	_, _ = h.Write([]byte(root))
	return filepath.Join(dir, hex.EncodeToString(h.Sum(nil))+".gob")
}

// loadIndex returns the index kept on disk for root when it still describes the folder (same
// stored form, same fingerprint), and nil otherwise. A missing, damaged or stale file is not an
// error: the index is built again.
func loadIndex(dir, root string, fp uint64) *Index {
	if dir == "" {
		return nil
	}
	f, err := os.Open(cachePath(dir, root))
	if err != nil {
		return nil
	}
	defer f.Close()
	var data indexData
	if err := gob.NewDecoder(f).Decode(&data); err != nil || data.Version != indexVersion || data.Fingerprint != fp {
		return nil
	}
	return &Index{root: root, indexData: data}
}

// saveIndex keeps ix on disk, privately: the terms of the source are in it. It is best effort, a
// disk that cannot be written only costs the next start a rebuild.
func saveIndex(dir string, ix *Index) {
	if dir == "" {
		return
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return
	}
	tmp, err := createTemp(dir, "index-*.tmp")
	if err != nil {
		return
	}
	name := tmp.Name()
	encodeErr := gob.NewEncoder(tmp).Encode(ix.indexData)
	closeErr := tmp.Close()
	if encodeErr != nil || closeErr != nil || os.Chmod(name, 0o600) != nil ||
		os.Rename(name, cachePath(dir, ix.root)) != nil {
		_ = os.Remove(name)
	}
}
