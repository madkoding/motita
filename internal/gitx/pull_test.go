package gitx

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ---------- PullFastForward ----------

// pushToOrigin gives repo an origin remote holding its current main, which is
// the state a project is in before a "continue": the remote is where the latest
// work lives.
func pushToOrigin(t *testing.T, repo string) string {
	t.Helper()
	remote := filepath.Join(t.TempDir(), "origin.git")
	git(t, repo, "clone", "--bare", "-q", repo, remote)
	git(t, repo, "remote", "add", "origin", remote)
	git(t, repo, "push", "-q", "origin", "main")
	return remote
}

// TestPullFastForwardBringsTheCheckoutUpToTheRemote: the project's main moves
// forward to what origin holds, and the pulled commit is in the working tree.
func TestPullFastForwardBringsTheCheckoutUpToTheRemote(t *testing.T) {
	ctx := context.Background()
	repo := newRepo(t)
	remote := pushToOrigin(t, repo)

	// A change made in ANOTHER clone and pushed, which this checkout does not have.
	elsewhere := filepath.Join(t.TempDir(), "elsewhere")
	git(t, filepath.Dir(repo), "clone", "-q", remote, elsewhere)
	git(t, elsewhere, "config", "user.email", "other@example.com")
	git(t, elsewhere, "config", "user.name", "Other")
	write(t, filepath.Join(elsewhere, "upstream.txt"), "from upstream\n")
	git(t, elsewhere, "add", "upstream.txt")
	git(t, elsewhere, "commit", "-qm", "upstream change")
	git(t, elsewhere, "push", "-q", "origin", "main")

	// The local checkout is behind by exactly that commit.
	before := git(t, repo, "rev-parse", "HEAD")
	if err := PullFastForward(ctx, repo, "main"); err != nil {
		t.Fatalf("PullFastForward: %v", err)
	}
	if after := git(t, repo, "rev-parse", "HEAD"); after == before {
		t.Error("the checkout must have moved forward to the remote's commit")
	}
	if !exists(filepath.Join(repo, "upstream.txt")) {
		t.Error("the pulled file must be in the checkout now")
	}
	if !strings.Contains(git(t, repo, "log", "--format=%s"), "upstream change") {
		t.Error("the pulled commit must be in the history")
	}
}

// TestPullFastForwardRefusesADivergedBranch: a remote whose history the local
// checkout does not contain must NOT be merged blindly - the user did not ask
// for a merge, and --ff-only is what refuses it.
func TestPullFastForwardRefusesADivergedBranch(t *testing.T) {
	ctx := context.Background()
	repo := newRepo(t)
	remote := pushToOrigin(t, repo)

	// A commit only on the remote, and a DIFFERENT commit only locally.
	elsewhere := filepath.Join(t.TempDir(), "elsewhere")
	git(t, filepath.Dir(repo), "clone", "-q", remote, elsewhere)
	git(t, elsewhere, "config", "user.email", "other@example.com")
	git(t, elsewhere, "config", "user.name", "Other")
	write(t, filepath.Join(elsewhere, "remote.txt"), "remote\n")
	git(t, elsewhere, "add", "remote.txt")
	git(t, elsewhere, "commit", "-qm", "remote side")
	git(t, elsewhere, "push", "-q", "origin", "main")

	write(t, filepath.Join(repo, "local.txt"), "local\n")
	git(t, repo, "add", "local.txt")
	git(t, repo, "commit", "-qm", "local side")

	err := PullFastForward(ctx, repo, "main")
	if err == nil {
		t.Fatal("a diverged branch must be refused rather than merged")
	}
	if !strings.Contains(err.Error(), "could not be updated") {
		t.Errorf("the refusal must say what could not be updated, got %q", err)
	}
	// The local commit must still be there: nothing was merged.
	if !exists(filepath.Join(repo, "local.txt")) {
		t.Error("the local work must be untouched by a refused pull")
	}
}

// TestPullFastForwardReportsAFailedFetch: a project with no origin remote cannot
// be fetched, and the failure names the fetch - not a merge that never ran.
func TestPullFastForwardReportsAFailedFetch(t *testing.T) {
	repo := newRepo(t)
	err := PullFastForward(context.Background(), repo, "main")
	if err == nil {
		t.Fatal("a project with no origin must not report a successful update")
	}
	if !strings.Contains(err.Error(), "could not fetch") {
		t.Errorf("the failure must name the fetch, got %q", err)
	}
}

// TestPullFastForwardReportsNoGit: a machine with no git cannot fetch or merge,
// and the absence is reported as ErrNoGit rather than a git failure.
func TestPullFastForwardReportsNoGit(t *testing.T) {
	repo := newRepo(t)
	noGit(t)
	if err := PullFastForward(context.Background(), repo, "main"); err != ErrNoGit {
		t.Errorf("a missing git must be reported as ErrNoGit, got %v", err)
	}
}

func TestHasRemoteSeesOnlyOrigin(t *testing.T) {
	ctx := context.Background()
	if HasRemote(ctx, t.TempDir()) {
		t.Error("a directory that is not a repository has no remote")
	}
	repo := newRepo(t)
	if HasRemote(ctx, repo) {
		t.Error("a repository with no remote has no origin")
	}
	git(t, repo, "remote", "add", "upstream", t.TempDir())
	if HasRemote(ctx, repo) {
		t.Error("a remote with another name is not origin")
	}
	git(t, repo, "remote", "add", "origin", t.TempDir())
	if !HasRemote(ctx, repo) {
		t.Error("origin must be found")
	}
}

// ---------- the main branch of a project ----------

func TestInitOnNamesTheBranchAndInitIsMain(t *testing.T) {
	ctx := context.Background()
	dir := filepath.Join(t.TempDir(), "p")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := InitOn(ctx, dir, "master"); err != nil {
		t.Fatal(err)
	}
	if got := Display(ctx, dir); got != "master" {
		t.Errorf("InitOn(master) left the repository on %q", got)
	}
	// An existing repository is left as it is.
	if err := InitOn(ctx, dir, "other"); err != nil || Display(ctx, dir) != "master" {
		t.Errorf("a second init must change nothing: %v %q", err, Display(ctx, dir))
	}
	plain := filepath.Join(t.TempDir(), "q")
	if err := os.MkdirAll(plain, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := Init(ctx, plain); err != nil || Display(ctx, plain) != "main" {
		t.Errorf("Init must make main: %v %q", err, Display(ctx, plain))
	}
}

func TestValidBranchAsksGit(t *testing.T) {
	ctx := context.Background()
	for name, want := range map[string]bool{
		"main": true, "feature/x": true, "": false, "  ": false, "-d": false,
		"has space": false, "a..b": false, "x.lock": false,
	} {
		if got := ValidBranch(ctx, name); got != want {
			t.Errorf("ValidBranch(%q) = %v, want %v", name, got, want)
		}
	}
}

func TestBranchesListsLocalAndRemoteOnesWithoutSessionBranches(t *testing.T) {
	ctx := context.Background()
	origin := newRepo(t)
	git(t, origin, "branch", "release")
	clone := filepath.Join(t.TempDir(), "clone")
	git(t, filepath.Dir(clone), "clone", "-q", origin, clone)
	git(t, clone, "branch", "motita/s1")
	git(t, clone, "branch", "local-only")

	got, err := Branches(ctx, clone)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(got, ",") != "local-only,main,release" {
		t.Errorf("Branches = %v: local and origin-only branches, once each, no HEAD and no motita/*", got)
	}
	if _, err := Branches(ctx, t.TempDir()); err == nil {
		t.Error("a directory that is not a repository must be an error")
	}
}

func TestCheckoutMovesAndCreatesATrackingBranch(t *testing.T) {
	ctx := context.Background()
	origin := newRepo(t)
	git(t, origin, "branch", "release")
	clone := filepath.Join(t.TempDir(), "clone")
	git(t, filepath.Dir(clone), "clone", "-q", origin, clone)

	if err := Checkout(ctx, clone, "release"); err != nil || Display(ctx, clone) != "release" {
		t.Fatalf("checking out an origin-only branch: %v %q", err, Display(ctx, clone))
	}
	if err := Checkout(ctx, clone, "ghost"); err == nil {
		t.Error("a branch that does not exist must be refused")
	}
}
