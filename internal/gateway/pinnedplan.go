package gateway

import "net/http"

// The plan a conversation keeps word for word (see session.Pin), which the person can read,
// change or drop. It is an optional capability of the service: a runner that keeps no plan, or a
// remote client, answers that it cannot.

// planKeeper is a service that keeps a pinned plan.
type planKeeper interface {
	PinnedPlan() string
	SetPinnedPlan(text string)
}

// keeperOf is the conversation's plan keeper, or nil with the refusal already written.
func keeperOf(w http.ResponseWriter, r *http.Request) planKeeper {
	k, ok := convOf(r).svc.(planKeeper)
	if !ok {
		writeError(w, http.StatusNotImplemented, "this conversation does not keep a plan")
		return nil
	}
	return k
}

// handleGetPlan answers the pinned plan, "" when there is none.
func (s *Server) handleGetPlan(w http.ResponseWriter, r *http.Request) {
	if k := keeperOf(w, r); k != nil {
		writeJSON(w, http.StatusOK, map[string]string{"plan": k.PinnedPlan()})
	}
}

// handlePutPlan replaces the pinned plan; a blank one drops it.
func (s *Server) handlePutPlan(w http.ResponseWriter, r *http.Request) {
	k := keeperOf(w, r)
	if k == nil {
		return
	}
	var body struct {
		Plan string `json:"plan"`
	}
	if !s.decodeBody(w, r, &body) {
		return
	}
	k.SetPinnedPlan(body.Plan)
	writeJSON(w, http.StatusOK, map[string]string{"plan": k.PinnedPlan()})
}
