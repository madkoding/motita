package gitx

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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
