package memory

import (
	"bytes"
	"context"
	"encoding/gob"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// forget drops every index kept in memory, as a restart does.
func forget(t *testing.T) {
	t.Helper()
	cacheMu.Lock()
	cache = map[string]*index{}
	cacheMu.Unlock()
}

// cached is the memory's index as the next search would find it in memory.
func cached(v *Store) *index {
	cacheMu.Lock()
	defer cacheMu.Unlock()
	return cache[v.root]
}

// readDisk decodes the copy of v's index.
func readDisk(t *testing.T, v *Store) diskIndex {
	t.Helper()
	data, err := os.ReadFile(v.cacheFile())
	if err != nil {
		t.Fatalf("no copy on disk: %v", err)
	}
	var d diskIndex
	if err := gob.NewDecoder(bytes.NewReader(data)).Decode(&d); err != nil {
		t.Fatal(err)
	}
	return d
}

// writeDisk replaces the copy of v's index.
func writeDisk(t *testing.T, v *Store, d diskIndex) {
	t.Helper()
	var buf bytes.Buffer
	if err := gob.NewEncoder(&buf).Encode(d); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(v.cacheFile(), buf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestTheIndexIsKeptOnDiskAndUsedAfterARestart(t *testing.T) {
	v := corpus(t).WithCache(filepath.Join(t.TempDir(), "cache"))
	q := Query{Text: "senior engineers salary band"}
	before := v.Search(context.Background(), q)
	if _, err := os.Stat(v.cacheFile()); err != nil {
		t.Fatalf("the index was not kept: %v", err)
	}
	if fi, _ := os.Stat(filepath.Dir(v.cacheFile())); fi.Mode().Perm() != 0o700 {
		t.Errorf("cache folder mode = %v", fi.Mode().Perm())
	}
	forget(t)
	// The copy answers without the files being read again.
	old := readFile
	defer func() { readFile = old }()
	readFile = func(string) ([]byte, error) { return nil, errors.New("the files must not be read") }
	after := v.Search(context.Background(), q)
	readFile = old
	if paths(after.Hits) != paths(before.Hits) || after.Hits[0].Block != before.Hits[0].Block {
		t.Errorf("after a restart %s, before %s", paths(after.Hits), paths(before.Hits))
	}
	ix := cached(v)
	if ix == nil || len(ix.holo.learned) == 0 || len(ix.holo.vecs) != len(ix.kids) {
		t.Fatalf("loaded index = %+v", ix)
	}
	// A copy whose files changed since is brought up to date, and kept again.
	forget(t)
	write(t, v, "HR/Benefits.md", "# Benefits\n\nDental plan and gym.\n")
	if res := v.Search(context.Background(), Query{Text: "dental gym"}); len(res.Hits) == 0 ||
		res.Hits[0].Path != "HR/Benefits.md" {
		t.Errorf("a change since the copy: %+v", res.Hits)
	}
	if d := readDisk(t, v); len(d.Docs) != 6 || d.Root != v.root || d.Version != diskVersion {
		t.Errorf("copy not updated: %d docs", len(d.Docs))
	}
}

func TestACopyThatCannotBeUsedIsIgnored(t *testing.T) {
	v := corpus(t).WithCache(t.TempDir())
	v.Search(context.Background(), Query{Text: "payroll"})
	good := readDisk(t, v)
	for name, d := range map[string]diskIndex{
		"another version": {Version: diskVersion + 1, Root: v.root, Print: good.Print},
		"another folder":  {Version: diskVersion, Root: "/elsewhere", Print: good.Print},
	} {
		writeDisk(t, v, d)
		if v.load() != nil {
			t.Errorf("%s: loaded", name)
		}
	}
	if err := os.WriteFile(v.cacheFile(), []byte("not gob"), 0o600); err != nil {
		t.Fatal(err)
	}
	if v.load() != nil {
		t.Error("garbage loaded")
	}
	os.Remove(v.cacheFile())
	if v.load() != nil {
		t.Error("a missing copy loaded")
	}
	// Without a cache folder nothing is kept or read.
	plain := Open(v.root)
	plain.save(cached(v))
	if plain.load() != nil || plain.cacheFile() != "" {
		t.Error("a memory without a cache folder")
	}
}

func TestACopyMissingVectorsEncodesThem(t *testing.T) {
	v := corpus(t).WithCache(t.TempDir())
	v.Search(context.Background(), Query{Text: "payroll"})
	d := readDisk(t, v)
	d.Docs[0].Kids[0].Vec = nil
	// A learned word the space does not know, or of the wrong size, is not taken.
	d.Rep["nosuchword"] = make([]uint16, dim)
	for w := range d.IDF {
		d.Rep[w] = []uint16{1}
		break
	}
	writeDisk(t, v, d)
	ix := v.load()
	if ix == nil {
		t.Fatal("not loaded")
	}
	for i, x := range ix.holo.vecs {
		if len(x) != dim {
			t.Fatalf("child %d has no vector", i)
		}
	}
	if _, ok := ix.holo.rep["nosuchword"]; ok {
		t.Error("a word outside the space was taken")
	}
}

func TestAnEmptyMemoryIsKeptAndLoaded(t *testing.T) {
	v := Open(t.TempDir()).WithCache(t.TempDir())
	write(t, v, "Empty.md", "")
	v.Search(context.Background(), Query{Text: "payroll"})
	forget(t)
	d := readDisk(t, v)
	d.IDF = nil
	writeDisk(t, v, d)
	ix := v.load()
	if ix == nil || len(ix.kids) != 0 || ix.holo.idf == nil {
		t.Fatalf("empty memory = %+v", ix)
	}
}

func TestEntriesPastTheCapAreKeptWithoutVectors(t *testing.T) {
	old := maxChildren
	defer func() { maxChildren = old }()
	maxChildren = 1
	v := corpus(t).WithCache(t.TempDir())
	v.Search(context.Background(), Query{Text: "payroll"})
	d := readDisk(t, v)
	empty := 0
	for _, doc := range d.Docs {
		for _, k := range doc.Kids {
			if len(k.Vec) == 0 {
				empty++
			}
		}
	}
	if empty == 0 {
		t.Error("children past the cap should have no vector")
	}
	forget(t)
	if ix := v.load(); ix == nil || len(ix.kids) > 3 {
		t.Errorf("loaded past the cap: %+v", ix)
	}
}

func TestACopyThatCannotBeWrittenIsNoLoss(t *testing.T) {
	dir := t.TempDir()
	blocker := filepath.Join(dir, "file")
	if err := os.WriteFile(blocker, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := writeCache(filepath.Join(blocker, "x.gob"), []byte("x")); err == nil {
		t.Error("a folder under a file")
	}
	oldTemp, oldMove, oldEnc := createTemp, moveFile, encodeIndex
	defer func() { createTemp, moveFile, encodeIndex = oldTemp, oldMove, oldEnc }()
	createTemp = func(string, string) (*os.File, error) { return nil, errors.New("boom") }
	if err := writeCache(filepath.Join(dir, "x.gob"), []byte("x")); err == nil {
		t.Error("temp failure")
	}
	createTemp = oldTemp
	moveFile = func(string, string) error { return errors.New("boom") }
	if err := writeCache(filepath.Join(dir, "x.gob"), []byte("x")); err == nil {
		t.Error("move failure")
	}
	if left, _ := filepath.Glob(filepath.Join(dir, ".index-*")); len(left) != 0 {
		t.Errorf("temp files left: %v", left)
	}
	moveFile = oldMove
	// An index that cannot be encoded leaves no copy, and the search still answers.
	encodeIndex = func(io.Writer, diskIndex) error { return errors.New("boom") }
	v := corpus(t).WithCache(t.TempDir())
	if res := v.Search(context.Background(), Query{Text: "payroll"}); len(res.Hits) == 0 {
		t.Error("no answer")
	}
	if _, err := os.Stat(v.cacheFile()); err == nil {
		t.Error("a copy was written")
	}
}

func TestBFloat16KeepsTheVectorClose(t *testing.T) {
	if toBF16(nil) != nil || fromBF16(nil) != nil {
		t.Error("nil must stay empty")
	}
	v := vec{0.0441941738, -0.0441941738, 1, -0.5, 0.123456}
	back := fromBF16(toBF16(v))
	for i := range v {
		if d := back[i] - v[i]; d > 0.004*abs32(v[i]) || -d > 0.004*abs32(v[i]) {
			t.Errorf("%d: %v became %v", i, v[i], back[i])
		}
	}
}

func abs32(x float32) float32 {
	if x < 0 {
		return -x
	}
	return x
}

func TestARebuildReadsOnlyTheEntriesThatChanged(t *testing.T) {
	v := corpus(t)
	v.Search(context.Background(), Query{Text: "payroll"})
	reads := 0
	old := readFile
	defer func() { readFile = old }()
	readFile = func(p string) ([]byte, error) {
		reads++
		return old(p)
	}
	time.Sleep(10 * time.Millisecond)
	write(t, v, "Engineering/Deploys.md", "# Deploys\n\nCanary first, then everyone.\n")
	res := v.Search(context.Background(), Query{Text: "canary"})
	if len(res.Hits) == 0 || res.Hits[0].Path != "Engineering/Deploys.md" {
		t.Errorf("hits = %+v", res.Hits)
	}
	// the changed entry, and the hit read for its answer at most
	if reads == 0 || reads > 2 {
		t.Errorf("read %d files to bring the index up to date", reads)
	}
}

func TestALargeMemoryKeepsItsSpaceUntilTooMuchIsNew(t *testing.T) {
	oldMin, oldShare := incrementalMin, staleShare
	defer func() { incrementalMin, staleShare = oldMin, oldShare }()
	incrementalMin, staleShare = 1, 0.5
	v := corpus(t)
	v.Search(context.Background(), Query{Text: "payroll"})
	first := cached(v)
	write(t, v, "Inbox.md", "Pick up the rollback dashboards.\n")
	res := v.Search(context.Background(), Query{Text: "rollback dashboards"})
	second := cached(v)
	if second.stale == 0 || len(second.holo.idf) != len(first.holo.idf) || second.holo.rep == nil {
		t.Errorf("the space was not kept: stale %d", second.stale)
	}
	if len(res.Hits) == 0 {
		t.Error("a passage encoded in the kept space is not found")
	}
	// Enough new passages and the space is learned again.
	staleShare = 0
	write(t, v, "Inbox.md", "Something else entirely about payroll forms.\n")
	v.Search(context.Background(), Query{Text: "payroll"})
	if third := cached(v); third.stale != 0 {
		t.Errorf("the space was kept past its share: stale %d", third.stale)
	}
}
