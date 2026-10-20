package gateway

import (
	"cmp"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// The workspace catalog: the repositories cloned once and shared by the projects that use them.

// errNoCatalog is catalogClone's answer when the workspace has no catalog to clone into.
var errNoCatalog = errors.New("the workspace has no catalog of repositories")

// catalogFolder is the workspace folder that holds the repositories projects share.
const catalogFolder = "repos"

// catalogDir is the catalog's folder, "" when there is none: no workspace, or a project of its
// own already in that folder.
func (s *Server) catalogDir() string {
	if s.opts.WorkspaceDir == "" {
		return ""
	}
	dir := filepath.Join(s.opts.WorkspaceDir, catalogFolder)
	if s.projectInDir(dir, "") != nil {
		return ""
	}
	return dir
}

// catalogClone answers the catalog's clone of gitURL, cloning it the first time a project asks
// for it, and whether it was cloned now. It is found by its origin, whatever its folder is called.
func (s *Server) catalogClone(gitURL, name string) (dir string, fresh bool, err error) {
	root := s.catalogDir()
	if root == "" {
		return "", false, errNoCatalog
	}
	env := s.gitCommandEnv()
	entries, _ := os.ReadDir(root)
	for _, e := range entries {
		if d := filepath.Join(root, e.Name()); e.IsDir() && sameOrigin(d, gitURL, env) {
			return d, false, nil
		}
	}
	dir = s.reserveCloneDir(filepath.Join(root, cmp.Or(repoNameOf("", gitURL), name)), gitURL, env)
	defer s.releaseCloneDir(dir)
	if _, err := cloneGitRepo(gitURL, dir, env); err != nil {
		return "", false, err
	}
	return dir, true, nil
}

// reserveCloneDir picks the folder for a clone of gitURL and marks it as being cloned into, in one
// step: two clones of different repositories with the same name get different folders.
func (s *Server) reserveCloneDir(base, gitURL string, env []string) string {
	s.cloningMu.Lock()
	defer s.cloningMu.Unlock()
	dir := freeCloneDir(base, gitURL, env, func(d string) bool { return s.cloning[d] })
	if s.cloning == nil {
		s.cloning = map[string]bool{}
	}
	s.cloning[dir] = true
	return dir
}

// releaseCloneDir says no clone is being made in dir any more.
func (s *Server) releaseCloneDir(dir string) {
	s.cloningMu.Lock()
	defer s.cloningMu.Unlock()
	delete(s.cloning, dir)
}

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
