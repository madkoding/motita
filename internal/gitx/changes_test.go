package gitx

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// The count is what a user reads as "how much have I changed". Every expectation
// below was measured against git 2.47.3 first, because the number is easy to get
// subtly wrong and a wrong number is worse than no number: it makes a session
// look idle when it has written files, or busy when it has not.

// TestWorkingTreeChangesOfACleanTreeIsZero: a fresh checkout has nothing to
// report, and that has to be 0 rather than "unknown".
func TestWorkingTreeChangesOfACleanTreeIsZero(t *testing.T) {
	n, err := WorkingTreeChanges(context.Background(), newRepo(t))
	if err != nil {
		t.Fatalf("WorkingTreeChanges: %v", err)
	}
	if n != 0 {
		t.Errorf("changes = %d, want 0 for a clean tree", n)
	}
}

// TestWorkingTreeChangesCountsModifiedAndStagedAndUntracked: the three ordinary
// kinds of uncommitted work, each counted once.
func TestWorkingTreeChangesCountsModifiedAndStagedAndUntracked(t *testing.T) {
	ctx := context.Background()
	repo := newRepo(t)
	write(t, filepath.Join(repo, "f.txt"), "modified\n")
	write(t, filepath.Join(repo, "staged.txt"), "staged\n")
	git(t, repo, "add", "staged.txt")
	write(t, filepath.Join(repo, "untracked.txt"), "untracked\n")

	n, err := WorkingTreeChanges(ctx, repo)
	if err != nil {
		t.Fatalf("WorkingTreeChanges: %v", err)
	}
	if n != 3 {
		t.Errorf("changes = %d, want 3 (one modified, one staged, one untracked)", n)
	}
}

// TestWorkingTreeChangesCountsTheFilesInsideAnUntrackedDirectory is the reason
// -uall is used, and it was measured: a folder holding three new files is ONE
// line of plain --porcelain and THREE with -uall. The user asked how many
// changes there are, and the folder is not one.
func TestWorkingTreeChangesCountsTheFilesInsideAnUntrackedDirectory(t *testing.T) {
	ctx := context.Background()
	repo := newRepo(t)
	for _, name := range []string{"a.txt", "b.txt", "c.txt"} {
		write(t, filepath.Join(repo, "newdir", name), "x\n")
	}

	n, err := WorkingTreeChanges(ctx, repo)
	if err != nil {
		t.Fatalf("WorkingTreeChanges: %v", err)
	}
	if n != 3 {
		t.Errorf("changes = %d, want 3: an untracked directory contributes its files, not itself", n)
	}
}

// TestWorkingTreeChangesCountsADeletion: removing a tracked file is a change,
// and a count that only looked at additions would hide it.
func TestWorkingTreeChangesCountsADeletion(t *testing.T) {
	ctx := context.Background()
	repo := newRepo(t)
	if err := os.Remove(filepath.Join(repo, "f.txt")); err != nil {
		t.Fatal(err)
	}

	n, err := WorkingTreeChanges(ctx, repo)
	if err != nil {
		t.Fatalf("WorkingTreeChanges: %v", err)
	}
	if n != 1 {
		t.Errorf("changes = %d, want 1", n)
	}
}

// TestWorkingTreeChangesOfAnEmptyDirectoryPathIsZero: there is nothing to count
// and no command to run. A free-standing session has no workspace, and its badge
// must be absent rather than an error.
func TestWorkingTreeChangesOfAnEmptyDirectoryPathIsZero(t *testing.T) {
	n, err := WorkingTreeChanges(context.Background(), "")
	if err != nil {
		t.Fatalf("an empty path must not be an error: %v", err)
	}
	if n != 0 {
		t.Errorf("changes = %d, want 0", n)
	}
}

// TestWorkingTreeChangesOfADirectoryThatIsNotARepositoryIsZero: a project that
// is not a repository has no changes to report, and the answer is 0 rather than
// an error - the count decorates a badge, and a badge that cannot be computed
// should be absent, not fatal.
func TestWorkingTreeChangesOfADirectoryThatIsNotARepositoryIsZero(t *testing.T) {
	n, err := WorkingTreeChanges(context.Background(), plainDir(t, "plain"))
	if err != nil {
		t.Fatalf("a directory that is not a repository must not be an error: %v", err)
	}
	if n != 0 {
		t.Errorf("changes = %d, want 0", n)
	}
}

// TestWorkingTreeChangesReportsAMissingGit: losing git is NOT the same as a clean
// tree, and the two must not be reported alike - one is a broken toolchain and
// the other is a finished piece of work. Here git is gone entirely, so the
// repository check itself is what fails.
func TestWorkingTreeChangesReportsAMissingGit(t *testing.T) {
	repo := newRepo(t)
	noGit(t)
	if _, err := WorkingTreeChanges(context.Background(), repo); err != ErrNoGit {
		t.Errorf("a count that loses git must report ErrNoGit, got %v", err)
	}
}

// TestWorkingTreeChangesReportsGitVanishingBeforeTheStatus: git is present for
// the repository check and gone by the time status runs - the machine the
// gateway is on when git is upgraded or removed underneath it. That must ALSO
// be ErrNoGit rather than a clean tree.
func TestWorkingTreeChangesReportsGitVanishingBeforeTheStatus(t *testing.T) {
	repo := newRepo(t)
	noGitOnGit(t, "status --porcelain")
	if _, err := WorkingTreeChanges(context.Background(), repo); err != ErrNoGit {
		t.Errorf("a count that loses git must report ErrNoGit, got %v", err)
	}
}

// TestWorkingTreeChangesReportsAFailedStatus: a status that fails for any other
// reason is reported rather than answered with a number. A wrong number on a
// badge is read as fact.
func TestWorkingTreeChangesReportsAFailedStatus(t *testing.T) {
	repo := newRepo(t)
	failOnGit(t, "status --porcelain")
	if _, err := WorkingTreeChanges(context.Background(), repo); err == nil {
		t.Fatal("a failed status must be reported, not counted as zero")
	}
}

// TestWorkingTreeChangesIgnoresBlankLines: porcelain output ends with a newline,
// so the split leaves a trailing empty entry. Counting it would report one
// more change than there are - a session would look dirty on a clean tree.
// The command is stubbed rather than run because the real one cannot be made to
// emit a blank interior line, and the parser must still be right about it.
func TestWorkingTreeChangesIgnoresBlankLines(t *testing.T) {
	repo := newRepo(t)
	stubOn(t, "status --porcelain", "\n M f.txt\n\n", nil)

	n, err := WorkingTreeChanges(context.Background(), repo)
	if err != nil {
		t.Fatalf("WorkingTreeChanges: %v", err)
	}
	if n != 1 {
		t.Errorf("changes = %d, want 1: blank lines are not changes", n)
	}
}

// TestWorkingTreeChangesOfADirectoryThatVanished: a session's workspace can be
// removed under it. The answer is 0, not an error, for the same reason a
// non-repository directory is: the badge is decoration.
func TestWorkingTreeChangesOfADirectoryThatVanished(t *testing.T) {
	dir := plainDir(t, "vanishing")
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	n, err := WorkingTreeChanges(context.Background(), dir)
	if err != nil {
		t.Fatalf("a directory that is gone must not be an error: %v", err)
	}
	if n != 0 {
		t.Errorf("changes = %d, want 0", n)
	}
}
