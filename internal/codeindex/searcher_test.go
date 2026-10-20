package codeindex

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"
)

// gateEmbedder holds its first call until release is closed: a long embedding of one folder.
type gateEmbedder struct {
	mu      sync.Mutex
	calls   int
	started chan struct{}
	release chan struct{}
}

func (g *gateEmbedder) Embed(_ context.Context, texts []string) ([][]float32, error) {
	g.mu.Lock()
	g.calls++
	first := g.calls == 1
	g.mu.Unlock()
	if first {
		close(g.started)
		<-g.release
	}
	out := make([][]float32, len(texts))
	for i := range out {
		out[i] = []float32{1}
	}
	return out, nil
}

// doneSpy says when a search first asks for Done: the moment it starts to wait.
type doneSpy struct {
	context.Context
	asked chan struct{}
	once  sync.Once
}

func (d *doneSpy) Done() <-chan struct{} {
	d.once.Do(func() { close(d.asked) })
	return d.Context.Done()
}

func gatedSearcher(t *testing.T) (*Searcher, *gateEmbedder, string, chan error) {
	t.Helper()
	slow := t.TempDir()
	write(t, slow, "a.go", "package a\nfunc alpha() {}\n")
	g := &gateEmbedder{started: make(chan struct{}), release: make(chan struct{})}
	s := NewSearcher(Options{EmbedURL: "http://x", CacheDir: t.TempDir()})
	s.embedder = g
	done := make(chan error, 1)
	go func() {
		_, err := s.Search(context.Background(), slow, "alpha", 5)
		done <- err
	}()
	<-g.started
	return s, g, slow, done
}

// TestABuildDoesNotHoldUpOtherSearches: one searcher serves every session, so the build of one
// folder (embedding thousands of windows) must not make a search of another folder wait, and a
// search of the same folder waits only as long as its caller lets it.
func TestABuildDoesNotHoldUpOtherSearches(t *testing.T) {
	s, g, slow, done := gatedSearcher(t)
	other := t.TempDir()
	write(t, other, "b.go", "package b\nfunc beta() {}\n")
	answered := make(chan error, 1)
	go func() {
		hits, err := s.Search(context.Background(), other, "beta", 5)
		if err == nil && len(hits) == 0 {
			err = errors.New("no hit")
		}
		answered <- err
	}()
	select {
	case err := <-answered:
		if err != nil {
			t.Fatalf("another folder: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("a search of another folder waited for the build of the first one")
	}
	ctx, cancel := context.WithCancel(context.Background())
	waited := make(chan error, 1)
	go func() {
		_, err := s.Search(ctx, slow, "alpha", 5)
		waited <- err
	}()
	cancel()
	select {
	case err := <-waited:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("a cancelled wait = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("a cancelled search kept waiting for the build")
	}
	// A search that waits for the build is answered by it once it ends.
	spy := &doneSpy{Context: context.Background(), asked: make(chan struct{})}
	patient := make(chan error, 1)
	go func() {
		_, err := s.Search(spy, slow, "alpha", 5)
		patient <- err
	}()
	<-spy.asked
	close(g.release)
	if err := <-done; err != nil {
		t.Fatalf("the build = %v", err)
	}
	if err := <-patient; err != nil || s.byAt[slow] == nil {
		t.Errorf("a search that waited = %v", err)
	}
	// A search of the same folder after the build reuses it.
	if _, err := s.Search(context.Background(), slow, "alpha", 5); err != nil || len(s.building) != 0 {
		t.Errorf("after the build: %v %v", err, s.building)
	}
}

// TestABuildWithAnOldEmbedderIsNotKept: the embeddings were switched on while a build ran, so its
// index (without the new vectors) is answered once and not kept.
func TestABuildWithAnOldEmbedderIsNotKept(t *testing.T) {
	s, g, slow, done := gatedSearcher(t)
	s.mu.Lock()
	s.gen++
	s.mu.Unlock()
	close(g.release)
	if err := <-done; err != nil || s.byAt[slow] != nil {
		t.Errorf("a build of another embedder must not be kept: %v", err)
	}
}

// TestTheSearcherKeepsAFewFolders: every background agent searches a worktree of its own, so the
// searcher keeps only the folders searched last, and forgets a folder that is gone along with its
// file on disk.
func TestTheSearcherKeepsAFewFolders(t *testing.T) {
	cache := t.TempDir()
	s := NewSearcher(Options{CacheDir: cache})
	var roots []string
	for i := range keepRoots + 1 {
		root := t.TempDir()
		write(t, root, "f.go", "func Thing"+strconv.Itoa(i)+"() {}\n")
		roots = append(roots, root)
		if _, err := s.Search(context.Background(), root, "thing", 1); err != nil {
			t.Fatal(err)
		}
	}
	if len(s.byAt) != keepRoots || s.byAt[roots[0]] != nil {
		t.Fatalf("kept %d folders, the oldest kept: %v", len(s.byAt), s.byAt[roots[0]] != nil)
	}
	if _, err := os.Stat(cachePath(cache, roots[0])); err != nil {
		t.Errorf("a folder that still exists keeps its file on disk: %v", err)
	}
	// Searching an older folder again makes it the most recent.
	if _, err := s.Search(context.Background(), roots[1], "thing", 1); err != nil {
		t.Fatal(err)
	}
	gone := roots[2]
	if err := os.RemoveAll(gone); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Search(context.Background(), roots[3], "thing", 1); err != nil {
		t.Fatal(err)
	}
	if _, kept := s.byAt[gone]; kept || s.byAt[roots[1]] == nil {
		t.Errorf("a removed folder must be forgotten and a recent one kept: %v", s.used)
	}
	if _, err := os.Stat(cachePath(cache, gone)); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the file of a removed folder must go: %v", err)
	}
	// Without a cache directory there is no file to remove.
	plain := NewSearcher(Options{})
	root := filepath.Join(t.TempDir(), "w")
	write(t, root, "f.go", "func Thing() {}\n")
	if _, err := plain.Search(context.Background(), root, "thing", 1); err != nil {
		t.Fatal(err)
	}
	_ = os.RemoveAll(root)
	if _, err := plain.Search(context.Background(), roots[4], "thing", 1); err != nil || plain.byAt[root] != nil {
		t.Errorf("without a cache: %v", err)
	}
}
