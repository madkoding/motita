package gateway

import (
	"cmp"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"github.com/madkoding/motita/internal/gitx"
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

// beingCloned reports whether a clone is being made in dir.
func (s *Server) beingCloned(dir string) bool {
	s.cloningMu.Lock()
	defer s.cloningMu.Unlock()
	return s.cloning[dir]
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

// catalogUse is a project that uses a repository of the catalog.
type catalogUse struct {
	ID    string `json:"id"`
	Title string `json:"title"`
}

// catalogRepo is one repository of the catalog, and the projects that use it.
type catalogRepo struct {
	Name     string       `json:"name"`
	GitURL   string       `json:"git_url"`
	Dir      string       `json:"dir"`
	Projects []catalogUse `json:"projects"`
	// Size is what the clone takes on disk, in bytes.
	Size int64 `json:"size"`
}

// dirSize adds up the size of the files under dir. What cannot be read counts as nothing: the
// figure is a guide to what deleting a clone frees, not an inventory.
func dirSize(dir string) int64 {
	var n int64
	_ = filepath.WalkDir(dir, func(_ string, d fs.DirEntry, err error) error {
		if err == nil && d.Type().IsRegular() {
			if info, ierr := d.Info(); ierr == nil {
				n += info.Size()
			}
		}
		return nil
	})
	return n
}

// allProjects is every project the gateway knows, none when it has no store or cannot read it.
func (s *Server) allProjects() []Project {
	if s.projects == nil {
		return nil
	}
	all, _ := s.projects.loadAll()
	return all
}

// catalogUsers are the projects whose repositories include the catalog's clone at dir.
func catalogUsers(projects []Project, dir string) []catalogUse {
	users := []catalogUse{}
	for _, p := range projects {
		if slices.ContainsFunc(p.Repos, func(r ProjectRepo) bool { return r.Shared && gitx.SamePath(r.Dir, dir) }) {
			users = append(users, catalogUse{ID: p.ID, Title: p.Title})
		}
	}
	return users
}

// handleListRepos answers the catalog: every repository cloned there, and which projects use it,
// so a project can add one that is already here without cloning it again.
func (s *Server) handleListRepos(w http.ResponseWriter, r *http.Request) {
	out := []catalogRepo{}
	root := s.catalogDir()
	entries, _ := os.ReadDir(root)
	var projects []Project
	if len(entries) > 0 {
		projects = s.allProjects()
	}
	for _, e := range entries {
		dir := filepath.Join(root, e.Name())
		gitURL := originOf(dir, s.gitCommandEnv())
		if !e.IsDir() || gitURL == "" {
			continue
		}
		out = append(out, catalogRepo{
			Name: e.Name(), GitURL: gitURL, Dir: dir, Projects: catalogUsers(projects, dir), Size: dirSize(dir),
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"repos": out, "repositories": s.repositories(r)})
}

// removeAll is how a clone leaves the catalog; a variable so a test can make it fail.
var removeAll = os.RemoveAll

// handleDeleteRepo deletes a clone of the catalog that no project uses any more. Uncommitted work
// in it stops the deletion, unless the user confirmed the discard with `?force=1`.
func (s *Server) handleDeleteRepo(w http.ResponseWriter, r *http.Request) {
	if rec, _ := s.repoRecord(r.PathValue("name")); rec != nil {
		s.deleteRepository(w, r, rec)
		return
	}
	dir := s.catalogRepoDir(r.PathValue("name"))
	if dir == "" {
		writeError(w, http.StatusNotFound, "the workspace has no repository with that name")
		return
	}
	if s.beingCloned(dir) {
		writeError(w, http.StatusConflict, "the repository is being cloned: try again when it is done")
		return
	}
	// Held against the projects that are adding or removing this same repository.
	defer s.lockCatalogRepo(originOf(dir, s.gitCommandEnv()))()
	if users := catalogUsers(s.allProjects(), dir); len(users) > 0 {
		titles := make([]string, len(users))
		for i, u := range users {
			titles[i] = u.Title
		}
		writeError(w, http.StatusConflict, fmt.Sprintf("the repository is used by %s: remove it from "+
			"those projects first", strings.Join(titles, ", ")))
		return
	}
	if _, dirty := firstDirtyDir(r.Context(), []string{dir}); dirty && !isForced(r) {
		writeError(w, http.StatusConflict, "the repository has uncommitted changes (or they could not be read): "+
			"commit them, or confirm the discard")
		return
	}
	if !isForced(r) && s.refuseUnpublished(w, r.Context(), dir) {
		return
	}
	if err := removeAll(dir); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.forgetRepositoriesAt(dir)
	w.WriteHeader(http.StatusNoContent)
}

// catalogRepoDir is the folder of the catalog's clone called name, "" when there is none.
func (s *Server) catalogRepoDir(name string) string {
	root := s.catalogDir()
	dir := filepath.Join(root, name)
	if root == "" || repoNameOf(name, "") != name || !pathExists(dir) {
		return ""
	}
	return dir
}

// handleUpdateRepo brings the catalog's clone called name up to date with its origin, on the
// branch it has out, when it can move forward without a merge.
func (s *Server) handleUpdateRepo(w http.ResponseWriter, r *http.Request) {
	dir := s.catalogRepoDir(r.PathValue("name"))
	if dir == "" {
		writeError(w, http.StatusNotFound, "the workspace has no repository with that name")
		return
	}
	ctx := r.Context()
	if err := gitx.PullFastForward(ctx, dir, gitx.Display(ctx, dir)); err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
