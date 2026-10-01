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

// Reported from a real session: `.npm/` showed up among the files the session had changed. The
// sandbox runs every command with HOME set to the working directory, so what npm, pip, cargo and
// the rest keep in their HOME lands inside the project. It is not the user's work and never goes
// to the repository.
func TestWhatToolsWriteInTheirHomeIsNotAChange(t *testing.T) {
	ctx := context.Background()
	repo := newRepo(t)
	write(t, filepath.Join(repo, "package.json"), "{}\n")
	git(t, repo, "add", ".")
	git(t, repo, "commit", "-qm", "manifest")
	wt := filepath.Join(filepath.Dir(repo), "worktrees", "s1")
	addWorktree(t, repo, wt, "motita/s1")

	write(t, filepath.Join(wt, ".npm", "_cacache", "index-v5", "ab", "cd"), "x")
	write(t, filepath.Join(wt, ".npm", "_logs", "2026-debug.log"), "x")
	write(t, filepath.Join(wt, ".cache", "pip", "http", "x"), "x")
	write(t, filepath.Join(wt, ".local", "share", "pnpm", "x"), "x")
	write(t, filepath.Join(wt, ".bash_history"), "x")
	write(t, filepath.Join(wt, "src", ".cache", "mine.txt"), "the project's own") // deeper: the project's
	write(t, filepath.Join(wt, ".cargo", "config.toml"), "[build]\n")             // committed by many projects
	write(t, filepath.Join(wt, "real.txt"), "work")

	changes, err := WorkingTreeChangeList(ctx, wt)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, c := range changes {
		got = append(got, c.Path)
	}
	want := map[string]bool{"real.txt": true, "src/.cache/mine.txt": true, ".cargo/config.toml": true}
	if len(got) != len(want) {
		t.Fatalf("changes = %v, want only %v", got, want)
	}
	for _, p := range got {
		if !want[p] {
			t.Errorf("%q is not the user's work: %v", p, got)
		}
	}
	if n, _ := WorkingTreeChanges(ctx, wt); n != 3 {
		t.Errorf("the count must agree with the list: %d, want 3", n)
	}
	files, err := ChangesSince(ctx, wt, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		if IsToolHome(f.Path) {
			t.Errorf("the run's summary lists %q", f.Path)
		}
	}
}

// A project that has no worktree (a session working in the project's own directory) never gets the
// exclude block, so the readers must not depend on it.
func TestToolHomeIsFilteredEvenWithoutTheExcludeBlock(t *testing.T) {
	ctx := context.Background()
	repo := newRepo(t)
	write(t, filepath.Join(repo, ".npm", "_cacache", "x"), "x")
	write(t, filepath.Join(repo, "real.txt"), "work")
	changes, _ := WorkingTreeChangeList(ctx, repo)
	if len(changes) != 1 || changes[0].Path != "real.txt" {
		t.Fatalf("changes = %+v", changes)
	}
	if n, _ := WorkingTreeChanges(ctx, repo); n != 1 {
		t.Errorf("count = %d, want 1", n)
	}
	files, _ := ChangesSince(ctx, repo, "")
	for _, f := range files {
		if IsToolHome(f.Path) {
			t.Errorf("the summary lists %q", f.Path)
		}
	}
	if len(files) == 0 {
		t.Error("the summary must still list the real work")
	}
}

// A file the project TRACKS is the project's even when it sits where a tool would write; and a
// rename out of or into a tool's home is not reported either.
func TestIsToolHomeAndRenames(t *testing.T) {
	for p, want := range map[string]bool{
		".npm": true, ".npm/_cacache/x": true, "./.npm/x": true, ".cache/pip/x": true, ".local/share/a": true,
		".bash_history": true, ".cargo/registry/x": true,
		".cargo/config.toml": false, "src/.cache/x": false, ".npmrc": false, ".npmx/y": false, "a/.npm/x": false,
		".yarn/releases/yarn.cjs": false, ".config/app.json": false, "npm/x": false,
	} {
		if got := IsToolHome(p); got != want {
			t.Errorf("IsToolHome(%q) = %v, want %v", p, got, want)
		}
	}
	// `R  .npm/a -> real.txt`: git lists the new path then the old one as its own record.
	out := "R  real.txt\x00.npm/a\x00 M other.txt\x00R  .npm/b\x00.npm/c\x00"
	got := parseChangeList(out)
	if len(got) != 2 || got[0].Path != "real.txt" || got[0].From != ".npm/a" || got[1].Path != "other.txt" {
		t.Errorf("parseChangeList = %+v", got)
	}
}

// A rename shows as `old -> new` in the count and as its own record in the list; and a file in a
// tool's home that git TRACKED (committed before anyone noticed) is still not reported as work.
func TestRenamesAndTrackedFilesInAToolHome(t *testing.T) {
	ctx := context.Background()
	repo := newRepo(t)
	write(t, filepath.Join(repo, ".npm", "tracked"), "old")
	write(t, filepath.Join(repo, "keep.txt"), "keep")
	git(t, repo, "add", "-f", ".")
	git(t, repo, "commit", "-qm", "tracked a cache by mistake")

	// Staged renames: one INTO a tool's home, one OUT of it.
	git(t, repo, "mv", "keep.txt", ".npm/moved")
	git(t, repo, "mv", ".npm/tracked", "out.txt")
	write(t, filepath.Join(repo, "real.txt"), "work")
	git(t, repo, "add", "real.txt")

	if n, _ := WorkingTreeChanges(ctx, repo); n != 2 { // out.txt (the rename out of .npm) + real.txt
		t.Errorf("count = %d, want 2", n)
	}
	for _, f := range mustChangesSince(t, repo) {
		if IsToolHome(f.Path) {
			t.Errorf("the summary lists %q", f.Path)
		}
	}
}

func mustChangesSince(t *testing.T, dir string) []FileChange {
	t.Helper()
	files, err := ChangesSince(context.Background(), dir, "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	return files
}
