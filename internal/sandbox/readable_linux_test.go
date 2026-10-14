package sandbox

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sync/atomic"
	"testing"
)

// TestReadableFollowsTheSameRuleAsTheConfinement: motita's own read tools answer what a confined
// `cat` could read - the workspace, the tools, the system - and never another place under a
// private tree or a hidden file, unless the whole disk is allowed (and even then not the secrets).
func TestReadableFollowsTheSameRuleAsTheConfinement(t *testing.T) {
	base, tools := t.TempDir(), t.TempDir()
	home := "/home/someone"
	secret := "/etc/motita-secret.yaml"
	s := &Sandbox{op: Options{GitHome: home, ToolsDir: tools, HiddenPaths: []string{secret}},
		base: base, fullDisk: new(atomic.Bool)}

	for path, want := range map[string]bool{
		filepath.Join(base, "src", "main.go"):            true,
		filepath.Join(tools, "tools", "go", "bin", "go"): true,
		filepath.Join(tools, "bin", "gh"):                true,
		filepath.Join(tools, "home", ".npmrc"):           true,
		filepath.Join(tools, "homes", "other", ".npmrc"): false,
		filepath.Join(tools, "worktrees", "x", "a.go"):   false,
		"/etc/os-release":                                true,
		"/home/other/project/main.go":                    false,
		filepath.Join(home, ".bashrc"):                   false,
		filepath.Join(home, ".gitconfig"):                true,
		secret:                                           false,
		"/root/.ssh/id_ed25519":                          false,
		"/mnt/backups/" + filepath.Base(base) + "/x":     false,
		filepath.Join(base, "..", filepath.Base(base)):   true,
	} {
		if got := s.Readable(path); got != want {
			t.Errorf("workspace mode, %s: got %v, want %v", path, got, want)
		}
	}

	s.SetFullDisk(true)
	for path, want := range map[string]bool{
		"/home/other/project/main.go":  true,
		filepath.Join(home, ".bashrc"): true,
		secret:                         false,
	} {
		if got := s.Readable(path); got != want {
			t.Errorf("whole disk, %s: got %v, want %v", path, got, want)
		}
	}
}

func TestPathUnder(t *testing.T) {
	if !pathUnder("/a/b", []string{"/"}) || !pathUnder("/a", []string{"/a/"}) || pathUnder("/ab", []string{"/a"}) {
		t.Error("pathUnder must match a root, its children and nothing that only shares a prefix")
	}
}

// TestWithDirWritesItsOwnWorktreesGitDirectory: a background agent's sandbox, in a worktree of its
// own, gets ITS worktree's git directory, not the parent's - or its commits fail and it can write
// the parent's index.
func TestWithDirWritesItsOwnWorktreesGitDirectory(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	repo := t.TempDir()
	git := func(dir string, args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t",
			"GIT_COMMITTER_EMAIL=t@t")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	git(repo, "init", "-q")
	git(repo, "commit", "-q", "--allow-empty", "-m", "init")
	parent, child := filepath.Join(t.TempDir(), "parent"), filepath.Join(t.TempDir(), "child")
	git(repo, "worktree", "add", "-q", "-b", "p", parent)
	git(repo, "worktree", "add", "-q", "-b", "c", child)

	s := &Sandbox{base: parent, gitRoots: gitDirs(parent), fullDisk: new(atomic.Bool)}
	c := s.WithDir(child)
	if len(c.gitRoots) == 0 || !slices.Equal(c.gitRoots, gitDirs(child)) {
		t.Fatalf("the copy must write its own worktree's git directory: %v", c.gitRoots)
	}
	if c.gitRoots[0] == s.gitRoots[0] {
		t.Errorf("the copy still writes the parent's: %s", c.gitRoots[0])
	}
}
