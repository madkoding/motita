package gitx

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ---------- AddWorktree ----------

// TestAddWorktreeChecksOutTheBranchBesideTheProject is the happy path a session
// takes: a second working tree on a branch of its own, which is what makes two
// sessions in one project stop colliding over the working tree.
func TestAddWorktreeChecksOutTheBranchBesideTheProject(t *testing.T) {
	ctx := context.Background()
	repo := newRepo(t)
	wt := filepath.Join(filepath.Dir(repo), "worktrees", "s1")

	addWorktree(t, repo, wt, "motita/s1")
	if got := Display(ctx, wt); got != "motita/s1" {
		t.Errorf("the worktree must be checked out on its branch, got %q", got)
	}
	// The whole point: a change in the worktree is NOT in the project's tree.
	write(t, filepath.Join(wt, "new.txt"), "session work\n")
	if exists(filepath.Join(repo, "new.txt")) {
		t.Fatal("a file created in the worktree must not appear in the project's checkout")
	}
	if !exists(filepath.Join(wt, "new.txt")) {
		t.Fatal("the worktree must be a real working tree")
	}
}

// TestAddWorktreeCreatesTheParentsOfTheWorktreeDirectory: `git worktree add`
// creates the final directory but not its parents, and its failure for a
// missing parent reads like a git problem rather than the missing folder it is.
func TestAddWorktreeCreatesTheParentsOfTheWorktreeDirectory(t *testing.T) {
	repo := newRepo(t)
	wt := filepath.Join(filepath.Dir(repo), "a", "b", "c", "w")
	addWorktree(t, repo, wt, "motita/s1")
	if !exists(wt) {
		t.Fatal("the worktree must exist")
	}
}

// TestAddWorktreeReplacesAnOrphanedRegistration is the recovery path a user
// actually hits: a worktree directory removed by hand - a cleanup, a container
// that did not persist, an rm -rf - leaves git's registration behind, and every
// later `worktree add` at that path is REFUSED ("Preparing worktree ..." and a
// non-zero status) even though nothing is there. Measured on this machine.
//
// Without this the session can never get its worktree back, which is exactly
// the state a gateway restart leaves it in.
func TestAddWorktreeReplacesAnOrphanedRegistration(t *testing.T) {
	ctx := context.Background()
	repo := newRepo(t)
	wt := filepath.Join(filepath.Dir(repo), "w")
	addWorktree(t, repo, wt, "motita/s1")

	// The directory goes away without git being told.
	if err := os.RemoveAll(wt); err != nil {
		t.Fatal(err)
	}
	if err := AddWorktree(ctx, repo, wt, "motita/s1"); err != nil {
		t.Fatalf("AddWorktree over an orphaned registration: %v", err)
	}
	if !exists(wt) {
		t.Fatal("the worktree must be usable again")
	}
	if branch := Display(ctx, wt); branch != "motita/s1" {
		t.Errorf("branch = %q, want motita/s1", branch)
	}
}

// TestAddWorktreeReportsAPruneThatFailed: clearing a stale registration is a
// recovery step, and a recovery step that fails has to be REPORTED rather than
// swallowed. Swallowing it would let the add below run against the
// registration that is still there and fail with git's own words, which say
// nothing about the real cause.
func TestAddWorktreeReportsAPruneThatFailed(t *testing.T) {
	repo := newRepo(t)
	failOnGit(t, "worktree prune")
	err := AddWorktree(context.Background(), repo, filepath.Join(filepath.Dir(repo), "w"), "motita/s1")
	if err == nil {
		t.Fatal("a failed prune must be reported")
	}
	if !strings.Contains(err.Error(), "stale worktree registration") {
		t.Errorf("the error must name the step that failed, got %q", err)
	}
}

// TestAddWorktreeRefusesABranchAlreadyCheckedOutElsewhere is the rule that
// stops two sessions from being pointed at one directory: git refuses it, and
// the refusal is reported rather than second-guessed.
func TestAddWorktreeRefusesABranchAlreadyCheckedOutElsewhere(t *testing.T) {
	repo := newRepo(t)
	first := filepath.Join(filepath.Dir(repo), "w1")
	addWorktree(t, repo, first, "motita/s1")
	err := AddWorktree(context.Background(), repo, filepath.Join(filepath.Dir(repo), "w2"), "motita/s1")
	if err == nil {
		t.Fatal("a branch already checked out in a worktree cannot be checked out again")
	}
	if !strings.Contains(err.Error(), "could not be created") {
		t.Errorf("the refusal must say what failed, got %q", err)
	}
}

// TestAddWorktreeAttachesAnExistingBranch is what makes a resumed session work.
//
// The branch outlives the worktree, so a session whose worktree was removed -
// by a cleanup, by a restart, by the user - must find its own commits again
// rather than starting on a fresh branch from HEAD. Measured: `worktree add
// <path> <existing-branch>` attaches; `worktree add -b <existing>` fails with
// "a branch named X already exists".
func TestAddWorktreeAttachesAnExistingBranch(t *testing.T) {
	ctx := context.Background()
	repo := newRepo(t)
	first := filepath.Join(filepath.Dir(repo), "w1")
	addWorktree(t, repo, first, "motita/s1")
	write(t, filepath.Join(first, "kept.txt"), "precious\n")
	git(t, first, "add", "kept.txt")
	git(t, first, "commit", "-qm", "precious work")
	if err := RemoveWorktree(ctx, repo, first, false); err != nil {
		t.Fatalf("RemoveWorktree: %v", err)
	}

	second := filepath.Join(filepath.Dir(repo), "w2")
	addWorktree(t, repo, second, "motita/s1")
	if !exists(filepath.Join(second, "kept.txt")) {
		t.Fatal("re-attaching must recover the branch's commits, not start from HEAD")
	}
}

// TestAddWorktreeRefusesARepositoryWithNoCommits: `worktree add -b` on an
// unborn HEAD fails with a message about an invalid reference, which names
// neither the cause nor the fix. A freshly created project folder is exactly
// this case.
func TestAddWorktreeRefusesARepositoryWithNoCommits(t *testing.T) {
	repo := plainDir(t, "proj")
	git(t, repo, "init", "-q", "-b", "main")
	err := AddWorktree(context.Background(), repo, filepath.Join(filepath.Dir(repo), "w"), "motita/s1")
	if err == nil {
		t.Fatal("a repository with no commits cannot be branched from")
	}
	if !strings.Contains(err.Error(), "no commits") {
		t.Errorf("the refusal must name the cause, got %q", err)
	}
}

func TestAddWorktreeRefusesADirectoryThatIsNotARepository(t *testing.T) {
	err := AddWorktree(context.Background(), plainDir(t, "plain"), filepath.Join(t.TempDir(), "w"), "motita/s1")
	if err != ErrNotARepo {
		t.Fatalf("a project that is not a repository cannot have a worktree, got %v", err)
	}
}

// TestAddWorktreeRefusesASubdirectoryOfARepository is the check that keeps a
// worktree from being a surprise.
//
// `git worktree` checks out the WHOLE repository. A project registered as
// <repo>/web would therefore get a worktree containing every other part of that
// repository - a copy of a tree the agent was never pointed at, including
// whatever else lives in it. Refusing is the only answer that keeps the
// worktree meaning what the project means.
func TestAddWorktreeRefusesASubdirectoryOfARepository(t *testing.T) {
	repo := newRepo(t)
	sub := filepath.Join(repo, "web")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	err := AddWorktree(context.Background(), sub, filepath.Join(filepath.Dir(repo), "w"), "motita/s1")
	if err == nil {
		t.Fatal("a subdirectory of a repository is not the project a worktree would check out")
	}
	if !strings.Contains(err.Error(), "root of its repository") {
		t.Errorf("the refusal must say why, got %q", err)
	}
}

// TestAddWorktreeReportsADirectoryThatCannotBePrepared: a worktree under a
// regular file cannot have its parent created, and the error must be the
// directory's rather than git's.
func TestAddWorktreeReportsADirectoryThatCannotBePrepared(t *testing.T) {
	repo := newRepo(t)
	write(t, filepath.Join(repo, "notadir"), "x\n")
	err := AddWorktree(context.Background(), repo, filepath.Join(repo, "notadir", "w"), "motita/s1")
	if err == nil {
		t.Fatal("a worktree under a regular file cannot be created")
	}
	if !strings.Contains(err.Error(), "directory could not be prepared") {
		t.Errorf("the refusal must name the directory, got %q", err)
	}
}

// ---------- RemoveWorktree and DeleteBranch ----------

// TestRemoveWorktreeKeepsTheBranch: the worktree is where the work happened and
// the branch IS the work. A cleanup that removed both would discard commits.
func TestRemoveWorktreeKeepsTheBranch(t *testing.T) {
	ctx := context.Background()
	repo := newRepo(t)
	wt := filepath.Join(filepath.Dir(repo), "w")
	addWorktree(t, repo, wt, "motita/s1")
	if err := RemoveWorktree(ctx, repo, wt, false); err != nil {
		t.Fatalf("RemoveWorktree: %v", err)
	}
	if exists(wt) {
		t.Error("the worktree directory must be gone")
	}
	if !BranchExists(ctx, repo, "motita/s1") {
		t.Error("the branch must survive the removal of its worktree")
	}
}

// TestRemoveWorktreeRefusesADirtyTreeAndForceOverrides: git's own refusal is
// passed through rather than decided here, because throwing away uncommitted
// work is the user's call.
func TestRemoveWorktreeRefusesADirtyTreeAndForceOverrides(t *testing.T) {
	ctx := context.Background()
	repo := newRepo(t)
	wt := filepath.Join(filepath.Dir(repo), "w")
	addWorktree(t, repo, wt, "motita/s1")
	write(t, filepath.Join(wt, "f.txt"), "uncommitted\n")

	if dirty, err := Dirty(ctx, wt); err != nil || !dirty {
		t.Fatalf("Dirty = %v, %v", dirty, err)
	}
	err := RemoveWorktree(ctx, repo, wt, false)
	if err == nil {
		t.Fatal("a worktree with uncommitted changes must not be removed without force")
	}
	if !strings.Contains(err.Error(), "could not be removed") {
		t.Errorf("the refusal must say what failed, got %q", err)
	}
	if !exists(wt) {
		t.Fatal("the refused worktree must still be there")
	}
	if err := RemoveWorktree(ctx, repo, wt, true); err != nil {
		t.Fatalf("force must remove it: %v", err)
	}
	if exists(wt) {
		t.Error("the forced removal must have happened")
	}
}

func TestDirtyOfACleanTreeIsFalse(t *testing.T) {
	dirty, err := Dirty(context.Background(), newRepo(t))
	if err != nil {
		t.Fatalf("Dirty: %v", err)
	}
	if dirty {
		t.Error("a freshly committed tree is clean")
	}
}

// TestDeleteBranchRefusesUnmergedWork: -d and never -D. A branch whose commits
// are merged nowhere is work that exists only there, and git's refusal names
// the commits it would have taken.
func TestDeleteBranchRefusesUnmergedWork(t *testing.T) {
	ctx := context.Background()
	repo := newRepo(t)
	wt := filepath.Join(filepath.Dir(repo), "w")
	addWorktree(t, repo, wt, "motita/s1")
	write(t, filepath.Join(wt, "x.txt"), "x\n")
	git(t, wt, "add", "x.txt")
	git(t, wt, "commit", "-qm", "unmerged work")
	if err := RemoveWorktree(ctx, repo, wt, false); err != nil {
		t.Fatalf("RemoveWorktree: %v", err)
	}

	err := DeleteBranch(ctx, repo, "motita/s1")
	if err == nil {
		t.Fatal("a branch with unmerged commits must not be deleted")
	}
	if !strings.Contains(err.Error(), "not deleted") {
		t.Errorf("the refusal must say what failed, got %q", err)
	}
	if !BranchExists(ctx, repo, "motita/s1") {
		t.Fatal("the refused branch must still be there")
	}
}

// TestDeleteBranchRefusesABranchCheckedOutInAWorktree reports git's refusal of
// a branch that is still checked out somewhere.
func TestDeleteBranchRefusesABranchCheckedOutInAWorktree(t *testing.T) {
	repo := newRepo(t)
	wt := filepath.Join(filepath.Dir(repo), "w")
	addWorktree(t, repo, wt, "motita/s1")
	if err := DeleteBranch(context.Background(), repo, "motita/s1"); err == nil {
		t.Fatal("a branch checked out in a worktree cannot be deleted")
	}
}

func TestDeleteBranchDeletesAMergedBranch(t *testing.T) {
	ctx := context.Background()
	repo := newRepo(t)
	wt := filepath.Join(filepath.Dir(repo), "w")
	addWorktree(t, repo, wt, "motita/s1")
	if _, err := MergeInto(ctx, repo, "main", "motita/s1", "integrate"); err != nil {
		t.Fatalf("MergeInto: %v", err)
	}
	if err := RemoveWorktree(ctx, repo, wt, false); err != nil {
		t.Fatalf("RemoveWorktree: %v", err)
	}
	if err := DeleteBranch(ctx, repo, "motita/s1"); err != nil {
		t.Fatalf("a fully merged branch must be deletable: %v", err)
	}
	if BranchExists(ctx, repo, "motita/s1") {
		t.Error("the branch must be gone")
	}
}
