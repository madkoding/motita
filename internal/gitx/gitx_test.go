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

// TestMain hides the machine's git configuration from every test. The tests
// assert what git does in a repository with NO identity and NO signing, so a
// developer's global identity or commit.gpgsign (and a system-wide one) would
// otherwise change the answer. CI has neither, which is why only a developer
// machine fails without this.
func TestMain(m *testing.M) {
	os.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	os.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	os.Exit(m.Run())
}

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
