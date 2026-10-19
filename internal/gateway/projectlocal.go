package gateway

import (
	"context"
	"fmt"
	"net/http"
	"path/filepath"
	"slices"
	"strings"

	"github.com/madkoding/motita/internal/gitx"
)

// Repositories the user already has on the machine, used where they are, and the dependencies
// their projects need.

// isLocalRepo reports whether dir is the folder of one of the project's local repositories.
func (p *Project) isLocalRepo(dir string) bool {
	return slices.ContainsFunc(p.Repos, func(r ProjectRepo) bool { return r.Local && gitx.SamePath(r.Dir, dir) })
}

// addLocalRepo adds a repository the machine already has to a project, without cloning it: the
// project's folder of it is a worktree of the user's own repository on a branch of the project's.
// Nothing the user has there moves - not the branch it has checked out, not its working tree - and
// the repository is not the catalog's, so no other project's clone is involved.
func (s *Server) addLocalRepo(w http.ResponseWriter, r *http.Request, p *Project, rawPath, rawName string) {
	dir, ok := s.externalProjectFolder(w, rawPath)
	if !ok {
		return
	}
	ctx := r.Context()
	name := repoNameOf(rawName, dir)
	dest := filepath.Join(p.Dir, name)
	if status, why := localRepoProblem(ctx, p, dir, name, dest); status != 0 {
		writeError(w, status, why)
		return
	}
	repo, err := placeLocalRepo(ctx, p, name, dir, dest, s.gitCommandEnv())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.saveNewRepo(w, r, p, repo)
}

// localRepoProblem is why the repository at dir cannot be added to p under name at dest, as the
// status to answer and the reason; 0 when it can.
func localRepoProblem(ctx context.Context, p *Project, dir, name, dest string) (int, string) {
	switch {
	case name == "":
		return http.StatusBadRequest, "the repository needs a simple folder name: letters, digits, '.', '_' or '-'"
	case !gitx.IsRepoRoot(ctx, dir):
		return http.StatusBadRequest, "that folder is not the top of a git repository with a working tree"
	case !gitx.HasCommits(ctx, dir):
		return http.StatusBadRequest, "that repository has no commits yet: nothing to branch from"
	case gitx.SamePath(dir, p.Dir) || pathWithin(p.Dir, dir) || pathWithin(dir, p.Dir):
		return http.StatusConflict, "that folder is the project's own, or holds it: choose another repository"
	case slices.ContainsFunc(p.Repos, func(x ProjectRepo) bool { return gitx.SamePath(x.Dir, dir) }):
		return http.StatusConflict, "the project already has that repository"
	case p.repoIndex(name) >= 0:
		return http.StatusConflict, fmt.Sprintf("the project already has a repository called %q", name)
	case pathExists(dest):
		return http.StatusConflict, fmt.Sprintf("the project already has a folder called %q: "+
			"choose another name for the repository", name)
	default:
		return 0, ""
	}
}

// placeLocalRepo adds the project's worktree of the user's repository src at dest, on a branch of
// the project's own. The repository's main branch is read, never switched to: whatever it has
// checked out stays.
func placeLocalRepo(ctx context.Context, p *Project, name, src, dest string, env []string) (ProjectRepo, error) {
	branch := freeProjectBranch(ctx, src, p)
	if err := gitx.AddWorktree(ctx, src, dest, branch); err != nil {
		return ProjectRepo{}, err
	}
	return ProjectRepo{Name: name, GitURL: originOf(src, env), Dir: src, MainBranch: localMainBranch(ctx, src),
		Shared: true, Local: true, ProjectBranch: branch}, nil
}

// localMainBranch is the main branch of a repository the user already had: main or master when it
// has one of its own (a branch only origin has is not one to branch from), else the branch it is
// on. Unlike settleMainBranch it only reads.
func localMainBranch(ctx context.Context, dir string) string {
	for _, candidate := range []string{defaultMainBranch, "master"} {
		if gitx.BranchExists(ctx, dir, candidate) {
			return candidate
		}
	}
	return gitx.Display(ctx, dir)
}

// missingDeps are the dependency folders the project's repositories need and do not have: the
// main repository's, and each other repository's where it is (the folder sessions link theirs from).
// Paths are relative to the project's folder, which is where a session finds them too.
func missingDeps(p *Project) []gitx.MissingDependency {
	if p == nil || p.Dir == "" {
		return nil
	}
	nested := map[string]bool{}
	for _, repo := range p.Repos {
		nested[strings.SplitN(filepath.ToSlash(repo.relPath(p.Dir)), "/", 2)[0]] = true
	}
	var out []gitx.MissingDependency
	for _, m := range gitx.MissingDependencies(p.Dir) {
		if !nested[strings.SplitN(m.Path, "/", 2)[0]] {
			out = append(out, m)
		}
	}
	for _, repo := range p.Repos {
		rel := filepath.ToSlash(repo.relPath(p.Dir))
		for _, m := range gitx.MissingDependencies(repo.Dir) {
			if m.Path != "." {
				m.Path = rel + "/" + m.Path
			} else {
				m.Path = rel
			}
			out = append(out, m)
		}
	}
	return out
}

// dependenciesNote tells a session's agent what is not installed, and what to do about it. "" when
// everything the project's manifests ask for is there.
//
// A missing dependency folder is the one thing that makes a check impossible to run, and the
// failure it invites is a check made to look as if it ran: a stand-in test runner that exits 0
// reports a pass that nobody measured. So the note says plainly to install, and to report when it
// cannot.
func dependenciesNote(p *Project) string {
	missing := missingDeps(p)
	if len(missing) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n\nThese dependencies are NOT installed, so the project's tests and builds cannot run until they are:")
	for _, m := range missing {
		fmt.Fprintf(&b, "\n- %s: %s/ is missing, install it with `%s` in that folder", m.Path, m.Dir, m.Command)
	}
	b.WriteString("\nInstall them with the project's own tool first and run its own commands (for example " +
		"`php artisan test` in a Laravel project). If the install fails - no network, a registry that does not " +
		"answer - stop and tell the user exactly that; the check was NOT RUN. Never write a stand-in executable " +
		"or script that exits 0 in place of a missing test runner, and never report a check as passed that did " +
		"not run.")
	return b.String()
}
