package gateway

import (
	"context"
	"path/filepath"
	"strings"

	"github.com/madkoding/motita/internal/gitx"
)

// sessionBranch is the branch a session works on. The prefix is what makes the
// branches findable: `git branch --list 'motita/*'` lists exactly the sessions.
func sessionBranch(sessionID string) string {
	return "motita/" + sessionID
}

// countSessionWorktrees counts the checkouts in a worktree listing that are a
// SESSION's worktree of the project at projectDir.
//
// The project's own checkout is in the same listing and is not one: it is where
// the user works, not a session. It is told apart by path, which is the only
// thing that distinguishes the two - every entry in this listing is a checkout
// of the same repository, and a branch-based rule would both count the project's
// own branch and miss a session whose worktree has been moved.
//
// A prunable registration is NOT counted. Its directory is gone, so there is no
// checkout to speak of, and reporting it as a live worktree would give the user
// a number they cannot reconcile with anything on disk.
//
// The count is also bounded by what the PROJECT can still reach, and this is the
// half that was missing. Measured on a real gateway: a project whose sessions had
// all been deleted still reported "worktrees: 1", because a registration from a
// run whose checkout lived outside the workspace was counted like any other. A
// number the user cannot reconcile with anything they can see is worse than no
// number: it says work is out there and offers no way to find it.
//
// `live` are the session ids the gateway still holds. A registration whose path
// does not end in one of them is not a session's worktree any more - whatever it
// is, it is not something this project can name or clean up.
func countSessionWorktrees(all []gitx.Worktree, projectDir string, live map[string]bool) int {
	n := 0
	for _, w := range all {
		if w.Prunable {
			continue
		}
		if gitx.SamePath(w.Path, projectDir) {
			continue
		}
		if !isLiveSessionWorktree(w.Path, live) {
			continue
		}
		n++
	}
	return n
}

// isLiveSessionWorktree reports whether a checkout's path is a session of this
// gateway's worktree.
//
// The path is `<workspace>/worktrees/<session id>`, so the LAST element carries
// the id and that is what is asked about. The id has to be one the gateway still
// holds: a registration left by a deleted session, or by a session of a project
// that no longer exists, would otherwise keep the count up for ever.
//
// A nil map means the caller cannot say which sessions are live, and then the
// answer is "yes" for every checkout: hiding a real one is the worse mistake, and
// the number is decoration either way.
func isLiveSessionWorktree(path string, live map[string]bool) bool {
	if live == nil {
		return true
	}
	id := filepath.Base(filepath.Clean(path))
	return live[id]
}

// sessionWorktree gives a session its own checkout of a project, so two
//
// It returns the directory the session should run in. When a worktree cannot be
// made it returns the PROJECT's directory and the reason, because worktrees
// need git: a project folder that is not a repository, or one with no commits
// yet, must keep working exactly as it did before rather than failing to start
// a session. Turning "this project is not a repo" into "this session cannot
// start" would be a far worse answer than running in the directory the user
// actually pointed at.
//
// It is IDEMPOTENT, and that is not a convenience: the common case is a session
// whose worktree already exists - one restored after a gateway restart, or one
// the caller asked about twice - and `git worktree add` REFUSES a directory it
// has already registered. Measured: it fails with "Preparing worktree (checking
// out 'motita/<id>')" and a non-zero status, which would turn every restored
// session into one that could not run.
//
// The session's branch is the unit of isolation, and it OUTLIVES the worktree:
// a session whose worktree is removed and recreated finds its own commits again
// rather than starting over. That is why AddWorktree attaches an existing
// branch instead of insisting on a new one.
//
// The path is derived from the workspace root and the session id alone, so the
// same session always resolves to the same directory. This is the ONLY place
// that decides where a session runs: a second copy of that rule is how a
// session ends up reporting one directory while running in another.
//
// start is the branch a NEW session branch starts from: the project's main branch (see
// freshStart), never whatever the project's folder has checked out. "" lets git start from the
// folder's HEAD, for a repository with no main branch yet. A branch the session already has is
// reattached as it is, wherever it started.
func (s *Server) sessionWorktree(ctx context.Context, repoDir, sessionID, start string) (string, error) {
	if strings.TrimSpace(s.opts.WorkspaceDir) == "" {
		// Without a workspace root there is nowhere to put a worktree, and a
		// relative path would be resolved against the process's own directory.
		// The project directory is a correct answer and needs no extra state.
		return repoDir, nil
	}
	path := filepath.Join(s.opts.WorkspaceDir, "worktrees", sessionID)
	branch := sessionBranch(sessionID)
	// A plain folder holding repositories: a worktree of each one (see sessionnested.go).
	if !projectIsRepo(ctx, repoDir) {
		return nestedSessionWorktree(ctx, repoDir, path, branch)
	}
	// Already ours: a LIVE worktree of this repository at this path is the
	// worktree asked for, whatever created it.
	//
	// The test is the PATH, deliberately, and not the branch. A session whose
	// worktree the user moved to a feature branch is still working in its own
	// tree - and asking about the branch instead answers "not ours" for it,
	// which then sends this function down the add below. Measured: that add
	// fails ("already exists", non-zero), the error propagates as the fallback
	// to the PROJECT's directory, and two sessions end up editing one checkout -
	// the exact collision worktrees exist to prevent.
	if _, ok, err := gitx.LiveWorktreeAt(ctx, repoDir, path); err == nil && ok {
		return path, nil
	}
	if err := gitx.AddWorktreeFrom(ctx, repoDir, path, branch, start); err != nil {
		return repoDir, err
	}
	// The checkout is complete as far as git is concerned, and that is not the same as
	// being able to RUN.
	//
	// `git worktree add` copies nothing git ignores, so a project whose dependencies live
	// in an ignored directory - `node_modules/` is the common one - gets a checkout where
	// `npm run lint`, `tsc` and `vitest` do not exist. The anchor then runs the project's
	// own gate and reports "failed checks: npm lint, npm typecheck, npm test", which
	// reads as broken CODE and is really a missing TOOLCHAIN. Measured on a real session:
	// the agent spent round after round discovering this, and the user's report was
	// "demasiado determinista?" about a gate that was simply running in an empty tree.
	//
	// Linking is what makes the checkout runnable without a fresh install per session
	// (measured: 860 MB and ~30 s for one project). It is a LINK and not a copy: the
	// dependencies are the same bytes, npm reads them without writing, and a copy per
	// session would multiply the disk by the number of sessions.
	gitx.LinkDependencyDirs(repoDir, path)
	return path, nil
}
