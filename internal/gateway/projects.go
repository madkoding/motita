package gateway

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/madkoding/motita/internal/gitx"
)

// handleListProjects answers every project this gateway knows about.
func (s *Server) handleListProjects(w http.ResponseWriter, _ *http.Request) {
	if s.projects == nil {
		writeJSON(w, http.StatusOK, map[string]any{"projects": []Project{}})
		return
	}
	all, err := s.projects.loadAll()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if all == nil {
		all = []Project{}
	}
	// The branch is read live: it changes when the user checks out another
	// one, and a value persisted at creation time would be a value that used
	// to be true. Reading it here is one git call per project, and a project
	// that is not a repository answers "" — which omitempty renders as absent.
	for i := range all {
		all[i].Branch = gitx.Display(context.Background(), all[i].Dir)
	}
	writeJSON(w, http.StatusOK, map[string]any{"projects": all})
}

// handleCreateProject mints a new project, optionally cloning a git repo.
//
// The request body carries:
//   - title: required, the human-readable name
//   - description: optional
//   - dir: a subfolder name under the workspace (relative, no path separators)
//   - git_url: optional; when set, the repo is cloned into dir
//
// When git_url is set, dir is used as the destination folder name and the
// clone happens synchronously. When git_url is empty, the folder is created
// under the workspace if it does not exist.
func (s *Server) handleCreateProject(w http.ResponseWriter, r *http.Request) {
	if s.projects == nil {
		writeError(w, http.StatusNotImplemented, "this gateway was started without a project directory")
		return
	}
	var body struct {
		Title       string `json:"title"`
		Description string `json:"description"`
		Dir         string `json:"dir"`
		GitURL      string `json:"git_url"`
	}
	if !s.decodeBody(w, r, &body) {
		return
	}
	title := strings.TrimSpace(body.Title)
	if title == "" {
		writeError(w, http.StatusBadRequest, "the title cannot be empty")
		return
	}
	dirName := strings.TrimSpace(body.Dir)
	if dirName == "" {
		writeError(w, http.StatusBadRequest, "the folder name cannot be empty")
		return
	}
	// The folder must be a simple name, not a path: a session inside a project
	// works in that folder, and a relative path with separators would let the
	// agent escape the workspace.
	if strings.ContainsAny(dirName, "/\\..") {
		writeError(w, http.StatusBadRequest, "the folder name must be a simple name, not a path")
		return
	}

	// Resolve the absolute directory under the workspace.
	workspace := s.opts.WorkspaceDir
	if workspace == "" {
		writeError(w, http.StatusInternalServerError, "no workspace directory is configured")
		return
	}
	absDir := filepath.Join(workspace, dirName)

	gitURL := strings.TrimSpace(body.GitURL)
	var cloneLog string
	if gitURL != "" {
		// Clone the repo into the directory.
		var err error
		cloneLog, err = cloneGitRepo(gitURL, absDir)
		if err != nil {
			writeError(w, http.StatusBadGateway, err.Error())
			return
		}
	} else {
		// Create the directory if it does not exist.
		if err := os.MkdirAll(absDir, 0o755); err != nil {
			writeError(w, http.StatusInternalServerError, "could not create the project directory: "+err.Error())
			return
		}
	}

	id, err := newSessionID()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	p := Project{
		ID:          id,
		Title:       title,
		Description: strings.TrimSpace(body.Description),
		Dir:         absDir,
		GitURL:      gitURL,
		Created:     time.Now(),
	}
	if err := s.projects.save(p); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"id":          p.ID,
		"title":       p.Title,
		"description": p.Description,
		"dir":         p.Dir,
		"git_url":     p.GitURL,
		"created":     p.Created,
		"clone_log":   cloneLog,
	})
}

// handleDeleteProject removes a project. Sessions that belong to it are NOT
// deleted: they remain, but their project_id becomes a dangling reference,
// which the front end renders as "no project".
func (s *Server) handleDeleteProject(w http.ResponseWriter, r *http.Request) {
	if s.projects == nil {
		writeError(w, http.StatusNotImplemented, "this gateway was started without a project directory")
		return
	}
	id := r.PathValue("id")
	if err := s.projects.delete(id); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// projectOf returns the project a request is about, or nil when it does not exist.
func (s *Server) projectOf(id string) *Project {
	if s.projects == nil || id == "" {
		return nil
	}
	p, err := s.projects.load(id)
	if err != nil || p == nil {
		return nil
	}
	return p
}

// ErrProjectNotFound is returned when a session is created for a project that
// does not exist. It is a sentinel so the handler can answer 404 rather than 500.
var ErrProjectNotFound = errors.New("there is no project with that id")

// handleMergeSession integrates the session's branch back into the project's
// base branch.
//
// "Volver" is an explicit action, not something that happens on close: a
// session's worktree is where the agent made its changes, and the branch is
// where those changes live as commits. Merging brings them into the checkout
// the user sees, in ONE commit whose message names the session, so the question
// "which session did this" is answered by the history.
//
// A conflict is NOT left behind: the merge is aborted and the error says so.
// The user's checkout is returned to exactly what it was, including any
// uncommitted change — --autostash sees to that, and the behaviour was
// measured before it was relied on.
func (s *Server) handleMergeSession(w http.ResponseWriter, r *http.Request) {
	c := convOf(r)
	if c.workspace == "" {
		writeError(w, http.StatusConflict, "this session does not belong to a project, so there is nothing to merge")
		return
	}
	p := s.projectOf(c.projectID)
	if p == nil {
		writeError(w, http.StatusNotFound, ErrProjectNotFound.Error())
		return
	}
	branch := sessionBranch(c.id)
	res, err := gitx.MergeInto(r.Context(), p.Dir, gitx.Display(r.Context(), p.Dir), branch,
		"motita: integrate session "+c.id)
	if err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"sha": res.SHA, "subject": res.Subject})
}

// sessionBranch is the branch a session works on. The prefix is what makes the
// branches findable: `git branch --list 'motita/*'` lists exactly the sessions.
func sessionBranch(sessionID string) string {
	return "motita/" + sessionID
}
