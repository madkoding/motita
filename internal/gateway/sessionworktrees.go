package gateway

import (
	"path/filepath"

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
