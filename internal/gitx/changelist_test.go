package gitx

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The user asked to SEE what a deletion would destroy, not just how many files.
//
// A count is enough to warn, but not to decide: the two changes in the case that
// motivated this were build artefacts, and the user could not tell that from a
// number. What decides is the list - which files, and in what state.
//
// The format was measured against git 2.47.3 first, because a list that is
// subtly wrong is worse than no list: it names files that are not there or
// mangles a path with a space in it. `-z` is what makes the last part possible.

// accentName is a path with a space and two accented letters, spelled with escapes so
// this source file holds no multi-byte characters of its own.
const accentName = "m\u00e1s \u00f1.txt"

// TestChangeListIsEmptyForACleanTree: nothing to show, and that is an empty list
// rather than an error.
func TestChangeListIsEmptyForACleanTree(t *testing.T) {
	changes, err := WorkingTreeChangeList(context.Background(), newRepo(t))
	if err != nil {
		t.Fatalf("WorkingTreeChangeList: %v", err)
	}
	if len(changes) != 0 {
		t.Errorf("changes = %v, want none for a clean tree", changes)
	}
}

// TestChangeListNamesEachChangeAndItsKind: the three ordinary kinds, each named.
func TestChangeListNamesEachChangeAndItsKind(t *testing.T) {
	ctx := context.Background()
	repo := newRepo(t)
	write(t, filepath.Join(repo, "f.txt"), "modified\n")
	write(t, filepath.Join(repo, "staged.txt"), "staged\n")
	git(t, repo, "add", "staged.txt")
	write(t, filepath.Join(repo, "untracked.txt"), "untracked\n")

	changes, err := WorkingTreeChangeList(ctx, repo)
	if err != nil {
		t.Fatalf("WorkingTreeChangeList: %v", err)
	}
	got := map[string]string{}
	for _, c := range changes {
		got[c.Path] = c.Kind
	}
	want := map[string]string{
		"f.txt":         "modified",
		"staged.txt":    "added",
		"untracked.txt": "untracked",
	}
	if len(got) != len(want) {
		t.Fatalf("changes = %v, want %v", got, want)
	}
	for path, kind := range want {
		if got[path] != kind {
			t.Errorf("%s: kind = %q, want %q", path, got[path], kind)
		}
	}
}

// TestChangeListKeepsAPathWithSpacesAndAccentsWhole is the reason `-z` is used.
//
// Without it, git QUOTES a path that holds a space and escapes a non-ASCII byte
// as octal, so the name shown to the user is not the name on disk. Measured:
// plain --porcelain answers `?? "dd/name with space.txt"`, quotes included.
func TestChangeListKeepsAPathWithSpacesAndAccentsWhole(t *testing.T) {
	ctx := context.Background()
	repo := newRepo(t)
	// Written with escapes so this FILE stays ASCII while the path on disk is not:
	// the repository's gate refuses accented characters in tracked sources, and what
	// is under test is the byte sequence git EMITS, not how it is spelled here.
	write(t, filepath.Join(repo, "dd", accentName), "x\n")

	changes, err := WorkingTreeChangeList(ctx, repo)
	if err != nil {
		t.Fatalf("WorkingTreeChangeList: %v", err)
	}
	if len(changes) != 1 {
		t.Fatalf("changes = %v, want exactly one", changes)
	}
	want := "dd/" + accentName
	if changes[0].Path != want {
		t.Errorf("path = %q, want %q: a quoted or escaped name is not the name on disk",
			changes[0].Path, want)
	}
	if strings.ContainsAny(changes[0].Path, `"\\`) {
		t.Errorf("path = %q: git's quoting must not reach the user", changes[0].Path)
	}
}

// TestChangeListReportsWhereARenameCameFrom: a renamed file is ONE change, and
// the old name is carried so the list can say "from what".
func TestChangeListReportsWhereARenameCameFrom(t *testing.T) {
	ctx := context.Background()
	repo := newRepo(t)
	git(t, repo, "mv", "f.txt", "renamed.txt")

	changes, err := WorkingTreeChangeList(ctx, repo)
	if err != nil {
		t.Fatalf("WorkingTreeChangeList: %v", err)
	}
	if len(changes) != 1 {
		t.Fatalf("changes = %v, want exactly one (a rename is one change)", changes)
	}
	if changes[0].Path != "renamed.txt" || changes[0].From != "f.txt" {
		t.Errorf("change = %+v, want path renamed.txt from f.txt", changes[0])
	}
	if changes[0].Kind != "renamed" {
		t.Errorf("kind = %q, want renamed", changes[0].Kind)
	}
}

// TestChangeListCountsTheFilesInsideAnUntrackedDirectory: -uall, for the same
// reason the count uses it. A folder is not one change.
func TestChangeListCountsTheFilesInsideAnUntrackedDirectory(t *testing.T) {
	ctx := context.Background()
	repo := newRepo(t)
	for _, name := range []string{"a.txt", "b.txt", "c.txt"} {
		write(t, filepath.Join(repo, "newdir", name), "x\n")
	}

	changes, err := WorkingTreeChangeList(ctx, repo)
	if err != nil {
		t.Fatalf("WorkingTreeChangeList: %v", err)
	}
	if len(changes) != 3 {
		t.Errorf("changes = %v, want the three files, not the folder", changes)
	}
}

// TestChangeListReportsADeletion: removing a tracked file is a change, and the
// list has to name it.
func TestChangeListReportsADeletion(t *testing.T) {
	ctx := context.Background()
	repo := newRepo(t)
	git(t, repo, "rm", "-q", "f.txt")

	changes, err := WorkingTreeChangeList(ctx, repo)
	if err != nil {
		t.Fatalf("WorkingTreeChangeList: %v", err)
	}
	if len(changes) != 1 || changes[0].Kind != "deleted" {
		t.Errorf("changes = %+v, want one deletion", changes)
	}
}

// TestChangeListOfAnEmptyPathIsEmpty: there is nothing to list and no command to
// run. A free-standing session has no workspace.
func TestChangeListOfAnEmptyPathIsEmpty(t *testing.T) {
	changes, err := WorkingTreeChangeList(context.Background(), "")
	if err != nil {
		t.Fatalf("an empty path must not be an error: %v", err)
	}
	if len(changes) != 0 {
		t.Errorf("changes = %v, want none", changes)
	}
}

// TestChangeListOfADirectoryThatIsNotARepositoryIsEmpty: nothing to list.
func TestChangeListOfADirectoryThatIsNotARepositoryIsEmpty(t *testing.T) {
	changes, err := WorkingTreeChangeList(context.Background(), plainDir(t, "plain"))
	if err != nil {
		t.Fatalf("a directory that is not a repository must not be an error: %v", err)
	}
	if len(changes) != 0 {
		t.Errorf("changes = %v, want none", changes)
	}
}

// TestChangeListOfADirectoryThatVanishedIsEmpty: a workspace can be removed
// under the gateway, and the list is then empty rather than fatal - the same
// rule the count follows.
func TestChangeListOfADirectoryThatVanishedIsEmpty(t *testing.T) {
	dir := plainDir(t, "vanishing")
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	changes, err := WorkingTreeChangeList(context.Background(), dir)
	if err != nil {
		t.Fatalf("a directory that is gone must not be an error: %v", err)
	}
	if len(changes) != 0 {
		t.Errorf("changes = %v, want none", changes)
	}
}

// TestChangeListReportsAMissingGit: losing git is not the same as a clean tree,
// and the two must not read alike.
func TestChangeListReportsAMissingGit(t *testing.T) {
	repo := newRepo(t)
	noGit(t)
	if _, err := WorkingTreeChangeList(context.Background(), repo); err != ErrNoGit {
		t.Errorf("a list that loses git must report ErrNoGit, got %v", err)
	}
}

// TestChangeListReportsAFailedStatus: a status that fails for any other reason
// is reported rather than answered with an empty list. An empty list reads as
// "nothing to lose", which is the wrong thing to tell someone about to delete.
func TestChangeListReportsAFailedStatus(t *testing.T) {
	repo := newRepo(t)
	failOnGit(t, "status --porcelain")
	if _, err := WorkingTreeChangeList(context.Background(), repo); err == nil {
		t.Fatal("a failed status must be reported, not listed as empty")
	}
}

// TestChangeListKeepsALeadingSpaceInTheStatus is the trap that `-z` alone does
// not solve: for an unstaged modification the porcelain status is " M", with a
// LEADING space. The package's exec seam trims its output, which would drop that
// space from the first record and leave the parser deciding between a staged and
// an unstaged file on the strength of where the change happens to sit in the
// list. The list is read from an untrimmed seam instead, and this pins it.
func TestChangeListKeepsALeadingSpaceInTheStatus(t *testing.T) {
	repo := newRepo(t)
	write(t, filepath.Join(repo, "f.txt"), "modified\n")

	changes, err := WorkingTreeChangeList(context.Background(), repo)
	if err != nil {
		t.Fatalf("WorkingTreeChangeList: %v", err)
	}
	if len(changes) != 1 {
		t.Fatalf("changes = %v, want exactly one", changes)
	}
	// An unstaged change: the worktree column carries the M, the index column is blank.
	if changes[0].Code != " M" {
		t.Errorf("code = %q, want %q (an unstaged modification, index blank)", changes[0].Code, " M")
	}
	if changes[0].Kind != "modified" {
		t.Errorf("kind = %q, want modified", changes[0].Kind)
	}
}
