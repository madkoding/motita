package gateway

import (
	"cmp"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"

	"github.com/madkoding/motita/internal/gitx"
)

// A project is not always one repository: a service and its web client, a library and the app
// that uses it. The project's own folder (Project.Dir) stays its MAIN repository, the first of
// the list, and everything that already worked on it keeps working on it. The others sit INSIDE
// it, as plain folders (`<project>/<name>`), so a session confined to its own checkout can reach
// them: a session with a worktree gets a worktree of each one at the same place inside its own,
// on the session's branch, and gives them back with it.
//
// One repository can belong to many projects, and many projects to one repository. So each one
// is cloned ONCE, in the workspace's catalog (catalogFolder), and the folder a project has of it
// is a worktree of that clone on the project's own branch: a commit made in one project is in
// the repository every other project uses, and adding the repository to one more project is a
// worktree, not another download. The ones cloned inside a project before the catalog (or under
// gitx.ReposDir before that) stay where they are, clones of their own.

// ProjectRepo is one of a project's other repositories. In a plain project folder the repositories
// are found by looking (foundRepos), and a clone made when the project was created is recorded here
// only so its URL is known: that URL, not an origin the agent can rewrite, is where its pull requests
// go (repoRemote). Every listing merges the two by folder, so none is counted twice.
type ProjectRepo struct {
	// Name is the repository's folder name, and what the user calls it.
	Name   string `json:"name"`
	GitURL string `json:"git_url"`
	Dir    string `json:"dir"`
	// MainBranch is the branch the clone was settled on when it was added.
	MainBranch string `json:"main_branch,omitempty"`
	// Shared marks a repository of the catalog: Dir is the clone every project that uses it
	// shares, and the project's folder holds a worktree of it.
	Shared bool `json:"shared,omitempty"`
	// Local marks a repository the user already had on this machine, added by its folder instead of
	// cloned: Dir is that folder, the user's own, and the project's folder holds a worktree of it
	// like for any shared repository (so Shared is set too). It is never fetched into, merged into
	// or deleted: the branch it has checked out and its working tree are the user's, and only the
	// branches and worktrees motita made in it are motita's.
	Local bool `json:"local,omitempty"`
	// ProjectBranch is the branch the project's worktree of a shared repository was made on. Empty
	// in a project saved before it was recorded: that one is on projectBranch's older name.
	ProjectBranch string `json:"project_branch,omitempty"`
	// Branch and Changes are read live, like the project's own.
	Branch  string `json:"branch,omitempty"`
	Changes int    `json:"changes,omitempty"`
	// Behind, read live for a shared repository, counts the commits of its main branch the
	// project's folder does not have: it stays behind when it holds work of its own.
	Behind int `json:"behind,omitempty"`
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

// relPath is where the repository sits inside the project's folder, and so inside every session's
// worktree of it: its name, or under gitx.ReposDir for a repository cloned there before.
func (r ProjectRepo) relPath(projectDir string) string {
	if rel, err := filepath.Rel(projectDir, r.Dir); err == nil && rel != "." && filepath.IsLocal(rel) {
		return rel
	}
	return r.Name
}

// repoPath is where the repository is in the project's own folder: the clone itself, or the
// project's worktree of the catalog's clone.
func (p *Project) repoPath(repo ProjectRepo) string {
	return filepath.Join(p.Dir, repo.relPath(p.Dir))
}

// projectBranch is the branch a project's worktree of a shared repository is on: one of its own,
// because git checks a branch out in one worktree only, and the catalog's clone has the main one.
func projectBranch(p *Project, repo ProjectRepo) string {
	return cmp.Or(repo.ProjectBranch, legacyProjectBranch(p))
}

// legacyProjectBranch is the name every project's branch had before it was recorded: project/ and
// the folder's name.
func legacyProjectBranch(p *Project) string {
	return "project/" + filepath.Base(p.Dir)
}

// sessionRepoPath is where a session that runs in workspace finds the project's repository repo.
func sessionRepoPath(workspace string, p *Project, repo ProjectRepo) string {
	return filepath.Join(workspace, repo.relPath(p.Dir))
}

// placeRepo clones the repository gitURL into the project's folder, at dest: a clone of the
// project's own, with nothing shared with other projects. (Projects used to share one clone per
// repository from the workspace's catalog; that is retired, see migrateSharedRepos.)
func (s *Server) placeRepo(ctx context.Context, name, gitURL, dest string) (ProjectRepo, error) {
	if _, err := cloneGitRepo(gitURL, dest, s.gitCommandEnv()); err != nil {
		return ProjectRepo{}, err
	}
	mainBranch := settleMainBranch(ctx, dest, defaultMainBranch)
	return ProjectRepo{Name: name, GitURL: gitURL, Dir: dest, MainBranch: mainBranch}, nil
}

// beforeProjectUpdate runs just before a repository handler saves its change; a variable only so a
// test can delete the project at that point, as a user can while a clone runs.
var beforeProjectUpdate = func(string) {
	// Nothing to do in production.
}

// updateProject is projectStore.update for a repository handler.
func (s *Server) updateProject(id string, change func(*Project)) (*Project, error) {
	beforeProjectUpdate(id)
	return s.projects.update(id, change)
}

// deletesClone reports whether taking repo out of its project deletes a clone: its own, in the
// workspace. A shared repository stays in the catalog, and one registered from outside the
// workspace is the user's and stays where it is.
func (s *Server) deletesClone(repo ProjectRepo) bool {
	return !repo.Shared && s.ownsProjectDir(repo.Dir)
}

// discardSharedRepoFolder is what a shared repository leaves behind when its project goes: the
// project's worktree of it, and its branch only when nothing on it is unmerged (what was committed
// there is work). Best effort, like the sessions' worktrees.
func discardSharedRepoFolder(ctx context.Context, p *Project, repo ProjectRepo) {
	_ = worktreeRemove(ctx, repo.Dir, p.repoPath(repo), true)
	_ = gitx.DeleteBranch(ctx, repo.Dir, projectBranch(p, repo))
}

// lockProjectRepos serialises the repository operations of one project and returns the unlock. A
// clone takes minutes: two adds to the same project would each decide from the project as it was
// before the other, and the second to finish would leave the first one's folder orphaned.
func (s *Server) lockProjectRepos(id string) func() {
	m, _ := s.repoLocks.LoadOrStore(id, &sync.Mutex{})
	mu := m.(*sync.Mutex)
	mu.Lock()
	return mu.Unlock
}

// releaseSharedRepos is what a deleted project leaves in the catalog: its folder is gone, so the
// registrations of its worktrees are cleared, and its branches go when nothing on them is unmerged.
func releaseSharedRepos(ctx context.Context, p *Project) {
	for _, repo := range p.Repos {
		if repo.Shared {
			_ = gitx.PruneWorktrees(ctx, repo.Dir)
			_ = gitx.DeleteBranch(ctx, repo.Dir, projectBranch(p, repo))
		}
	}
}

// goesWithProject reports whether deleting p removes repo's clone: a shared repository never does
// (its worktree is released, the clone stays in the catalog); any other goes with the folder it is
// in, which only happens when the deletion removes that folder (wipe).
func (s *Server) goesWithProject(p *Project, repo ProjectRepo, wipe bool) bool {
	return wipe && !repo.Shared && (s.deletesClone(repo) || pathWithinOrSame(p.Dir, repo.Dir))
}

// releaseProjectFolder is what a deleted project gives back: with its folder removed (projectDir,
// "" when it stays) the catalog registrations of what it held, or, in a folder that stays, the
// worktrees motita put inside it and the branches they hold in the shared repositories.
func (s *Server) releaseProjectFolder(ctx context.Context, p *Project, projectDir string) {
	switch {
	case p == nil:
	case projectDir != "":
		removeProjectFolder(projectDir)
		releaseSharedRepos(ctx, p)
	case s.projectInDir(p.Dir, p.ID) == nil:
		for _, repo := range p.Repos {
			if repo.Shared {
				discardSharedRepoFolder(ctx, p, repo)
			}
		}
	default:
		// A folder another project also stands on: nothing of it is this one's.
	}
}

// discardedRepoChanges are the uncommitted changes deleting p throws away in the project's folders
// of its other repositories (the worktrees of shared ones always go; any other clone goes only with
// the folder, when wipe), each path under the repository's folder, and the folders they are in.
func (s *Server) discardedRepoChanges(ctx context.Context, p *Project, wipe bool) map[string][]gitx.Change {
	out := map[string][]gitx.Change{}
	if p == nil {
		return out
	}
	for _, repo := range p.Repos {
		var changes []gitx.Change
		if repo.Shared || s.goesWithProject(p, repo, wipe) {
			// A folder that cannot be read is not proven to hold work; the deletion's own
			// confirmation covers it.
			changes, _ = worktreeInspectList(ctx, p.repoPath(repo))
		}
		if len(changes) == 0 {
			continue
		}
		rel := repo.relPath(p.Dir)
		for i := range changes {
			changes[i].Path = filepath.Join(rel, changes[i].Path)
		}
		out[rel] = changes
	}
	return out
}

// fastForward moves the branch checked out at dir forward to ref, when it can without a merge.
func fastForward(ctx context.Context, dir, ref string) error {
	args := append(append([]string{"-C", dir}, gitx.HardeningFor(ctx, dir)...), "merge", "--ff-only", ref)
	return exec.CommandContext(ctx, "git", args...).Run()
}

// syncSharedRepos brings the folders of the project's shared repositories up to each one's main
// branch after an integration, in every project that uses them, so none stays behind what was
// merged. The clone learns what the host merged first.
func (s *Server) syncSharedRepos(ctx context.Context, p *Project) {
	if p == nil {
		return
	}
	var all []Project
	for _, repo := range p.Repos {
		if !repo.Shared {
			continue
		}
		// Advisory: a clone with no origin, or a merge made here, has nothing to bring in. The
		// user's own repository is never fetched into: what they merge there is theirs to do.
		if !repo.Local {
			_ = gitx.PullFastForward(ctx, repo.Dir, cmp.Or(repo.MainBranch, defaultMainBranch))
		}
		if all == nil {
			all = s.allProjects()
		}
		syncUsersOf(ctx, all, repo.Dir)
	}
}

// syncUsersOf brings up to date the folder every project of all has of the clone at dir.
func syncUsersOf(ctx context.Context, all []Project, dir string) {
	for i := range all {
		for _, r := range all[i].Repos {
			if r.Shared && gitx.SamePath(r.Dir, dir) {
				syncProjectFolder(ctx, &all[i], r)
			}
		}
	}
}

// syncProjectFolder fast-forwards the project's folder of a shared repository to its main branch.
// Only a clean folder still on the project's branch moves, and only forward: work committed or
// left there is never touched.
func syncProjectFolder(ctx context.Context, p *Project, repo ProjectRepo) {
	dir := p.repoPath(repo)
	if gitx.Display(ctx, dir) != projectBranch(p, repo) {
		return
	}
	if n, err := gitx.WorkingTreeChanges(ctx, dir); err == nil && n == 0 {
		_ = fastForward(ctx, dir, cmp.Or(repo.MainBranch, defaultMainBranch))
	}
}

// sessionWorktreesOf lists the live worktrees the project's sessions have of repo.
func (s *Server) sessionWorktreesOf(ctx context.Context, p *Project, repo ProjectRepo) []string {
	var worktrees []string
	for _, c := range s.sessionsOfProject(p.ID) {
		path := sessionRepoPath(c.workspaceDir(), p, repo)
		// A session working in the project's folder shares the project's own checkout of the
		// repository: that is not a worktree of the session's.
		if gitx.SamePath(path, p.repoPath(repo)) {
			continue
		}
		if _, ok, err := gitx.LiveWorktreeAt(ctx, repo.Dir, path); err == nil && ok {
			worktrees = append(worktrees, path)
		}
	}
	return worktrees
}

// firstDirtyDir returns the first of dirs with uncommitted changes, or whose changes could not be
// read, and whether there is one.
func firstDirtyDir(ctx context.Context, dirs []string) (string, bool) {
	for _, dir := range dirs {
		if n, err := gitx.WorkingTreeChanges(ctx, dir); err != nil || n > 0 {
			return dir, true
		}
	}
	return "", false
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
		path := sessionRepoPath(workspace, p, repo)
		if _, ok, err := gitx.LiveWorktreeAt(ctx, repo.Dir, path); err == nil && ok {
			continue
		}
		// A new session starts from current code, as the main repository's does. Advisory: a
		// clone with no origin, or one that cannot fast-forward, branches from what it has.
		// The user's own repository is never fetched into: the branch they have out is theirs.
		if !repo.Local {
			_ = gitx.PullFastForward(ctx, repo.Dir, gitx.Display(ctx, repo.Dir))
		}
		if err := gitx.AddWorktreeFrom(ctx, repo.Dir, path, sessionBranch(sessionID), sessionStart(ctx, repo)); err != nil {
			s.warn("the session has no worktree of one of the project's repositories",
				"id", sessionID, "repo", repo.Name, "error", err.Error())
			continue
		}
		gitx.LinkDependencyDirs(repo.Dir, path)
	}
}

// sessionStart is where a session's branch of one of the project's other repositories starts: the
// repository's main branch when it is shared (the catalog's clone, or the user's own repository,
// whose checked-out branch is theirs), else the clone's HEAD.
func sessionStart(ctx context.Context, repo ProjectRepo) string {
	if repo.Shared && repo.MainBranch != "" && gitx.BranchExists(ctx, repo.Dir, repo.MainBranch) {
		return repo.MainBranch
	}
	return ""
}

// sessionRepoWorktrees are the worktrees of the project's other repositories a session has
// inside its own: each repository's clone and the path of its worktree.
func sessionRepoWorktrees(ctx context.Context, p *Project, workspace string) [][2]string {
	var out [][2]string
	if p == nil {
		return out
	}
	for _, repo := range p.Repos {
		path := sessionRepoPath(workspace, p, repo)
		if _, ok, err := gitx.LiveWorktreeAt(ctx, repo.Dir, path); err == nil && ok {
			out = append(out, [2]string{repo.Dir, path})
		}
	}
	return out
}

// liveRepos fills in what each of the project's other repositories says right now.
func liveRepos(ctx context.Context, p *Project) {
	for i := range p.Repos {
		dir := p.repoPath(p.Repos[i])
		p.Repos[i].Branch = gitx.Display(ctx, dir)
		p.Repos[i].Changes, _ = gitx.WorkingTreeChanges(ctx, dir)
		if p.Repos[i].Shared {
			mainBranch := cmp.Or(p.Repos[i].MainBranch, defaultMainBranch)
			_, behind, _ := gitx.CommitsBetween(ctx, dir, mainBranch, "HEAD")
			p.Repos[i].Behind = len(behind)
		}
	}
}

// projectScopeNote is what a session's agent is told about the project it belongs to: its name,
// the folder it works in, and that the project is the scope of everything it is asked. "" for a
// session that belongs to none.
//
// The sentence is a scope, not a wall: what the agent can REACH is the sandbox's business and the
// person's choice in the security settings (workspace or the whole disk). This says what it should
// WANT to reach, which matters in both modes - with the whole disk allowed it is the only thing
// that keeps "fix the tests" on this project's tests.
func projectScopeNote(p *Project, workspace string) string {
	if p == nil {
		return ""
	}
	name := strings.TrimSpace(p.Title)
	if name == "" {
		name = filepath.Base(p.Dir)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "\n\nThis session belongs to the project %q. ", name)
	if workspace != "" && !gitx.SamePath(workspace, p.Dir) {
		fmt.Fprintf(&b, "Its root is your working directory, %s: this session's own worktree of the project's "+
			"checkout at %s. ", workspace, p.Dir)
	} else if p.Dir != "" {
		fmt.Fprintf(&b, "Its root is your working directory, %s. ", p.Dir)
	} else {
		// A project without a folder has no root to name: the scope sentence below is enough.
	}
	b.WriteString("Everything you are asked here is about this project and is resolved inside it: take " +
		"relative paths against that root, run commands there, and do not read, change or run anything in " +
		"another folder (other projects, other sessions, the user's home) unless the user names that path. " +
		"When a request could mean somewhere else, it means this project.")
	return b.String()
}

// projectReposNote is what a session's agent is told about the project's other repositories, ""
// for a project with only one.
func projectReposNote(p *Project) string {
	if p == nil || len(p.Repos) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n\nThis project has more than one git repository. The folder you work in holds them: " +
		"each other repository is a checkout of its own, with its own history, in a folder of it:")
	for _, repo := range p.Repos {
		fmt.Fprintf(&b, "\n- %s (%s)", filepath.ToSlash(repo.relPath(p.Dir)), repo.GitURL)
		switch {
		case repo.Local:
			fmt.Fprintf(&b, ": the user's own repository, used where it is (%s). Work only in your worktree of it "+
				"and commit on your branch; never check out, merge, reset or change files in that folder itself", repo.Dir)
		case repo.Shared:
			b.WriteString(": shared with other projects, so its main branch is theirs too")
		default:
			// A clone of the project's own: nothing more to say about it.
		}
	}
	b.WriteString("\nRun git inside the repository you changed, and commit each one on its own. " +
		"The project's own repository ignores these folders. A change that spans repositories goes in one pull " +
		"request per repository, each on this session's branch: name the other pull requests in each description " +
		"and say in which order to merge them.")
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
	var own []string
	b.WriteString("\n\nThe project has other repositories, each with a worktree of this session on " + branch +
		". Integrate each one that has work the same way, into its own main branch, from its own directory:")
	for _, wt := range worktrees {
		if p.isLocalRepo(wt[0]) {
			own = append(own, wt[1])
			continue
		}
		fmt.Fprintf(&b, "\n- worktree %s, repository %s", wt[1], wt[0])
	}
	for _, path := range own {
		fmt.Fprintf(&b, "\n- worktree %s is of the user's own repository: leave its work committed on %s and "+
			"do not merge it; the user's checked-out branch and files there are not yours to change", path, branch)
	}
	return b.String()
}

// renamePath is how a folder is moved on disk; a variable so a test can make it fail.
var renamePath = os.Rename

// pathExists reports whether anything is at path.
func pathExists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}
