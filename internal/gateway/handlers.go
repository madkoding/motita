package gateway

import (
	"net/http"

	"github.com/madkoding/starlight/internal/agent"
)

// The endpoints that answer in one shot: they read or change a small thing and return. The
// streamed runs (task and plan) and the approval round trip share the streaming path and live in
// runs.go.

// handleSession answers the figures a status bar draws.
//
// session.Snapshot goes on the wire as it is, with no parallel DTO. Its fields are already
// exported and already have the shape the wire wants, and a second struct with the same fields
// is drift waiting to happen - which is exactly the mistake this repository already had to write
// a test to catch in its published documents.
//
// It is the opposite decision from configView, and for the opposite reason: there is a secret in
// the configuration and there is none here.
func (s *Server) handleSession(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.svc.ConversationSummary())
}

// handleSessionReport answers the human-readable report.
//
// The text is a rendered block, not a structure, because that is what the runner produces: it is
// the same block the /session command prints, and re-deriving it from the snapshot here would be
// a second implementation of the same report.
func (s *Server) handleSessionReport(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"text": s.svc.ConversationReport()})
}

// handleReset starts a new conversation.
//
// 204 rather than a body: there is nothing to say about it, and a client that gets an empty 200
// has to guess whether it worked.
func (s *Server) handleReset(w http.ResponseWriter, _ *http.Request) {
	s.svc.ResetConversation()
	w.WriteHeader(http.StatusNoContent)
}

// handleModels answers the catalogue report.
func (s *Server) handleModels(w http.ResponseWriter, r *http.Request) {
	text, err := s.svc.RunModels(r.Context())
	if err != nil {
		// The runner's own report is NOT produced on this path - it returns the error instead -
		// so the failure is reported here. 502 and not 500: the agent is fine, the provider it
		// asked is what did not answer. A client can act on that difference (check the network,
		// check the key), which is why it is not flattened into "server error".
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"text": text})
}

// handleReasoning changes the reasoning level.
func (s *Server) handleReasoning(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Level string `json:"level"`
	}
	if !s.decodeBody(w, r, &body) {
		return
	}
	s.svc.SetReasoning(body.Level)
	w.WriteHeader(http.StatusNoContent)
}

// handleVerdict applies the user's verdict on the last turn and returns the report.
func (s *Server) handleVerdict(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Good bool   `json:"good"`
		Note string `json:"note"`
	}
	if !s.decodeBody(w, r, &body) {
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"text": s.svc.RecordVerdict(body.Good, body.Note)})
}

// handleReward answers what the library has learned.
func (s *Server) handleReward(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"text": s.svc.RewardReport()})
}

// handleQuestions answers the questions of the last turn and clears them.
//
// An empty LIST rather than null, always. A client that has to tell "there are no questions"
// from "the field is missing" is a client with a bug waiting to happen, and the fix costs one
// line here.
func (s *Server) handleQuestions(w http.ResponseWriter, _ *http.Request) {
	items, origin := s.svc.TakePendingQuestions()
	if items == nil {
		items = []agent.AskItem{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "origin": origin})
}
