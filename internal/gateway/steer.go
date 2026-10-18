package gateway

import (
	"net/http"
	"strings"
)

// steerer is a service whose task in flight reads the person's messages: see agent.Steer.
type steerer interface {
	Steer(text string) bool
}

// handleSteer sends a message to the task in flight without stopping it: the agent reads it at
// its next round and the turn's trail shows it. When nothing is running, or the run cannot be
// steered (a plan, a task between rounds), the message is queued or started as /queue would, and
// the answer says which happened.
func (s *Server) handleSteer(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Task string `json:"task"`
	}
	if !s.decodeBody(w, r, &body) {
		return
	}
	if strings.TrimSpace(body.Task) == "" {
		writeError(w, http.StatusBadRequest, "the task is empty")
		return
	}
	c := convOf(r)
	if c.isMerged() {
		writeError(w, http.StatusConflict, msgSessionIntegrated)
		return
	}
	if rn, busy := c.currentRun(); busy {
		if st, ok := c.svc.(steerer); ok && st.Steer(body.Task) {
			rn.progress()("you: %s", strings.TrimSpace(body.Task))
			writeJSON(w, http.StatusAccepted, map[string]any{"steered": true, "queued": false, "started": false})
			return
		}
	}
	started, pos := s.startOrQueue(c, body.Task)
	if started {
		writeJSON(w, http.StatusAccepted, map[string]any{"steered": false, "queued": false, "started": true})
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"steered": false, "queued": true, "started": false, "position": pos})
}
