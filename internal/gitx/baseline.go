package gitx

import "context"

// AddDetachedWorktree checks commit out, detached, at path: a throwaway checkout that has no
// branch of its own, so creating and removing it never touches the user's branches.
//
// It is the anchor's baseline. Reported from a real session: the project's gate already failed
// on the commit the run started from (tests that needed a tool the machine did not have), so no
// change could ever pass it, and the agent spent round after round proving by hand - git stash,
// a clean worktree in /tmp - that the failures were not its own. The comparison is mechanical,
// so the program makes it: the commit is the Snapshot the run took when it began.
func AddDetachedWorktree(ctx context.Context, repoDir, path, commit string) error {
	_, err := noGitOr(ctx, "the baseline checkout could not be created", repoDir,
		"worktree", "add", "--detach", path, commit)
	return err
}
