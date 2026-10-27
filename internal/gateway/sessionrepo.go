package gateway

import (
	"context"
	"path/filepath"
	"sort"
	"strings"

	"github.com/madkoding/motita/internal/gitx"
)

// repoDirsIn lists the git repositories a session's folder holds: the folder itself when it is the
// top of a real one (not a container of repositories, see below), then each repository inside it, one
// or two folders down (see reposUnder). A project is a plain folder and the repositories in it are
// whatever was cloned or copied there, so this is how the gateway finds them instead of keeping a
// list.
func repoDirsIn(ctx context.Context, dir string) []string {
	var subs []string
	for _, rel := range reposUnder(dir, "", nestedRepoDepth) {
		if sub := filepath.Join(dir, filepath.FromSlash(rel)); gitx.IsRepoRoot(ctx, sub) {
			subs = append(subs, sub)
		}
	}
	sort.Strings(subs)
	// A folder that holds repositories and has no origin is a container: a stray git init there
	// (the agent used to make one) is not a repository a pull request could go to, so its loose
	// files count for nothing.
	if gitx.IsRepoRoot(ctx, dir) && (len(subs) == 0 || gitx.HasRemote(ctx, dir)) {
		return append([]string{dir}, subs...)
	}
	return subs
}

// sessionRepoDir is the repository a session's git work (its pull request) is about: the one the
// caller named (a folder name inside the session's folder), else the one holding the session's work
// (see sessionRepoDirs), else the first. "" when the folder holds none.
func (c *conversation) sessionRepoDir(ctx context.Context, named string) string {
	repos := repoDirsIn(ctx, c.workspace)
	if len(repos) == 0 {
		return ""
	}
	if named = strings.TrimSpace(named); named != "" {
		return c.namedRepo(repos, named)
	}
	return c.sessionRepoDirs(ctx, repos)[0]
}

// sessionRepoDirs orders repos by how much a pull request is about them: first those with work to
// propose, then the others, each group with the repositories already on the session's branch first.
// A session of a project that holds several repositories has a worktree of each on its branch, so
// being on that branch says nothing about where the work is.
func (c *conversation) sessionRepoDirs(ctx context.Context, repos []string) []string {
	branch := sessionBranch(c.id)
	rank := func(dir string) int {
		r := 0
		if !repoHasWork(ctx, dir) {
			r += 2
		}
		if gitx.Display(ctx, dir) != branch {
			r++
		}
		return r
	}
	ranks := make(map[string]int, len(repos))
	for _, dir := range repos {
		ranks[dir] = rank(dir)
	}
	out := append([]string(nil), repos...)
	sort.SliceStable(out, func(i, j int) bool { return ranks[out[i]] < ranks[out[j]] })
	return out
}

// knownRepoName is the name of the repository of the session's folder that name asks for, taken
// from the folder and not from the request, so what reaches git is never the caller's own text. ""
// when name is empty, and a name no repository has when it matches none (see namedRepo).
func (c *conversation) knownRepoName(ctx context.Context, name string) string {
	if name = strings.TrimSpace(name); name == "" {
		return ""
	}
	for _, dir := range repoDirsIn(ctx, c.workspace) {
		if c.namedRepo([]string{dir}, name) == dir {
			return c.repoName(dir)
		}
	}
	return "?"
}

// repoName is what a request calls the repository at dir: "." for the session's folder itself, else
// its path in that folder. Two repositories in different groups can share a folder name
// (`web/api`, `mobile/api`), so the name alone would not tell them apart.
func (c *conversation) repoName(dir string) string {
	if dir == c.workspace {
		return "."
	}
	if rel, err := filepath.Rel(c.workspace, dir); err == nil && filepath.IsLocal(rel) {
		return filepath.ToSlash(rel)
	}
	return filepath.Base(dir)
}

// namedRepo is the repository called name among repos ("." is the session's folder itself), or "".
// A bare folder name still finds the repository when only one has it.
func (c *conversation) namedRepo(repos []string, name string) string {
	byBase := ""
	matches := 0
	for _, dir := range repos {
		if c.repoName(dir) == name {
			return dir
		}
		if filepath.Base(dir) == name && dir != c.workspace {
			byBase, matches = dir, matches+1
		}
	}
	if matches == 1 {
		return byBase
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

// hasProposableWork reports whether any repository in the session's folder has something a pull
// request could carry: uncommitted changes, or commits on a work branch that its default branch
// lacks. It does not need the session to have a worktree of its own: the project's main session
// works in the project's folder, and its work is as proposable as an isolated session's.
func (c *conversation) hasProposableWork(ctx context.Context) bool {
	for _, dir := range repoDirsIn(ctx, c.workspace) {
		if repoHasWork(ctx, dir) {
			return true
		}
	}
	return false
}

// repoHasWork reports whether the repository at dir has uncommitted changes or commits on a work
// branch that its default branch lacks.
func repoHasWork(ctx context.Context, dir string) bool {
	if n, err := gitx.WorkingTreeChanges(ctx, dir); err == nil && n > 0 {
		return true
	}
	base, head := gitx.DefaultBranch(ctx, dir), gitx.Display(ctx, dir)
	if base == "" || head == "" || head == base {
		return false
	}
	ahead, _, err := gitx.CommitsBetween(ctx, dir, base, head)
	return err == nil && len(ahead) > 0
}
