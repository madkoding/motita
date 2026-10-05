// Package gitx is the interface's knowledge of git, and nothing else's.
//
// It exists as a package of its own for two reasons. The first is that the
// gateway has to answer questions about a working tree - which branch a project
// is on, whether a session's work has been integrated - and those answers are
// the SAME questions a session's worktree needs answered. A second copy of them
// in a handler would be a second copy of the rules about detached HEADs and
// repositories with no commits. The second reason is that every one of those
// rules is a rule about a program running on somebody else's machine: git may
// not be installed, the directory may not be a repository, the checkout may be
// detached in the middle of a rebase. The package is where those cases are
// named, so a caller never has to interpret git's own words.
//
// It runs git as a subprocess and parses the result, which is deliberate. There
// is no pure-Go git here and there must not be one: the repository being asked
// is the user's, with the user's config, hooks and worktree registrations, and
// only the user's git gives an answer that matches what a terminal in front of
// that machine would show.
//
// Every command is read-only except three: AddWorktree, RemoveWorktree and
// MergeInto. Those are the mutations this package exists to perform, and each
// one is written to refuse rather than to guess.
package gitx

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"
	"time"
)

// ErrNoGit is returned when git itself cannot be executed. It is a sentinel so
// a caller can tell "this machine has no git" from "this directory is not a
// repository": the first is a missing program, the second is a missing
// checkout, and the message a user needs is different for each.
var ErrNoGit = errors.New("git is not installed")

// ErrNotARepo is returned when a directory is not inside a git work tree.
var ErrNotARepo = errors.New("the directory is not inside a git work tree")

// gitTimeout bounds one git command. A read that hangs must not hang the
// request that asked for it: a slow read is a read that reports nothing.
const gitTimeout = 10 * time.Second

// execCommand is the ONE place a process is started, held in a variable so that
// a test can make the start fail without uninstalling git.
//
// The failure it makes reachable is otherwise UNREACHABLE on a machine that has
// git: exec.ErrNotFound needs a missing program, and a test cannot ask a CI
// runner to remove one. That is exactly the shape the coverage gate rejects, so
// the call is a seam and a test drives it - the same pattern as readAsset in
// internal/webui. Holding the seam at the process, rather than at the git
// command, is deliberate: it means every ErrNoGit check in this package is
// exercised by the one test that drives it, instead of each of them needing a
// seam of its own.
var execCommand = func(ctx context.Context, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	return cmd.CombinedOutput()
}

// execute runs git once in dir and returns its trimmed output.
func execute(ctx context.Context, dir string, args ...string) (string, error) {
	return executeWithin(ctx, gitTimeout, dir, args...)
}

// removeTimeout bounds `git worktree remove`. It is NOT a read: it deletes every file of the
// checkout, and a checkout carrying a real node_modules (measured: 120 MB, ~1100 entries) does
// not finish in gitTimeout. The kill left a half-removed worktree and an error with no text -
// reported as "I cannot delete the project session".
const removeTimeout = 5 * time.Minute

// removeTimeoutFor is removeTimeout as a variable, so a test can reach the expiry.
var removeTimeoutFor = removeTimeout

// executeWithin runs git once in dir, killed after limit.
func executeWithin(ctx context.Context, limit time.Duration, dir string, args ...string) (string, error) {
	c, cancel := context.WithTimeout(ctx, limit)
	defer cancel()
	out, err := execCommand(c, "git", append([]string{"-C", dir}, args...)...)
	if err != nil && errors.Is(err, exec.ErrNotFound) {
		return "", ErrNoGit
	}
	if err != nil && errors.Is(c.Err(), context.DeadlineExceeded) {
		// A killed process reports "signal: killed", which names nothing.
		err = context.DeadlineExceeded
	}
	return strings.TrimSpace(string(out)), err
}

// isNoGit is the ONE place "git is not installed" is recognized.
//
// Every failure in this package is classified through it, so a missing program
// is reported as itself - a different message, and a different thing for a user
// to fix, than "that folder is not a repository".
func isNoGit(err error) bool {
	return errors.Is(err, ErrNoGit)
}

// describe turns a failed git command into the error a caller reports: ErrNoGit
// untouched when git itself is missing, and otherwise the description of what
// was attempted, with the one line of git's output that says why.
//
// Every command in this package that is not asked for its output reports its
// failure through here, which is what keeps one description of WHY each command
// was run beside the call that ran it.
func describe(what, out string, err error) error {
	if isNoGit(err) {
		return ErrNoGit
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return fmt.Errorf("%s: git did not finish in time", what)
	}
	return fmt.Errorf("%s: %s", what, firstLine(out))
}

// noGitOr runs a git command and returns its output, or the described failure.
func noGitOr(ctx context.Context, what, dir string, args ...string) (string, error) {
	out, err := execute(ctx, dir, args...)
	if err == nil {
		return out, nil
	}
	return "", describe(what, out, err)
}

// firstLine is the one line of git's output that belongs in an error message.
//
// The full output of a failed merge is a paragraph about conflicts, and the
// caller puts this string in an HTTP error body: a paragraph belongs in a log.
//
// It is NOT used for control flow. Git's messages are LOCALIZED - measured on
// this machine: a conflicting merge prints "CONFLICTO (contenido): Conflicto de
// spanish-fixture: fusión en f.txt" where an English locale prints "CONFLICT (content): Merge
// conflict in f.txt". A program that read those words would work in one locale
// and break in another, so nothing here parses them: exit status and MERGE_HEAD
// are what decide, and this only reports.
func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	s = strings.TrimSpace(s)
	if s == "" {
		return "git reported no detail"
	}
	return s
}

// Repo reports whether dir is inside a git work tree.
//
// The distinction it is built on was measured: a directory with no repository
// above it makes the probe FAIL ("fatal: not a git repository"), while a bare
// repository answers the probe successfully with "false". Those are two
// different states and both mean the same thing here.
func Repo(ctx context.Context, dir string) error {
	if strings.TrimSpace(dir) == "" {
		return ErrNotARepo
	}
	out, err := execute(ctx, dir, "rev-parse", "--is-inside-work-tree")
	switch {
	case isNoGit(err):
		return ErrNoGit
	case err != nil, out != "true":
		return ErrNotARepo
	}
	return nil
}

// Init makes dir a git repository, on branch `main`.
//
// Nothing is written to the repository's config: git reads the user's GLOBAL
// configuration (identity, credential helper) on its own for every repository,
// and copying it into .git/config would freeze a value the user may change.
// An existing repository is left untouched, so calling this twice is harmless.
func Init(ctx context.Context, dir string) error {
	return InitOn(ctx, dir, "main")
}

// InitOn is Init on a branch of the caller's choosing: the one a project is told
// to treat as its main line.
func InitOn(ctx context.Context, dir, branch string) error {
	if Repo(ctx, dir) == nil {
		return nil
	}
	_, err := noGitOr(ctx, "could not initialise a git repository", dir, "init", "--initial-branch="+branch)
	return err
}

// ValidBranch reports whether name is a branch name git accepts. It is asked of git, which
// owns the rules (no spaces, no "..", no trailing ".lock"), rather than approximated here.
func ValidBranch(ctx context.Context, name string) bool {
	if strings.TrimSpace(name) == "" || strings.HasPrefix(name, "-") {
		return false
	}
	_, err := execute(ctx, ".", "check-ref-format", "--branch", name)
	return err == nil
}

// Branches lists the branches a repository can be put on: the local ones and those that exist
// only on origin (a clone has the remote's other branches as remote-tracking refs, and
// checking one out creates the local branch). The session branches this program makes
// (motita/...) are left out: they are not lines of work a person chooses between.
func Branches(ctx context.Context, dir string) ([]string, error) {
	out, err := noGitOr(ctx, "could not list the branches", dir,
		"for-each-ref", "--format=%(refname)", "refs/heads", "refs/remotes/origin")
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var names []string
	for _, ref := range strings.Fields(out) {
		name := strings.TrimPrefix(strings.TrimPrefix(ref, "refs/heads/"), "refs/remotes/origin/")
		if name == "HEAD" || strings.HasPrefix(name, "motita/") || seen[name] {
			continue
		}
		seen[name] = true
		names = append(names, name)
	}
	sort.Strings(names)
	return names, nil
}

// Checkout puts the checkout at dir on a branch. A branch that exists only on origin is created
// locally, tracking it. Git's own refusal (uncommitted changes that would be overwritten, a
// branch checked out in another worktree) is reported as it is.
func Checkout(ctx context.Context, dir, branch string) error {
	_, err := noGitOr(ctx, "could not check out "+branch, dir, "checkout", branch, "--")
	return err
}

// GlobalIdentity is the name and email in the user's GLOBAL git configuration,
// which is where a repository made by Init gets the identity of its commits.
// Either is empty when it is not set.
func GlobalIdentity(ctx context.Context) (name, email string) {
	name, _ = execute(ctx, ".", "config", "--global", "user.name")
	email, _ = execute(ctx, ".", "config", "--global", "user.email")
	return strings.TrimSpace(name), strings.TrimSpace(email)
}

// SetGlobalIdentity writes the name and email into the user's global git
// configuration, so that every repository - this one and the ones already on
// the machine - commits under them.
func SetGlobalIdentity(ctx context.Context, name, email string) error {
	if _, err := noGitOr(ctx, "could not save the git name", ".", "config", "--global", "user.name", name); err != nil {
		return err
	}
	_, err := noGitOr(ctx, "could not save the git email", ".", "config", "--global", "user.email", email)
	return err
}

// Head reports the branch a directory is checked out on, or the short sha when
// the checkout is detached.
//
// The three cases git has, in the order they must be asked:
//
//  1. A normal checkout answers `rev-parse --abbrev-ref HEAD` with the branch
//     name.
//  2. A detached HEAD answers "HEAD", which is not a branch. Reporting "HEAD"
//     would be reporting git's internal name for "no branch" as if it were one,
//     so the short sha is what comes back instead.
//  3. A repository with no commits fails `rev-parse --abbrev-ref HEAD` with
//     "ambiguous argument 'HEAD'" - measured - and its branch still exists as a
//     symbolic ref. That is the state of `git init` with nothing committed, and
//     it is exactly the state of a project folder a user has just created.
func Head(ctx context.Context, dir string) (branch string, detached string, err error) {
	if err := Repo(ctx, dir); err != nil {
		return "", "", err
	}
	if name, nameErr := execute(ctx, dir, "rev-parse", "--abbrev-ref", "HEAD"); nameErr == nil && name != "" && name != "HEAD" {
		return name, "", nil
	}
	if sha, shaErr := execute(ctx, dir, "rev-parse", "--short", "HEAD"); shaErr == nil && sha != "" {
		return "", sha, nil
	}
	// No commits yet: the branch is still known, as a symbolic ref.
	out, err := execute(ctx, dir, "symbolic-ref", "--short", "HEAD")
	if err != nil {
		return "", "", describe("could not read the branch this checkout is on", out, err)
	}
	return out, "", nil
}

// Display is the branch a front end draws, and "" when the directory is not a
// repository.
//
// Empty rather than a word like "unknown": a folder that is not a repository
// has no branch, and a front end that draws "unknown" for it is inventing a
// state - which then has to be explained to every user who has an ordinary
// non-git project.
func Display(ctx context.Context, dir string) string {
	branch, detached, err := Head(ctx, dir)
	if err != nil {
		return ""
	}
	if branch != "" {
		return branch
	}
	return "detached at " + detached
}

// Root reports the repository's top-level directory.
func Root(ctx context.Context, dir string) (string, error) {
	out, err := execute(ctx, dir, "rev-parse", "--show-toplevel")
	switch {
	case isNoGit(err):
		return "", ErrNoGit
	case err != nil, out == "":
		return "", ErrNotARepo
	}
	return out, nil
}

// HasCommits reports whether HEAD resolves to a commit.
//
// A repository with no commits cannot be branched from: `worktree add -b` fails
// with a message that names neither the cause nor the fix.
func HasCommits(ctx context.Context, dir string) bool {
	out, err := execute(ctx, dir, "rev-parse", "--verify", "HEAD")
	return err == nil && out != ""
}

// BranchExists reports whether a local branch of that name exists.
func BranchExists(ctx context.Context, dir, branch string) bool {
	out, err := execute(ctx, dir, "rev-parse", "--verify", "refs/heads/"+branch)
	return err == nil && out != ""
}

// Commit is one commit, as a report draws it.
type Commit struct {
	SHA     string
	Subject string
}

// parseCommits reads the output of a `git log --format=%h<TAB>%s` listing.
func parseCommits(out string) []Commit {
	if strings.TrimSpace(out) == "" {
		return nil
	}
	var list []Commit
	for _, line := range strings.Split(out, "\n") {
		sha, subject, _ := strings.Cut(line, "\t")
		list = append(list, Commit{SHA: sha, Subject: subject})
	}
	return list
}

// CommitsBetween lists the commits branch has that baseBranch does not, and
// then the commits baseBranch has that branch does not.
//
// The two lists are what "how far apart is this session from where it started"
// means, and they are asked of git rather than computed from a number recorded
// when the session began: the base branch may have moved since, and a recorded
// number would then be a number that used to be true.
func CommitsBetween(ctx context.Context, dir, baseBranch, branch string) (ahead, behind []Commit, err error) {
	out, err := noGitOr(ctx, "could not read the branch's commits", dir, "log", "--format=%h\t%s", baseBranch+".."+branch)
	if err != nil {
		return nil, nil, err
	}
	behindOut, err := noGitOr(ctx, "could not read the base branch's commits", dir, "log", "--format=%h\t%s", branch+".."+baseBranch)
	if err != nil {
		return nil, nil, err
	}
	return parseCommits(out), parseCommits(behindOut), nil
}

// Dirty reports whether the working tree at dir has changes git can see.
//
// `--porcelain` rather than a count, because every caller asks the same
// question - "would git refuse to touch this tree?" - and the list is what git
// answers it with. What the changes ARE is not this package's business.
func Dirty(ctx context.Context, dir string) (bool, error) {
	out, err := noGitOr(ctx, "could not read the working tree's state", dir, "status", "--porcelain")
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(out) != "", nil
}

// AddWorktree checks branch out at path, creating the branch if it does not
// exist.
//
// The order of the checks is the order a user would want to be told:
//
//  1. Is this a repository at all?
//  2. Is it the ROOT of its repository? `git worktree` checks out the whole
//     repository, so a project registered as a subdirectory of a repository
//     would silently get a worktree of everything above it, and the agent would
//     then work in a copy of a tree it was never pointed at. Refusing here is
//     the difference between a worktree and a surprise.
//  3. Is there a commit to branch from?
//
// A path git has a registration for but which is GONE from disk is PRUNED
// first. That state is not exotic: a worktree directory removed by hand - a
// cleanup, an rm -rf, a container whose volumes did not persist - leaves the
// registration in .git/worktrees behind, and git then refuses every later
// `worktree add` at that path with "Preparing worktree (checking out ...)" and
// a non-zero status. Measured on this machine. Without the prune a session
// whose directory disappeared could never get its worktree back, which is
// exactly the state a restart leaves it in.
//
// The branch is created when it does not exist and ATTACHED when it does, which
// is what makes a resumed session work: the session's branch outlives its
// worktree, so a session restored after a restart - or one whose worktree was
// removed and recreated - finds its own work again rather than starting over.
//
// git's own refusal is reported and not second-guessed: a branch already
// checked out in another worktree is refused by git ("is already checked out
// at"), which is the rule that stops two sessions from working in one directory
// at the same time.
func AddWorktree(ctx context.Context, repoDir, path, branch string) error {
	root, err := Root(ctx, repoDir)
	if err != nil {
		return err
	}
	if !samePath(root, repoDir) {
		return fmt.Errorf("%q is not the root of its repository (%s): a worktree of the whole repository is not this project", repoDir, root)
	}
	if !HasCommits(ctx, repoDir) {
		return errors.New("the repository has no commits to branch from")
	}
	// A registration whose directory is gone would block the add below, so it
	// is cleared first. `worktree prune` only removes registrations git itself
	// considers missing, so a live worktree is never touched by this.
	if !dirExists(path) {
		if _, pruneErr := noGitOr(ctx, "the stale worktree registration could not be cleared", repoDir, "worktree", "prune"); pruneErr != nil {
			return pruneErr
		}
	}
	if err := prepareParent(path); err != nil {
		return fmt.Errorf("the worktree's directory could not be prepared: %w", err)
	}
	// Advisory: a worktree whose status is noisy is still a worktree, so a failure here must
	// not stop the session from getting one.
	_ = EnsureIgnoreBase(ctx, repoDir)
	var out string
	if BranchExists(ctx, repoDir, branch) {
		out, err = execute(ctx, repoDir, "worktree", "add", path, branch)
	} else {
		out, err = execute(ctx, repoDir, "worktree", "add", "-b", branch, path)
	}
	if err != nil {
		return describe("the worktree could not be created", out, err)
	}
	return nil
}

// dirExists reports whether a path is present on disk.
//
// Named apart from the test helper of the same job because both live in this
// package and the collision is a compile error, not a subtlety.
func dirExists(path string) bool {
	_, statErr := os.Stat(path)
	return statErr == nil
}

// RemoveWorktree drops the worktree at path.
//
// The branch is NOT deleted: the worktree is where the work happened and the
// branch IS the work, so a cleanup that took both would be a cleanup that threw
// commits away. Removing the branch is DeleteBranch, and it is a separate
// decision.
//
// force is git's own --force, and it is passed through rather than decided
// here: a dirty worktree refuses removal (measured: "contains modified or
// untracked files"), and whether to override that is the user's call, not this
// package's. It is passed TWICE: a single --force still refuses a LOCKED
// worktree (measured: "cannot remove a locked working tree; use 'remove -f -f'"),
// and a caller that asked to force meant the removal, not half of it.
func RemoveWorktree(ctx context.Context, repoDir, path string, force bool) error {
	args := []string{"worktree", "remove"}
	if force {
		args = append(args, "--force", "--force")
	}
	out, err := executeWithin(ctx, removeTimeoutFor, repoDir, append(args, path)...)
	if err == nil {
		return nil
	}
	return describe("the worktree could not be removed", out, err)
}

// PruneWorktrees clears the registrations whose directory no longer exists. It never
// touches a live worktree: git only prunes what it considers missing.
func PruneWorktrees(ctx context.Context, repoDir string) error {
	_, err := noGitOr(ctx, "the stale worktree registrations could not be cleared", repoDir, "worktree", "prune")
	return err
}

// FallbackName and FallbackEmail are the identity a merge is committed under
// when the repository has none of its own.
//
// A repository cloned by this program has no user.name and no user.email, and
// the machine may have no ~/.gitconfig either - both were MEASURED true on this
// machine: the project the gateway cloned has neither, and there is no
// ~/.gitconfig at all. `git merge` then fails with "unable to auto-detect email
// address", which tells the user nothing they can act on. The identity is
// therefore resolved explicitly and passed to the merge, so the merge cannot
// fail for a reason that is not about the merge.
const (
	FallbackName  = "motita"
	FallbackEmail = "motita@localhost"
)

// Identity is the name and email a commit in this repository is made under: the
// repository's own when it has one, and the fallback when it does not.
//
// A name without an email, or an email without a name, is treated as no
// identity at all: git needs both, so half of one is not half a solution.
func Identity(ctx context.Context, dir string) (name, email string) {
	name, _ = execute(ctx, dir, "config", "user.name")
	email, _ = execute(ctx, dir, "config", "user.email")
	if strings.TrimSpace(name) == "" || strings.TrimSpace(email) == "" {
		return FallbackName, FallbackEmail
	}
	return strings.TrimSpace(name), strings.TrimSpace(email)
}

// MergeResult is what a successful merge produced.
type MergeResult struct {
	SHA     string
	Subject string
}

// MergeInto merges branch into the branch the checkout at repoDir has out.
//
// Four decisions are load-bearing:
//
//   - The checkout must be ON baseBranch. Merging while it is on another branch
//     would put the session's work somewhere the user did not ask for, and the
//     mistake is invisible until they look. A detached checkout is refused for
//     the same reason: there is no branch to merge into.
//   - --autostash, so an uncommitted change in the user's checkout cannot block
//     the merge and cannot be lost by it. Measured: with a dirty file and a
//     CONFLICTING merge, `merge --abort` restores the dirty file and leaves no
//     stash behind.
//   - --no-ff, so the integration is one commit whose message names the
//     session. A fast-forward would leave no record that a session's work
//     arrived, and "which session did this" is the question the branch names
//     exist to answer.
//   - The identity is passed with -c rather than assumed, because a repository
//     cloned by this program has none. Measured: without it the merge fails
//     outright, with it the merge commit is authored by the resolved identity.
//
// A conflict is NOT left in the user's checkout. The merge is aborted and the
// error says so: an unresolved conflict sitting in somebody's working tree is
// the most confusing state git has, and a program that leaves one behind is a
// program that broke the thing the user was in the middle of.
func MergeInto(ctx context.Context, repoDir, baseBranch, branch, message string) (MergeResult, error) {
	branchNow, detached, err := Head(ctx, repoDir)
	if err != nil {
		return MergeResult{}, err
	}
	if branchNow == "" {
		return MergeResult{}, fmt.Errorf("the checkout is detached at %s, so there is no branch to merge into: check out %q first", detached, baseBranch)
	}
	if branchNow != baseBranch {
		return MergeResult{}, fmt.Errorf("the checkout is on %q, and this session branched from %q: check out %q first", branchNow, baseBranch, baseBranch)
	}
	name, email := Identity(ctx, repoDir)
	out, err := execute(ctx, repoDir, "-c", "user.name="+name, "-c", "user.email="+email,
		"merge", "--no-ff", "--autostash", "-m", message, branch)
	if err == nil {
		sha, _ := execute(ctx, repoDir, "rev-parse", "--short", "HEAD")
		return MergeResult{SHA: sha, Subject: message}, nil
	}
	if isNoGit(err) {
		return MergeResult{}, ErrNoGit
	}
	// A conflict leaves MERGE_HEAD behind; a refusal that never started a merge
	// (an unknown branch, a checkout git will not touch) does not. Aborting only
	// in the first case is what keeps a failed merge from being reported as a
	// rollback it did not perform. Measured both ways: the conflict case leaves
	// MERGE_HEAD and aborts cleanly, and the refusal case has no MERGE_HEAD to
	// abort.
	if _, mhErr := execute(ctx, repoDir, "rev-parse", "-q", "--verify", "MERGE_HEAD"); mhErr != nil {
		return MergeResult{}, fmt.Errorf("the merge did not happen: %s", firstLine(out))
	}
	if _, abErr := noGitOr(ctx, "the merge conflicted and could not be rolled back", repoDir, "merge", "--abort"); abErr != nil {
		return MergeResult{}, abErr
	}
	return MergeResult{}, fmt.Errorf("the merge conflicted and was rolled back; nothing was changed: %s", firstLine(out))
}

// PullFastForward updates the checkout at dir to the latest state of branch on
// the default remote, but only if it can fast-forward. It refuses a diverged
// remote because pulling and merging blindly would change history the user did
// not ask to merge.
func PullFastForward(ctx context.Context, dir, branch string) error {
	if _, err := noGitOr(ctx, "could not fetch the latest changes", dir, "fetch", "origin", branch); err != nil {
		return err
	}
	// A fetch is not a merge. The local branch must be able to move forward only.
	out, err := execute(ctx, dir, "merge", "--ff-only", "origin/"+branch)
	if err != nil {
		return describe("the project could not be updated to the latest "+branch, out, err)
	}
	return nil
}

// HasRemote reports whether the repository at dir has a remote called origin.
// A project without one has nothing to bring in, which is not a failure.
func HasRemote(ctx context.Context, dir string) bool {
	out, err := execute(ctx, dir, "remote")
	if err != nil {
		return false
	}
	for _, name := range strings.Fields(out) {
		if name == "origin" {
			return true
		}
	}
	return false
}

// DeleteBranch deletes a local branch at dir.
//
// -d and never -D: a branch whose commits are merged nowhere is work that
// exists only there, and asking git is what stops this program from throwing it
// away. A refusal names the commits it would have taken, which is the message
// the user needs in order to decide.
func DeleteBranch(ctx context.Context, dir, branch string) error {
	_, err := noGitOr(ctx, "the branch was not deleted", dir, "branch", "-d", branch)
	return err
}
