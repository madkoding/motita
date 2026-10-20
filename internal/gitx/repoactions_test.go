package gitx

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"testing"
)

func TestLogListsTheLastCommitsNewestFirst(t *testing.T) {
	ctx := context.Background()
	repo := newRepo(t)
	write(t, filepath.Join(repo, "g.txt"), "g\n")
	git(t, repo, "add", "g.txt")
	git(t, repo, "commit", "-qm", "second")
	got, err := Log(ctx, repo, 1)
	if err != nil || len(got) != 1 || got[0].Subject != "second" {
		t.Fatalf("Log(1) = %+v, %v", got, err)
	}
	all, _ := Log(ctx, repo, 10)
	if len(all) != 2 || all[1].Subject != "init" {
		t.Errorf("Log(10) = %+v", all)
	}
	if _, err := Log(ctx, t.TempDir(), 5); err == nil {
		t.Error("a folder that is no repository has no history")
	}
}

func TestStashPutsTheWorkAsideAndRefusesWhenThereIsNone(t *testing.T) {
	ctx := context.Background()
	repo := newRepo(t)
	if err := Stash(ctx, repo, "x"); !errors.Is(err, ErrNothingToStash) {
		t.Fatalf("clean checkout: %v", err)
	}
	write(t, filepath.Join(repo, "wip.txt"), "new\n")
	write(t, filepath.Join(repo, "f.txt"), "changed\n")
	if err := Stash(ctx, repo, "motita: api"); err != nil {
		t.Fatalf("Stash: %v", err)
	}
	if n, _ := WorkingTreeChanges(ctx, repo); n != 0 {
		t.Errorf("the checkout must be clean, changes = %d", n)
	}
	if out := git(t, repo, "stash", "list"); out == "" {
		t.Error("the work must stay in the stash list")
	}
	if err := Stash(ctx, t.TempDir(), "x"); err == nil {
		t.Error("no repository, no stash")
	}
	write(t, filepath.Join(repo, "again.txt"), "n\n")
	stubOn(t, "stash push", "", errors.New("exit status 1"))
	if err := Stash(ctx, repo, "x"); err == nil {
		t.Error("a failing stash is an error")
	}
}

func TestMergedBranchesAreTheOnesSafeToDelete(t *testing.T) {
	ctx := context.Background()
	repo := newRepo(t)
	git(t, repo, "branch", "done")
	git(t, repo, "switch", "-q", "-c", "work")
	write(t, filepath.Join(repo, "w.txt"), "w\n")
	git(t, repo, "add", "w.txt")
	git(t, repo, "commit", "-qm", "unmerged")
	git(t, repo, "switch", "-q", "main")
	got, err := MergedBranches(ctx, repo, "main")
	if err != nil || !reflect.DeepEqual(got, []string{"done"}) {
		t.Errorf("merged = %v, %v", got, err)
	}
	if _, err := MergedBranches(ctx, t.TempDir(), "main"); err == nil {
		t.Error("no repository, no branches")
	}
}

func TestDiscardChangesRestoresTheLastCommitAndRemovesNewFiles(t *testing.T) {
	ctx := context.Background()
	repo := newRepo(t)
	write(t, filepath.Join(repo, "f.txt"), "changed\n")
	write(t, filepath.Join(repo, "dir", "new.txt"), "new\n")
	if err := DiscardChanges(ctx, repo); err != nil {
		t.Fatalf("DiscardChanges: %v", err)
	}
	if n, _ := WorkingTreeChanges(ctx, repo); n != 0 {
		t.Errorf("changes left = %d", n)
	}
	if err := DiscardChanges(ctx, t.TempDir()); err == nil {
		t.Error("no repository, nothing to discard")
	}
	stubOn(t, "clean -fd", "", errors.New("exit status 1"))
	if err := DiscardChanges(ctx, repo); err == nil {
		t.Error("a failing clean is an error")
	}
}
