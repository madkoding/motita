package gateway

import (
	"errors"
	"net/http"

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
