package gitx

import (
	"context"
	"errors"
)

// ErrNoCheckpoint is returned when there is nothing to restore: the checkpoint was taken where
// git had no commit to anchor it, so it holds no files.
var ErrNoCheckpoint = errors.New("this checkpoint holds no file state")

// Snapshot records the working tree at dir as a commit that hangs from ref, and returns the
// commit HEAD was on (head) and the snapshot (snap). Restore turns the tree back into it.
//
// The snapshot is a real commit, not a stash: `git stash create` leaves out untracked files,
// and an agent's new files are exactly what a checkpoint has to be able to take away again.
// It is built without moving anything the user can see. The index is written to a tree before
// `add -A` and read back after it, so what the user had staged is still staged; no branch
// moves and no file is touched. The commit is kept reachable from ref because an unreferenced
// commit is garbage the next `git gc` may collect.
//
// Ignored files are NOT part of it (add -A honours .gitignore), which is what keeps a
// node_modules or a build directory out of every checkpoint, and out of every restore.
//
// It returns empty strings and no error when dir has no commit yet: there is nothing to
// anchor the snapshot to, and the checkpoint is simply not restorable.
func Snapshot(ctx context.Context, dir, ref string) (head, snap string, err error) {
	if !ownsRepo(ctx, dir) {
		return "", "", nil
	}
	head = HeadRev(ctx, dir)
	if head == "" {
		return "", "", nil
	}
	before, err := noGitOr(ctx, "the index could not be read", dir, "write-tree")
	if err != nil {
		return "", "", err
	}
	// The index is put back whether or not the snapshot succeeded.
	defer func() {
		if _, rerr := noGitOr(ctx, "the index could not be put back", dir, "read-tree", before); rerr != nil && err == nil {
			head, snap, err = "", "", rerr
		}
	}()
	if _, err = noGitOr(ctx, "the files could not be read", dir, "add", "-A"); err != nil {
		return "", "", err
	}
	tree, err := noGitOr(ctx, "the files could not be recorded", dir, "write-tree")
	if err != nil {
		return "", "", err
	}
	name, email := Identity(ctx, dir)
	snap, err = noGitOr(ctx, "the checkpoint could not be made", dir,
		"-c", "user.name="+name, "-c", "user.email="+email,
		"commit-tree", tree, "-p", head, "-m", "motita checkpoint")
	if err != nil {
		return "", "", err
	}
	if _, err = noGitOr(ctx, "the checkpoint could not be kept", dir, "update-ref", ref, snap); err != nil {
		return "", "", err
	}
	return head, snap, nil
}

// Restore turns the tree at dir back into what Snapshot recorded.
//
// Four steps, in this order, and each is there for a case the others miss:
//   - reset --hard head: commits made after the checkpoint are undone (they stay in the reflog);
//   - clean -fd: files created after it are removed (ignored files are left alone);
//   - read-tree --reset -u snap: the files the checkpoint had, exactly, including the ones the
//     user had deleted or added but not committed;
//   - reset: the index goes back to head, so the changes read as changes again, not as staged.
func Restore(ctx context.Context, dir, head, snap string) error {
	if head == "" || snap == "" || !ownsRepo(ctx, dir) {
		return ErrNoCheckpoint
	}
	steps := []struct {
		what string
		args []string
	}{
		{"the commits made since could not be undone", []string{"reset", "-q", "--hard", head}},
		{"the files created since could not be removed", []string{"clean", "-fdq"}},
		{"the files could not be restored", []string{"read-tree", "--reset", "-u", snap}},
		{"the index could not be put back", []string{"reset", "-q"}},
	}
	for _, s := range steps {
		if _, err := noGitOr(ctx, s.what, dir, s.args...); err != nil {
			return err
		}
	}
	return nil
}

// ownsRepo reports whether dir is the top of its own repository (or worktree). A checkpoint
// resets and cleans a WHOLE work tree, so it is only ever taken where that tree is the project
// itself: a folder that merely sits inside some other repository, or an empty path (which git
// reads as "the current directory"), must never be reset on the strength of a checkpoint.
func ownsRepo(ctx context.Context, dir string) bool {
	if dir == "" {
		return false
	}
	root, err := Root(ctx, dir)
	return err == nil && SamePath(root, dir)
}

// DropRef removes the reference that keeps a snapshot alive. It reports nothing: a reference
// that is already gone is the outcome that was asked for.
func DropRef(ctx context.Context, dir, ref string) {
	_, _ = execute(ctx, dir, "update-ref", "-d", ref)
}
