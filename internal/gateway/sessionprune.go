package gateway

import (
	"context"
	"net/http"
	"slices"
	"sort"
	"strings"

	"github.com/madkoding/motita/internal/gitx"
)

// sessionRoutes registers the endpoints about the set of sessions.
func (s *Server) sessionRoutes(mux *http.ServeMux, plain func(http.HandlerFunc) http.Handler) {
	mux.Handle("GET /v1/sessions", plain(s.handleListSessions))
	mux.Handle("POST /v1/sessions", plain(s.handleCreateSession))
	mux.Handle("POST /v1/sessions/prune-blank", plain(s.handlePruneBlankSessions))
}

// closeIfBlank marks a session as closing when it holds nothing worth keeping and reports whether
// it did: it was opened and never used. It has no turn, no checkpoint, nothing queued, no run, no
// pull request and no name of its own, and it is not the default session (which is only reset).
//
// The mark is taken in the same step as the check, so a message arriving in between is refused as
// it is for any session being deleted, and the session it would have started is not lost.
func (c *conversation) closeIfBlank() bool {
	if c.id == DefaultSession {
		return false
	}
	c.stateMu.Lock()
	keep := []bool{
		c.running, c.closing, c.merged, c.prWatch != nil, len(c.queue) > 0,
		c.title != "" && !isPlaceholderTitle(c.title),
	}
	if slices.Contains(keep, true) {
		c.stateMu.Unlock()
		return false
	}
	c.closing = true
	c.epoch++
	c.stateMu.Unlock()

	c.cpMu.Lock()
	marks := len(c.checkpoints)
	c.cpMu.Unlock()
	if marks > 0 || len(c.svc.Transcript()) > 0 {
		c.reopen(false)
		return false
	}
	return true
}

// pruneBlankSessions removes the sessions that were opened and never used, and returns their ids.
//
// A checkout with uncommitted work is never discarded here: releasing it is refused as in any
// deletion, and that session stays.
func (s *Server) pruneBlankSessions() []string {
	removed := []string{}
	for _, c := range s.snapshot() {
		if !c.closeIfBlank() {
			continue
		}
		if err := s.releaseWorktree(c, false); err != nil {
			s.reopenAndDrain(c, false)
			continue
		}
		c.closeForGood()
		s.releaseFreeSessionDir(c)
		s.forgetCheckpoints(c)
		s.forget(c.id)
		s.deletePersistedSession(c.id)
		removed = append(removed, c.id)
	}
	sort.Strings(removed)
	return removed
}

// handlePruneBlankSessions answers which unused sessions it removed.
func (s *Server) handlePruneBlankSessions(w http.ResponseWriter, _ *http.Request) {
	removed := s.pruneBlankSessions()
	writeJSON(w, http.StatusOK, map[string]any{"removed": removed, "count": len(removed)})
}

// pruneSessionBranches deletes, once on the way in and in the background, the branches of sessions
// no record names any more from every project's repositories: the ones a session left before its
// branch went with it. Only a branch with nothing unmerged goes (`git branch -d`); one that holds
// work stays for the user to look at.
func (s *Server) pruneSessionBranches(known map[string]bool) {
	dirs := []string{}
	for _, p := range s.allProjects() {
		dirs = append(dirs, p.Dir)
		for _, repo := range p.Repos {
			dirs = append(dirs, repo.Dir)
		}
	}
	s.mainRefresh.wg.Add(1)
	go func() {
		defer s.mainRefresh.wg.Done()
		for _, dir := range distinctDirs(dirs) {
			pruneSessionBranchesIn(s.baseCtx, dir, known)
		}
	}()
}

// pruneSessionBranchesIn deletes the merged branches of the sessions known does not name from the
// repository at dir.
func pruneSessionBranchesIn(ctx context.Context, dir string, known map[string]bool) {
	for _, branch := range gitx.SessionBranches(ctx, dir) {
		if id := strings.TrimPrefix(branch, "motita/"); validSessionBranchID(id) && !known[id] {
			// Advisory: unmerged work keeps its branch.
			_ = gitx.DeleteBranch(ctx, dir, branch)
		}
	}
}

// distinctDirs is dirs without the ones that name a folder already in it.
func distinctDirs(dirs []string) []string {
	out := []string{}
	for _, dir := range dirs {
		if !slices.ContainsFunc(out, func(seen string) bool { return gitx.SamePath(seen, dir) }) {
			out = append(out, dir)
		}
	}
	return out
}

// validSessionBranchID reports whether the part of a branch after motita/ is a session's id, and
// not a branch of something else under the same prefix (a background agent's).
func validSessionBranchID(id string) bool {
	return len(id) == 25 && id[0] == 's' && strings.Trim(id[1:], "0123456789abcdef") == ""
}
