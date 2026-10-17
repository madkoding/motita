package codeindex

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestIndexSurvivesARestartFromDisk(t *testing.T) {
	root, cache := project(t), filepath.Join(t.TempDir(), "cache")
	first := NewSearcher(Options{CacheDir: cache})
	want, err := first.Search(context.Background(), root, "rotate wal segment", 3)
	if err != nil || len(want) == 0 {
		t.Fatalf("first search: %v %v", want, err)
	}
	info, err := os.Stat(cachePath(cache, root))
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("index file: %v %v", info, err)
	}
	second := NewSearcher(Options{CacheDir: cache})
	_, fp, _ := scan(context.Background(), root, Options{})
	if ix := loadIndex(cache, root, fp); ix == nil || len(ix.Chunks) == 0 {
		t.Fatal("the index on disk was not loaded")
	}
	got, err := second.Search(context.Background(), root, "rotate wal segment", 3)
	if err != nil || len(got) != len(want) || got[0] != want[0] {
		t.Errorf("after a restart: %+v (%v), want %+v", got, err, want)
	}
}

func TestDiskIndexIsNotUsedWhenStaleOrDamaged(t *testing.T) {
	root, cache := project(t), t.TempDir()
	s := NewSearcher(Options{CacheDir: cache})
	if _, err := s.Search(context.Background(), root, "wal", 1); err != nil {
		t.Fatal(err)
	}
	_, fp, _ := scan(context.Background(), root, Options{})
	if loadIndex(cache, root, fp+1) != nil {
		t.Error("an index of another fingerprint was loaded")
	}
	if loadIndex("", root, fp) != nil || loadIndex(cache, filepath.Join(root, "other"), fp) != nil {
		t.Error("a missing index must load as nil")
	}
	if err := os.WriteFile(cachePath(cache, root), []byte("not gob"), 0o600); err != nil {
		t.Fatal(err)
	}
	if loadIndex(cache, root, fp) != nil {
		t.Error("a damaged index was loaded")
	}
	hits, err := NewSearcher(Options{CacheDir: cache}).Search(context.Background(), root, "wal", 1)
	if err != nil || len(hits) != 1 {
		t.Errorf("a damaged index must be rebuilt: %v %v", hits, err)
	}
}

func TestDiskIndexHoldsNoSourceText(t *testing.T) {
	root, cache := t.TempDir(), t.TempDir()
	write(t, root, "a.txt", "password hunter2 lives here\n")
	if _, err := NewSearcher(Options{CacheDir: cache}).Search(context.Background(), root, "password", 1); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(cachePath(cache, root))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "lives here") {
		t.Error("the index on disk holds a line of the source")
	}
}

func TestSavingIsBestEffort(t *testing.T) {
	root := project(t)
	ix, _ := Build(context.Background(), root, Options{})
	saveIndex("", ix)
	blocker := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	saveIndex(filepath.Join(blocker, "sub"), ix) // a folder that cannot be made
	readOnly := t.TempDir()
	if err := os.Chmod(readOnly, 0o500); err != nil {
		t.Fatal(err)
	}
	saveIndex(readOnly, ix) // a folder that cannot be written (or can, as root)
	_ = os.Chmod(readOnly, 0o700)
	createTemp = func(string, string) (*os.File, error) { return nil, os.ErrPermission }
	saveIndex(t.TempDir(), ix) // no temp file can be made
	createTemp = os.CreateTemp
	dir := t.TempDir()
	if err := os.Mkdir(cachePath(dir, root), 0o700); err != nil {
		t.Fatal(err)
	}
	saveIndex(dir, ix) // the target is a folder: the rename fails and the temp file is removed
	left, _ := filepath.Glob(filepath.Join(dir, "*.tmp"))
	if len(left) != 0 {
		t.Errorf("temp files left behind: %v", left)
	}
}

func TestSemanticVectorsSurviveARestartAndFollowTheSwitch(t *testing.T) {
	root, cache := semanticProject(t), t.TempDir()
	ctx := context.Background()
	plain := NewSearcher(Options{CacheDir: cache})
	if _, err := plain.Search(ctx, root, "roll", 3); err != nil || plain.byAt[root].Sem != nil {
		t.Fatalf("a search without the switch must not learn vectors: %v", err)
	}
	_, fp, _ := scan(ctx, root, Options{})
	if ix := loadIndex(cache, root, fp); ix == nil || ix.Sem != nil {
		t.Fatal("the plain index on disk must hold no vectors")
	}
	learned := NewSearcher(Options{CacheDir: cache, Semantic: true})
	if _, err := learned.Search(ctx, root, "roll", 3); err != nil || learned.byAt[root].Sem == nil {
		t.Fatalf("the switch must learn the vectors for an index that lacks them: %v", err)
	}
	if ix := loadIndex(cache, root, fp); ix == nil || ix.Sem == nil || len(ix.Sem.Ctx) == 0 {
		t.Fatal("the vectors were not kept on disk")
	}
	again := NewSearcher(Options{CacheDir: cache, Semantic: true})
	hits, err := again.Search(ctx, root, "roll", 5)
	if err != nil || again.byAt[root].Sem == nil || len(again.byAt[root].Sem.Chunk) != len(again.byAt[root].Chunks) {
		t.Fatalf("restart: %v %v", hits, err)
	}
	var found bool
	for _, h := range hits {
		found = found || strings.HasSuffix(h.Path, "rotate.go")
	}
	if !found {
		t.Errorf("the reloaded vectors must still bring rotate.go in: %+v", hits)
	}
	off := NewSearcher(Options{CacheDir: cache})
	if _, err := off.Search(ctx, root, "roll", 3); err != nil || off.byAt[root].Sem != nil {
		t.Fatalf("switched off, the vectors on disk must not be used: %v", err)
	}
}
