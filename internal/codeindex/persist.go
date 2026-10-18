package codeindex

import (
	"encoding/gob"
	"encoding/hex"
	"errors"
	"hash/fnv"
	"os"
	"path/filepath"
)

// createTemp is os.CreateTemp, a variable so a test can make the write fail on a disk that allows it.
var createTemp = os.CreateTemp

// removeFile is os.Remove, a variable so a test can make the deletion fail.
var removeFile = os.Remove

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
	data := readData(dir, root)
	if data == nil || data.Fingerprint != fp {
		return nil
	}
	return &Index{root: root, indexData: *data}
}

// readData decodes the index file kept for root, whatever folder state it describes. It is nil
// for a missing, damaged or old-form file.
func readData(dir, root string) *indexData {
	if dir == "" {
		return nil
	}
	f, err := os.Open(cachePath(dir, root))
	if err != nil {
		return nil
	}
	defer f.Close()
	var data indexData
	if err := gob.NewDecoder(f).Decode(&data); err != nil || data.Version != indexVersion {
		return nil
	}
	return &data
}

// saveIndex keeps ix on disk, privately: the terms of the source are in it. It is best effort, a
// disk that cannot be written only costs the next start a rebuild.
func saveIndex(dir string, ix *Index) { _ = writeIndex(dir, ix) }

// writeIndex is saveIndex with the failure told, for the caller that must not stay silent about it.
func writeIndex(dir string, ix *Index) error {
	if dir == "" {
		return errors.New("no cache directory is set")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := createTemp(dir, "index-*.tmp")
	if err != nil {
		return err
	}
	name := tmp.Name()
	encodeErr := gob.NewEncoder(tmp).Encode(ix.indexData)
	closeErr := tmp.Close()
	err = errors.Join(encodeErr, closeErr, os.Chmod(name, 0o600))
	if err == nil {
		err = os.Rename(name, cachePath(dir, ix.root))
	}
	if err != nil {
		_ = os.Remove(name)
	}
	return err
}

// ClearCache deletes the index files kept in dir and says how many it removed and how many bytes
// they held. Only files this package writes (*.gob) are touched; a missing or empty dir is nothing
// to clear.
func ClearCache(dir string) (files int, bytes int64, err error) {
	if dir == "" {
		return 0, 0, nil
	}
	names, err := filepath.Glob(filepath.Join(dir, "*.gob"))
	if err != nil {
		return 0, 0, err
	}
	for _, name := range names {
		info, statErr := os.Lstat(name)
		if statErr != nil || !info.Mode().IsRegular() {
			continue
		}
		if err := removeFile(name); err != nil {
			return files, bytes, err
		}
		files++
		bytes += info.Size()
	}
	return files, bytes, nil
}
