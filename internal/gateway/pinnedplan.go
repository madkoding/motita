package gateway

import "net/http"

// The plan a conversation keeps word for word (see session.Pin), which the person can read,
// change or drop. It is an optional capability of the service: a runner that keeps no plan, or a
// remote client, answers that it cannot.

// planKeeper is a service that keeps a pinned plan. PinnedPlanState says whether the plan is the
// one in force ("active") or one the agent proposed and the person has not approved ("proposed").
type planKeeper interface {
	PinnedPlan() string
	PinnedPlanState() string
	SetPinnedPlan(text string)
}

// planView is the plan as the web reads it.
func planView(k planKeeper) map[string]string {
	return map[string]string{"plan": k.PinnedPlan(), "state": k.PinnedPlanState()}
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

// handleGetPlan answers the pinned plan and its state, both "" when there is none.
func (s *Server) handleGetPlan(w http.ResponseWriter, r *http.Request) {
	if k := keeperOf(w, r); k != nil {
		writeJSON(w, http.StatusOK, planView(k))
	}
}

// handlePutPlan replaces the pinned plan; a blank one drops it. A plan the person saves is one
// they approved, so the service keeps it as active.
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
	writeJSON(w, http.StatusOK, planView(k))
}
