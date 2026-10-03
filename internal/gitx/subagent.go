package gitx

import (
	"context"
	"strings"
)

// AddWorktreeAt checks commit out at path on a NEW branch. It is where a background agent works:
// a branch of its own, so its commits are kept when its worktree is removed and the agent that
// started it can merge them, and a start point that is the tree as that agent had it - a
// Snapshot, uncommitted work included - rather than the last commit.
func AddWorktreeAt(ctx context.Context, repoDir, path, branch, commit string) error {
	if err := prepareParent(path); err != nil {
		return err
	}
	_, err := noGitOr(ctx, "the background agent's worktree could not be created", repoDir,
		"worktree", "add", "-q", "-b", branch, path, commit)
	return err
}

// CommitAll commits everything in the working tree at dir, untracked files included, and reports
// whether there was anything to commit.
//
// It is the bookkeeping commit made when a background agent ends with work it did not commit
// itself: the worktree is removed afterwards, and uncommitted files would go with it. The identity
// is resolved like a merge's (a clone made by this program has none), and signing is off for this
// one commit: a signing key that asks for a passphrase has nobody to ask here, and a commit that
// fails is work that is lost.
func CommitAll(ctx context.Context, dir, message string) (bool, error) {
	if _, err := noGitOr(ctx, "the work could not be staged", dir, "add", "-A"); err != nil {
		return false, err
	}
	staged, err := noGitOr(ctx, "the staged work could not be read", dir, "diff", "--cached", "--name-only")
	if err != nil {
		return false, err
	}
	if strings.TrimSpace(staged) == "" {
		return false, nil
	}
	name, email := Identity(ctx, dir)
	if _, err := noGitOr(ctx, "the work could not be committed", dir,
		"-c", "user.name="+name, "-c", "user.email="+email, "-c", "commit.gpgsign=false",
		"commit", "-q", "-m", message); err != nil {
		return false, err
	}
	return true, nil
}
