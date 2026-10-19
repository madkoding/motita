package gateway

import (
	"cmp"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/madkoding/motita/internal/gitx"
)

// migrateRepositories gives every git repository the gateway already uses a Repository record:
// the project folders that are repositories, the other repositories of each project, and the clones
// of the catalog that no project holds any more. It only adds records and links to them: no
// folder, branch or setting of a project is touched or deleted, and a second run finds everything
// recorded and does nothing.
func (s *Server) migrateRepositories() {
	if s.repos == nil || s.projects == nil {
		return
	}
	known, err := s.repos.loadAll()
	if err != nil {
		s.warn("could not read the repositories; they are not migrated", "error", err.Error())
		return
	}
	for _, p := range s.allProjects() {
		known = s.migrateProject(p, known)
	}
	s.migrateCatalog(known)
}

// repoAt is the record whose clone is dir, or nil.
func repoAt(known []Repository, dir string) *Repository {
	i := slices.IndexFunc(known, func(r Repository) bool { return gitx.SamePath(r.Dir, dir) })
	if i < 0 {
		return nil
	}
	return &known[i]
}

// record answers the id of the record for dir, making it from fresh when there is none.
func (s *Server) record(known []Repository, fresh Repository) ([]Repository, string) {
	if r := repoAt(known, fresh.Dir); r != nil {
		return known, r.ID
	}
	id, err := newSessionID()
	if err != nil {
		return known, ""
	}
	fresh.ID = id
	fresh.Created = cmp.Or(fresh.Created, time.Now())
	if err := s.repos.save(fresh); err != nil {
		s.warn("could not record a repository", "dir", fresh.Dir, "error", err.Error())
		return known, ""
	}
	return append(known, fresh), id
}

// migrateProject records the repositories of one project and links the project to them.
func (s *Server) migrateProject(p Project, known []Repository) []Repository {
	ids := map[string]string{}
	mainID := p.RepoID
	if mainID == "" && (p.GitURL != "" || pathExists(filepath.Join(p.Dir, ".git"))) {
		known, mainID = s.record(known, Repository{
			Name:         cmp.Or(repoNameOf("", p.GitURL), repoNameOf(filepath.Base(p.Dir), "")),
			GitURL:       p.GitURL,
			Dir:          p.Dir,
			MainBranch:   p.MainBranch,
			MergeMethod:  p.MergeMethod,
			PRMaxFixes:   p.PRMaxFixes,
			AutoMerge:    p.AutoMerge,
			AutoContinue: p.AutoContinue,
			Created:      p.Created,
		})
	}
	for _, repo := range p.Repos {
		if repo.RepoID != "" {
			continue
		}
		var id string
		known, id = s.record(known, Repository{
			Name: repo.Name, GitURL: repo.GitURL, Dir: repo.Dir, Local: repo.Local, MainBranch: repo.MainBranch,
		})
		ids[repo.Name] = id
	}
	if (mainID == "" || mainID == p.RepoID) && len(ids) == 0 {
		return known
	}
	_, err := s.projects.update(p.ID, func(cur *Project) {
		if cur.RepoID == "" {
			cur.RepoID = mainID
		}
		for i := range cur.Repos {
			if cur.Repos[i].RepoID == "" {
				cur.Repos[i].RepoID = ids[cur.Repos[i].Name]
			}
		}
	})
	if err != nil {
		s.warn("could not link a project to its repositories", "project", p.ID, "error", err.Error())
	}
	return known
}

// migrateCatalog records the clones in the workspace's catalog that no project links to.
func (s *Server) migrateCatalog(known []Repository) {
	root := s.catalogDir()
	if root == "" {
		return
	}
	entries, _ := os.ReadDir(root)
	env := s.gitCommandEnv()
	for _, e := range entries {
		dir := filepath.Join(root, e.Name())
		url := originOf(dir, env)
		if !e.IsDir() || url == "" || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		known, _ = s.record(known, Repository{Name: e.Name(), GitURL: url, Dir: dir})
	}
}
