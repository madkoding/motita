package gateway

import (
	"strings"
	"sync"
)

// The workspace catalog was the folder of repositories cloned once and shared by the projects that
// used them. It is retired: nothing clones into it any more, and every project folder that is still
// a worktree of one of its clones becomes a clone of its own on start (migrateSharedRepos). What is
// left here is what those leftovers still need until they move.

// catalogFolder is the workspace folder that held the repositories projects shared. It stays a
// reserved folder name: the clones of projects not migrated yet are still in it.
const catalogFolder = "repos"

// lockCatalogRepo holds the catalog's clone of gitURL against anything else that uses or deletes it,
// whatever way the URL is spelled (a trailing ".git" or slash, the host's case).
func (s *Server) lockCatalogRepo(gitURL string) func() {
	key := strings.ToLower(strings.TrimSuffix(strings.TrimSuffix(strings.TrimSpace(gitURL), "/"), ".git"))
	m, _ := s.catalogLocks.LoadOrStore(key, &sync.Mutex{})
	mu := m.(*sync.Mutex)
	mu.Lock()
	return mu.Unlock
}

// allProjects is every project the gateway knows, none when it has no store or cannot read it.
func (s *Server) allProjects() []Project {
	if s.projects == nil {
		return nil
	}
	all, _ := s.projects.loadAll()
	return all
}
