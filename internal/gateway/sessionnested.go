package gateway

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/madkoding/motita/internal/gitx"
)

// A project whose folder is not a repository but holds several (see projectnested.go) cannot have a
// worktree of its own: git has nothing to check out at its top. A session that wants its own
// checkout gets a plain folder instead, with a worktree of EACH repository at the same place it has
// in the project's folder, all on the session's branch, and a link to everything else the folder
// holds. Deleting the session gives each worktree back and then removes the folder.

// nestedSessionWorktree fills path with a worktree of every repository inside projectDir, on branch.
// Like sessionWorktree it is idempotent: a repository already checked out there is left as it is. A
// repository that cannot be branched (no commits yet) is skipped; with none at all the session
// falls back to the project's folder.
func nestedSessionWorktree(ctx context.Context, projectDir, path, branch string) (string, error) {
	made := 0
	var firstErr error
	repos := reposUnder(projectDir, "", nestedRepoDepth)
	for _, rel := range repos {
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
	linkLooseEntries(projectDir, path, repos)
	return path, nil
}

// nestedSessionRepos are the live worktrees a session has in workspace of the repositories inside
// projectDir: each repository and the worktree's path. Empty when projectDir is a repository itself,
// or is the session's own folder.
func nestedSessionRepos(ctx context.Context, projectDir, workspace string) [][2]string {
	var out [][2]string
	if projectDir == "" || workspace == "" || gitx.SamePath(projectDir, workspace) || gitx.Repo(ctx, projectDir) == nil {
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

// nestedReposNote is what the agent is told when its folder is not a repository but holds some: the
// folder is only a container, so it must not be turned into a repository (`git init`, `git add .`),
// which would swallow the repositories inside as embedded ones. "" when the folder is a repository
// itself, or holds none.
func nestedReposNote(ctx context.Context, workspace string) string {
	if workspace == "" || gitx.IsRepoRoot(ctx, workspace) {
		return ""
	}
	repos := reposUnder(workspace, "", nestedRepoDepth)
	if len(repos) == 0 {
		return ""
	}
	return "\n\nYour working directory is NOT a git repository and must stay that way: it only holds " +
		"repositories, each with its own history. Never run `git init`, and never run `git add`, `git commit` " +
		"or `git status` at its top. Run git inside the repository you changed and commit each one on its own. " +
		"The repositories are: " + strings.Join(repos, ", ") + "."
}

// linkLooseEntries links into the session's folder what the project's folder holds outside its
// repositories (a README, an .env, a compose file), at the same place. Those files are in no
// repository and have no branch to isolate them, so the session shares them with the project's
// folder, as it did before it had a folder of its own. Removing the session removes the links only.
func linkLooseEntries(projectDir, path string, repos []string) {
	isRepo, holdsRepo := map[string]bool{}, map[string]bool{}
	for _, rel := range repos {
		rel = filepath.FromSlash(rel)
		isRepo[rel] = true
		for d := filepath.Dir(rel); d != "."; d = filepath.Dir(d) {
			holdsRepo[d] = true
		}
	}
	var walk func(rel string)
	walk = func(rel string) {
		entries, _ := os.ReadDir(filepath.Join(projectDir, rel))
		for _, e := range entries {
			child := filepath.Join(rel, e.Name())
			switch {
			case isRepo[child]:
			case holdsRepo[child]:
				walk(child)
			default:
				dest := filepath.Join(path, child)
				if _, err := os.Lstat(dest); err != nil {
					_ = os.Symlink(filepath.Join(projectDir, child), dest)
				}
			}
		}
	}
	walk("")
}

// nestedRepoWork is one repository of a plain project folder where the session has work to
// integrate: commits on its branch the repository's checked-out branch lacks, or uncommitted changes.
type nestedRepoWork struct {
	Repo, Worktree, Base string
}

// nestedWork lists the repositories of a plain project folder where the session has work.
func nestedWork(ctx context.Context, projectDir, workspace, branch string) []nestedRepoWork {
	var out []nestedRepoWork
	for _, wt := range nestedSessionRepos(ctx, projectDir, workspace) {
		base := gitx.Display(ctx, wt[0])
		ahead, _, _ := gitx.CommitsBetween(ctx, wt[0], base, branch)
		changes, _ := gitx.WorkingTreeChanges(ctx, wt[1])
		if base != "" && base != branch && (len(ahead) > 0 || changes > 0) {
			out = append(out, nestedRepoWork{Repo: wt[0], Worktree: wt[1], Base: base})
		}
	}
	return out
}

// nestedMergeTask is the integration task of a session of a plain project folder: each repository
// with work is merged on its own, into the branch its folder in the project has checked out.
func nestedMergeTask(id, branch, workspace string, work []nestedRepoWork) string {
	var b strings.Builder
	fmt.Fprintf(&b, `Integrate this session's work into the project.

The project's folder is not a repository: it holds several, and this session has a worktree of each `+
		`inside %s, all on the branch %s. Follow the git-in-a-repository skill for EACH repository below: `+
		`commit any pending changes in its worktree, then, from the repository's directory, merge %s into `+
		`its base branch with a merge commit whose subject is "chore(motita): integrate session %s".
`, workspace, branch, branch, id)
	for _, w := range work {
		fmt.Fprintf(&b, "\n- worktree %s, repository %s, base branch %s", w.Worktree, w.Repo, w.Base)
	}
	b.WriteString("\n\nReport each merge commit SHA and the files changed. If there is a conflict in a " +
		"repository, abort that merge and explain what needs to be resolved first.")
	return b.String()
}

// projectRev is the mark a merge run compares the project against: the HEAD of its folder, or for
// a plain folder the HEAD of each repository in it, joined by commas.
func projectRev(ctx context.Context, projectDir string) string {
	if gitx.Repo(ctx, projectDir) == nil {
		return gitx.HeadRev(ctx, projectDir)
	}
	var revs []string
	for _, rel := range reposUnder(projectDir, "", nestedRepoDepth) {
		revs = append(revs, gitx.HeadRev(ctx, filepath.Join(projectDir, filepath.FromSlash(rel))))
	}
	return strings.Join(revs, ",")
}

// movedRev is the first HEAD of now that differs from start (both from projectRev), "" when none
// moved. A repository added or removed between the two counts as moved.
func movedRev(start, now string) string {
	if start == now {
		return ""
	}
	a, b := strings.Split(start, ","), strings.Split(now, ",")
	for i, rev := range b {
		if i >= len(a) || a[i] != rev {
			return rev
		}
	}
	return b[0]
}
