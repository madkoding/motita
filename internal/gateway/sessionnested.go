package gateway

import (
	"cmp"
	"context"
	"errors"
	"os"
	"path/filepath"

	"github.com/madkoding/motita/internal/gitx"
)

// A project whose folder is not a repository but holds several (see projectnested.go) cannot have a
// worktree of its own: git has nothing to check out at its top. A session that wants its own
// checkout gets a plain folder instead, with a worktree of EACH repository at the same place it has
// in the project's folder, all on the session's branch. Deleting the session gives each one back and
// then removes the folder.

// nestedSessionWorktree fills path with a worktree of every repository inside projectDir, on branch.
// Like sessionWorktree it is idempotent: a repository already checked out there is left as it is. A
// repository that cannot be branched (no commits yet) is skipped; with none at all the session
// falls back to the project's folder.
func nestedSessionWorktree(ctx context.Context, projectDir, path, branch string) (string, error) {
	made := 0
	var firstErr error
	for _, rel := range reposUnder(projectDir, "", nestedRepoDepth) {
		repo := filepath.Join(projectDir, filepath.FromSlash(rel))
		dest := filepath.Join(path, filepath.FromSlash(rel))
		if _, ok, err := gitx.LiveWorktreeAt(ctx, repo, dest); err == nil && ok {
			made++
			continue
		}
		if err := gitx.AddWorktree(ctx, repo, dest, branch); err != nil {
			firstErr = cmp.Or(firstErr, err)
			continue
		}
		gitx.LinkDependencyDirs(repo, dest)
		made++
	}
	if made == 0 {
		_ = os.Remove(path)
		return projectDir, cmp.Or(firstErr, errors.New("the project's folder holds no repository to check out"))
	}
	return path, nil
}

// nestedSessionRepos are the live worktrees a session has in workspace of the repositories inside
// projectDir: each repository and the worktree's path. Empty when projectDir is a repository itself.
func nestedSessionRepos(ctx context.Context, projectDir, workspace string) [][2]string {
	var out [][2]string
	if gitx.Repo(ctx, projectDir) == nil {
		return out
	}
	for _, rel := range reposUnder(projectDir, "", nestedRepoDepth) {
		repo := filepath.Join(projectDir, filepath.FromSlash(rel))
		dest := filepath.Join(workspace, filepath.FromSlash(rel))
		if _, ok, err := gitx.LiveWorktreeAt(ctx, repo, dest); err == nil && ok {
			out = append(out, [2]string{repo, dest})
		}
	}
	return out
}
