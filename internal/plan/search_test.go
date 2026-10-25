package plan

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// searchTree writes a small tree to search: two text files, a binary one and a nested one.
func searchTree(t *testing.T) string {
	t.Helper()
	return searchTreeIn(t, t.TempDir())
}

// searchTreeIn is searchTree in the directory given: a test that hands the tree to a confined
// command must put it inside the workspace, since t.TempDir() lands under TMPDIR and a TMPDIR
// inside the user's home is a tree the sandbox keeps private.
func searchTreeIn(t *testing.T, dir string) string {
	t.Helper()
	write := func(name, body string) {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("a.txt", "alpha\nfunc main() {}\r\nomega\n")
	write("sub/b.go", "package b\nfunc helper() {}\n")
	write("bin.dat", "func\x00binary")
	return dir
}

// The Go search answers the way grep -R -n does, so the model reads the same shape on Windows.
func TestSearchFilesAnswersLikeGrep(t *testing.T) {
	dir := searchTree(t)
	ctx := context.Background()

	out, exit, err := searchFiles(ctx, `func \w+\(`, dir, false)
	if err != nil || exit != 0 {
		t.Fatalf("exit=%d err=%v", exit, err)
	}
	want := []string{
		filepath.Join(dir, "a.txt") + ":2:func main() {}",
		filepath.Join(dir, "sub", "b.go") + ":2:func helper() {}",
	}
	for _, w := range want {
		if !strings.Contains(out, w+"\n") {
			t.Errorf("missing %q in %q", w, out)
		}
	}
	if strings.Contains(out, "bin.dat") {
		t.Errorf("a binary file was searched: %q", out)
	}

	// A literal is not a pattern, and a single file is answered without its name.
	out, exit, _ = searchFiles(ctx, "main()", filepath.Join(dir, "a.txt"), true)
	if exit != 0 || out != "2:func main() {}\n" {
		t.Errorf("literal in one file: exit=%d %q", exit, out)
	}
	if _, exit, _ = searchFiles(ctx, "nowhere", dir, false); exit != 1 {
		t.Errorf("no match: exit=%d, want 1", exit)
	}
	if _, exit, err = searchFiles(ctx, "(", dir, false); exit != 2 || err == nil {
		t.Errorf("bad pattern: exit=%d err=%v", exit, err)
	}
	if _, exit, err = searchFiles(ctx, "x", filepath.Join(dir, "missing"), false); exit != 2 || err == nil {
		t.Errorf("missing path: exit=%d err=%v", exit, err)
	}
}

// A file too large to be worth reading is skipped, not read.
func TestSearchFilesSkipsAHugeFile(t *testing.T) {
	dir := t.TempDir()
	f, err := os.Create(filepath.Join(dir, "huge.log"))
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(maxSearchFileBytes + 1); err != nil {
		t.Fatal(err)
	}
	f.Close()
	if _, exit, _ := searchFiles(context.Background(), "", dir, false); exit != 1 {
		t.Errorf("exit=%d, want 1 (nothing searched)", exit)
	}
}

// What cannot be read is reported, and decides the exit only when nothing matched.
func TestSearchFilesReportsWhatItCouldNotRead(t *testing.T) {
	dir := searchTree(t)
	defer func(r func(string) ([]byte, error), w func(string, fs.WalkDirFunc) error) {
		searchReadFile, searchWalkDir = r, w
	}(searchReadFile, searchWalkDir)

	searchReadFile = func(string) ([]byte, error) { return nil, errors.New("denied") }
	out, exit, _ := searchFiles(context.Background(), "func", dir, false)
	if exit != 2 || !strings.Contains(out, "denied") {
		t.Errorf("unreadable files: exit=%d %q", exit, out)
	}

	searchReadFile = os.ReadFile
	searchWalkDir = func(root string, fn fs.WalkDirFunc) error {
		if err := fn(filepath.Join(root, "locked"), nil, errors.New("no access")); err != nil {
			return err
		}
		return filepath.WalkDir(root, fn)
	}
	out, exit, _ = searchFiles(context.Background(), "func", dir, false)
	if exit != 0 || !strings.Contains(out, "no access") {
		t.Errorf("an unreadable directory beside a match: exit=%d %q", exit, out)
	}

	// A stat that fails between the walk and the read is the same kind of failure.
	searchWalkDir = func(root string, fn fs.WalkDirFunc) error {
		return filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			if d != nil && d.Type().IsRegular() {
				os.Remove(p)
			}
			return fn(p, d, err)
		})
	}
	if _, exit, _ = searchFiles(context.Background(), "func", dir, false); exit != 2 {
		t.Errorf("a file gone before it was read: exit=%d, want 2", exit)
	}
}

// The walk stops on a cancelled context and once the output is past what the tool returns.
func TestSearchFilesStopsWhenThereIsNoPointGoingOn(t *testing.T) {
	dir := searchTree(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, exit, err := searchFiles(ctx, "func", dir, false); exit != 2 || err == nil {
		t.Errorf("cancelled: exit=%d err=%v", exit, err)
	}

	big := strings.Repeat("match\n", maxToolOutputBytes/6+10)
	for _, name := range []string{"1.txt", "2.txt"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(big), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	out, exit, _ := searchFiles(context.Background(), "match", dir, false)
	if exit != 0 || strings.Contains(out, "2.txt") {
		t.Errorf("the walk went on past the cap: exit=%d, has 2.txt=%v", exit, strings.Contains(out, "2.txt"))
	}
}

// On Windows the tool searches in Go and never runs grep.
func TestSearchInFilesSearchesNativelyWhereThereIsNoGrep(t *testing.T) {
	defer func(v bool) { searchNatively = v }(searchNatively)
	searchNatively = true
	work := t.TempDir()
	a, ex := makeAgentIn(t, true, work)
	dir := searchTreeIn(t, filepath.Join(work, "tree"))
	ex.defaultOutput = "grep was run"
	srv := llmServer(t, nil)
	defer srv.Close()
	p := New(newClient(t, srv), a)

	got := p.toolSearchInFiles(context.Background(), json.RawMessage(`{"pattern":"helper","path":`+
		mustJSON(t, dir)+`,"literal":"true"}`))
	if strings.Contains(got, "grep") || !strings.Contains(got, "func helper() {}") || !strings.HasSuffix(got, "[exit=0]") {
		t.Errorf("got %q", got)
	}
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
