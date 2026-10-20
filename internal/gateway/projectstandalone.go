package gateway

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"strings"

	"github.com/madkoding/motita/internal/gitx"
)

// standaloneRepo does the migration of one repository and says which HTTP status a refusal maps to.
func (s *Server) standaloneRepo(ctx context.Context, id, name string) (*Project, int, error) {
	defer s.lockProjectRepos(id)()
	p := s.projectOf(id)
	if p == nil {
		return nil, http.StatusNotFound, ErrProjectNotFound
	}
	i := p.repoIndex(name)
	if i < 0 || !p.Repos[i].Shared || p.Repos[i].Local {
		return nil, http.StatusNotFound, errors.New("the project has no shared repository with that name")
	}
	repo := p.Repos[i]
	if strings.TrimSpace(repo.GitURL) == "" {
		return nil, http.StatusConflict, errors.New("the repository has no URL recorded for its origin to point at")
	}
	defer s.lockCatalogRepo(repo.GitURL)()
	dest := p.repoPath(repo)
	if len(s.sessionWorktreesOf(ctx, p, repo)) > 0 {
		return nil, http.StatusConflict, errors.New("sessions with a worktree of their own still use this " +
			"repository: close them first")
	}
	if dir, dirty := firstDirtyDir(ctx, []string{dest}); dirty {
		return nil, http.StatusConflict, fmt.Errorf("%s has uncommitted changes (or they could not be read): "+
			"commit them first", dir)
	}
	if err := s.makeStandalone(ctx, repo, dest); err != nil {
		return nil, http.StatusConflict, err
	}
	updated, err := s.updateProject(p.ID, func(cur *Project) {
		if j := cur.repoIndex(repo.Name); j >= 0 {
			cur.Repos[j].Shared = false
			cur.Repos[j].Dir = dest
			cur.Repos[j].ProjectBranch = ""
		}
	})
	if err != nil {
		return nil, http.StatusInternalServerError, err
	}
	for _, c := range s.sessionsOfProject(updated.ID) {
		applyRuntimeTo(c.svc, updated, c.workspaceDir())
	}
	return updated, http.StatusOK, nil
}

// migrateSharedRepos runs on start: every project folder that is still a worktree of the catalog
// becomes a clone of its own, so no project depends on the catalog any more. What cannot move yet
// (uncommitted work, sessions with a worktree of their own, no URL) stays shared and is tried again
// on the next start; the person can also do it by hand from the project's settings.
func (s *Server) migrateSharedRepos(ctx context.Context) {
	for _, p := range s.allProjects() {
		for _, repo := range p.Repos {
			if !repo.Shared || repo.Local {
				continue
			}
			if _, _, err := s.standaloneRepo(ctx, p.ID, repo.Name); err != nil {
				s.warn("shared repository kept as it is", "project", p.ID, "repo", repo.Name, "reason", err.Error())
			}
		}
	}
}

// makeStandalone replaces dest, a clean worktree of the catalog clone repo.Dir, with a clone of its
// own on the same branch. The clone is made next to it and checked before the worktree goes, and a
// worktree that cannot be swapped for it is put back.
func (s *Server) makeStandalone(ctx context.Context, repo ProjectRepo, dest string) error {
	branch := gitx.Display(ctx, dest)
	if branch == "" || strings.HasPrefix(branch, "detached at ") {
		return fmt.Errorf("%s is not on a branch, so there is nothing to keep it on", dest)
	}
	env := s.gitCommandEnv()
	tmp := dest + ".standalone"
	if _, err := cloneGitRepo(repo.Dir, tmp, env); err != nil {
		return err
	}
	fail := func(err error) error {
		_ = os.RemoveAll(tmp)
		return err
	}
	for _, args := range [][]string{
		{"checkout", "-q", "-B", branch, "origin/" + branch},
		{"remote", "set-url", "origin", repo.GitURL},
	} {
		gitArgs := append(append([]string{"-C", tmp}, gitx.HardeningFor(ctx, tmp)...), args...)
		cmd := exec.CommandContext(ctx, "git", gitArgs...)
		cmd.Env = env
		if out, err := cmd.CombinedOutput(); err != nil {
			return fail(fmt.Errorf("could not prepare the clone: %s", strings.TrimSpace(string(out))))
		}
	}
	if err := worktreeRemove(ctx, repo.Dir, dest, false); err != nil {
		return fail(fmt.Errorf("the project's worktree could not be removed: %v", err))
	}
	if err := renamePath(tmp, dest); err != nil {
		_ = gitx.AddWorktree(ctx, repo.Dir, dest, branch)
		return fail(fmt.Errorf("the clone could not take the worktree's place: %v", err))
	}
	return nil
}
