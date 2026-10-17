package codeindex

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func write(t *testing.T, root, rel, body string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func project(t *testing.T) string {
	root := t.TempDir()
	write(t, root, "wal/segment.go",
		"package wal\n\n// rotates the log\nfunc RotateWalSegment(s *Segment) error {\n\treturn s.close()\n}\n")
	write(t, root, "wal/other.go", "package wal\n\nfunc caller() { RotateWalSegment(nil) }\n")
	write(t, root, "docs/readme.md", "How the cache evicts old entries.\n")
	write(t, root, "node_modules/lib/x.js", "function RotateWalSegment() {}\n")
	write(t, root, ".env", "SECRET=RotateWalSegment\n")
	write(t, root, ".git/config", "RotateWalSegment\n")
	write(t, root, "bin.dat", "RotateWalSegment\x00\x01")
	write(t, root, "empty.txt", "")
	return root
}

func TestTokensSplitIdentifiersAndDropStopWords(t *testing.T) {
	got := strings.Join(tokens("how do we rotateWalSegment, snake_case_name 42"), " ")
	want := "rotatewalsegment rotate wal segment snakecasename snake case name 42"
	if got != want {
		t.Errorf("tokens = %q, want %q", got, want)
	}
	if tokens("the of a") != nil && len(tokens("the of a")) != 0 {
		t.Error("stop words kept")
	}
	if got := splitIdentifier("__"); len(got) != 0 {
		t.Errorf("empty identifier = %v", got)
	}
}

func TestSearchRanksTheDefinitionFirstAndSkipsNoise(t *testing.T) {
	root := project(t)
	ix, err := Build(context.Background(), root, Options{})
	if err != nil {
		t.Fatal(err)
	}
	hits := ix.Search("where do we rotate the wal segment", 0)
	if len(hits) != 2 || hits[0].Path != "wal/segment.go" || hits[0].Line != 4 {
		t.Fatalf("hits = %+v", hits)
	}
	if !strings.Contains(hits[0].Snippet, "func RotateWalSegment") || hits[0].Score <= hits[1].Score {
		t.Errorf("first hit: %+v / %+v", hits[0], hits[1])
	}
	for _, h := range ix.Search("RotateWalSegment", 20) {
		if strings.Contains(h.Path, "node_modules") || strings.HasPrefix(h.Path, ".") || h.Path == "bin.dat" {
			t.Errorf("indexed what must be skipped: %s", h.Path)
		}
	}
	if got := ix.Search("cache evicts", 1); len(got) != 1 || got[0].Path != "docs/readme.md" {
		t.Errorf("prose: %+v", got)
	}
	if ix.Search("zzzunknown", 5) != nil || ix.Search("the of", 5) != nil || (&Index{}).Search("x", 1) != nil {
		t.Error("a query with no match or no terms must return nothing")
	}
}

func TestSearchLimitAndOverlappingWindows(t *testing.T) {
	root := t.TempDir()
	var sb strings.Builder
	for i := 0; i < 200; i++ {
		sb.WriteString("filler line\n")
		if i%50 == 0 {
			sb.WriteString("needle " + strings.Repeat("x", 300) + "\n")
		}
	}
	write(t, root, "big.txt", sb.String())
	ix, _ := Build(context.Background(), root, Options{})
	hits := ix.Search("needle", 2)
	if len(hits) != 2 {
		t.Fatalf("limit not applied: %+v", hits)
	}
	if !strings.HasSuffix(strings.Split(hits[0].Snippet, "\n")[0], "…") {
		t.Errorf("long lines are clipped: %q", hits[0].Snippet)
	}
	if got := ix.Search("needle", 999); len(got) > MaxLimit {
		t.Errorf("limit above the cap: %d", len(got))
	}
}

func TestSnippetStopsAtTheEndOfTheWindow(t *testing.T) {
	root := t.TempDir()
	write(t, root, "a.txt", "alpha\nbeta\n")
	ix, _ := Build(context.Background(), root, Options{})
	hits := ix.Search("beta", 1)
	if len(hits) != 1 || hits[0].Line != 2 || hits[0].Snippet != "beta\n" && hits[0].Snippet != "beta" {
		t.Errorf("hits = %+v", hits)
	}
}

func TestOptionsBoundWhatIsIndexed(t *testing.T) {
	root := t.TempDir()
	write(t, root, "a.txt", "needle")
	write(t, root, "b.txt", "needle")
	write(t, root, "big.txt", "needle "+strings.Repeat("y", 3000))
	ix, _ := Build(context.Background(), root, Options{MaxFileKB: 1, MaxFiles: 1})
	if got := ix.Search("needle", 5); len(got) != 1 || got[0].Path != "a.txt" {
		t.Errorf("hits = %+v", got)
	}
	if (Options{}).maxBytes() != DefaultMaxFileKB*1024 || (Options{}).maxFiles() != DefaultMaxFiles {
		t.Error("zero options must take the defaults")
	}
}

func TestSymlinksAreNotFollowed(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	write(t, outside, "secret.txt", "needle")
	write(t, outside, "dir/x.txt", "needle")
	if err := os.Symlink(filepath.Join(outside, "secret.txt"), filepath.Join(root, "link.txt")); err != nil {
		t.Skip("no symlinks here:", err)
	}
	if err := os.Symlink(filepath.Join(outside, "dir"), filepath.Join(root, "linkdir")); err != nil {
		t.Fatal(err)
	}
	ix, _ := Build(context.Background(), root, Options{})
	if got := ix.Search("needle", 5); len(got) != 0 {
		t.Errorf("followed a link out of the folder: %+v", got)
	}
}

func TestBuildHonoursCancellationAndMissingRoots(t *testing.T) {
	root := project(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Build(ctx, root, Options{}); err == nil {
		t.Error("a cancelled build must fail")
	}
	if _, err := buildFrom(ctx, root, []fileInfo{{rel: "x"}}, 0); err == nil {
		t.Error("a cancelled read loop must fail")
	}
	ix, err := Build(context.Background(), filepath.Join(root, "missing"), Options{})
	if err != nil || len(ix.Search("anything", 3)) != 0 {
		t.Errorf("a missing folder is an empty index: %v", err)
	}
}

func TestBuildSkipsUnreadableFiles(t *testing.T) {
	root := t.TempDir()
	write(t, root, "a.txt", "needle")
	ix, err := buildFrom(context.Background(), root, []fileInfo{{rel: "gone.txt"}, {rel: "a.txt"}}, 0)
	if err != nil || len(ix.Files) != 1 {
		t.Fatalf("files = %v err = %v", ix.Files, err)
	}
}

func TestSearcherReusesAndRebuilds(t *testing.T) {
	root := project(t)
	s := NewSearcher(Options{})
	first, err := s.Search(context.Background(), root, "rotate wal", 1)
	if err != nil || len(first) != 1 {
		t.Fatalf("first: %v %v", first, err)
	}
	built := s.byAt[root]
	if _, err := s.Search(context.Background(), root, "cache", 1); err != nil || s.byAt[root] != built {
		t.Error("an unchanged folder must reuse its index")
	}
	write(t, root, "new.go", "func BrandNewThing() {}\n")
	later := time.Now().Add(time.Hour)
	_ = os.Chtimes(filepath.Join(root, "new.go"), later, later)
	hits, _ := s.Search(context.Background(), root, "brand new thing", 1)
	if len(hits) != 1 || hits[0].Path != "new.go" || s.byAt[root] == built {
		t.Errorf("a changed folder must be re-read: %+v", hits)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := s.Search(ctx, root, "x", 1); err == nil {
		t.Error("a cancelled scan must fail")
	}
	write(t, root, "later.go", "func Later() {}\n")
	_ = os.Chtimes(filepath.Join(root, "later.go"), later.Add(time.Hour), later.Add(time.Hour))
	if _, err := s.Search(ctx, root, "x", 1); err == nil {
		t.Error("a cancelled rebuild must fail")
	}
}

func TestSharedIsPerOptions(t *testing.T) {
	a, b := Shared(Options{MaxFiles: 7}), Shared(Options{MaxFiles: 7})
	if a != b || a == Shared(Options{MaxFiles: 8}) {
		t.Error("Shared must return one searcher per options")
	}
}

func TestFormat(t *testing.T) {
	if got := Format(`say "hi"`, nil); !strings.Contains(got, `"say 'hi'"`) || !strings.Contains(got, "search_in_files") {
		t.Errorf("empty: %q", got)
	}
	got := Format("q", []Hit{{Path: "a.go", Line: 3, Snippet: "x"}, {Path: "b.go", Line: 1, Snippet: "y"}})
	if got != "a.go:3\nx\n---\nb.go:1\ny" {
		t.Errorf("format = %q", got)
	}
}

func TestOverlappingWindowsDoNotRepeatAPassage(t *testing.T) {
	root := t.TempDir()
	write(t, root, "a.txt", strings.Repeat("filler\n", 34)+"needle\n"+strings.Repeat("filler\n", 30))
	ix, _ := Build(context.Background(), root, Options{})
	if len(ix.Chunks) < 2 {
		t.Fatalf("expected overlapping windows, got %d", len(ix.Chunks))
	}
	if got := ix.Search("needle", 5); len(got) != 1 || got[0].Line != 35 {
		t.Errorf("hits = %+v", got)
	}
}

// countdownCtx stays alive for the first n calls to Err, then reports cancellation: the way to
// cancel a search after its scan finished and before its index is built.
type countdownCtx struct {
	context.Context
	left int
}

func (c *countdownCtx) Err() error {
	if c.left <= 0 {
		return context.Canceled
	}
	c.left--
	return nil
}

func TestSearcherStopsWhenCancelledBetweenScanAndBuild(t *testing.T) {
	root := project(t)
	counter := &countdownCtx{Context: context.Background(), left: 1 << 20}
	if _, _, err := scan(counter, root, Options{}); err != nil {
		t.Fatal(err)
	}
	calls := 1<<20 - counter.left
	s := NewSearcher(Options{})
	if _, err := s.Search(&countdownCtx{Context: context.Background(), left: calls}, root, "x", 1); err == nil {
		t.Error("a build cancelled after the scan must fail")
	}
}
