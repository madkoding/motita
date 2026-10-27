package gateway

import (
	"net/http"
	"strings"
)

// openProjectStore opens the project store when a directory is configured.
func (s *Server) openProjectStore() {
	// The git that gitx runs for the projects (fetch, pull, worktrees, the remote watch) gets the
	// credential helper and no-prompt environment of the clones, or it ignores the stored login.
	s.installGitEnv()
	pdir := strings.TrimSpace(s.opts.ProjectDir)
	if pdir == "" {
		return
	}
	ps, err := newProjectStore(pdir)
	if err != nil {
		s.warn("could not open the project store; projects will not be available", "error", err.Error())
		return
	}
	s.projects = ps
}

// projectRoutes is the table of projects and their repositories; the notes have their own.
func (s *Server) projectRoutes(mux *http.ServeMux, plain func(http.HandlerFunc) http.Handler) {
	mux.Handle("GET /v1/projects", plain(s.handleListProjects))
	s.notesRoutes(mux, plain)
	s.nestedRoutes(mux, plain)
	mux.Handle("GET /v1/projects/{id}/code-index", plain(s.handleProjectCodeIndex))
	mux.Handle("POST /v1/projects/{id}/code-index", plain(s.handleBuildProjectCodeIndex))
	mux.Handle("POST /v1/projects", plain(s.handleCreateProject))
	mux.Handle("PATCH /v1/projects/{id}", plain(s.handleUpdateProject))
	mux.Handle("GET /v1/projects/{id}/branches", plain(s.handleProjectBranches))
	mux.Handle("DELETE /v1/projects/{id}", plain(s.handleDeleteProject))
	mux.Handle("POST /v1/projects/{id}/main/refresh", plain(s.handleRefreshProjectMain))
}
