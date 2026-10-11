package gateway

import (
	"errors"
	"net/http"

	"github.com/madkoding/motita/internal/skills"
)

// ProposalService is the review of the skills the background review proposed. It is optional:
// a SkillService that does not implement it answers 501, so a double that has no proposals
// keeps compiling.
type ProposalService interface {
	ProposedSkills() ([]string, error)
	ProposedSkill(name string) (skills.Skill, error)
	AcceptProposedSkill(name string) error
	RejectProposedSkill(name string) error
}

// noProposals is the answer when the library cannot hold proposals.
const noProposals = "this gateway's library does not keep proposed skills"

// proposals is the service behind the proposal endpoints, or nil after a 501 was written.
func (s *Server) proposals(w http.ResponseWriter) ProposalService {
	if s.opts.Skills == nil {
		writeError(w, http.StatusNotImplemented, noLibrary)
		return nil
	}
	p, ok := s.opts.Skills.(ProposalService)
	if !ok {
		writeError(w, http.StatusNotImplemented, noProposals)
		return nil
	}
	return p
}

// writeProposalError answers a failed proposal action: 404 for a name with no proposal, 500 for
// a disk that refused.
func writeProposalError(w http.ResponseWriter, err error) {
	status := http.StatusInternalServerError
	if errors.Is(err, skills.ErrNotFound) {
		status = http.StatusNotFound
	}
	writeError(w, status, err.Error())
}

// handleProposedSkills lists the proposals waiting for the user. An empty list, never null.
func (s *Server) handleProposedSkills(w http.ResponseWriter, _ *http.Request) {
	p := s.proposals(w)
	if p == nil {
		return
	}
	names, err := p.ProposedSkills()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if names == nil {
		names = []string{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"skills": names})
}

// handleProposedSkill answers one proposal, body included, for the user to read before deciding.
func (s *Server) handleProposedSkill(w http.ResponseWriter, r *http.Request) {
	p := s.proposals(w)
	if p == nil {
		return
	}
	sk, err := p.ProposedSkill(r.PathValue("name"))
	if err != nil {
		writeProposalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"name": sk.Name, "title": sk.Title, "body": sk.Body})
}

// handleAcceptProposedSkill puts a proposal in the library, where sessions use it.
func (s *Server) handleAcceptProposedSkill(w http.ResponseWriter, r *http.Request) {
	p := s.proposals(w)
	if p == nil {
		return
	}
	if err := p.AcceptProposedSkill(r.PathValue("name")); err != nil {
		writeProposalError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleRejectProposedSkill deletes a proposal.
func (s *Server) handleRejectProposedSkill(w http.ResponseWriter, r *http.Request) {
	p := s.proposals(w)
	if p == nil {
		return
	}
	if err := p.RejectProposedSkill(r.PathValue("name")); err != nil {
		writeProposalError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
