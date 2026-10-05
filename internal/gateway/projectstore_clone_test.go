package gateway

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestCloneGitRepoReusesExistingClone(t *testing.T) {
	dest := filepath.Join(t.TempDir(), "proj")
	if err := os.MkdirAll(dest, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"init", "-q"}, {"remote", "add", "origin", "https://example.com/a/b.git"}} {
		if out, err := exec.Command("git", append([]string{"-C", dest}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	if _, err := cloneGitRepo("https://example.com/a/b", dest, nil); err != nil {
		t.Fatalf("same origin should be reused: %v", err)
	}
	if _, err := cloneGitRepo("https://example.com/other/repo.git", dest, nil); err == nil {
		t.Fatal("a different repository in a non-empty folder must fail")
	}
}

func TestFreeCloneDirPicksNextFreeName(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "proj")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "x"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := freeCloneDir(dir, "https://example.com/a/b.git", nil); got != dir+"-2" {
		t.Fatalf("got %q, want %q", got, dir+"-2")
	}
	if got := freeCloneDir(filepath.Join(root, "new"), "https://example.com/a/b.git", nil); got != filepath.Join(root, "new") {
		t.Fatalf("a missing folder must be used as is, got %q", got)
	}
}

func TestOwnsProjectDirOnlyInsideTheWorkspace(t *testing.T) {
	root := t.TempDir()
	s := &Server{opts: Options{WorkspaceDir: root}}
	for dir, want := range map[string]bool{
		filepath.Join(root, "proj"):            true,
		filepath.Join(root, "a", "b"):          true,
		root:                                   false,
		filepath.Dir(root):                     false,
		filepath.Join(filepath.Dir(root), "x"): false,
		"":                                     false,
	} {
		if got := s.ownsProjectDir(dir); got != want {
			t.Errorf("ownsProjectDir(%q) = %v, want %v", dir, got, want)
		}
	}
	if (&Server{}).ownsProjectDir(filepath.Join(root, "proj")) {
		t.Error("with no workspace configured nothing is owned")
	}
}
