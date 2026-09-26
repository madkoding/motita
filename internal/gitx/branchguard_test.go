package gitx

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// These tests are about the one thing that can break a session's isolation from
// the outside, and about the identification that prevents it.
//
// A session's worktree has to be recognised by its PATH. Recognising it by the
// branch it has checked out is the mistake that breaks isolation: the user can
// move a session to a feature branch, and every branch-based check then mistakes
// its own worktree for a stranger. Measured against git 2.47.3 first; each
// assertion below is a measurement, not a belief about git.

// ---------- Worktrees ----------

// TestWorktreesNamesEveryCheckoutOfTheRepository: the repository's own checkout
// and each worktree, which is the population every question in this file is
// answered from. The project's own checkout is listed by git and is part of the
// answer: leaving it out would make "who holds this branch" blind to the most
// likely holder.
func TestWorktreesNamesEveryCheckoutOfTheRepository(t *testing.T) {
	ctx := context.Background()
	repo := newRepo(t)
	first := filepath.Join(filepath.Dir(repo), "w1")
	second := filepath.Join(filepath.Dir(repo), "w2")
	addWorktree(t, repo, first, "motita/s1")
	addWorktree(t, repo, second, "motita/s2")

	all, err := Worktrees(ctx, repo)
	if err != nil {
		t.Fatalf("Worktrees: %v", err)
	}
	if len(all) != 3 {
		t.Fatalf("got %d checkouts, want the repository plus its two worktrees: %+v", len(all), all)
	}
	for _, want := range []struct{ path, branch string }{
		{repo, "main"},
		{first, "motita/s1"},
		{second, "motita/s2"},
	} {
		found := false
		for _, got := range all {
			if samePath(got.Path, want.path) {
				found = true
				if got.Branch != want.branch {
					t.Errorf("%s is on %q, want %q", want.path, got.Branch, want.branch)
				}
			}
		}
		if !found {
			t.Errorf("the listing must name %q, got %+v", want.path, all)
		}
	}
}

// TestWorktreesReportsADetachedAndAPrunableCheckout: the two states that are not
// an ordinary branch checkout, and they are DIFFERENT states. A detached
// worktree holds no branch; a prunable registration holds nothing at all because
// its directory is gone. Both are reported as what they are rather than skipped,
// so a caller can decide what to do about each.
func TestWorktreesReportsADetachedAndAPrunableCheckout(t *testing.T) {
	ctx := context.Background()
	repo := newRepo(t)

	detached := filepath.Join(filepath.Dir(repo), "w-detached")
	if err := os.MkdirAll(detached, 0o755); err != nil {
		t.Fatal(err)
	}
	git(t, repo, "worktree", "add", "--detach", detached, "main")

	gone := filepath.Join(filepath.Dir(repo), "w-gone")
	addWorktree(t, repo, gone, "motita/s1")
	if err := os.RemoveAll(gone); err != nil {
		t.Fatal(err)
	}

	all, err := Worktrees(ctx, repo)
	if err != nil {
		t.Fatalf("Worktrees: %v", err)
	}
	var sawDetached, sawPrunable bool
	for _, w := range all {
		if samePath(w.Path, detached) {
			sawDetached = true
			if !w.Detached {
				t.Errorf("the detached worktree must be reported detached: %+v", w)
			}
			if w.Branch != "" {
				t.Errorf("a detached worktree has no branch, got %q", w.Branch)
			}
			if w.Prunable {
				t.Error("a detached worktree is not prunable: its directory is there")
			}
		}
		if samePath(w.Path, gone) {
			sawPrunable = true
			if !w.Prunable {
				t.Errorf("a registration whose directory is gone must be reported prunable: %+v", w)
			}
		}
	}
	if !sawDetached {
		t.Errorf("the detached worktree must appear in the listing: %+v", all)
	}
	if !sawPrunable {
		t.Errorf("the prunable registration must appear in the listing: %+v", all)
	}
}

// TestWorktreesReportsAMissingGit: the listing is what every guard in this file
// rests on, so losing git must be reported as itself. Answering "no worktrees"
// would silently free a branch that a session is working in.
func TestWorktreesReportsAMissingGit(t *testing.T) {
	repo := newRepo(t)
	noGitOnGit(t, "worktree list")
	if _, err := Worktrees(context.Background(), repo); err != ErrNoGit {
		t.Errorf("a listing that loses git must report ErrNoGit, got %v", err)
	}
}

// TestWorktreesReportsADirectoryThatIsNotARepository: a plain folder has no
// checkouts and the answer is the package's own ErrNotARepo. An empty list would
// make a non-git project indistinguishable from a git one nobody has branched,
// which is a difference the caller has to be able to see.
func TestWorktreesReportsADirectoryThatIsNotARepository(t *testing.T) {
	if _, err := Worktrees(context.Background(), plainDir(t, "plain")); err != ErrNotARepo {
		t.Errorf("a directory that is not a repository must report ErrNotARepo, got %v", err)
	}
}

// TestWorktreesFailsLoudlyOnAMalformedListing: dropping a checkout is the one
// mistake that matters here, because a dropped checkout is a branch the guards
// below would believe nobody holds. A listing that names a branch before naming
// any worktree is a shape git never produces, so it is reported rather than
// skipped.
func TestWorktreesFailsLoudlyOnAMalformedListing(t *testing.T) {
	repo := newRepo(t)
	stubOn(t, "worktree list", "HEAD abc\nbranch refs/heads/main\n", nil)
	if _, err := Worktrees(context.Background(), repo); err == nil {
		t.Fatal("a malformed listing must be an error, not a silently shortened list")
	}
}

// TestWorktreesFailsOnADetachedLineBeforeAnyWorktree: the same rule for the
// other two keys that describe a checkout. Each is checked, because each is a
// way for a parser to drop a checkout while looking like it succeeded.
func TestWorktreesFailsOnADetachedLineBeforeAnyWorktree(t *testing.T) {
	repo := newRepo(t)
	for _, body := range []string{
		"HEAD abc\ndetached\n",
		"HEAD abc\nprunable reason here\n",
	} {
		stubOn(t, "worktree list", body, nil)
		if _, err := Worktrees(context.Background(), repo); err == nil {
			t.Errorf("a %q line before any worktree must be an error, got none for %q", "detached/prunable", body)
		}
	}
}

// TestWorktreesIgnoresAKeyItDoesNotKnow: git may add a key to the porcelain
// format, and an unknown one must not shorten the list. Ignoring it is safe
// precisely because the keys that carry a path or a branch are handled
// explicitly.
func TestWorktreesIgnoresAKeyItDoesNotKnow(t *testing.T) {
	repo := newRepo(t)
	real := filepath.Join(filepath.Dir(repo), "w1")
	addWorktree(t, repo, real, "motita/s1")
	stubOn(t, "worktree list",
		"worktree "+real+"\nHEAD abc\nbranch refs/heads/motita/s1\nlocked\n\n", nil)

	all, err := Worktrees(context.Background(), repo)
	if err != nil {
		t.Fatalf("an unknown key must not be an error: %v", err)
	}
	if len(all) != 1 || !samePath(all[0].Path, real) || all[0].Branch != "motita/s1" {
		t.Errorf("the known keys must still parse, got %+v", all)
	}
}

// ---------- LiveWorktreeAt ----------

// TestLiveWorktreeAtFindsTheSessionWorktreeByPath is the identification that
// keeps a session's checkout its own. It is asked by PATH, so the answer does not
// depend on which branch the session happens to be working on.
func TestLiveWorktreeAtFindsTheSessionWorktreeByPath(t *testing.T) {
	ctx := context.Background()
	repo := newRepo(t)
	wt := filepath.Join(filepath.Dir(repo), "w1")
	addWorktree(t, repo, wt, "motita/s1")

	got, ok, err := LiveWorktreeAt(ctx, repo, wt)
	if err != nil {
		t.Fatalf("LiveWorktreeAt: %v", err)
	}
	if !ok {
		t.Fatal("the session's worktree must be found by its path")
	}
	if !samePath(got.Path, wt) {
		t.Errorf("Path = %q, want %q", got.Path, wt)
	}
}

// TestLiveWorktreeAtFindsAWorktreeMovedToAnotherBranch is the case that broke
// isolation: the user moves the session's worktree to a feature branch, and the
// worktree is STILL the session's own. A branch-based identification answers "not
// found" here, which is exactly how a session ends up running in the project's
// checkout instead of its own worktree.
func TestLiveWorktreeAtFindsAWorktreeMovedToAnotherBranch(t *testing.T) {
	ctx := context.Background()
	repo := newRepo(t)
	wt := filepath.Join(filepath.Dir(repo), "w1")
	addWorktree(t, repo, wt, "motita/s1")

	git(t, repo, "branch", "feature")
	git(t, wt, "checkout", "-q", "feature")

	got, ok, err := LiveWorktreeAt(ctx, repo, wt)
	if err != nil {
		t.Fatalf("LiveWorktreeAt: %v", err)
	}
	if !ok {
		t.Fatal("a worktree on a feature branch is still the worktree at that path")
	}
	if got.Branch != "feature" {
		t.Errorf("Branch = %q, want feature: the answer must report where it actually is", got.Branch)
	}
}

// TestLiveWorktreeAtFindsADetachedWorktree: a session left on a detached HEAD is
// still working in its own tree, and its work must not be discarded by
// concluding that the worktree is gone.
func TestLiveWorktreeAtFindsADetachedWorktree(t *testing.T) {
	ctx := context.Background()
	repo := newRepo(t)
	wt := filepath.Join(filepath.Dir(repo), "w1")
	addWorktree(t, repo, wt, "motita/s1")
	git(t, wt, "checkout", "-q", "--detach", "HEAD")

	_, ok, err := LiveWorktreeAt(ctx, repo, wt)
	if err != nil {
		t.Fatalf("LiveWorktreeAt: %v", err)
	}
	if !ok {
		t.Fatal("a detached worktree is still a live worktree at its path")
	}
}

// TestLiveWorktreeAtIsFalseForAGoneDirectory: a worktree removed by hand is not
// live, and the session must be able to get a new one. Reporting it as live
// would hand the agent a directory that does not exist.
func TestLiveWorktreeAtIsFalseForAGoneDirectory(t *testing.T) {
	ctx := context.Background()
	repo := newRepo(t)
	wt := filepath.Join(filepath.Dir(repo), "w1")
	addWorktree(t, repo, wt, "motita/s1")
	if err := os.RemoveAll(wt); err != nil {
		t.Fatal(err)
	}

	if _, ok, err := LiveWorktreeAt(ctx, repo, wt); err != nil {
		t.Fatalf("LiveWorktreeAt: %v", err)
	} else if ok {
		t.Error("a worktree whose directory is gone is not live")
	}
}

// TestLiveWorktreeAtFindsTheProjectCheckoutToo: the repository's own checkout is
// a live checkout of the repository, so it is found - and that is deliberate.
// The caller tells it apart from a session's worktree by comparing paths against
// the project directory it already knows, which is a comparison the caller is
// the only one able to make.
//
// What must NOT happen is this function inventing a distinction: a caller that
// asked "is this a usable checkout" and got "no" for the project's own
// directory would conclude the project does not exist.
func TestLiveWorktreeAtFindsTheProjectCheckoutToo(t *testing.T) {
	ctx := context.Background()
	repo := newRepo(t)
	addWorktree(t, repo, filepath.Join(filepath.Dir(repo), "w1"), "motita/s1")

	got, ok, err := LiveWorktreeAt(ctx, repo, repo)
	if err != nil {
		t.Fatalf("LiveWorktreeAt: %v", err)
	}
	if !ok {
		t.Fatal("the project's own checkout is a live checkout of the repository")
	}
	if !samePath(got.Path, repo) {
		t.Errorf("Path = %q, want %q", got.Path, repo)
	}
}

// TestLiveWorktreeAtReportsADirectoryThatIsNotARepository: there is no worktree
// to be had in a plain folder, and the caller distinguishes that from "the
// worktree is missing" by the error.
func TestLiveWorktreeAtReportsADirectoryThatIsNotARepository(t *testing.T) {
	plain := plainDir(t, "plain")
	if _, _, err := LiveWorktreeAt(context.Background(), plain, plain); err != ErrNotARepo {
		t.Errorf("a directory that is not a repository must report ErrNotARepo, got %v", err)
	}
}

// TestLiveWorktreeAtReportsAMissingGit: without git there is no listing, and a
// missing program must not read as "the worktree is gone" - that would let a
// session's own tree be replaced by a fresh one.
func TestLiveWorktreeAtReportsAMissingGit(t *testing.T) {
	repo := newRepo(t)
	noGitOnGit(t, "worktree list")
	if _, _, err := LiveWorktreeAt(context.Background(), repo, repo); err != ErrNoGit {
		t.Errorf("a probe that loses git must report ErrNoGit, got %v", err)
	}
}

// TestLiveWorktreeAtReportsAMalformedListing: a listing that cannot be trusted
// must be reported, because the alternative is answering "not found" for a
// worktree that IS there - and that answer replaces a session's tree.
func TestLiveWorktreeAtReportsAMalformedListing(t *testing.T) {
	repo := newRepo(t)
	stubOn(t, "worktree list", "branch refs/heads/main\n", nil)
	if _, _, err := LiveWorktreeAt(context.Background(), repo, repo); err == nil {
		t.Fatal("a malformed listing must be an error rather than a not-found")
	}
}

// ---------- BranchHolder ----------

// TestBranchHolderNamesTheWorktreeHoldingTheBranch is the question asked before
// a branch is taken: a branch is free only when nothing is working in it.
func TestBranchHolderNamesTheWorktreeHoldingTheBranch(t *testing.T) {
	ctx := context.Background()
	repo := newRepo(t)
	wt := filepath.Join(filepath.Dir(repo), "w1")
	addWorktree(t, repo, wt, "motita/s1")

	holder, ok, err := BranchHolder(ctx, repo, "motita/s1")
	if err != nil {
		t.Fatalf("BranchHolder: %v", err)
	}
	if !ok {
		t.Fatal("the branch is checked out and its holder must be reported")
	}
	if !samePath(holder, wt) {
		t.Errorf("holder = %q, want the worktree %q", holder, wt)
	}
}

// TestBranchHolderFindsTheProjectCheckoutItself: the repository's own checkout is
// in the same listing, and a caller has to be able to tell "someone else holds
// this" from "I am on it" by comparing paths - so it must be reported, not
// skipped.
func TestBranchHolderFindsTheProjectCheckoutItself(t *testing.T) {
	ctx := context.Background()
	repo := newRepo(t)

	holder, ok, err := BranchHolder(ctx, repo, "main")
	if err != nil {
		t.Fatalf("BranchHolder: %v", err)
	}
	if !ok {
		t.Fatal("the checkout on main holds main")
	}
	if !samePath(holder, repo) {
		t.Errorf("holder = %q, want the repository %q", holder, repo)
	}
}

// TestBranchHolderIsFalseForAFreeBranch: a branch nobody has checked out is
// free, and the answer is ok=false rather than an error. This is the common
// case: the branches a user creates by hand.
func TestBranchHolderIsFalseForAFreeBranch(t *testing.T) {
	repo := newRepo(t)
	git(t, repo, "branch", "free")

	holder, ok, err := BranchHolder(context.Background(), repo, "free")
	if err != nil {
		t.Fatalf("BranchHolder: %v", err)
	}
	if ok {
		t.Errorf("a branch nothing has checked out is free, got holder %q", holder)
	}
}

// TestBranchHolderIgnoresADetachedWorktree: a detached checkout holds no branch,
// so no branch name may resolve to it. Reporting one would block a branch
// nothing is using.
func TestBranchHolderIgnoresADetachedWorktree(t *testing.T) {
	ctx := context.Background()
	repo := newRepo(t)
	detached := filepath.Join(filepath.Dir(repo), "w-detached")
	if err := os.MkdirAll(detached, 0o755); err != nil {
		t.Fatal(err)
	}
	git(t, repo, "worktree", "add", "--detach", detached, "main")

	holder, ok, err := BranchHolder(ctx, repo, "main")
	if err != nil {
		t.Fatalf("BranchHolder: %v", err)
	}
	if !ok {
		t.Fatal("main is held by the project's own checkout")
	}
	if samePath(holder, detached) {
		t.Error("a detached checkout cannot hold a branch")
	}
}

// TestBranchHolderIgnoresAPrunableRegistration: a registration whose directory
// is gone holds nothing, and counting it would block a branch nobody is working
// in - the state a hand-deleted worktree leaves behind.
func TestBranchHolderIgnoresAPrunableRegistration(t *testing.T) {
	ctx := context.Background()
	repo := newRepo(t)
	gone := filepath.Join(filepath.Dir(repo), "w-gone")
	addWorktree(t, repo, gone, "motita/s1")
	if err := os.RemoveAll(gone); err != nil {
		t.Fatal(err)
	}

	holder, ok, err := BranchHolder(ctx, repo, "motita/s1")
	if err != nil {
		t.Fatalf("BranchHolder: %v", err)
	}
	if ok {
		t.Errorf("a registration whose directory is gone holds nothing, got holder %q", holder)
	}
}

// TestBranchHolderReportsAMissingGit: the guard built on this cannot be honoured
// without git, and a missing program must not read as "the branch is free".
func TestBranchHolderReportsAMissingGit(t *testing.T) {
	repo := newRepo(t)
	noGitOnGit(t, "worktree list")
	if _, _, err := BranchHolder(context.Background(), repo, "main"); err != ErrNoGit {
		t.Errorf("a probe that loses git must report ErrNoGit, got %v", err)
	}
}

// TestBranchHolderReportsADirectoryThatIsNotARepository: there are no checkouts
// in a plain folder, and the caller needs to see that rather than an empty
// answer.
func TestBranchHolderReportsADirectoryThatIsNotARepository(t *testing.T) {
	plain := plainDir(t, "plain")
	if _, _, err := BranchHolder(context.Background(), plain, "main"); err != ErrNotARepo {
		t.Errorf("a directory that is not a repository must report ErrNotARepo, got %v", err)
	}
}

// TestBranchHolderIgnoresAListedCheckoutWhoseDirectoryIsGone: git normally marks
// such a registration prunable, but this probe must not depend on that - a
// version of git that does not, or a directory removed between the listing and
// the check, would otherwise be reported as the holder. The holder would then be
// a path that does not exist, and a caller would refuse a branch on the strength
// of a worktree nobody can work in.
func TestBranchHolderIgnoresAListedCheckoutWhoseDirectoryIsGone(t *testing.T) {
	repo := newRepo(t)
	git(t, repo, "branch", "motita/s1")
	missing := filepath.Join(t.TempDir(), "vanished")
	stubOn(t, "worktree list",
		"worktree "+missing+"\nHEAD abc\nbranch refs/heads/motita/s1\n", nil)

	holder, ok, err := BranchHolder(context.Background(), repo, "motita/s1")
	if err != nil {
		t.Fatalf("BranchHolder: %v", err)
	}
	if ok {
		t.Errorf("a checkout whose directory is gone cannot hold a branch, got holder %q", holder)
	}
}

// TestBranchHolderReportsAMalformedListing: this probe answers "is the branch
// free", and a listing it cannot trust must be reported. Answering "free" for a
// listing that failed is the one answer that would let a branch in use be taken.
func TestBranchHolderReportsAMalformedListing(t *testing.T) {
	repo := newRepo(t)
	stubOn(t, "worktree list", "branch refs/heads/main\n", nil)
	if _, _, err := BranchHolder(context.Background(), repo, "main"); err == nil {
		t.Fatal("a malformed listing must be an error rather than a free branch")
	}
}
