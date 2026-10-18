package codeindex

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestInspectFollowsTheIndexOnDisk(t *testing.T) {
	root, cache := project(t), t.TempDir()
	o := Options{CacheDir: cache, Semantic: true}
	ctx := context.Background()

	st, err := Inspect(ctx, root, o)
	if err != nil || st.State != StateNone || st.Candidates == 0 || st.Files != 0 || st.Percent != 0 || st.MaxFiles != DefaultMaxFiles {
		t.Fatalf("before indexing: %+v %v", st, err)
	}
	if _, err := NewSearcher(o).Search(ctx, root, "wal", 1); err != nil {
		t.Fatal(err)
	}
	st, _ = Inspect(ctx, root, o)
	if st.State != StateReady || st.Percent != 100 || st.Files == 0 || st.Chunks == 0 || st.Terms == 0 ||
		st.CacheBytes == 0 || !st.Semantic || st.Embeddings {
		t.Errorf("after indexing: %+v", st)
	}
	if err := os.WriteFile(filepath.Join(root, "new.go"), []byte("package x\nfunc New() {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	st, _ = Inspect(ctx, root, o)
	if st.State != StateStale || st.Percent <= 0 || st.Percent >= 100 {
		t.Errorf("after a new file: %+v", st)
	}
	if st, _ = Inspect(ctx, root, Options{CacheDir: cache, MaxFiles: 1}); !st.Capped {
		t.Errorf("one file allowed: %+v", st)
	}
	if st, _ = Inspect(ctx, root, Options{}); st.State != StateNone {
		t.Errorf("no cache dir: %+v", st)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := Inspect(cancelled, root, o); err == nil {
		t.Error("a cancelled walk is an error")
	}
}

func TestCacheSizeCountsOnlyIndexFiles(t *testing.T) {
	dir := t.TempDir()
	for name, body := range map[string]string{"a.gob": "123", "b.gob": "45", "c.txt": "6789"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir(filepath.Join(dir, "d.gob"), 0o700); err != nil {
		t.Fatal(err)
	}
	if files, size := CacheSize(dir); files != 2 || size != 5 {
		t.Errorf("CacheSize = %d, %d", files, size)
	}
	if files, size := CacheSize(filepath.Join(dir, "missing")); files != 0 || size != 0 {
		t.Errorf("missing = %d, %d", files, size)
	}
}
