package gitx

import (
	"context"
	"errors"
	"strings"
)

// This file is the repository's knowledge of its own checkouts.
//
// A worktree isolates the WORKING TREE and nothing else. `git worktree add` on a
// branch that is already checked out is refused by git itself ("'<branch>' is
// already used by worktree at ..."), and that refusal is what stops two sessions
// from sharing a directory. What git does NOT do is keep that branch attached to
// that session: as soon as the worktree moves to another branch, git considers the
// session's branch free, and any other checkout of the repository can take it.
// Measured on git 2.47.3:
//
//	# the session's worktree moved to "feature"; project checkout on "main"
//	git -C <project>  checkout motita/s1  ->  exit 0, "Cambiado a rama 'motita/s1'"
//	git -C <worktree> checkout motita/s1  ->  exit 128
//	#   fatal: 'motita/s1' is already used by worktree at '<project>'
//
// The lesson is that a session's worktree has to be identified by its PATH and
// its repository, NOT by the branch it currently has checked out. Identifying it
// by branch is what turns "the user moved a session to a feature branch" into
// "the session lost its own checkout", because the path is then no longer on the
// session's branch and every branch-based check mistakes it for a stranger.
//
// The listing below is what makes the path-based identification possible.

// Worktree is one checkout of a repository, as `git worktree list` reports it.
//
// The repository's OWN checkout is one of them: git lists it first, and leaving
// it out would make any "which checkout holds this" question blind to the most
// likely holder.
type Worktree struct {
	// Path is where this checkout lives.
	Path string
	// Branch is the branch checked out here, and "" when the checkout is
	// detached or when a prunable registration names none.
	Branch string
	// Detached reports a checkout with no branch at all, which is a checkout
	// that holds no branch and can therefore never be the holder of one.
	Detached bool
	// Prunable reports a registration whose directory is GONE: a worktree
	// removed by hand, an rm -rf, a container whose volumes did not persist.
	// It is not a checkout anyone can work in, and counting it would block a
	// branch nobody is using - the opposite of the point.
	Prunable bool
}

// Worktrees lists every checkout of the repository at repoDir: its own first,
// then each registered worktree, in git's own order.
//
// Prunable registrations ARE returned, because only the caller knows what it
// needs: a guard skips them, and a report that shows a stale registration is
// telling the user something true about their repository.
//
// Losing git is reported as ErrNoGit rather than as "no worktrees". A caller
// that guards on this listing must not be able to mistake a missing program for
// an empty answer, which would silently disable the guard.
func Worktrees(ctx context.Context, repoDir string) ([]Worktree, error) {
	if err := Repo(ctx, repoDir); err != nil {
		return nil, err
	}
	out, err := noGitOr(ctx, "could not list the repository's worktrees", repoDir, "worktree", "list", "--porcelain")
	if err != nil {
		return nil, err
	}
	return parseWorktreeList(out)
}

// LiveWorktreeAt reports whether path is a usable checkout of the repository at
// repoDir, and returns the entry when it is.
//
// This is the question that identifies a session's OWN working tree, and it is
// deliberately not a question about branches: a session whose worktree has been
// moved to a feature branch, or onto a detached HEAD, is still working in its
// own tree, and asking about the branch would report that as a stranger.
//
// A registration whose directory is gone is not a live worktree, so a session
// whose worktree was deleted by hand is reported absent and gets a new one.
func LiveWorktreeAt(ctx context.Context, repoDir, path string) (Worktree, bool, error) {
	all, err := Worktrees(ctx, repoDir)
	if err != nil {
		return Worktree{}, false, err
	}
	for _, w := range all {
		if w.Prunable || !dirExists(w.Path) {
			continue
		}
		if samePath(w.Path, path) {
			return w, true, nil
		}
	}
	return Worktree{}, false, nil
}

// BranchHolder reports which live checkout has branch, and false when no
// checkout has it.
//
// This is what a caller asks before it takes a branch: a branch is free only if
// nothing is working in it. The repository's own checkout is an answer like any
// other, and the caller compares paths to tell "someone else holds it" from "I
// am the one on it".
//
// A detached checkout holds no branch and a prunable registration holds
// nothing, so neither can be the answer.
func BranchHolder(ctx context.Context, repoDir, branch string) (holder string, ok bool, err error) {
	all, err := Worktrees(ctx, repoDir)
	if err != nil {
		return "", false, err
	}
	for _, w := range all {
		if w.Prunable || w.Detached || w.Branch != branch {
			continue
		}
		if !dirExists(w.Path) {
			continue
		}
		return w.Path, true, nil
	}
	return "", false, nil
}

// parseWorktreeList reads `git worktree list --porcelain`.
//
// The KEYS are stable and are not localized: measured with LC_ALL at es_CL,
// es_ES, C and en_US, "worktree", "HEAD", "branch", "detached" and "prunable"
// are the same in all four. Only prunable's REASON is translated, so the key is
// read and the reason never is - the rule this package follows for every piece
// of git's output.
//
// A "branch"/"detached"/"prunable" line with no "worktree" line before it is a
// parse failure rather than something to skip: skipping it would drop a checkout
// from the listing, and a dropped checkout is a branch that a guard would then
// believe nobody holds.
func parseWorktreeList(out string) ([]Worktree, error) {
	var list []Worktree
	var cur *Worktree
	flush := func() {
		if cur != nil {
			list = append(list, *cur)
			cur = nil
		}
	}
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimRight(line, "\r")
		if strings.TrimSpace(line) == "" {
			flush()
			continue
		}
		key, value, _ := strings.Cut(line, " ")
		switch key {
		case "worktree":
			flush()
			cur = &Worktree{Path: value}
		case "branch":
			if cur == nil {
				return nil, errors.New("the worktree listing named a branch before naming a worktree")
			}
			cur.Branch = strings.TrimPrefix(value, "refs/heads/")
		case "detached":
			if cur == nil {
				return nil, errors.New("the worktree listing named a detached checkout before naming a worktree")
			}
			cur.Detached = true
		case "prunable":
			if cur == nil {
				return nil, errors.New("the worktree listing named a prunable registration before naming a worktree")
			}
			cur.Prunable = true
		}
	}
	flush()
	return list, nil
}
