package gateway

import (
	"net/http"
	"sort"
)

// blank reports whether a session holds nothing worth keeping: it was opened and never used. It
// has no turn, no checkpoint, nothing queued, no run, no pull request and no name of its own, and it
// is not the default session (which is only reset, never removed).
func (c *conversation) blank() bool {
	if c.id == DefaultSession || c.svc == nil {
		return false
	}
	c.stateMu.Lock()
	idle := !c.running && !c.closing && !c.merged && c.prWatch == nil && len(c.queue) == 0 &&
		(c.title == "" || isPlaceholderTitle(c.title))
	c.stateMu.Unlock()
	if !idle {
		return false
	}
	c.cpMu.Lock()
	marks := len(c.checkpoints)
	c.cpMu.Unlock()
	return marks == 0 && len(c.svc.Transcript()) == 0
}

// pruneBlankSessions removes the sessions that were opened and never used, and returns their ids.
//
// A checkout with uncommitted work is never discarded here: releasing it is refused as in any
// deletion, and that session stays.
func (s *Server) pruneBlankSessions() []string {
	removed := []string{}
	for _, c := range s.snapshot() {
		if !c.blank() {
			continue
		}
		c.beginClosing()
		if c.isRunning() || !c.blankWhileClosing() {
			s.reopenAndDrain(c, false)
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
		if c.projectID != "" {
			s.returnToMain(s.projectOf(c.projectID))
		}
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

// blankWhileClosing is blank for a session this prune already marked as closing: a message that
// arrived between the first check and the mark must keep the session.
func (c *conversation) blankWhileClosing() bool {
	c.stateMu.Lock()
	c.closing = false
	c.stateMu.Unlock()
	ok := c.blank()
	c.stateMu.Lock()
	c.closing = true
	c.stateMu.Unlock()
	return ok
}
