package gateway

import "net/http"

// The endpoints below are routed, authenticated and framed, and their behaviour is not written
// yet. Each one is replaced by its real handler in the next slices of work.
//
// They are here rather than absent for a concrete reason: the routing table, the token check and
// the JSON framing are the parts that are cheap to get wrong and expensive to debug later, and
// landing them green first means the streamed runs are built on something already verified. Each
// one answers 501 with the same shape every other refusal uses, so a client never has to guess
// what "not yet" looks like.
//
// This file disappears entirely when the last handler is real.

func (s *Server) handleSession(w http.ResponseWriter, r *http.Request)       { notImplemented(w, r) }
func (s *Server) handleSessionReport(w http.ResponseWriter, r *http.Request) { notImplemented(w, r) }
func (s *Server) handleReset(w http.ResponseWriter, r *http.Request)         { notImplemented(w, r) }
func (s *Server) handleModels(w http.ResponseWriter, r *http.Request)        { notImplemented(w, r) }
func (s *Server) handleReasoning(w http.ResponseWriter, r *http.Request)     { notImplemented(w, r) }
func (s *Server) handleVerdict(w http.ResponseWriter, r *http.Request)       { notImplemented(w, r) }
func (s *Server) handleReward(w http.ResponseWriter, r *http.Request)        { notImplemented(w, r) }
func (s *Server) handleQuestions(w http.ResponseWriter, r *http.Request)     { notImplemented(w, r) }
func (s *Server) handleTask(w http.ResponseWriter, r *http.Request)          { notImplemented(w, r) }
func (s *Server) handlePlan(w http.ResponseWriter, r *http.Request)          { notImplemented(w, r) }
func (s *Server) handleApproval(w http.ResponseWriter, r *http.Request)      { notImplemented(w, r) }
