package gitx

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestABaselineCheckoutIsTheTreeTheRunStartedFrom: the run snapshots the tree (uncommitted and
// untracked work included), changes it, and the checkout made from the snapshot holds the code
// as it was - which is what a check is run against to tell an old failure from a new one.
func TestABaselineCheckoutIsTheTreeTheRunStartedFrom(t *testing.T) {
	ctx := context.Background()
	repo := newRepo(t)
	write(t, filepath.Join(repo, "new.txt"), "untracked before the run\n")
	_, start, err := Snapshot(ctx, repo, "refs/motita/test-baseline")
	if err != nil || start == "" {
		t.Fatalf("Snapshot = %q, %v", start, err)
	}
	write(t, filepath.Join(repo, "f.txt"), "changed by the run\n")
	git(t, repo, "commit", "-qam", "the run's work")

	wt := filepath.Join(t.TempDir(), "baseline")
	if err := AddDetachedWorktree(ctx, repo, wt, start); err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]string{"f.txt": "base\n", "new.txt": "untracked before the run\n"} {
		body, err := os.ReadFile(filepath.Join(wt, name))
		if err != nil || string(body) != want {
			t.Errorf("%s in the baseline = %q (%v), want %q", name, body, err, want)
		}
	}
	if branch, _, _ := Head(ctx, wt); branch != "" {
		t.Errorf("the baseline must be detached, it is on %q", branch)
	}
	if err := RemoveWorktree(ctx, repo, wt, true); err != nil {
		t.Fatal(err)
	}
}

// TestAddDetachedWorktreeReportsAFailure: an unknown commit is git's refusal, passed on.
func TestAddDetachedWorktreeReportsAFailure(t *testing.T) {
	repo := newRepo(t)
	err := AddDetachedWorktree(context.Background(), repo, filepath.Join(t.TempDir(), "b"), strings.Repeat("0", 40))
	if err == nil || !strings.Contains(err.Error(), "baseline checkout") {
		t.Errorf("an unknown commit must be refused, got %v", err)
	}
}
