package gateway

import (
	"context"
	"path/filepath"
	"sort"
	"strings"

	"github.com/madkoding/motita/internal/gitx"
)

// repoDirsIn lists the git repositories a session's folder holds: the folder itself when it is the
// top of one, then each repository inside it, one or two folders down (see reposUnder). A project is
// a plain folder and the repositories in it are whatever was cloned or copied there, so this is how
// the gateway finds them instead of keeping a list.
func repoDirsIn(ctx context.Context, dir string) []string {
	var out []string
	if gitx.IsRepoRoot(ctx, dir) {
		out = append(out, dir)
	}
	var subs []string
	for _, rel := range reposUnder(dir, "", nestedRepoDepth) {
		if sub := filepath.Join(dir, filepath.FromSlash(rel)); gitx.IsRepoRoot(ctx, sub) {
			subs = append(subs, sub)
		}
	}
	sort.Strings(subs)
	return append(out, subs...)
}

// sessionRepoDir is the repository a session's git work (its pull request) is about: the one the
// caller named (a folder name inside the session's folder), else the one already on the session's
// branch, else the one holding uncommitted changes, else the first. "" when the folder holds none.
func (c *conversation) sessionRepoDir(ctx context.Context, named string) string {
	repos := repoDirsIn(ctx, c.workspace)
	if len(repos) == 0 {
		return ""
	}
	if named = strings.TrimSpace(named); named != "" {
		return c.namedRepo(repos, named)
	}
	branch := sessionBranch(c.id)
	for _, dir := range repos {
		if gitx.Display(ctx, dir) == branch {
			return dir
		}
	}
	for _, dir := range repos {
		if n, err := gitx.WorkingTreeChanges(ctx, dir); err == nil && n > 0 {
			return dir
		}
	}
	return repos[0]
}

// namedRepo is the repository called name among repos ("." is the session's folder itself), or "".
func (c *conversation) namedRepo(repos []string, name string) string {
	for _, dir := range repos {
		if (name == "." && dir == c.workspace) || filepath.Base(dir) == name {
			return dir
		}
	}
	return ""
}

// proposalBranch is the branch a pull request of repo comes from: the branch the repository is on
// when it is a work branch, and the session's own when it is still on the repository's default one
// (see ensureProposalBranch).
func (c *conversation) proposalBranch(ctx context.Context, repo string) string {
	head := gitx.Display(ctx, repo)
	if head == "" || strings.HasPrefix(head, "detached at ") || head == gitx.DefaultBranch(ctx, repo) {
		return sessionBranch(c.id)
	}
	return head
}

// ensureProposalBranch moves repo onto its proposal branch, carrying uncommitted work with it: a
// pull request needs a branch of its own, and a repository left on main would push its work there.
func (c *conversation) ensureProposalBranch(ctx context.Context, repo string) (string, error) {
	branch := c.proposalBranch(ctx, repo)
	if gitx.Display(ctx, repo) == branch {
		return branch, nil
	}
	return branch, gitx.SwitchNew(ctx, repo, branch)
}

// changesAcrossRepos counts the uncommitted changes of every repository in the session's folder.
func (c *conversation) changesAcrossRepos(ctx context.Context) int {
	total := 0
	for _, dir := range repoDirsIn(ctx, c.workspace) {
		if n, err := gitx.WorkingTreeChanges(ctx, dir); err == nil {
			total += n
		}
	}
	return total
}
