package gateway

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/madkoding/motita/internal/skills"
	"github.com/madkoding/motita/internal/usage"
)

// SkillService is the procedure library, as a front end needs it.
//
// It is a SEPARATE interface from Service, and deliberately: Service is "everything a front
// end may ask of the AGENT", and this is about the library the process keeps beside it.
// Keeping them apart also keeps every existing double for Service compiling — with a
// 100%-per-package coverage gate, a widely-used double must not grow a method for a feature
// it does not exercise.
type SkillService interface {
	Skills() ([]skills.Skill, error)
	Skill(name string) (skills.Skill, error)
	SaveSkill(name, body string) (skills.Skill, error)
	SkillTelemetry() map[string]usage.Entry
	SetSkillPinned(name string, pinned bool) error
	SetSkillDisabled(name string, disabled bool) error
	DeleteSkill(name string) error
	ArchiveSkill(name string) error
	RestoreSkill(name string) error
	ArchivedSkills() ([]string, error)
}

// CuratorService is the maintenance pass, as a front end needs it.
type CuratorService interface {
	CuratorStatus() string
	CuratorRun(ctx context.Context, consolidate, dryRun bool) (string, error)
}

// skillView is one entry of the index.
//
// There is no body: a body is what a user opens a document to read, and sending forty
// procedures to draw forty titles is how a browser feels slow for no reason. The lifecycle
// telemetry rides along so the list can show the state without a round trip per skill.
type skillView struct {
	Name       string     `json:"name"`
	Title      string     `json:"title"`
	Summary    string     `json:"summary"`
	Path       string     `json:"path"`
	CreatedBy  string     `json:"created_by,omitempty"`
	State      string     `json:"state,omitempty"`
	Pinned     bool       `json:"pinned,omitempty"`
	Disabled   bool       `json:"disabled,omitempty"`
	UseCount   int        `json:"use_count"`
	ViewCount  int        `json:"view_count"`
	PatchCount int        `json:"patch_count"`
	LastUsedAt *time.Time `json:"last_used_at,omitempty"`
}

// noLibrary is the answer every skill endpoint gives when the gateway was started without a
// library. 501 and not 404: "this gateway has no library" is a different fact from "there is
// no such skill", and a client acts differently on each.
const noLibrary = "this gateway was started without a procedure library"

// noCurator is the same fact for the maintenance pass.
const noCurator = "this gateway was started without a curator"

// handleSkills answers the index a library browser draws.
func (s *Server) handleSkills(w http.ResponseWriter, _ *http.Request) {
	if s.opts.Skills == nil {
		writeError(w, http.StatusNotImplemented, noLibrary)
		return
	}
	index, err := s.opts.Skills.Skills()
	if err != nil {
		// 500 and not an empty list: an unreadable library is not an empty one, and a
		// browser that drew "no skills" for a broken directory would send the user looking
		// for documents they still have.
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	telemetry := s.opts.Skills.SkillTelemetry()
	views := make([]skillView, 0, len(index))
	for _, sk := range index {
		v := skillView{Name: sk.Name, Title: sk.Title, Summary: sk.Summary, Path: sk.Path}
		if e, ok := telemetry[sk.Name]; ok {
			v.CreatedBy = string(e.CreatedBy)
			v.State = string(e.State)
			v.Pinned = e.Pinned
			v.Disabled = e.Disabled
			v.UseCount, v.ViewCount, v.PatchCount = e.UseCount, e.ViewCount, e.PatchCount
			if !e.LastUsedAt.IsZero() {
				at := e.LastUsedAt
				v.LastUsedAt = &at
			}
		}
		views = append(views, v)
	}
	writeJSON(w, http.StatusOK, map[string]any{"skills": views})
}

// handleSkill answers ONE document, body included.
func (s *Server) handleSkill(w http.ResponseWriter, r *http.Request) {
	if s.opts.Skills == nil {
		writeError(w, http.StatusNotImplemented, noLibrary)
		return
	}
	sk, err := s.opts.Skills.Skill(r.PathValue("name"))
	if err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, skills.ErrNotFound) {
			status = http.StatusNotFound
		}
		writeError(w, status, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"name": sk.Name, "title": sk.Title, "path": sk.Path, "body": sk.Body,
	})
}

// handleSaveSkill writes a document, creating or replacing it.
func (s *Server) handleSaveSkill(w http.ResponseWriter, r *http.Request) {
	if s.opts.Skills == nil {
		writeError(w, http.StatusNotImplemented, noLibrary)
		return
	}
	var body struct {
		Name string `json:"name"`
		Body string `json:"body"`
	}
	if !s.decodeBody(w, r, &body) {
		return
	}
	if strings.TrimSpace(body.Name) == "" {
		writeError(w, http.StatusBadRequest, "the name cannot be empty")
		return
	}
	if strings.TrimSpace(body.Body) == "" {
		writeError(w, http.StatusBadRequest, "the body cannot be empty")
		return
	}
	saved, err := s.opts.Skills.SaveSkill(body.Name, body.Body)
	if err != nil {
		// 400 and not 500: the library refuses a name it cannot use and a body past the
		// cap, and both are things the CLIENT sent.
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"name": saved.Name, "title": saved.Title, "path": saved.Path,
	})
}

// handlePinSkill exempts a skill from every automatic transition, or stops.
func (s *Server) handlePinSkill(w http.ResponseWriter, r *http.Request) {
	if s.opts.Skills == nil {
		writeError(w, http.StatusNotImplemented, noLibrary)
		return
	}
	var body struct {
		Pinned bool `json:"pinned"`
	}
	if !s.decodeBody(w, r, &body) {
		return
	}
	if err := s.opts.Skills.SetSkillPinned(r.PathValue("name"), body.Pinned); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleDisableSkill turns a skill off or back on.
func (s *Server) handleDisableSkill(w http.ResponseWriter, r *http.Request) {
	if s.opts.Skills == nil {
		writeError(w, http.StatusNotImplemented, noLibrary)
		return
	}
	var body struct {
		Disabled bool `json:"disabled"`
	}
	if !s.decodeBody(w, r, &body) {
		return
	}
	// 400 and not 500: the documented failure is a gateway without a usage ledger, which
	// is the environment the client is talking to rather than a broken server, and the
	// message says which of the two it was.
	if err := s.opts.Skills.SetSkillDisabled(r.PathValue("name"), body.Disabled); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleDeleteSkill removes a document for good.
//
// The confirmation is the FRONT END's job: this answers 204 for a request that was made,
// and asking a question the server cannot see the answer to would be theatre.
func (s *Server) handleDeleteSkill(w http.ResponseWriter, r *http.Request) {
	if s.opts.Skills == nil {
		writeError(w, http.StatusNotImplemented, noLibrary)
		return
	}
	err := s.opts.Skills.DeleteSkill(r.PathValue("name"))
	if err == nil {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	// The two refusals are different facts and get different codes. 404 for a name that is
	// not there: the client's to fix, and the same answer the read gives. 409 for a shipped
	// procedure: a real skill with a real name, where what failed is the library's STATE.
	if errors.Is(err, skills.ErrNotFound) {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	writeError(w, http.StatusConflict, err.Error())
}

// handleRestoreSkill brings an archived document back.
func (s *Server) handleRestoreSkill(w http.ResponseWriter, r *http.Request) {
	if s.opts.Skills == nil {
		writeError(w, http.StatusNotImplemented, noLibrary)
		return
	}
	// 404 and not 500: the failing case is a name that is not in the archive, which is
	// the client's to fix.
	if err := s.opts.Skills.RestoreSkill(r.PathValue("name")); err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleArchivedSkills answers what the curator moved aside.
//
// An EMPTY list and never null, the same rule handleMessages follows: a client that has
// to tell "nothing was archived" from "the field is missing" is a client with a bug
// waiting to happen.
func (s *Server) handleArchivedSkills(w http.ResponseWriter, _ *http.Request) {
	if s.opts.Skills == nil {
		writeError(w, http.StatusNotImplemented, noLibrary)
		return
	}
	names, err := s.opts.Skills.ArchivedSkills()
	if err != nil {
		// The same distinction as the index: an unreadable archive is not an empty one.
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if names == nil {
		names = []string{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"skills": names})
}

// handleCurator answers the maintenance report.
func (s *Server) handleCurator(w http.ResponseWriter, _ *http.Request) {
	if s.opts.Curator == nil {
		writeError(w, http.StatusNotImplemented, noCurator)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"text": s.opts.Curator.CuratorStatus()})
}

// handleCuratorRun runs one maintenance pass, or previews one.
func (s *Server) handleCuratorRun(w http.ResponseWriter, r *http.Request) {
	if s.opts.Curator == nil {
		writeError(w, http.StatusNotImplemented, noCurator)
		return
	}
	var body struct {
		Consolidate bool `json:"consolidate"`
		DryRun      bool `json:"dry_run"`
	}
	if !s.decodeBody(w, r, &body) {
		return
	}
	text, err := s.opts.Curator.CuratorRun(r.Context(), body.Consolidate, body.DryRun)
	if err != nil {
		// 502 and not 500: the pass failed because the MODEL it consolidated with
		// failed, which is the same reading handleModels uses.
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"text": text})
}
