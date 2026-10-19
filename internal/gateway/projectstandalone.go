package gateway

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"strings"

	"github.com/madkoding/motita/internal/gitx"
)

// handleStandaloneProjectRepo is the migration from a shared repository to a plain clone: the
// project's folder of it stops being a worktree of the catalog's clone and becomes a clone of its
// own, on the same branch and with the same commits, with origin pointing at the repository's URL.
// Nothing is deleted: the catalog's clone keeps every branch (the project's included) for the other
// projects that use it. A folder with uncommitted work, or one that isolated sessions still have a
// worktree of, is left as it is until that is dealt with.
func (s *Server) handleStandaloneProjectRepo(w http.ResponseWriter, r *http.Request) {
	defer s.lockProjectRepos(r.PathValue("id"))()
	p := s.projectOf(r.PathValue("id"))
	if p == nil {
		writeError(w, http.StatusNotFound, ErrProjectNotFound.Error())
		return
	}
	i := p.repoIndex(r.PathValue("name"))
	if i < 0 || !p.Repos[i].Shared || p.Repos[i].Local {
		writeError(w, http.StatusNotFound, "the project has no shared repository with that name")
		return
	}
	repo := p.Repos[i]
	if strings.TrimSpace(repo.GitURL) == "" {
		writeError(w, http.StatusConflict, "the repository has no URL recorded for its origin to point at")
		return
	}
	defer s.lockCatalogRepo(repo.GitURL)()
	ctx := r.Context()
	dest := p.repoPath(repo)
	if len(s.sessionWorktreesOf(ctx, p, repo)) > 0 {
		writeError(w, http.StatusConflict, "sessions with a worktree of their own still use this repository: "+
			"close them first")
		return
	}
	if dir, dirty := firstDirtyDir(ctx, []string{dest}); dirty {
		writeError(w, http.StatusConflict, fmt.Sprintf("%s has uncommitted changes (or they could not be read): "+
			"commit them first", dir))
		return
	}
	if err := s.makeStandalone(ctx, repo, dest); err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	updated, err := s.updateProject(p.ID, func(cur *Project) {
		if j := cur.repoIndex(repo.Name); j >= 0 {
			cur.Repos[j].Shared = false
			cur.Repos[j].Dir = dest
			cur.Repos[j].ProjectBranch = ""
		}
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	for _, c := range s.sessionsOfProject(updated.ID) {
		applyRuntimeTo(c.svc, updated, c.workspaceDir())
	}
	writeJSON(w, http.StatusOK, updated)
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
