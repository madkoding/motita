package gitx

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// These tests drive REAL git in a REAL temporary repository. That is not
// thoroughness for its own sake: every rule this package implements is a rule
// about what git actually does, and a fake would only assert what the author
// believed git does. The branch of a detached HEAD, the message a merge prints
// for a conflict, whether --autostash leaks a stash - each was measured against
// git 2.47.3 before it was written down here, and each would be a wrong test if
// it were assumed.
//
// The exec seam is the ONE exception, and it is used only for failures a test
// cannot otherwise cause: a missing git, and a recovery step that fails at the
// exact moment the package is mid-recovery. Everything else runs the real
// program.

// git runs git in dir and fails the test if it does not succeed. Setup uses it;
// the package under test never does.
func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

// withExec replaces the exec seam for one test.
//
// The seam is at the PROCESS, not at the git command, and that is what makes
// every ErrNoGit check in the package reachable from one place: the fake returns
// exec.ErrNotFound - the REAL error the real call produces - so the mapping
// inside execute is exercised rather than bypassed.
func withExec(t *testing.T, fn func(context.Context, string, ...string) ([]byte, error)) {
	t.Helper()
	previous := execCommand
	execCommand = fn
	t.Cleanup(func() { execCommand = previous })
}

// noGit makes every git call report a missing program.
func noGit(t *testing.T) {
	t.Helper()
	withExec(t, func(context.Context, string, ...string) ([]byte, error) {
		return nil, exec.ErrNotFound
	})
}

// stubOn runs real git EXCEPT for commands containing needle, which are
// answered with out and err instead.
//
// It is how the failure branches that cannot be produced on demand are
// produced: a recovery step that fails, a git command that prints nothing, a
// program that disappears mid-merge. Each of those is a branch a user can
// reach, so each is a branch that has to be run here rather than assumed.
func stubOn(t *testing.T, needle, out string, err error) {
	t.Helper()
	previous := execCommand
	withExec(t, func(ctx context.Context, name string, args ...string) ([]byte, error) {
		if strings.Contains(strings.Join(args, " "), needle) {
			return []byte(out), err
		}
		return previous(ctx, name, args...)
	})
}

// failOnGit makes the commands containing needle fail the way a real git
// command fails: exit status non-zero, with a line on the error stream.
func failOnGit(t *testing.T, needle string) {
	t.Helper()
	stubOn(t, needle, "fatal: simulated failure", errors.New("exit status 128"))
}

// failSilentlyOnGit makes the commands containing needle fail with NO output -
// which is what a git killed by a signal, or a wrapper script that swallowed
// its own error, looks like from here.
func failSilentlyOnGit(t *testing.T, needle string) {
	t.Helper()
	stubOn(t, needle, "", errors.New("exit status 1"))
}

// noGitOnGit makes the commands containing needle report a missing program,
// while every other command still runs real git.
//
// That combination is the one a machine reaches when git is removed or the
// PATH changes while the gateway is running, and it is the only way to reach
// the ErrNoGit check that sits INSIDE a recovery.
func noGitOnGit(t *testing.T, needle string) {
	t.Helper()
	stubOn(t, needle, "", exec.ErrNotFound)
}

// newRepo returns a repository with one commit on main, with an identity set.
func newRepo(t *testing.T) string {
	t.Helper()
	repo := filepath.Join(t.TempDir(), "proj")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	git(t, repo, "init", "-q", "-b", "main")
	git(t, repo, "config", "user.email", "test@example.com")
	git(t, repo, "config", "user.name", "Test")
	write(t, filepath.Join(repo, "f.txt"), "base\n")
	git(t, repo, "add", "f.txt")
	git(t, repo, "commit", "-qm", "init")
	return repo
}

// newRepoNoIdentity is a repository with no user.name and no user.email, which
// is what this program's own clone produces: measured on this machine, the
// project the gateway cloned has neither, and there is no ~/.gitconfig.
func newRepoNoIdentity(t *testing.T) string {
	t.Helper()
	repo := newRepo(t)
	git(t, repo, "config", "--unset", "user.name")
	git(t, repo, "config", "--unset", "user.email")
	return repo
}

func write(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func read(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(b))
}

func plainDir(t *testing.T, name string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

// addWorktree is the three lines every test that needs a session worktree
// repeats; keeping it here is what keeps the tests about behaviour.
func addWorktree(t *testing.T, repo, wt, branch string) {
	t.Helper()
	if err := AddWorktree(context.Background(), repo, wt, branch); err != nil {
		t.Fatalf("AddWorktree: %v", err)
	}
}

// ---------- Repo ----------

// TestRepoKnowsARepositoryFromADirectory: the two failures are DIFFERENT - a
// directory with no repository makes the probe fail ("fatal: not a git
// repository"), a bare repository answers it successfully with "false" - and
// both are ErrNotARepo.
func TestRepoKnowsARepositoryFromADirectory(t *testing.T) {
	ctx := context.Background()
	if err := Repo(ctx, newRepo(t)); err != nil {
		t.Errorf("a checkout is a repository: %v", err)
	}
	if err := Repo(ctx, plainDir(t, "plain")); err != ErrNotARepo {
		t.Errorf("a plain directory is not a repository, got %v", err)
	}
	if err := Repo(ctx, ""); err != ErrNotARepo {
		t.Errorf("an empty path is not a repository, got %v", err)
	}
	bare := plainDir(t, "bare.git")
	git(t, bare, "init", "-q", "--bare", "-b", "main")
	if err := Repo(ctx, bare); err != ErrNotARepo {
		t.Errorf("a bare repository has no working tree, got %v", err)
	}
}

// ---------- Head and Display ----------

// TestDisplayNamesEveryStateAFrontEndDraws is the matrix a front end depends on.
//
// Each of these was measured against git 2.47.3, and two of them are the reason
// this is not one call to `rev-parse --abbrev-ref HEAD`: a detached checkout
// answers "HEAD" (git's internal name for "no branch") and a repository with no
// commits FAILS that command with "ambiguous argument 'HEAD'".
func TestDisplayNamesEveryStateAFrontEndDraws(t *testing.T) {
	ctx := context.Background()

	if got := Display(ctx, plainDir(t, "plain")); got != "" {
		t.Errorf("a directory that is not a repository must draw no branch, got %q", got)
	}

	unborn := plainDir(t, "unborn")
	git(t, unborn, "init", "-q", "-b", "trunk")
	if got := Display(ctx, unborn); got != "trunk" {
		t.Errorf("a repository with no commits still has a branch, got %q", got)
	}

	if got := Display(ctx, newRepo(t)); got != "main" {
		t.Errorf("a normal checkout must draw its branch, got %q", got)
	}

	detached := newRepo(t)
	git(t, detached, "checkout", "-q", "--detach", "HEAD")
	got := Display(ctx, detached)
	if !strings.HasPrefix(got, "detached at ") || len(got) <= len("detached at ") {
		t.Errorf("a detached checkout must name the commit it is on, got %q", got)
	}
}

// TestDisplayOfASubdirectoryIsItsRepositorysBranch: a session's workspace may
// be a subdirectory of a project, and the branch it is working on is still the
// repository's branch. Answering "" there would hide the branch from every user
// whose project has a src/ layout.
func TestDisplayOfASubdirectoryIsItsRepositorysBranch(t *testing.T) {
	repo := newRepo(t)
	sub := filepath.Join(repo, "internal", "deep")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if got := Display(context.Background(), sub); got != "main" {
		t.Errorf("a subdirectory of a checkout is on the checkout's branch, got %q", got)
	}
}

// TestHeadOfAWorkTreeWithNoUsableHEAD: a directory that looks like a checkout
// but whose HEAD names a branch that does not exist. Measured: every probe
// answers "fatal: not a git repository", including the symbolic-ref fallback,
// so the branch is reported as the repository error it is.
func TestHeadOfAWorkTreeWithNoUsableHEAD(t *testing.T) {
	repo := plainDir(t, "broken")
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, ".git", "HEAD"), []byte("ref: refs/heads/nope\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	branch, detached, err := Head(context.Background(), repo)
	if err == nil {
		t.Fatalf("a broken HEAD is not a branch: %q %q", branch, detached)
	}
	if err != ErrNotARepo {
		t.Errorf("a work tree git cannot read is not a repository, got %v", err)
	}
}

func TestRootAndHasCommitsAndBranchExists(t *testing.T) {
	ctx := context.Background()
	repo := newRepo(t)
	root, err := Root(ctx, repo)
	if err != nil {
		t.Fatalf("Root: %v", err)
	}
	if root != resolve(repo) {
		t.Errorf("Root = %q, want %q", root, resolve(repo))
	}
	if !HasCommits(ctx, repo) {
		t.Error("a repository with a commit must report one")
	}
	if BranchExists(ctx, repo, "nope") {
		t.Error("a branch that does not exist must not be reported as existing")
	}
	git(t, repo, "branch", "there")
	if !BranchExists(ctx, repo, "there") {
		t.Error("a branch that exists must be reported as existing")
	}
}

func TestRootOfADirectoryThatIsNotARepositoryIsAnError(t *testing.T) {
	if _, err := Root(context.Background(), plainDir(t, "plain")); err != ErrNotARepo {
		t.Fatalf("Root of a non-repository must be ErrNotARepo, got %v", err)
	}
}

// ---------- CommitsBetween ----------

// TestCommitsBetweenReadsBothDirections: "how far apart is this session from
// where it started" is two lists, and a stored count would be a number that
// used to be true.
func TestCommitsBetweenReadsBothDirections(t *testing.T) {
	ctx := context.Background()
	repo := newRepo(t)
	wt := filepath.Join(filepath.Dir(repo), "w")
	addWorktree(t, repo, wt, "motita/s1")
	for _, name := range []string{"one", "two"} {
		write(t, filepath.Join(wt, name+".txt"), name+"\n")
		git(t, wt, "add", name+".txt")
		git(t, wt, "commit", "-qm", "commit "+name)
	}
	// The base branch moves too, so `behind` is not empty.
	write(t, filepath.Join(repo, "onmain.txt"), "on main\n")
	git(t, repo, "add", "onmain.txt")
	git(t, repo, "commit", "-qm", "work on main")

	ahead, behind, err := CommitsBetween(ctx, repo, "main", "motita/s1")
	if err != nil {
		t.Fatalf("CommitsBetween: %v", err)
	}
	if len(ahead) != 2 {
		t.Errorf("ahead = %d commits, want 2: %+v", len(ahead), ahead)
	}
	if len(behind) != 1 {
		t.Errorf("behind = %d commits, want 1: %+v", len(behind), behind)
	}
	if ahead[0].SHA == "" || ahead[0].Subject != "commit two" {
		t.Errorf("a commit must carry its sha and subject: %+v", ahead[0])
	}
}

// TestCommitsBetweenOfBranchesThatAgreeIsEmpty: git prints nothing for a range
// with no commits in it, and a report of "0 ahead, 0 behind" must not be a list
// holding one empty commit.
func TestCommitsBetweenOfBranchesThatAgreeIsEmpty(t *testing.T) {
	ahead, behind, err := CommitsBetween(context.Background(), newRepo(t), "main", "main")
	if err != nil {
		t.Fatalf("CommitsBetween: %v", err)
	}
	if len(ahead) != 0 || len(behind) != 0 {
		t.Errorf("identical branches are neither ahead nor behind: %+v %+v", ahead, behind)
	}
}

func TestCommitsBetweenReportsAnUnknownBranch(t *testing.T) {
	ctx := context.Background()
	repo := newRepo(t)
	if _, _, err := CommitsBetween(ctx, repo, "main", "nope"); err == nil {
		t.Error("asking about a branch that does not exist must be an error")
	}
	if _, _, err := CommitsBetween(ctx, repo, "nope", "main"); err == nil {
		t.Error("asking about a base branch that does not exist must be an error")
	}
}

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

// ---------- MergeInto ----------

// TestMergeIntoIntegratesTheBranchIntoTheProject is the whole feature: the
// session's commits arrive in the project's checkout, in ONE commit whose
// message names the session.
func TestMergeIntoIntegratesTheBranchIntoTheProject(t *testing.T) {
	ctx := context.Background()
	repo := newRepo(t)
	wt := filepath.Join(filepath.Dir(repo), "w")
	addWorktree(t, repo, wt, "motita/s1")
	write(t, filepath.Join(wt, "new.txt"), "session work\n")
	git(t, wt, "add", "new.txt")
	git(t, wt, "commit", "-qm", "session work")

	res, err := MergeInto(ctx, repo, "main", "motita/s1", "motita: integrate session s1")
	if err != nil {
		t.Fatalf("MergeInto: %v", err)
	}
	if res.SHA == "" || res.Subject != "motita: integrate session s1" {
		t.Errorf("a merge must report the commit it produced: %+v", res)
	}
	if !exists(filepath.Join(repo, "new.txt")) {
		t.Fatal("the session's work must be in the project's checkout now")
	}
	// --no-ff is load-bearing: a fast-forward would leave no record that a
	// session's work arrived.
	if subject := git(t, repo, "log", "-1", "--format=%s"); subject != "motita: integrate session s1" {
		t.Errorf("the integration commit must carry its message, got %q", subject)
	}
	if parents := git(t, repo, "log", "-1", "--format=%p"); len(strings.Fields(parents)) != 2 {
		t.Errorf("a --no-ff merge has two parents, got %q", parents)
	}
}

// TestMergeIntoRefusesAnotherBranch: merging while the checkout is on a
// different branch would put the session's work somewhere the user did not ask
// for, and the mistake is invisible until they look.
func TestMergeIntoRefusesAnotherBranch(t *testing.T) {
	ctx := context.Background()
	repo := newRepo(t)
	wt := filepath.Join(filepath.Dir(repo), "w")
	addWorktree(t, repo, wt, "motita/s1")
	git(t, repo, "checkout", "-q", "-b", "elsewhere")

	_, err := MergeInto(ctx, repo, "main", "motita/s1", "integrate")
	if err == nil {
		t.Fatal("merging into a branch the checkout is not on must be refused")
	}
	if !strings.Contains(err.Error(), "elsewhere") {
		t.Errorf("the refusal must name the branch it found, got %q", err)
	}
}

func TestMergeIntoRefusesADetachedCheckout(t *testing.T) {
	repo := newRepo(t)
	git(t, repo, "checkout", "-q", "--detach", "HEAD")
	_, err := MergeInto(context.Background(), repo, "main", "motita/s1", "integrate")
	if err == nil {
		t.Fatal("a detached checkout has no branch to merge into")
	}
	if !strings.Contains(err.Error(), "detached") {
		t.Errorf("the refusal must say the checkout is detached, got %q", err)
	}
}

// TestMergeIntoRefusesAWorkTreeItCannotRead: the state a rebase or a bisect
// leaves behind. The failure must be reported, not turned into a merge into
// whatever branch git happened to name.
func TestMergeIntoRefusesAWorkTreeItCannotRead(t *testing.T) {
	repo := plainDir(t, "broken")
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, ".git", "HEAD"), []byte("ref: refs/heads/nope\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := MergeInto(context.Background(), repo, "main", "b", "integrate"); err == nil {
		t.Fatal("a checkout git cannot read has no branch to merge into")
	}
}

// TestMergeIntoRollsBackAConflict is the one that protects the user's checkout.
//
// Measured on git 2.47.3: a conflicting merge leaves MERGE_HEAD behind and
// `merge --abort` restores the tree completely. Leaving that state behind would
// be leaving somebody in the middle of a merge they never asked for.
func TestMergeIntoRollsBackAConflict(t *testing.T) {
	ctx := context.Background()
	repo := newRepo(t)
	wt := filepath.Join(filepath.Dir(repo), "w")
	addWorktree(t, repo, wt, "motita/s1")
	write(t, filepath.Join(wt, "f.txt"), "from the session\n")
	git(t, wt, "commit", "-qam", "session edit")
	write(t, filepath.Join(repo, "f.txt"), "from main\n")
	git(t, repo, "commit", "-qam", "main edit")

	_, err := MergeInto(ctx, repo, "main", "motita/s1", "integrate")
	if err == nil {
		t.Fatal("a conflicting merge must be reported as a failure")
	}
	if !strings.Contains(err.Error(), "rolled back") {
		t.Errorf("the error must say the merge was rolled back, got %q", err)
	}
	if got := read(t, filepath.Join(repo, "f.txt")); got != "from main" {
		t.Errorf("the checkout must be back to its own content, got %q", got)
	}
	if exists(filepath.Join(repo, ".git", "MERGE_HEAD")) {
		t.Fatal("no merge may be left in progress in the user's checkout")
	}
	if dirty, err := Dirty(ctx, repo); err != nil || dirty {
		t.Errorf("the checkout must be clean after the rollback: dirty=%v err=%v", dirty, err)
	}
}

// TestMergeIntoReportsARollbackThatFailed drives the one recovery step that
// cannot be made to fail with real git: the conflict is real, and only the
// abort is simulated as failing. The error must say the checkout was left in
// the conflicted state rather than claim a rollback that did not happen.
func TestMergeIntoReportsARollbackThatFailed(t *testing.T) {
	ctx := context.Background()
	repo := newRepo(t)
	wt := filepath.Join(filepath.Dir(repo), "w")
	addWorktree(t, repo, wt, "motita/s1")
	write(t, filepath.Join(wt, "f.txt"), "from the session\n")
	git(t, wt, "commit", "-qam", "session edit")
	write(t, filepath.Join(repo, "f.txt"), "from main\n")
	git(t, repo, "commit", "-qam", "main edit")

	failOnGit(t, "merge --abort")
	_, err := MergeInto(ctx, repo, "main", "motita/s1", "integrate")
	if err == nil {
		t.Fatal("a conflict must be reported as a failure")
	}
	if !strings.Contains(err.Error(), "could not be rolled back") {
		t.Errorf("the error must say the rollback failed, got %q", err)
	}
}

// TestMergeIntoReportsARefusalThatStartedNoMerge: a merge that never began
// (an unknown branch) has no MERGE_HEAD, so nothing may be aborted and the
// error must not claim a rollback.
func TestMergeIntoReportsARefusalThatStartedNoMerge(t *testing.T) {
	repo := newRepo(t)
	_, err := MergeInto(context.Background(), repo, "main", "does-not-exist", "m")
	if err == nil {
		t.Fatal("merging a branch that does not exist must fail")
	}
	if strings.Contains(err.Error(), "rolled back") {
		t.Errorf("nothing was rolled back, so the error must not say so: %q", err)
	}
	if got := read(t, filepath.Join(repo, "f.txt")); got != "base" {
		t.Errorf("the checkout must be untouched, got %q", got)
	}
}

// TestMergeIntoKeepsAnUncommittedChangeInTheCheckout: --autostash is what stops
// a user's work-in-progress from blocking the integration, and what stops the
// integration from taking it away.
//
// Measured both ways: with a dirty, UNRELATED file the merge succeeds and the
// dirty file is unchanged; with a dirty file AND a conflict, `merge --abort`
// restores the dirty file and no stash is left behind.
func TestMergeIntoKeepsAnUncommittedChangeInTheCheckout(t *testing.T) {
	ctx := context.Background()
	repo := newRepo(t)
	write(t, filepath.Join(repo, "other.txt"), "tracked\n")
	git(t, repo, "add", "other.txt")
	git(t, repo, "commit", "-qm", "add other")
	wt := filepath.Join(filepath.Dir(repo), "w")
	addWorktree(t, repo, wt, "motita/s1")
	write(t, filepath.Join(wt, "new.txt"), "session work\n")
	git(t, wt, "add", "new.txt")
	git(t, wt, "commit", "-qm", "session work")
	write(t, filepath.Join(repo, "other.txt"), "uncommitted user change\n")

	if _, err := MergeInto(ctx, repo, "main", "motita/s1", "integrate"); err != nil {
		t.Fatalf("MergeInto with a dirty unrelated file: %v", err)
	}
	if got := read(t, filepath.Join(repo, "other.txt")); got != "uncommitted user change" {
		t.Errorf("the user's uncommitted change must survive the merge, got %q", got)
	}
	if got := git(t, repo, "stash", "list"); got != "" {
		t.Errorf("no stash may be left behind, got %q", got)
	}
}

// TestMergeIntoKeepsAnUncommittedChangeWhenTheMergeConflicts is the harder half
// of the same promise, and it is the case that was measured rather than
// reasoned about: --autostash pops the change back even when the merge it
// preceded was aborted.
func TestMergeIntoKeepsAnUncommittedChangeWhenTheMergeConflicts(t *testing.T) {
	ctx := context.Background()
	repo := newRepo(t)
	write(t, filepath.Join(repo, "other.txt"), "tracked\n")
	git(t, repo, "add", "other.txt")
	git(t, repo, "commit", "-qm", "add other")
	wt := filepath.Join(filepath.Dir(repo), "w")
	addWorktree(t, repo, wt, "motita/s1")
	write(t, filepath.Join(wt, "f.txt"), "from the session\n")
	git(t, wt, "commit", "-qam", "session edit")
	write(t, filepath.Join(repo, "f.txt"), "from main\n")
	git(t, repo, "commit", "-qam", "main edit")
	write(t, filepath.Join(repo, "other.txt"), "uncommitted user change\n")

	if _, err := MergeInto(ctx, repo, "main", "motita/s1", "integrate"); err == nil {
		t.Fatal("this merge conflicts")
	}
	if got := read(t, filepath.Join(repo, "f.txt")); got != "from main" {
		t.Errorf("the conflicting file must be back to its own content, got %q", got)
	}
	if got := read(t, filepath.Join(repo, "other.txt")); got != "uncommitted user change" {
		t.Errorf("the unrelated uncommitted change must survive the rollback, got %q", got)
	}
	if got := git(t, repo, "stash", "list"); got != "" {
		t.Errorf("no stash may be left behind, got %q", got)
	}
}

// TestMergeIntoSuppliesAnIdentityWhenTheRepositoryHasNone.
//
// A repository this program cloned has no user.name and no user.email, and the
// machine may have no ~/.gitconfig - both measured here. Without an explicit
// identity the merge fails outright ("unable to auto-detect email address"),
// which is a failure the user cannot act on.
func TestMergeIntoSuppliesAnIdentityWhenTheRepositoryHasNone(t *testing.T) {
	ctx := context.Background()
	repo := newRepoNoIdentity(t)
	wt := filepath.Join(filepath.Dir(repo), "w")
	addWorktree(t, repo, wt, "motita/s1")
	write(t, filepath.Join(wt, "new.txt"), "work\n")
	git(t, wt, "-c", "user.name=session", "-c", "user.email=session@localhost", "add", "new.txt")
	git(t, wt, "-c", "user.name=session", "-c", "user.email=session@localhost", "commit", "-qm", "session work")

	name, email := Identity(ctx, repo)
	if name != FallbackName || email != FallbackEmail {
		t.Errorf("a repository with no identity must fall back, got %q <%s>", name, email)
	}
	if _, err := MergeInto(ctx, repo, "main", "motita/s1", "integrate"); err != nil {
		t.Fatalf("the merge must not fail for a missing identity: %v", err)
	}
	if got := git(t, repo, "log", "-1", "--format=%an <%ae>"); got != FallbackName+" <"+FallbackEmail+">" {
		t.Errorf("the merge commit must carry the resolved identity, got %q", got)
	}
}

func TestIdentityPrefersTheRepositorysOwn(t *testing.T) {
	name, email := Identity(context.Background(), newRepo(t))
	if name != "Test" || email != "test@example.com" {
		t.Errorf("the repository's own identity must win, got %q <%s>", name, email)
	}
}

// TestIdentityTreatsHalfAnIdentityAsNone: git needs both, so half of one is not
// half a solution.
func TestIdentityTreatsHalfAnIdentityAsNone(t *testing.T) {
	repo := newRepo(t)
	git(t, repo, "config", "--unset", "user.email")
	name, email := Identity(context.Background(), repo)
	if name != FallbackName || email != FallbackEmail {
		t.Errorf("a name without an email must fall back, got %q <%s>", name, email)
	}
}

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
