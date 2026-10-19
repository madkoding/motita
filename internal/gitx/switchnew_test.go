package gitx

import (
	"context"
	"path/filepath"
	"testing"
)

func TestSwitchNewCreatesTheBranchAndCarriesUncommittedWork(t *testing.T) {
	repo := newRepo(t)
	write(t, filepath.Join(repo, "wip.txt"), "uncommitted\n")
	ctx := context.Background()
	if err := SwitchNew(ctx, repo, "feat/x"); err != nil {
		t.Fatalf("SwitchNew: %v", err)
	}
	if got := Display(ctx, repo); got != "feat/x" {
		t.Errorf("branch = %q", got)
	}
	if n, _ := WorkingTreeChanges(ctx, repo); n != 1 {
		t.Errorf("the uncommitted file must come along, changes = %d", n)
	}
	git(t, repo, "checkout", "-q", "main")
	if err := SwitchNew(ctx, repo, "feat/x"); err != nil || Display(ctx, repo) != "feat/x" {
		t.Errorf("an existing branch is switched to: %v on %q", err, Display(ctx, repo))
	}
}

func TestDefaultBranchPrefersOriginsHead(t *testing.T) {
	ctx := context.Background()
	repo := newRepo(t)
	if got := DefaultBranch(ctx, repo); got != "main" {
		t.Errorf("no origin: main, got %q", got)
	}
	pushToOrigin(t, repo)
	git(t, repo, "remote", "set-head", "origin", "main")
	if got := DefaultBranch(ctx, repo); got != "main" {
		t.Errorf("origin/HEAD, got %q", got)
	}
	empty := t.TempDir()
	git(t, empty, "init", "-q", "-b", "trunk")
	if got := DefaultBranch(ctx, empty); got != "" {
		t.Errorf("nothing to name, got %q", got)
	}
}
