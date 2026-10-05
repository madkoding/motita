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
