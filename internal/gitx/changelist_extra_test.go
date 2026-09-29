package gitx

// The three states the first pass did not cover, and the one guard in the parser
// that no ordinary git state produces.
//
// Conflicted and typechanged are produced FOR REAL here, because both are states
// a user reaches without trying: a merge stopped by a conflict, and a tracked
// file replaced by a symlink. A list that called a conflict "modified" would tell
// someone about to delete nothing about what is actually in the way.
//
// Copied is stubbed, the way this package stubs every state a test cannot produce
// on demand: `git status` reports a copy only when copy detection is switched on,
// which this program does not ask for - but the parser still has to name one if
// it arrives, rather than falling through to "modified".

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// gitMayFail runs git and ignores its exit code, for the commands whose FAILURE
// is the point - a merge that stops on a conflict exits non-zero.
func gitMayFail(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	_ = cmd.Run()
}

// TestChangeListReportsAConflictedFile: the state a stopped merge leaves, which
// is exactly when someone needs the list to be right - the files they have to
// resolve by hand.
func TestChangeListReportsAConflictedFile(t *testing.T) {
	ctx := context.Background()
	repo := newRepo(t)
	base := git(t, repo, "rev-parse", "--abbrev-ref", "HEAD")
	git(t, repo, "checkout", "-q", "-b", "theirs")
	write(t, filepath.Join(repo, "f.txt"), "theirs\n")
	git(t, repo, "commit", "-qam", "theirs")
	git(t, repo, "checkout", "-q", base)
	write(t, filepath.Join(repo, "f.txt"), "ours\n")
	git(t, repo, "commit", "-qam", "ours")
	// The merge must FAIL. That failure IS the state under test.
	gitMayFail(t, repo, "merge", "theirs")

	changes, err := WorkingTreeChangeList(ctx, repo)
	if err != nil {
		t.Fatalf("WorkingTreeChangeList: %v", err)
	}
	if len(changes) != 1 {
		t.Fatalf("changes = %v, want the one conflicted file", changes)
	}
	if changes[0].Kind != "conflicted" {
		t.Errorf("kind = %q (code %q), want conflicted: a merge in progress is not an ordinary edit",
			changes[0].Kind, changes[0].Code)
	}
}

// TestChangeListReportsATypeChange: a tracked file replaced by a symlink. Git
// reports the TYPE change rather than a modification, and the list has to follow,
// or the user reads "modified" about a file that is not a file any more.
func TestChangeListReportsATypeChange(t *testing.T) {
	ctx := context.Background()
	repo := newRepo(t)
	path := filepath.Join(repo, "f.txt")
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("somewhere-else", path); err != nil {
		t.Fatal(err)
	}

	changes, err := WorkingTreeChangeList(ctx, repo)
	if err != nil {
		t.Fatalf("WorkingTreeChangeList: %v", err)
	}
	if len(changes) != 1 {
		t.Fatalf("changes = %v, want one", changes)
	}
	if changes[0].Kind != "typechanged" {
		t.Errorf("kind = %q (code %q), want typechanged", changes[0].Kind, changes[0].Code)
	}
}

// TestChangeListReportsACopy: stubbed. The parser must name a copy rather than
// fall through to "modified", and it must carry the original path like a rename.
func TestChangeListReportsACopy(t *testing.T) {
	repo := newRepo(t)
	stubOn(t, "status --porcelain", "C  copy.txt\x00orig.txt\x00", nil)

	changes, err := WorkingTreeChangeList(context.Background(), repo)
	if err != nil {
		t.Fatalf("WorkingTreeChangeList: %v", err)
	}
	if len(changes) != 1 {
		t.Fatalf("changes = %v, want one", changes)
	}
	if changes[0].Kind != "copied" || changes[0].Path != "copy.txt" || changes[0].From != "orig.txt" {
		t.Errorf("change = %+v, want a copy of orig.txt to copy.txt", changes[0])
	}
}

// TestChangeListDropsATruncatedRecord: output that stops mid-record is dropped
// rather than guessed at. A partial path, shown to someone about to delete, is
// worse than a name that is simply missing.
func TestChangeListDropsATruncatedRecord(t *testing.T) {
	repo := newRepo(t)
	stubOn(t, "status --porcelain", "?\x00 M kept.txt\x00", nil)

	changes, err := WorkingTreeChangeList(context.Background(), repo)
	if err != nil {
		t.Fatalf("WorkingTreeChangeList: %v", err)
	}
	if len(changes) != 1 || changes[0].Path != "kept.txt" {
		t.Errorf("changes = %+v, want only the record that parsed", changes)
	}
}

// TestChangeListReportsGitVanishingBeforeTheStatus: git answered the repository
// check and was gone by the time the list was read - a machine where git is
// upgraded or removed under a running gateway. Losing git is NOT the same as a
// clean tree, and the two must never read alike: one is a broken toolchain, the
// other is "nothing to lose".
func TestChangeListReportsGitVanishingBeforeTheStatus(t *testing.T) {
	repo := newRepo(t)
	noGitOnGit(t, "status --porcelain")
	if _, err := WorkingTreeChangeList(context.Background(), repo); err != ErrNoGit {
		t.Errorf("a list that loses git must report ErrNoGit, got %v", err)
	}
}
