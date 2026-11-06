package gateway

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/madkoding/motita/internal/gitx"
)

// A session's git has four parts, and every rule here names them the same way:
//
//   - its BASE branch: the project's main branch (Project.MainBranch, or the main/master the
//     repository has), the one a session's work starts from and is proposed into;
//   - its OWN branch: motita/<id> for a session with a worktree, or whatever work branch the
//     checkout is on for a session in the project's folder;
//   - its CHECKOUT: the project's folder for the main session, a worktree of its own for a thread;
//   - its PULL REQUEST, when one was opened from that branch.
//
// The base is decided when the session is placed (and again when the project's main branch is
// changed) and kept on the conversation: everything that compares the session's work with "the
// project" (mergeable, the agent's note, where a new worktree starts) reads it from there, instead
// of from whatever branch the project's folder happens to have checked out. Measured on a real
// gateway: the main session proposed a pull request, which moved the project's folder onto
// motita/<main id>; after the merge nothing moved it back, the next thread was branched FROM that
// dead branch, and "mergeable" compared every thread with it. The folder's branch is a fact about
// the folder, not about the project.

// setBase records the session's base branch as last decided; "" for a session outside a project.
func (c *conversation) setBase(branch string) {
	c.stateMu.Lock()
	defer c.stateMu.Unlock()
	c.baseBranch = branch
}

// projectBase is the branch a session of p starts from and is compared with: the project's main
// branch when the repository has it, else "" (the checkout's HEAD, for a repository with no such
// branch yet). A project whose folder is not a repository has no single base: each repository
// inside has its own (see startBranchOf).
func projectBase(ctx context.Context, p *Project) string {
	if p == nil || !projectIsRepo(ctx, p.Dir) {
		return ""
	}
	return startBranchOf(ctx, p)
}

// startBranchOf is the main branch of p's repository when the repository has it, else "".
func startBranchOf(ctx context.Context, p *Project) string {
	main := mainBranchOf(ctx, p)
	if main == "" || !gitx.BranchExists(ctx, p.Dir, main) {
		return ""
	}
	return main
}

// freshStart is the branch a NEW session branch of the repository at dir starts from: its main
// branch, brought up to date with origin first when that can be done without touching anything
// (forward only, never a dirty checkout, never a folder of the user's own). A session that starts from a stale main builds on code the project has already
// left behind, and its pull request conflicts for no reason. The update is advisory: offline, the
// branch is what it is. "" when the repository has no main branch to start from.
func (s *Server) freshStart(ctx context.Context, p *Project) string {
	start := startBranchOf(ctx, p)
	if start == "" || s.usersFolder(p.Dir) {
		return start // a folder of the user's own is never updated by a new session
	}
	if err := gitx.RefreshBranch(ctx, p.Dir, start); err != nil && !errors.Is(err, gitx.ErrBranchDirty) {
		s.warn("a new session starts from the main branch as it is: it could not be brought up to date",
			"project", p.Dir, "branch", start, "error", err.Error())
	}
	return start
}

// sessionBranchNote is what a session's agent is told about its branches, so it knows where to
// commit and what a pull request goes into. A folder that is a repository gets one note about it;
// a plain folder holding several gets one line per repository, each with its own main branch, and
// nothing is said about a folder that holds none.
func sessionBranchNote(ctx context.Context, workspace, base string) string {
	if workspace == "" {
		return ""
	}
	if gitx.IsRepoRoot(ctx, workspace) {
		return repoBranchNote(ctx, workspace, base)
	}
	var b strings.Builder
	for _, dir := range repoDirsIn(ctx, workspace) {
		main := startBranchOf(ctx, &Project{Dir: dir})
		if main == "" {
			continue
		}
		rel, _ := filepath.Rel(workspace, dir)
		fmt.Fprintf(&b, "\n- %s: on %q, main branch %q", filepath.ToSlash(rel), gitx.Display(ctx, dir), main)
	}
	if b.Len() == 0 {
		return ""
	}
	return "\n\nGit in this session, repository by repository (each has its own history and its own main " +
		"branch; commit in the repository you changed, on a work branch of it, and propose the work with a " +
		"pull request into that repository's main branch; never commit on a main branch):" + b.String()
}

// repoBranchNote is sessionBranchNote for a session whose folder is one repository.
func repoBranchNote(ctx context.Context, workspace, base string) string {
	if base == "" {
		return ""
	}
	head := gitx.Display(ctx, workspace)
	var b strings.Builder
	fmt.Fprintf(&b, "\n\nGit in this session: the project's main branch is %q", base)
	switch {
	case head == "" || strings.HasPrefix(head, "detached at "):
		b.WriteString(". The checkout is not on a branch: switch to one before committing (" +
			"`git switch -c <type>/<name>` from the main branch).")
	case head == base:
		fmt.Fprintf(&b, ", and the checkout is on it. Never commit on %q: before the first commit, make a work "+
			"branch (`git switch -c <type>/<name>`) and commit there; a pull request goes from that branch into %q.",
			base, base)
	default:
		fmt.Fprintf(&b, " and this session's branch is %q, which the checkout is on. Commit on %q and propose the "+
			"work with a pull request into %q; never switch the checkout to %q or merge into it yourself.",
			head, head, base, base)
	}
	return b.String()
}

// settleAfterPR puts the repository a pull request came from back where the project wants it once
// the pull request is over, merged or closed: on its main branch, brought up to date with origin.
//
// Only a checkout the SESSION SHARES WITH THE PROJECT is moved - the project's folder (the main
// session's) or a repository inside it. A thread's own worktree is left on the thread's branch: git
// refuses to check a branch out twice, so it could not go onto the main branch anyway, and the
// session is the integrated one from here on (see finishMerge) and is released with its branch
// when it is deleted.
//
// Nothing is forced. A dirty checkout stays where it is, with its changes, and a checkout that
// cannot be moved is logged, not reported: the pull request has already ended either way. After a
// MERGE the session's own branch (motita/<id>) is deleted too, with -D: the host merged it, by a
// squash or a rebase as often as not, after which `branch -d` refuses it for ever although every
// line of it is on the base. A branch with any other name is the user's, and only goes when git
// agrees it is merged.
func (s *Server) settleAfterPR(ctx context.Context, c *conversation, repo, base string, merged bool) {
	p := s.projectOf(c.projectID)
	if p == nil {
		return
	}
	dir := c.sessionRepoDir(ctx, repo)
	if dir == "" || !gitx.SamePath(c.workspace, c.projectDir) {
		// A thread's worktree, or nothing to settle.
		return
	}
	main := settleBranch(ctx, p, dir, base)
	if main == "" {
		return
	}
	head := gitx.Display(ctx, dir)
	if head != main && !s.returnToBranch(ctx, c, dir, head, main) {
		return
	}
	s.bringUpToDate(ctx, c, p, dir, main)
	if head != main && merged {
		s.dropMergedBranch(ctx, c, dir, head)
	}
}

// settleBranch is the main branch the repository at dir goes back to: the project's when dir is
// its folder, the repository's own otherwise, and the pull request's base when neither is known.
func settleBranch(ctx context.Context, p *Project, dir, base string) string {
	if dir == p.Dir {
		return cmp.Or(startBranchOf(ctx, p), base)
	}
	return cmp.Or(startBranchOf(ctx, &Project{Dir: dir}), base)
}

// bringUpToDate moves main forward from origin after a pull request: the project's folder by the
// project's own rule (see syncProject), a repository inside a plain folder by its own fetch.
func (s *Server) bringUpToDate(ctx context.Context, c *conversation, p *Project, dir, main string) {
	if dir == p.Dir {
		s.syncProject(ctx, c, main)
		return
	}
	if err := gitx.RefreshBranch(ctx, dir, main); err != nil {
		s.warn("the repository could not be brought up to date after the pull request",
			"session", c.id, "dir", dir, "branch", main, "error", err.Error())
	}
}

// returnToBranch checks dir out on main, and reports whether it did: a dirty checkout, or one git
// refuses to move, stays on head with a warning.
func (s *Server) returnToBranch(ctx context.Context, c *conversation, dir, head, main string) bool {
	if dirty, err := gitx.Dirty(ctx, dir); err != nil || dirty {
		s.warn("the checkout stays on the pull request's branch: it has uncommitted changes",
			"session", c.id, "dir", dir, "branch", head)
		return false
	}
	if err := gitx.Checkout(ctx, dir, main); err != nil {
		s.warn("the checkout could not go back to the main branch after the pull request",
			"session", c.id, "dir", dir, "branch", main, "error", err.Error())
		return false
	}
	return true
}

// dropMergedBranch deletes the branch a merged pull request came from: the session's own with
// -D (the host merged it), any other only when git agrees it is merged.
func (s *Server) dropMergedBranch(ctx context.Context, c *conversation, dir, head string) {
	var err error
	if head == sessionBranch(c.id) {
		err = gitx.ForceDeleteBranch(ctx, dir, head)
	} else {
		err = gitx.DeleteBranch(ctx, dir, head)
	}
	if err != nil {
		s.warn("the pull request's branch stays after the merge", "session", c.id, "dir", dir, "branch", head,
			"error", err.Error())
	}
}
