package gateway

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/madkoding/motita/internal/gitx"
)

// A project is not always one repository: a service and its web client, a library and the app
// that uses it. The project's own folder (Project.Dir) stays its MAIN repository, the first of
// the list, and everything that already worked on it keeps working on it. The others are
// cloned INSIDE it, under gitx.ReposDir, so a session confined to its own checkout can reach
// them: a session with a worktree gets a worktree of each one at the same place inside its own,
// on the session's branch, and gives them back with it.

// ProjectRepo is one of a project's other repositories.
type ProjectRepo struct {
	// Name is the repository's folder under gitx.ReposDir, and what the user calls it.
	Name   string `json:"name"`
	GitURL string `json:"git_url"`
	Dir    string `json:"dir"`
	// MainBranch is the branch the clone was settled on when it was added.
	MainBranch string `json:"main_branch,omitempty"`
	// Branch and Changes are read live, like the project's own.
	Branch  string `json:"branch,omitempty"`
	Changes int    `json:"changes,omitempty"`
}

// repoNamePattern is what a repository's folder may be called: a simple name that is not hidden.
var repoNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

// repoNameOf is the folder name of a repository added to a project: the one asked for, or the
// last element of its URL without ".git". "" when neither makes a usable name.
func repoNameOf(raw, gitURL string) string {
	name := strings.TrimSpace(raw)
	if name == "" {
		u := strings.TrimSuffix(strings.TrimRight(gitURL, "/"), ".git")
		name = u[strings.LastIndexAny(u, "/:\\")+1:]
	}
	if !repoNamePattern.MatchString(name) || strings.Contains(name, "..") {
		return ""
	}
	return name
}

// repoIndex is the position of the repository called name in the project's list, or -1.
func (p *Project) repoIndex(name string) int {
	return slices.IndexFunc(p.Repos, func(r ProjectRepo) bool { return r.Name == name })
}

// sessionRepoPath is where a session that runs in workspace finds the project's repository name.
func sessionRepoPath(workspace, name string) string {
	return filepath.Join(workspace, gitx.ReposDir, name)
}

// handleAddProjectRepo clones one more repository into a project. The body carries git_url
// (required) and name (optional: the URL's last element). The sessions the project already has
// get a worktree of it at once, so the next turn can use it.
func (s *Server) handleAddProjectRepo(w http.ResponseWriter, r *http.Request) {
	p := s.projectOf(r.PathValue("id"))
	if p == nil {
		writeError(w, http.StatusNotFound, ErrProjectNotFound.Error())
		return
	}
	var body struct {
		GitURL string `json:"git_url"`
		Name   string `json:"name"`
	}
	if !s.decodeBody(w, r, &body) {
		return
	}
	gitURL := strings.TrimSpace(body.GitURL)
	name := repoNameOf(body.Name, gitURL)
	switch {
	case gitURL == "":
		writeError(w, http.StatusBadRequest, "the repository URL cannot be empty")
		return
	case name == "":
		writeError(w, http.StatusBadRequest, "the repository needs a simple folder name: letters, digits, '.', '_' or '-'")
		return
	case p.repoIndex(name) >= 0:
		writeError(w, http.StatusConflict, fmt.Sprintf("the project already has a repository called %q", name))
		return
	}
	dest := filepath.Join(p.Dir, gitx.ReposDir, name)
	if _, err := cloneGitRepo(gitURL, dest, s.gitCommandEnv()); err != nil {
		s.writeCloneError(w, gitURL, err)
		return
	}
	ctx := r.Context()
	// The project's own repository must not see the clone as a change of its own. Advisory, as it
	// is for a worktree: a folder that is not a repository has nothing to ignore it in.
	_ = gitx.EnsureIgnoreBase(ctx, p.Dir)
	p.Repos = append(p.Repos, ProjectRepo{
		Name: name, GitURL: gitURL, Dir: dest, MainBranch: settleMainBranch(ctx, dest, defaultMainBranch),
	})
	if err := s.projects.save(*p); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	for _, c := range s.sessionsOfProject(p.ID) {
		s.attachProjectRepos(ctx, p, c.workspaceDir(), c.id)
		applyRuntimeTo(c.svc, p)
	}
	writeJSON(w, http.StatusCreated, p)
}

// handleRemoveProjectRepo takes a repository out of a project: its worktree in every session,
// and its clone. Uncommitted work in any of them stops it, unless the user confirmed the discard
// with `?force=1` - the same rule a session's own checkout follows.
func (s *Server) handleRemoveProjectRepo(w http.ResponseWriter, r *http.Request) {
	p := s.projectOf(r.PathValue("id"))
	if p == nil {
		writeError(w, http.StatusNotFound, ErrProjectNotFound.Error())
		return
	}
	i := p.repoIndex(r.PathValue("name"))
	if i < 0 {
		writeError(w, http.StatusNotFound, "the project has no repository with that name")
		return
	}
	repo := p.Repos[i]
	ctx := r.Context()
	var worktrees []string
	for _, c := range s.sessionsOfProject(p.ID) {
		path := sessionRepoPath(c.workspaceDir(), repo.Name)
		if _, ok, err := gitx.LiveWorktreeAt(ctx, repo.Dir, path); err == nil && ok {
			worktrees = append(worktrees, path)
		}
	}
	if !isForced(r) {
		for _, dir := range append([]string{repo.Dir}, worktrees...) {
			if n, err := gitx.WorkingTreeChanges(ctx, dir); err != nil || n > 0 {
				writeError(w, http.StatusConflict, fmt.Sprintf(
					"the repository %s has uncommitted changes in %s (or they could not be read): "+
						"commit them, or confirm the discard", repo.Name, dir))
				return
			}
		}
	}
	for _, path := range worktrees {
		// Best effort: the clone goes below, and a registration left behind is pruned with it.
		_ = worktreeRemove(ctx, repo.Dir, path, true)
	}
	p.Repos = slices.Delete(p.Repos, i, i+1)
	if err := s.projects.save(*p); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if s.ownsProjectDir(repo.Dir) {
		_ = os.RemoveAll(repo.Dir)
	}
	for _, c := range s.sessionsOfProject(p.ID) {
		applyRuntimeTo(c.svc, p)
	}
	writeJSON(w, http.StatusOK, p)
}

// attachProjectRepos gives a session that has a worktree of its own a worktree of each of the
// project's other repositories, inside its own and on the session's branch. A session that runs
// in the project's checkout already has the clones there. It is idempotent, like
// sessionWorktree, and a repository that cannot be branched is logged and skipped: the session
// still runs, with the clone the project has.
func (s *Server) attachProjectRepos(ctx context.Context, p *Project, workspace, sessionID string) {
	if p == nil || workspace == "" || gitx.SamePath(workspace, p.Dir) {
		return
	}
	for _, repo := range p.Repos {
		path := sessionRepoPath(workspace, repo.Name)
		if _, ok, err := gitx.LiveWorktreeAt(ctx, repo.Dir, path); err == nil && ok {
			continue
		}
		// A new session starts from current code, as the main repository's does. Advisory: a
		// clone with no origin, or one that cannot fast-forward, branches from what it has.
		_ = gitx.PullFastForward(ctx, repo.Dir, gitx.Display(ctx, repo.Dir))
		if err := gitx.AddWorktree(ctx, repo.Dir, path, sessionBranch(sessionID)); err != nil {
			s.warn("the session has no worktree of one of the project's repositories",
				"id", sessionID, "repo", repo.Name, "error", err.Error())
			continue
		}
		gitx.LinkDependencyDirs(repo.Dir, path)
	}
}

// sessionRepoWorktrees are the worktrees of the project's other repositories a session has
// inside its own: each repository's clone and the path of its worktree.
func sessionRepoWorktrees(ctx context.Context, p *Project, workspace string) [][2]string {
	var out [][2]string
	if p == nil {
		return out
	}
	for _, repo := range p.Repos {
		path := sessionRepoPath(workspace, repo.Name)
		if _, ok, err := gitx.LiveWorktreeAt(ctx, repo.Dir, path); err == nil && ok {
			out = append(out, [2]string{repo.Dir, path})
		}
	}
	return out
}

// liveRepos fills in what each of the project's other repositories says right now.
func liveRepos(ctx context.Context, p *Project) {
	for i := range p.Repos {
		p.Repos[i].Branch = gitx.Display(ctx, p.Repos[i].Dir)
		p.Repos[i].Changes, _ = gitx.WorkingTreeChanges(ctx, p.Repos[i].Dir)
	}
}

// projectReposNote is what a session's agent is told about the project's other repositories, ""
// for a project with only one.
func projectReposNote(p *Project) string {
	if p == nil || len(p.Repos) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n\nThis project has more than one git repository. The folder you work in is the main one; " +
		"each other repository is a checkout of its own, with its own history, at:")
	for _, repo := range p.Repos {
		fmt.Fprintf(&b, "\n- %s/%s (%s)", gitx.ReposDir, repo.Name, repo.GitURL)
	}
	b.WriteString("\nRun git inside the repository you changed, and commit each one on its own. " +
		"The main repository ignores the " + gitx.ReposDir + " folder.")
	return b.String()
}

// workspaceDir is the directory the session runs in.
func (c *conversation) workspaceDir() string {
	c.stateMu.Lock()
	defer c.stateMu.Unlock()
	return c.workspace
}

// otherReposMergeNote is what the integration task adds for a project with other repositories:
// the session's worktree of each one, to integrate the same way into that repository's main
// branch. "" when the project has only one.
func otherReposMergeNote(p *Project, workspace, branch string) string {
	worktrees := sessionRepoWorktrees(context.Background(), p, workspace)
	if len(worktrees) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n\nThe project has other repositories, each with a worktree of this session on " + branch +
		". Integrate each one that has work the same way, into its own main branch, from its own directory:")
	for _, wt := range worktrees {
		fmt.Fprintf(&b, "\n- worktree %s, repository %s", wt[1], wt[0])
	}
	return b.String()
}
