package gateway

import "net/http"

// handleIsolateSession gives a thread a worktree of its own when it works in the project's folder.
// That is the state of the threads made out of the sessions that existed before threads did (see
// adoptThreads): they keep the folder they had until the person asks for the isolation they would
// have had if they were born as threads. A thread that already has its worktree answers as it is.
func (s *Server) handleIsolateSession(w http.ResponseWriter, r *http.Request) {
	c := convOf(r)
	c.stateMu.Lock()
	parent, pid, shared := c.parentID, c.projectID, c.workspace == c.projectDir
	c.stateMu.Unlock()
	if parent == "" {
		writeError(w, http.StatusConflict, "only a thread has a worktree of its own: this is the main session")
		return
	}
	if !shared {
		writeJSON(w, http.StatusOK, c.status())
		return
	}
	project := s.scopeOf(pid)
	if project == nil {
		writeError(w, http.StatusNotFound, ErrProjectNotFound.Error())
		return
	}
	// A run writes in the directory it started in: moving under it would split one turn in two.
	if !c.takeRunSlot() {
		writeError(w, http.StatusConflict, "this thread is working: isolate it when the turn ends")
		return
	}
	defer c.releaseRunSlot()
	s.placeInProject(c, pid, project, true)
	s.saveSession(c)
	writeJSON(w, http.StatusOK, c.status())
}
