package gateway

import (
	"cmp"
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"

	"github.com/madkoding/motita/internal/gitx"
)

// The retired workspace catalog, as it used to place repositories: the tests that need a project
// from before (a shared repository, a worktree of the catalog's clone) make it this way.

// errNoCatalog is catalogClone's answer when the workspace has no catalog to clone into.
var errNoCatalog = errors.New("the workspace has no catalog of repositories")

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

var (
	legacyCloningMu sync.Mutex
	legacyCloning   = map[string]bool{}
)

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
	legacyCloningMu.Lock()
	dir = freeCloneDir(filepath.Join(root, cmp.Or(repoNameOf("", gitURL), name)), gitURL, env,
		func(d string) bool { return legacyCloning[d] })
	legacyCloning[dir] = true
	legacyCloningMu.Unlock()
	defer func() {
		legacyCloningMu.Lock()
		delete(legacyCloning, dir)
		legacyCloningMu.Unlock()
	}()
	if _, err := cloneGitRepo(gitURL, dir, env); err != nil {
		return "", false, err
	}
	return dir, true, nil
}

// freeProjectBranch is the branch a new worktree of the project in the catalog's clone src takes:
// the usual name, or that name and the start of the project's id when the clone already has a
// branch by it. Two projects whose folders share a name, or a project made after another of the
// same name was deleted, would otherwise share a branch, and the new one would start on the work
// the other left on it.
func freeProjectBranch(ctx context.Context, src string, p *Project) string {
	branch := legacyProjectBranch(p)
	if !gitx.BranchExists(ctx, src, branch) {
		return branch
	}
	return branch + "-" + p.ID[:min(len(p.ID), 8)]
}

// placeLegacyRepo is placeRepo as it was: with shared, a worktree of the catalog's clone on the
// project's own branch; an empty repository, or a workspace without a catalog, a clone of its own.
func (s *Server) placeLegacyRepo(
	ctx context.Context, p *Project, name, gitURL, dest string, shared bool,
) (ProjectRepo, error) {
	if !shared {
		return s.placeRepo(ctx, name, gitURL, dest)
	}
	src, fresh, err := s.catalogClone(gitURL, name)
	switch {
	case err == nil && gitx.HasCommits(ctx, src):
		mainBranch := settleMainBranch(ctx, src, defaultMainBranch)
		branch := freeProjectBranch(ctx, src, p)
		if err := gitx.AddWorktree(ctx, src, dest, branch); err != nil {
			return ProjectRepo{}, err
		}
		return ProjectRepo{Name: name, GitURL: gitURL, Dir: src, MainBranch: mainBranch, Shared: true,
			ProjectBranch: branch}, nil
	case fresh:
		// An empty repository has no commit a worktree could start from: the project takes the clone.
		if err := renamePath(src, dest); err != nil {
			return ProjectRepo{}, err
		}
	case err == nil || errors.Is(err, errNoCatalog):
		if _, err := cloneGitRepo(gitURL, dest, s.gitCommandEnv()); err != nil {
			return ProjectRepo{}, err
		}
	default:
		return ProjectRepo{}, err
	}
	return ProjectRepo{Name: name, GitURL: gitURL, Dir: dest, MainBranch: settleMainBranch(ctx, dest, defaultMainBranch)}, nil
}
