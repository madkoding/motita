package gitx

import (
	"context"
	"errors"
	"strconv"
	"strings"
)

// The small actions a person does by hand in a checkout, which the project menu offers for each
// repository of a folder. None of them forces anything: what git refuses stays refused, and the
// one that destroys work (DiscardChanges) is the only one the caller must ask a person about.

// ErrNothingToStash is why Stash does nothing: no change is there to put aside.
var ErrNothingToStash = errors.New("there are no changes to put aside")

// Log is the last n commits of the checked-out branch, newest first.
func Log(ctx context.Context, dir string, n int) ([]Commit, error) {
	out, err := noGitOr(ctx, "could not read the history", dir,
		"log", "-n", strconv.Itoa(n), "--format=%h\t%s")
	if err != nil {
		return nil, err
	}
	return parseCommits(out), nil
}

// Stash puts the uncommitted work aside, new files included, under a label that says where it
// came from. It is kept in the repository's stash list, so nothing is lost.
func Stash(ctx context.Context, dir, label string) error {
	dirty, err := Dirty(ctx, dir)
	if err != nil {
		return err
	}
	if !dirty {
		return ErrNothingToStash
	}
	_, err = noGitOr(ctx, "could not put the changes aside", dir, "stash", "push", "--include-untracked", "-m", label)
	return err
}

// MergedBranches are the local branches whose work is already in base, other than base and the one
// checked out: the ones that are safe to delete, because every commit they hold is somewhere else.
func MergedBranches(ctx context.Context, dir, base string) ([]string, error) {
	out, err := noGitOr(ctx, "could not list the merged branches", dir,
		"branch", "--merged", base, "--format=%(refname:short)")
	if err != nil {
		return nil, err
	}
	current := Display(ctx, dir)
	var list []string
	for _, name := range strings.Fields(out) {
		if name != base && name != current {
			list = append(list, name)
		}
	}
	return list, nil
}

// DiscardChanges throws the uncommitted work away: tracked files go back to the last commit and
// new files are deleted. It cannot be undone, which is why a caller asks first.
func DiscardChanges(ctx context.Context, dir string) error {
	if _, err := noGitOr(ctx, "could not discard the changes", dir, "reset", "--hard"); err != nil {
		return err
	}
	_, err := noGitOr(ctx, "could not delete the new files", dir, "clean", "-fd")
	return err
}
