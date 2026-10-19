package gateway

import (
	"net/http"
	"strings"
)

// handleMoveSessionToProject moves a free-standing session into an existing project: the same
// conversation, now running in its own worktree of the project like any session born there. It is
// what "this talk belongs in a project" does once the project exists. The directory it worked in
// before is released when empty and kept otherwise, as when a session is deleted: what it holds is
// the user's work.
func (s *Server) handleMoveSessionToProject(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ProjectID string `json:"project_id"`
	}
	if !s.decodeBody(w, r, &body) {
		return
	}
	p := s.projectOf(strings.TrimSpace(body.ProjectID))
	if p == nil {
		writeError(w, http.StatusNotFound, ErrProjectNotFound.Error())
		return
	}
	c := convOf(r)
	c.stateMu.Lock()
	inProject := c.projectID != ""
	c.stateMu.Unlock()
	if inProject {
		writeError(w, http.StatusConflict, "this session already belongs to a project")
		return
	}
	// A run writes in the directory it started in: moving under it would split one turn between two.
	if !c.takeRunSlot() {
		writeError(w, http.StatusConflict, "this session is working: move it when the turn ends")
		return
	}
	defer c.releaseRunSlot()
	s.refreshMainInBackground(p)
	s.awaitMainRefresh(p)
	s.releaseFreeSessionDir(c)
	s.placeInProject(c, p.ID, p)
	s.saveSession(c)
	writeJSON(w, http.StatusOK, c.status())
}
