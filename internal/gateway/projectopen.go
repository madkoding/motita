package gateway

import (
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
