package gateway

import (
	"net/http"
	"slices"
	"sort"
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
		s.returnToMain(s.projectOf(c.projectID))
		// As for any closed session, the project's main branch is brought up to date.
		s.refreshMainInBackground(s.projectOf(c.projectID))
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
