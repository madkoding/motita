package gateway

import (
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
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
