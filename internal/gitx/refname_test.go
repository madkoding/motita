package gitx

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

// A branch name that starts with a dash is an option to git, not a branch: a remote can hand one over.
func TestABranchNamedLikeAnOptionIsRefused(t *testing.T) {
	ctx := context.Background()
	dir := newRepo(t)
	bad := "--orphan=x"
	calls := map[string]error{
		"Checkout":          Checkout(ctx, dir, bad),
		"AddWorktreeFrom":   AddWorktreeFrom(ctx, dir, filepath.Join(t.TempDir(), "w"), bad, ""),
		"AddWorktree start": AddWorktreeFrom(ctx, dir, filepath.Join(t.TempDir(), "w"), "ok", "--detach"),
		"DeleteBranch":      DeleteBranch(ctx, dir, "-f"),
		"ForceDeleteBranch": ForceDeleteBranch(ctx, dir, bad),
		"SwitchNew":         SwitchNew(ctx, dir, bad),
		"PullFastForward":   PullFastForward(ctx, dir, bad),
	}
	_, calls["MergeInto"] = MergeInto(ctx, dir, "main", bad, "m")
	for name, err := range calls {
		if err == nil || !strings.Contains(err.Error(), "starts with a dash") {
			t.Errorf("%s: want the dash refusal, got %v", name, err)
		}
	}
	if err := refName("feature/x", "main"); err != nil {
		t.Errorf("a plain name is fine: %v", err)
	}
	if head, _, _ := Head(ctx, dir); strings.HasPrefix(head, "x") {
		t.Errorf("the checkout moved: %q", head)
	}
}
