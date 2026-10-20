package gitx

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

// upstreamCommit pushes a commit to origin's main from another clone, which repo does not have.
func upstreamCommit(t *testing.T, repo, remote, file string) {
	t.Helper()
	elsewhere := filepath.Join(t.TempDir(), "elsewhere")
	git(t, filepath.Dir(repo), "clone", "-q", remote, elsewhere)
	git(t, elsewhere, "config", "user.email", "other@example.com")
	git(t, elsewhere, "config", "user.name", "Other")
	write(t, filepath.Join(elsewhere, file), "from upstream\n")
	git(t, elsewhere, "add", file)
	git(t, elsewhere, "commit", "-qm", "upstream change")
	git(t, elsewhere, "push", "-q", "origin", "main")
}

func TestRefreshBranchMovesACleanCheckoutOnTheBranchForward(t *testing.T) {
	repo := newRepo(t)
	remote := pushToOrigin(t, repo)
	upstreamCommit(t, repo, remote, "upstream.txt")
	if err := RefreshBranch(context.Background(), repo, "main"); err != nil {
		t.Fatalf("RefreshBranch: %v", err)
	}
	if !exists(filepath.Join(repo, "upstream.txt")) {
		t.Error("the checkout on the branch must have the upstream commit")
	}
}

func TestBranchCommitNamesWhereTheBranchPoints(t *testing.T) {
	repo := newRepo(t)
	ctx := context.Background()
	before := BranchCommit(ctx, repo, "main")
	if len(before) < 40 {
		t.Fatalf("main points at %q", before)
	}
	remote := pushToOrigin(t, repo)
	upstreamCommit(t, repo, remote, "upstream.txt")
	if err := RefreshBranch(ctx, repo, "main"); err != nil {
		t.Fatal(err)
	}
	if after := BranchCommit(ctx, repo, "main"); after == before || after == "" {
		t.Errorf("main did not move: %q", after)
	}
	if got := BranchCommit(ctx, repo, "nope"); got != "" {
		t.Errorf("a missing branch points at %q", got)
	}
}

func TestRefreshBranchAdvancesTheBranchWithoutLeavingTheCurrentOne(t *testing.T) {
	repo := newRepo(t)
	remote := pushToOrigin(t, repo)
	git(t, repo, "checkout", "-q", "-b", "feature")
	write(t, filepath.Join(repo, "wip.txt"), "uncommitted\n")
	upstreamCommit(t, repo, remote, "upstream.txt")
	if err := RefreshBranch(context.Background(), repo, "main"); err != nil {
		t.Fatalf("RefreshBranch: %v", err)
	}
	if got := git(t, repo, "rev-parse", "--abbrev-ref", "HEAD"); got != "feature" {
		t.Errorf("the checkout moved to %q, want it left on feature", got)
	}
	if !strings.Contains(git(t, repo, "log", "main", "--format=%s"), "upstream change") {
		t.Error("main must have advanced to origin's commit")
	}
	if !exists(filepath.Join(repo, "wip.txt")) || exists(filepath.Join(repo, "upstream.txt")) {
		t.Error("the working tree of the other branch must be untouched")
	}
}

func TestRefreshBranchLeavesDirtyWorkAlone(t *testing.T) {
	repo := newRepo(t)
	remote := pushToOrigin(t, repo)
	upstreamCommit(t, repo, remote, "upstream.txt")
	write(t, filepath.Join(repo, "wip.txt"), "uncommitted\n")
	err := RefreshBranch(context.Background(), repo, "main")
	if !errors.Is(err, ErrBranchDirty) {
		t.Fatalf("a dirty checkout must be reported as ErrBranchDirty, got %v", err)
	}
	if exists(filepath.Join(repo, "upstream.txt")) {
		t.Error("nothing may be merged into a dirty checkout")
	}
}

func TestRefreshBranchRefusesADivergedBranchAndKeepsLocalWork(t *testing.T) {
	repo := newRepo(t)
	remote := pushToOrigin(t, repo)
	upstreamCommit(t, repo, remote, "remote.txt")
	write(t, filepath.Join(repo, "local.txt"), "local\n")
	git(t, repo, "add", "local.txt")
	git(t, repo, "commit", "-qm", "local side")
	if err := RefreshBranch(context.Background(), repo, "main"); err == nil {
		t.Fatal("a diverged branch must be refused")
	}
	if !exists(filepath.Join(repo, "local.txt")) {
		t.Error("the local commit must still be there")
	}
	// The same from another branch: git refuses the non-fast-forward ref update.
	git(t, repo, "checkout", "-q", "-b", "feature")
	if err := RefreshBranch(context.Background(), repo, "main"); err == nil {
		t.Fatal("a diverged main must not be advanced from another branch either")
	}
}

func TestRefreshBranchWithNothingToDo(t *testing.T) {
	repo := newRepo(t)
	if err := RefreshBranch(context.Background(), repo, "main"); err != nil {
		t.Errorf("a repository with no origin has nothing to bring in: %v", err)
	}
	if err := RefreshBranch(context.Background(), repo, ""); err != nil {
		t.Errorf("no branch assigned: %v", err)
	}
}

func TestRefreshBranchReportsAWorkingTreeItCannotRead(t *testing.T) {
	repo := newRepo(t)
	pushToOrigin(t, repo)
	write(t, filepath.Join(repo, ".git", "index"), "not an index")
	err := RefreshBranch(context.Background(), repo, "main")
	if err == nil || errors.Is(err, ErrBranchDirty) {
		t.Fatalf("an unreadable working tree must be reported as such, got %v", err)
	}
}

func TestRemoteDriftCountsWhatOriginHasThatTheBranchDoesNot(t *testing.T) {
	repo := newRepo(t)
	remote := pushToOrigin(t, repo)
	ctx := context.Background()
	if err := FetchRemoteBranch(ctx, repo, ""); err != nil {
		t.Fatal(err)
	}
	upstreamCommit(t, repo, remote, "up.txt")
	if behind, _, _ := RemoteDrift(ctx, repo, "main"); len(behind) != 0 {
		t.Fatal("nothing is known before the fetch")
	}
	if err := FetchRemoteBranch(ctx, repo, "main"); err != nil {
		t.Fatal(err)
	}
	behind, ahead, err := RemoteDrift(ctx, repo, "main")
	if err != nil || len(behind) != 1 || ahead != 0 {
		t.Fatalf("behind %v ahead %d err %v", behind, ahead, err)
	}
	if exists(filepath.Join(repo, "up.txt")) {
		t.Fatal("the fetch must not touch the checkout")
	}
	if err := FetchRemoteBranch(ctx, t.TempDir(), "main"); err != nil {
		t.Errorf("no origin, nothing to ask: %v", err)
	}
	if err := FetchRemoteBranch(ctx, repo, "nope"); err == nil {
		t.Error("a branch origin lacks is an error")
	}
}
