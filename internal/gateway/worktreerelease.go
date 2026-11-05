package gateway

import (
	"context"

	"github.com/madkoding/motita/internal/gitx"
)

// A thread whose pull request was merged is done: its work is on the base branch, and the session
// is read-only from here on. Its worktree used to stay on disk until the thread was deleted, one
// full checkout per finished thread, for nothing. It is given back as soon as the merge is seen,
// when that can be done without losing anything: a clean checkout, no run in flight. The session
// keeps its transcript, its commit and its place in the list, and says its checkout is gone.

// releaseMergedWorktree gives back the worktree of a session that was just integrated. Nothing is
// forced: a checkout with uncommitted work, or one a run is still writing in, stays and the
// session goes on as before (released when it is deleted). After a release the session runs in
// the project's folder, like a merged session that never had a worktree, and remembers that it
// was isolated so that continuing it isolates again.
func (s *Server) releaseMergedWorktree(c *conversation) {
	if !c.isMerged() || c.isRunning() || gitx.SamePath(c.workspace, c.projectDir) {
		return
	}
	if c.ownWorktreeName(context.Background()) == "" {
		return
	}
	if err := s.releaseWorktree(c, false); err != nil {
		s.warn("the merged session keeps its worktree", "id", c.id, "error", err.Error())
		return
	}
	c.stateMu.Lock()
	c.workspace = c.projectDir
	c.worktreeReleased = true
	c.stateMu.Unlock()
	c.svc.SetWorkspace(c.projectDir)
}

// wasIsolated reports whether the session has, or had before its merge, a worktree of its own.
func (c *conversation) wasIsolated() bool {
	c.stateMu.Lock()
	defer c.stateMu.Unlock()
	return c.worktreeReleased || c.projectDir != "" && !gitx.SamePath(c.workspace, c.projectDir)
}
