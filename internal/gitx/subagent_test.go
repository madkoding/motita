package gitx

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

// TestABackgroundAgentWorksOnItsOwnBranchFromASnapshot: the worktree starts from the snapshot
// (uncommitted work included), its leftovers are committed on its own branch, and the branch
// outlives the worktree.
func TestABackgroundAgentWorksOnItsOwnBranchFromASnapshot(t *testing.T) {
	ctx := context.Background()
	repo := newRepo(t)
	write(t, filepath.Join(repo, "wip.txt"), "uncommitted\n")
	_, snap, err := Snapshot(ctx, repo, "refs/motita/test-sub")
	if err != nil || snap == "" {
		t.Fatalf("Snapshot = %q, %v", snap, err)
	}
	wt := filepath.Join(t.TempDir(), "nested", "tree")
	if err := AddWorktreeAt(ctx, repo, wt, "motita/sub/a1", snap); err != nil {
		t.Fatal(err)
	}
	if got := read(t, filepath.Join(wt, "wip.txt")); got != "uncommitted" {
		t.Fatalf("the worktree must hold the snapshot, wip.txt = %q", got)
	}
	if committed, err := CommitAll(ctx, wt, "nothing yet"); err != nil || committed {
		t.Fatalf("a clean tree has nothing to commit: %v %v", committed, err)
	}
	write(t, filepath.Join(wt, "docs.txt"), "written by the agent\n")
	if committed, err := CommitAll(ctx, wt, "the agent's work"); err != nil || !committed {
		t.Fatalf("CommitAll = %v, %v", committed, err)
	}
	if err := RemoveWorktree(ctx, repo, wt, true); err != nil {
		t.Fatal(err)
	}
	if !BranchExists(ctx, repo, "motita/sub/a1") {
		t.Fatal("the branch is the work: it must outlive the worktree")
	}
	if subject, _ := execute(ctx, repo, "log", "-1", "--format=%s", "motita/sub/a1"); subject != "the agent's work" {
		t.Errorf("last commit on the branch = %q", subject)
	}
}

func TestAddWorktreeAtReportsItsFailures(t *testing.T) {
	ctx := context.Background()
	repo := newRepo(t)
	if err := AddWorktreeAt(ctx, repo, filepath.Join(t.TempDir(), "t"), "b", strings.Repeat("0", 40)); err == nil ||
		!strings.Contains(err.Error(), "background agent's worktree") {
		t.Errorf("an unknown commit must be refused, got %v", err)
	}
	blocker := filepath.Join(t.TempDir(), "file")
	write(t, blocker, "not a directory")
	if err := AddWorktreeAt(ctx, repo, filepath.Join(blocker, "t"), "b", "HEAD"); err == nil {
		t.Error("a parent that cannot be created must be reported")
	}
}

func TestCommitAllReportsEachStepThatFails(t *testing.T) {
	ctx := context.Background()
	for _, needle := range []string{"add -A", "diff --cached", "commit -q"} {
		t.Run(needle, func(t *testing.T) {
			repo := newRepo(t)
			write(t, filepath.Join(repo, "x.txt"), "x\n")
			failOnGit(t, needle)
			if committed, err := CommitAll(ctx, repo, "m"); err == nil || committed {
				t.Errorf("a failing %q must be reported: %v %v", needle, committed, err)
			}
		})
	}
}
