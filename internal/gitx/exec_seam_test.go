package gitx

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// ---------- the exec seam ----------

// TestNoGitIsReportedAsItsOwnFailure drives the branch that cannot be reached
// on a machine that has git: exec.ErrNotFound needs a missing program, and a
// test cannot ask a CI runner to remove one.
//
// It is not decoration: this program runs on other people's machines, and "git
// is not installed" is a different message from "that folder is not a
// repository". Without the seam the branch would ship untested, and the
// coverage gate rejects exactly that.
func TestNoGitIsReportedAsItsOwnFailure(t *testing.T) {
	noGit(t)
	ctx := context.Background()
	repo := t.TempDir()

	if got := Display(ctx, repo); got != "" {
		t.Errorf("with no git there is no branch to draw, got %q", got)
	}
	if err := Repo(ctx, repo); err != ErrNoGit {
		t.Errorf("Repo must report ErrNoGit, got %v", err)
	}
	if _, _, err := Head(ctx, repo); err != ErrNoGit {
		t.Errorf("Head must report ErrNoGit, got %v", err)
	}
	if _, err := Root(ctx, repo); err != ErrNoGit {
		t.Errorf("Root must report ErrNoGit, got %v", err)
	}
	if err := AddWorktree(ctx, repo, filepath.Join(repo, "w"), "b"); err != ErrNoGit {
		t.Errorf("AddWorktree must report ErrNoGit, got %v", err)
	}
	if err := RemoveWorktree(ctx, repo, filepath.Join(repo, "w"), false); err != ErrNoGit {
		t.Errorf("RemoveWorktree must report ErrNoGit, got %v", err)
	}
	if err := PruneWorktrees(ctx, repo); err != ErrNoGit {
		t.Errorf("PruneWorktrees must report ErrNoGit, got %v", err)
	}
	if _, err := MergeInto(ctx, repo, "main", "b", "m"); err != ErrNoGit {
		t.Errorf("MergeInto must report ErrNoGit, got %v", err)
	}
	if err := DeleteBranch(ctx, repo, "b"); err != ErrNoGit {
		t.Errorf("DeleteBranch must report ErrNoGit, got %v", err)
	}
	if _, _, err := CommitsBetween(ctx, repo, "main", "b"); err != ErrNoGit {
		t.Errorf("CommitsBetween must report ErrNoGit, got %v", err)
	}
	if _, err := Dirty(ctx, repo); err != ErrNoGit {
		t.Errorf("Dirty must report ErrNoGit, got %v", err)
	}
	if HasCommits(ctx, repo) {
		t.Error("HasCommits must be false without git")
	}
	if BranchExists(ctx, repo, "b") {
		t.Error("BranchExists must be false without git")
	}
}

// TestNoGitIsReportedByAMerge: the headless half of the same test. With git
// gone from the start there is nothing to merge, and the missing program must
// be what the caller is told.
func TestNoGitIsReportedByAMerge(t *testing.T) {
	noGit(t)
	if _, err := MergeInto(context.Background(), t.TempDir(), "main", "b", "m"); err != ErrNoGit {
		t.Errorf("a merge with no git must report ErrNoGit, got %v", err)
	}
}

// ---------- samePath ----------

// TestSamePathHandlesUnresolvablePaths: EvalSymlinks fails for a path that does
// not exist, and the comparison must fall back to the written path rather than
// reporting two identical paths as different. samePath's only caller is a guard,
// and a guard that refuses a valid worktree is worse than one that compares
// literally.
func TestSamePathHandlesUnresolvablePaths(t *testing.T) {
	if !samePath("/nonexistent/a", "/nonexistent/a") {
		t.Error("the same written path must compare equal even when it does not exist")
	}
	if samePath("/nonexistent/a", "/nonexistent/b") {
		t.Error("different paths must not compare equal")
	}
	if samePath("", "/tmp") || samePath("/tmp", "") {
		t.Error("an empty path names nothing and must never compare equal")
	}
}

// TestSamePathResolvesSymlinks: a home directory or /tmp is commonly reached
// through a symlink, and the guard must not call a project and its own
// repository root two different places.
func TestSamePathResolvesSymlinks(t *testing.T) {
	real := plainDir(t, "real")
	link := filepath.Join(filepath.Dir(real), "link")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("symlinks are not available here: %v", err)
	}
	if !samePath(real, link) {
		t.Errorf("a symlink and its target are the same directory: %q vs %q", real, link)
	}
	// A removed worktree under a link (macOS's /var) is listed by git in its resolved form.
	if gone := "removed"; !samePath(filepath.Join(link, gone), filepath.Join(real, gone)) {
		t.Errorf("a path that does not exist resolves through its parent: %q vs %q", link, real)
	}
}

// TestSamePathForCallersOutsideThePackage: SamePath is the exported door to
// samePath, and a caller outside the package has no other way to ask the
// question. It must give the same answer.
func TestSamePathForCallersOutsideThePackage(t *testing.T) {
	dir := plainDir(t, "outside")
	if !SamePath(dir, dir) {
		t.Error("a path must be the same place as itself")
	}
	if SamePath(dir, filepath.Join(dir, "..")) {
		t.Error("a directory and its parent are not the same place")
	}
}

// TestSamePathComparesRelativePaths: a relative path must be resolved against
// the working directory rather than compared to an absolute one as text.
func TestSamePathComparesRelativePaths(t *testing.T) {
	dir := plainDir(t, "here")
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(wd) })
	if !samePath(".", dir) {
		t.Errorf("a relative path names the directory it points at: %q vs %q", ".", dir)
	}
}

// ---------- the failure branches that need a stub ----------

// TestFirstLineOfASilentFailure: git can fail with nothing on its error stream,
// and the message a user reads must still say something rather than end in a
// colon.
func TestFirstLineOfASilentFailure(t *testing.T) {
	failSilentlyOnGit(t, "status --porcelain")
	_, err := Dirty(context.Background(), newRepo(t))
	if err == nil {
		t.Fatal("the status must fail")
	}
	if !strings.Contains(err.Error(), "git reported no detail") {
		t.Errorf("a silent failure must still explain itself, got %q", err)
	}
}

// TestHeadOfAnUnbornRepositoryWhoseSymbolicRefFails: the last fallback of Head
// is `symbolic-ref`, and a git that cannot answer THAT leaves nothing to report
// but the failure.
func TestHeadOfAnUnbornRepositoryWhoseSymbolicRefFails(t *testing.T) {
	repo := plainDir(t, "unborn")
	git(t, repo, "init", "-q", "-b", "trunk")
	failOnGit(t, "symbolic-ref")
	_, _, err := Head(context.Background(), repo)
	if err == nil {
		t.Fatal("a repository whose branch cannot be read has no branch to report")
	}
	if !strings.Contains(err.Error(), "could not read the branch") {
		t.Errorf("the failure must say what could not be read, got %q", err)
	}
}

// TestCommitsBetweenReportsASecondProbeThatFails: the range that asks what the
// BASE branch has is a second command, and it can fail on its own.
func TestCommitsBetweenReportsASecondProbeThatFails(t *testing.T) {
	repo := newRepo(t)
	addWorktree(t, repo, filepath.Join(filepath.Dir(repo), "w"), "motita/s1")
	// Both branches exist, so the FIRST listing succeeds and only the second -
	// the range that asks what the base branch has - is made to fail.
	failOnGit(t, "motita/s1..main")
	_, _, err := CommitsBetween(context.Background(), repo, "main", "motita/s1")
	if err == nil {
		t.Fatal("a failed listing of the base branch's commits must be an error")
	}
	if !strings.Contains(err.Error(), "base branch's commits") {
		t.Errorf("the failure must name which listing failed, got %q", err)
	}
}

// TestNoGitIsReportedByAMergeThatReachedGitOnce: git is present when the
// checkout's branch is read and gone by the time the merge runs - which is what
// an uninstall or a PATH change during a long-running gateway looks like.
func TestNoGitIsReportedByAMergeThatReachedGitOnce(t *testing.T) {
	repo := newRepo(t)
	noGitOnGit(t, "merge --no-ff")
	_, err := MergeInto(context.Background(), repo, "main", "main", "integrate")
	if err != ErrNoGit {
		t.Errorf("a merge that loses git must report ErrNoGit, got %v", err)
	}
}

// TestForceRemovesALockedWorktree: a single --force still refuses a LOCKED worktree
// ("use 'remove -f -f'"), and a caller that asked to force meant the removal.
func TestForceRemovesALockedWorktree(t *testing.T) {
	ctx := context.Background()
	repo := newRepo(t)
	wt := filepath.Join(filepath.Dir(repo), "locked")
	addWorktree(t, repo, wt, "motita/locked")
	write(t, filepath.Join(wt, "f.txt"), "uncommitted\n")
	if out, err := exec.Command("git", "-C", repo, "worktree", "lock", wt).CombinedOutput(); err != nil {
		t.Fatalf("lock: %v %s", err, out)
	}
	if err := RemoveWorktree(ctx, repo, wt, true); err != nil {
		t.Fatalf("force must remove a locked worktree: %v", err)
	}
	if exists(wt) {
		t.Error("the locked worktree must be gone")
	}
}

// TestPruneClearsAMissingRegistration: a worktree whose directory vanished is pruned, and a
// live one is left alone.
func TestPruneClearsAMissingRegistration(t *testing.T) {
	ctx := context.Background()
	repo := newRepo(t)
	gone := filepath.Join(filepath.Dir(repo), "gone")
	live := filepath.Join(filepath.Dir(repo), "live")
	addWorktree(t, repo, gone, "motita/gone")
	addWorktree(t, repo, live, "motita/live")
	if err := os.RemoveAll(gone); err != nil {
		t.Fatal(err)
	}
	if err := PruneWorktrees(ctx, repo); err != nil {
		t.Fatalf("PruneWorktrees: %v", err)
	}
	out, _ := exec.Command("git", "-C", repo, "worktree", "list").CombinedOutput()
	if strings.Contains(string(out), gone) || !strings.Contains(string(out), live) {
		t.Errorf("after the prune the list must hold the live worktree only:\n%s", out)
	}
}

// TestRemovingAWorktreeIsNotHeldToTheReadTimeout: `git worktree remove` deletes every file of
// the checkout. It inherited the 10 s bound meant for reads, so a session whose checkout held a
// real node_modules was killed halfway and the user was told "git reported no detail" - the
// reported "cannot delete a project session".
func TestRemovingAWorktreeIsNotHeldToTheReadTimeout(t *testing.T) {
	var limit time.Duration
	withExec(t, func(ctx context.Context, name string, args ...string) ([]byte, error) {
		dl, ok := ctx.Deadline()
		if !ok {
			t.Fatal("the removal has no bound at all")
		}
		limit = time.Until(dl)
		return nil, nil
	})
	if err := RemoveWorktree(context.Background(), t.TempDir(), "x", true); err != nil {
		t.Fatal(err)
	}
	if limit <= gitTimeout {
		t.Errorf("the removal was given %v, no more than a read (%v): a large checkout cannot finish", limit, gitTimeout)
	}
}

// TestAKilledGitSaysItRanOutOfTime: a process killed by its deadline prints nothing, and the
// message used to be "git reported no detail" - which names no cause.
func TestAKilledGitSaysItRanOutOfTime(t *testing.T) {
	withExec(t, func(ctx context.Context, name string, args ...string) ([]byte, error) {
		<-ctx.Done()
		return nil, errors.New("signal: killed")
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	prev := removeTimeoutFor
	removeTimeoutFor = 20 * time.Millisecond
	defer func() { removeTimeoutFor = prev }()
	err := RemoveWorktree(ctx, t.TempDir(), "x", false)
	if err == nil || !strings.Contains(err.Error(), "did not finish in time") {
		t.Fatalf("a timeout must say so, got %v", err)
	}
}
