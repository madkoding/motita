package gateway

import "net/http"

// The endpoints that are routed, authenticated and framed and whose behaviour is not written yet.
// Each one is replaced by its real handler in the next slice of work, and this file disappears
// when the last one is real.
//
// They are here rather than absent for a concrete reason: the routing table, the token check and
// the JSON framing are the parts that are cheap to get wrong and expensive to debug later, and
// landing them green first means the streamed runs are built on something already verified. Each
// answers 501 with the same shape every other refusal uses, so a client never has to guess what
// "not yet" looks like.

func (s *Server) handleTask(w http.ResponseWriter, r *http.Request)     { notImplemented(w, r) }
func (s *Server) handlePlan(w http.ResponseWriter, r *http.Request)     { notImplemented(w, r) }
func (s *Server) handleApproval(w http.ResponseWriter, r *http.Request) { notImplemented(w, r) }
