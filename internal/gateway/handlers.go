package gateway

import (
	"net/http"
	"strings"

	"github.com/madkoding/motita/internal/agent"
)

// The endpoints that answer in one shot: they read or change a small thing and return. The
// streamed runs (task and plan) and the approval round trip share the streaming path and live in
// runs.go.
//
// Every one of them speaks about a CONVERSATION, so each starts by asking the request which one
// it is about. That is what makes two front ends independent: the figures, the configuration and
// the questions belong to a conversation, not to the process.

// handleSession answers the figures a status bar draws.
//
// session.Snapshot goes on the wire as it is, with no parallel DTO. Its fields are already
// exported and already have the shape the wire wants, and a second struct with the same fields
// is drift waiting to happen - which is exactly the mistake this repository already had to write
// a test to catch in its published documents.
//
// It is the opposite decision from configView, and for the opposite reason: there is a secret in
// the configuration and there is none here.
func (s *Server) handleSession(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, convOf(r).svc.ConversationSummary())
}

// handleMessages answers the conversation so far.
//
// The route did not exist until now, and that is worth saying: the plan that was supposed to add
// it (the remote-client plan) was never implemented, so a client could switch conversations but
// had nothing to draw. It is registered the same way every other conversation endpoint is, by
// session, and never by a second addressing scheme - one rule with no exceptions is what makes the
// table readable.
//
// agent.DialogueTurn goes on the wire as it is, with no parallel DTO, for the same reason
// session.Snapshot does in handleSession: its fields are already exported and already have the
// shape the wire wants, and a second struct with the same fields is drift waiting to happen.
//
// It is behind the token, like every other conversation endpoint: it is the most revealing thing
// this gateway holds - every task the user described and everything the agent answered.
//
// An empty LIST rather than null, always: a client that has to tell "nothing has been said" from
// "the field is missing" is a client with a bug waiting to happen, and the fix costs one line.
func (s *Server) handleMessages(w http.ResponseWriter, r *http.Request) {
	turns := convOf(r).svc.Transcript()
	if turns == nil {
		turns = []agent.DialogueTurn{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"messages": turns})
}

// handleSessionReport answers the human-readable report.
//
// The text is a rendered block, not a structure, because that is what the runner produces: it is
// the same block the /session command prints, and re-deriving it from the snapshot here would be
// a second implementation of the same report.
func (s *Server) handleSessionReport(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"text": convOf(r).svc.ConversationReport()})
}

// handleReset starts a new conversation IN THIS session.
//
// 204 rather than a body: there is nothing to say about it, and a client that gets an empty 200
// has to guess whether it worked.
func (s *Server) handleReset(w http.ResponseWriter, r *http.Request) {
	convOf(r).svc.ResetConversation()
	w.WriteHeader(http.StatusNoContent)
}

// handleModels answers the catalogue report.
func (s *Server) handleModels(w http.ResponseWriter, r *http.Request) {
	text, err := convOf(r).svc.RunModels(r.Context())
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
	convOf(r).svc.SetReasoning(body.Level)
	w.WriteHeader(http.StatusNoContent)
}

// handleModel changes the model the next turns use.
//
// An empty id is refused rather than passed on: it names no model, and the next turn would fail
// at the provider far from the request that caused it.
func (s *Server) handleModel(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Model string `json:"model"`
	}
	if !s.decodeBody(w, r, &body) {
		return
	}
	if strings.TrimSpace(body.Model) == "" {
		writeError(w, http.StatusBadRequest, "model cannot be empty")
		return
	}
	convOf(r).svc.SetModel(strings.TrimSpace(body.Model))
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
	writeJSON(w, http.StatusOK, map[string]string{"text": convOf(r).svc.RecordVerdict(body.Good, body.Note)})
}

// handleReward answers what the library has learned.
func (s *Server) handleReward(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"text": convOf(r).svc.RewardReport()})
}

// handleQuestions answers the questions of the last turn and clears them.
//
// An empty LIST rather than null, always. A client that has to tell "there are no questions"
// from "the field is missing" is a client with a bug waiting to happen, and the fix costs one
// line here.
func (s *Server) handleQuestions(w http.ResponseWriter, r *http.Request) {
	items, origin := convOf(r).svc.TakePendingQuestions()
	if items == nil {
		items = []agent.AskItem{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "origin": origin})
}
