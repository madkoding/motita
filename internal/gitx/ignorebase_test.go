package gitx

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func excludeOf(t *testing.T, repo string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(repo, ".git", "info", "exclude"))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// TestWorktreeIgnoresWhatTheLanguageGeneratesEvenWithoutACommittedGitignore is the reported
// case: a project with no .gitignore of its own, whose worktree reported dependency folders
// and caches as changes the session made.
func TestWorktreeIgnoresWhatTheLanguageGeneratesEvenWithoutACommittedGitignore(t *testing.T) {
	ctx := context.Background()
	repo := newRepo(t)
	write(t, filepath.Join(repo, "package.json"), "{}\n")
	write(t, filepath.Join(repo, "pyproject.toml"), "\n")
	git(t, repo, "add", ".")
	git(t, repo, "commit", "-qm", "manifests")
	wt := filepath.Join(filepath.Dir(repo), "worktrees", "s1")
	addWorktree(t, repo, wt, "motita/s1")

	write(t, filepath.Join(wt, "node_modules", "x", "i.js"), "x")
	write(t, filepath.Join(wt, "pkg", "__pycache__", "m.pyc"), "x")
	write(t, filepath.Join(wt, ".DS_Store"), "x")
	write(t, filepath.Join(wt, "real.txt"), "work")

	changes, err := WorkingTreeChangeList(ctx, wt)
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) != 1 || changes[0].Path != "real.txt" {
		t.Fatalf("only the file the session wrote is a change, got %+v", changes)
	}
}

// TestIgnoreBaseIsPerEcosystem: a Go project gets no node rules, so a folder it does track is
// not hidden.
func TestIgnoreBaseIsPerEcosystem(t *testing.T) {
	repo := newRepo(t)
	write(t, filepath.Join(repo, "go.mod"), "module x\n")
	if err := EnsureIgnoreBase(context.Background(), repo); err != nil {
		t.Fatal(err)
	}
	got := excludeOf(t, repo)
	if !strings.Contains(got, "*.test") || strings.Contains(got, "node_modules/") {
		t.Fatalf("a Go project gets Go rules only:\n%s", got)
	}
}

// TestIgnoreBaseCarriesAnUncommittedGitignore: rules that live only in the project's checkout
// are what a fresh worktree lacks.
func TestIgnoreBaseCarriesAnUncommittedGitignore(t *testing.T) {
	ctx := context.Background()
	repo := newRepo(t)
	write(t, filepath.Join(repo, ".gitignore"), "secrets.local\n\n")
	wt := filepath.Join(filepath.Dir(repo), "worktrees", "s1")
	addWorktree(t, repo, wt, "motita/s1")
	write(t, filepath.Join(wt, "secrets.local"), "x")
	changes, err := WorkingTreeChangeList(ctx, wt)
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) != 0 {
		t.Fatalf("an ignored file of the project's own .gitignore is not a change: %+v", changes)
	}
}

// TestIgnoreBaseIsIdempotentAndKeepsTheUsersRules: rewriting the block never duplicates it and
// never touches what surrounds it, including a torn block with no end marker.
func TestIgnoreBaseIsIdempotentAndKeepsTheUsersRules(t *testing.T) {
	ctx := context.Background()
	repo := newRepo(t)
	write(t, filepath.Join(repo, "Cargo.toml"), "\n")
	path := filepath.Join(repo, ".git", "info", "exclude")
	write(t, path, "mine-no-newline")
	for i := 0; i < 2; i++ {
		if err := EnsureIgnoreBase(ctx, repo); err != nil {
			t.Fatal(err)
		}
	}
	got := excludeOf(t, repo)
	if strings.Count(got, ignoreBlockBegin) != 1 || !strings.HasPrefix(got, "mine-no-newline\n") || !strings.Contains(got, "target/") {
		t.Fatalf("one block, the user's line kept:\n%s", got)
	}
	// A rule the user adds after the block survives the next rewrite, and a change of
	// ecosystem rewrites the block in place.
	write(t, path, got+"after-block\n")
	write(t, filepath.Join(repo, "Gemfile"), "\n")
	if err := EnsureIgnoreBase(ctx, repo); err != nil {
		t.Fatal(err)
	}
	got = excludeOf(t, repo)
	if strings.Count(got, ignoreBlockBegin) != 1 || !strings.Contains(got, "after-block\n") || !strings.Contains(got, ".bundle/") {
		t.Fatalf("block rewritten in place:\n%s", got)
	}
	// Torn: begin marker, no end.
	write(t, path, "keep\n"+ignoreBlockBegin+"\nstale\n")
	if err := EnsureIgnoreBase(ctx, repo); err != nil {
		t.Fatal(err)
	}
	got = excludeOf(t, repo)
	if strings.Contains(got, "stale") || !strings.HasPrefix(got, "keep\n") || strings.Count(got, ignoreBlockBegin) != 1 {
		t.Fatalf("a torn block is replaced:\n%s", got)
	}
}

func TestIgnoreBaseFailures(t *testing.T) {
	ctx := context.Background()
	t.Run("not a repository", func(t *testing.T) {
		if err := EnsureIgnoreBase(ctx, t.TempDir()); err == nil {
			t.Fatal("no repository, no exclude file")
		}
	})
	t.Run("absolute common dir", func(t *testing.T) {
		// A worktree's --git-common-dir is absolute.
		repo := newRepo(t)
		wt := filepath.Join(filepath.Dir(repo), "worktrees", "s1")
		addWorktree(t, repo, wt, "motita/s1")
		if err := EnsureIgnoreBase(ctx, wt); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("exclude unreadable", func(t *testing.T) {
		repo := newRepo(t)
		p := filepath.Join(repo, ".git", "info", "exclude")
		_ = os.Remove(p)
		if err := os.MkdirAll(p, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := EnsureIgnoreBase(ctx, repo); err == nil {
			t.Fatal("a directory where the file should be is an error")
		}
	})
	t.Run("info dir cannot be made", func(t *testing.T) {
		repo := newRepo(t)
		info := filepath.Join(repo, ".git", "info")
		if err := os.RemoveAll(info); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(filepath.Join(repo, "nowhere"), info); err != nil {
			t.Fatal(err)
		}
		if err := EnsureIgnoreBase(ctx, repo); err == nil {
			t.Fatal("a dangling info link cannot be made into a directory")
		}
	})
	t.Run("exclude cannot be written", func(t *testing.T) {
		repo := newRepo(t)
		p := filepath.Join(repo, ".git", "info", "exclude")
		_ = os.Remove(p)
		if err := os.Symlink(filepath.Join(repo, "nowhere", "x"), p); err != nil {
			t.Fatal(err)
		}
		if err := EnsureIgnoreBase(ctx, repo); err == nil {
			t.Fatal("a dangling exclude link cannot be written")
		}
	})
	t.Run("gitignore deleted after status, or status failing, adds nothing", func(t *testing.T) {
		repo := newRepo(t)
		write(t, filepath.Join(repo, ".gitignore"), "a\n")
		git(t, repo, "add", ".gitignore")
		git(t, repo, "commit", "-qm", "ig")
		if err := os.Remove(filepath.Join(repo, ".gitignore")); err != nil {
			t.Fatal(err)
		}
		if got := uncommittedGitignore(ctx, repo); got != "" {
			t.Fatalf("an unreadable .gitignore contributes nothing, got %q", got)
		}
		failOnGit(t, "status")
		if got := uncommittedGitignore(ctx, repo); got != "" {
			t.Fatalf("a failing status contributes nothing, got %q", got)
		}
	})
}

// TestAddWorktreeSurvivesAnIgnoreBaseFailure: the base is advisory.
func TestAddWorktreeSurvivesAnIgnoreBaseFailure(t *testing.T) {
	repo := newRepo(t)
	info := filepath.Join(repo, ".git", "info")
	_ = os.RemoveAll(info)
	if err := os.Symlink(filepath.Join(repo, "nowhere"), info); err != nil {
		t.Fatal(err)
	}
	addWorktree(t, repo, filepath.Join(filepath.Dir(repo), "worktrees", "s1"), "motita/s1")
}
