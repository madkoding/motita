package gateway

import (
	"cmp"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/madkoding/motita/internal/gitx"
)

// The repositories category: one record per git repository, with its own settings.

// repositoryView is a repository as the API answers it: the record plus what is read live.
type repositoryView struct {
	Repository
	Branch   string       `json:"branch,omitempty"`
	Changes  int          `json:"changes,omitempty"`
	Projects []catalogUse `json:"projects"`
}

// repositoryUsers are the projects that link to the repository: by its record, or by its folder
// for a project whose links were made before the record existed.
func repositoryUsers(projects []Project, repo Repository) []catalogUse {
	users := []catalogUse{}
	for _, p := range projects {
		used := p.RepoID == repo.ID || gitx.SamePath(p.Dir, repo.Dir)
		for _, r := range p.Repos {
			used = used || r.RepoID == repo.ID || gitx.SamePath(r.Dir, repo.Dir)
		}
		if used {
			users = append(users, catalogUse{ID: p.ID, Title: p.Title})
		}
	}
	return users
}

// repositories lists every repository record with its live state, none without a store.
func (s *Server) repositories(r *http.Request) []repositoryView {
	out := []repositoryView{}
	if s.repos == nil {
		return out
	}
	s.migrateRepositories()
	all, _ := s.repos.loadAll()
	projects := s.allProjects()
	for _, repo := range all {
		v := repositoryView{Repository: repo, Projects: repositoryUsers(projects, repo)}
		v.Branch = gitx.Display(r.Context(), repo.Dir)
		v.Changes, _ = gitx.WorkingTreeChanges(r.Context(), repo.Dir)
		out = append(out, v)
	}
	return out
}

// repoUpdate is the body of handleUpdateRepository: the pull-request settings and main branch of
// the repository, every field optional. They are the fields of projectUpdate, so a repository and
// a project are validated alike.
type repoUpdate struct {
	MainBranch   *string `json:"main_branch"`
	MergeMethod  *string `json:"merge_method"`
	PRMaxFixes   *int    `json:"pr_max_fixes"`
	AutoMerge    *bool   `json:"auto_merge"`
	AutoContinue *bool   `json:"auto_continue"`
}

func (u repoUpdate) asProject() projectUpdate {
	return projectUpdate{
		MainBranch: u.MainBranch, MergeMethod: u.MergeMethod, PRMaxFixes: u.PRMaxFixes,
		AutoMerge: u.AutoMerge, AutoContinue: u.AutoContinue,
	}
}

// handleUpdateRepository changes the settings of one repository. Until projects read them from the
// repository, the projects that have it as their own folder are given the same values.
func (s *Server) handleUpdateRepository(w http.ResponseWriter, r *http.Request) {
	if s.repos == nil {
		writeError(w, http.StatusNotImplemented, "this gateway was started without a project directory")
		return
	}
	repo, err := s.repos.load(r.PathValue("id"))
	if err != nil || repo == nil {
		writeError(w, http.StatusNotFound, "there is no repository with that id")
		return
	}
	var body repoUpdate
	if !s.decodeBody(w, r, &body) {
		return
	}
	u := body.asProject()
	// Validated on a stand-in that has the repository's folder, with the rules of a project.
	probe := Project{Dir: repo.Dir}
	if !u.applyPR(w, &probe) || !u.applyMainBranch(w, r, &probe) {
		return
	}
	saved, err := s.repos.update(repo.ID, func(cur *Repository) {
		if u.MainBranch != nil {
			cur.MainBranch = probe.MainBranch
		}
		if u.MergeMethod != nil {
			cur.MergeMethod = probe.MergeMethod
		}
		if u.PRMaxFixes != nil {
			cur.PRMaxFixes = probe.PRMaxFixes
		}
		if u.AutoMerge != nil {
			cur.AutoMerge = probe.AutoMerge
		}
		if u.AutoContinue != nil {
			cur.AutoContinue = probe.AutoContinue
		}
	})
	if errors.Is(err, errRepoGone) {
		writeError(w, http.StatusNotFound, "there is no repository with that id")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	for _, p := range s.allProjects() {
		if p.RepoID == saved.ID {
			_, _ = s.updateProject(p.ID, func(cur *Project) { u.copyTo(cur, &probe) })
		}
	}
	writeJSON(w, http.StatusOK, saved)
}

// mirrorToRepository gives the repository that is the project's own folder the project's
// pull-request settings, the other direction of handleUpdateRepository.
func (s *Server) mirrorToRepository(p *Project) {
	if s.repos == nil || p.RepoID == "" {
		return
	}
	_, _ = s.repos.update(p.RepoID, func(cur *Repository) {
		cur.MainBranch, cur.MergeMethod, cur.PRMaxFixes = p.MainBranch, p.MergeMethod, p.PRMaxFixes
		cur.AutoMerge, cur.AutoContinue = p.AutoMerge, p.AutoContinue
	})
}

// linkRepository adds the recorded repository id to the project p: the project's folder gets a
// worktree of the repository's one clone on a branch of the project's own, and the repository
// stays where it is for every other project that uses it.
func (s *Server) linkRepository(w http.ResponseWriter, r *http.Request, p *Project, id, rawName string) {
	repo, err := s.repoRecord(id)
	if err != nil || repo == nil {
		writeError(w, http.StatusNotFound, "there is no repository with that id")
		return
	}
	name := repoNameOf(rawName, repo.Dir)
	dest := filepath.Join(p.Dir, name)
	switch {
	case name == "":
		writeError(w, http.StatusBadRequest, "the repository needs a simple folder name: letters, digits, '.', '_' or '-'")
		return
	case p.repoIndex(name) >= 0 || p.RepoID == repo.ID || gitx.SamePath(p.Dir, repo.Dir):
		writeError(w, http.StatusConflict, "the project already has that repository")
		return
	case pathExists(dest):
		writeError(w, http.StatusConflict, fmt.Sprintf("the project already has a folder called %q: "+
			"choose another name for the repository", name))
		return
	case !gitx.HasCommits(r.Context(), repo.Dir):
		writeError(w, http.StatusConflict, "the repository has no commit yet to branch the project from")
		return
	}
	defer s.lockCatalogRepo(repo.Dir)()
	placed, err := placeSharedRepo(r.Context(), p, name, repo.GitURL, repo.Dir, dest)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	placed.RepoID, placed.Local = repo.ID, repo.Local
	s.saveNewRepo(w, r, p, placed)
}

// repoRecord is the repository with that id, nil when there is none or no store.
func (s *Server) repoRecord(id string) (*Repository, error) {
	if s.repos == nil {
		return nil, nil
	}
	return s.repos.load(id)
}

// handleCreateRepository records one more repository: cloned from git_url into the workspace's
// catalog, or the folder at path that the machine already has (with the whole disk allowed). A
// repository that is already recorded answers 409.
func (s *Server) handleCreateRepository(w http.ResponseWriter, r *http.Request) {
	if s.repos == nil {
		writeError(w, http.StatusNotImplemented, "this gateway was started without a project directory")
		return
	}
	var body struct {
		GitURL string `json:"git_url"`
		Name   string `json:"name"`
		Path   string `json:"path"`
	}
	if !s.decodeBody(w, r, &body) {
		return
	}
	fresh, ok := s.newRepository(w, r, strings.TrimSpace(body.GitURL), strings.TrimSpace(body.Path), body.Name)
	if !ok {
		return
	}
	known, _ := s.repos.loadAll()
	if repoAt(known, fresh.Dir) != nil {
		writeError(w, http.StatusConflict, "that repository is already recorded")
		return
	}
	known, id := s.record(known, fresh)
	rec, err := s.repos.load(id)
	if err != nil || rec == nil {
		writeError(w, http.StatusInternalServerError, "could not record the repository")
		return
	}
	writeJSON(w, http.StatusCreated, rec)
}

// newRepository prepares the record of the repository asked for, cloning it when it has to. It
// answers the request itself, and returns false, when it cannot.
func (s *Server) newRepository(w http.ResponseWriter, r *http.Request, gitURL, path, rawName string) (Repository, bool) {
	if path != "" {
		dir, ok := s.externalProjectFolder(w, path)
		if !ok {
			return Repository{}, false
		}
		if gitx.Repo(r.Context(), dir) != nil {
			writeError(w, http.StatusBadRequest, "that folder is not a git repository")
			return Repository{}, false
		}
		return Repository{
			Name: cmp.Or(repoNameOf(rawName, dir), repoNameOf("", filepath.Base(dir))), GitURL: originOf(dir, s.gitCommandEnv()),
			Dir: dir, Local: true, MainBranch: localMainBranch(r.Context(), dir),
		}, true
	}
	name := repoNameOf(rawName, gitURL)
	switch {
	case gitURL == "":
		writeError(w, http.StatusBadRequest, "a repository needs a URL or a folder")
		return Repository{}, false
	case name == "":
		writeError(w, http.StatusBadRequest, "the repository needs a simple folder name: letters, digits, '.', '_' or '-'")
		return Repository{}, false
	}
	defer s.lockCatalogRepo(gitURL)()
	dir, _, err := s.catalogClone(gitURL, name)
	if err != nil {
		s.writeCloneError(w, gitURL, err)
		return Repository{}, false
	}
	return Repository{Name: name, GitURL: gitURL, Dir: dir, MainBranch: settleMainBranch(r.Context(), dir, defaultMainBranch)}, true
}

// deleteRepository removes the record of a repository that no project uses. The clone goes with it
// only when it is the catalog's and not the user's own, and uncommitted work stops that unless the
// discard was confirmed with `?force=1`; a folder of the user's is left exactly as it is.
func (s *Server) deleteRepository(w http.ResponseWriter, r *http.Request, repo *Repository) {
	if users := repositoryUsers(s.allProjects(), *repo); len(users) > 0 {
		titles := make([]string, len(users))
		for i, u := range users {
			titles[i] = u.Title
		}
		writeError(w, http.StatusConflict, fmt.Sprintf("the repository is used by %s: remove it from "+
			"those projects first", strings.Join(titles, ", ")))
		return
	}
	root := s.catalogDir()
	if !repo.Local && root != "" && pathWithin(root, repo.Dir) {
		if _, dirty := firstDirtyDir(r.Context(), []string{repo.Dir}); dirty && !isForced(r) {
			writeError(w, http.StatusConflict, "the repository has uncommitted changes (or they could not be read): "+
				"commit them, or confirm the discard")
			return
		}
		if !isForced(r) && s.refuseUnpublished(w, r.Context(), repo.Dir) {
			return
		}
		if err := removeAll(repo.Dir); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
	}
	if err := s.repos.delete(repo.ID); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// forgetRepositoriesAt drops the records of the clone at dir, which is gone.
func (s *Server) forgetRepositoriesAt(dir string) {
	if s.repos == nil {
		return
	}
	all, _ := s.repos.loadAll()
	for _, rec := range all {
		if gitx.SamePath(rec.Dir, dir) {
			_ = s.repos.delete(rec.ID)
		}
	}
}
