package gateway

import (
	"context"
	"path/filepath"
	"slices"

	"github.com/madkoding/motita/internal/gitx"
)

// foundRepo is a repository inside a plain project folder that the project's recorded list does not
// name: its path in the folder, and where it is.
type foundRepo struct{ rel, dir string }

// foundRepos lists the repositories a plain project folder holds (see projectnested.go) beyond the
// ones the project recorded, whose worktrees or clones are in that folder too. A project whose
// folder is a repository has none: it is one repository.
func foundRepos(ctx context.Context, p *Project) []foundRepo {
	if p.Dir == "" || projectIsRepo(ctx, p.Dir) {
		return nil
	}
	var out []foundRepo
	for _, rel := range reposUnder(p.Dir, "", nestedRepoDepth) {
		dir := filepath.Join(p.Dir, filepath.FromSlash(rel))
		if !slices.ContainsFunc(p.Repos, func(r ProjectRepo) bool { return gitx.SamePath(p.repoPath(r), dir) }) {
			out = append(out, foundRepo{rel, dir})
		}
	}
	return out
}

// projectIsRepo reports whether a project's folder is a repository of its own. A folder git sees
// inside a work tree usually is (a package of a monorepo), except when it is a container of
// repositories that only happens to sit inside another one (a workspace under a dotfiles repository):
// what it holds are the repositories, as for any plain folder.
func projectIsRepo(ctx context.Context, dir string) bool {
	if gitx.Repo(ctx, dir) != nil {
		return false
	}
	return gitx.IsRepoRoot(ctx, dir) || len(reposUnder(dir, "", nestedRepoDepth)) == 0
}
