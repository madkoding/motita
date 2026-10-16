package gateway

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
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

// ProjectRepo is one of a project's other repositories.
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

// sessionRepoPath is where a session that runs in workspace finds the project's repository repo.
func sessionRepoPath(workspace string, p *Project, repo ProjectRepo) string {
	return filepath.Join(workspace, repo.relPath(p.Dir))
}

// handleAddProjectRepo clones one more repository into a project. The body carries git_url
// (required) and name (optional: the URL's last element). The sessions the project already has
// get a worktree of it at once, so the next turn can use it.
func (s *Server) handleAddProjectRepo(w http.ResponseWriter, r *http.Request) {
	defer s.lockProjectRepos(r.PathValue("id"))()
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
	default:
		// A URL and a free folder name: the clone can start.
	}
	dest := filepath.Join(p.Dir, name)
	if _, err := os.Lstat(dest); err == nil {
		writeError(w, http.StatusConflict, fmt.Sprintf("the project already has a folder called %q: "+
			"choose another name for the repository", name))
		return
	}
	// The catalog is one folder for every project: a second project asking for the same repository
	// waits for the clone of the first instead of finding it half made, and a deletion cannot
	// take the clone between this project's use of it and the save that records it.
	s.catalogMu.Lock()
	defer s.catalogMu.Unlock()
	ctx := r.Context()
	repo, err := s.placeRepo(ctx, p, name, gitURL, dest)
	if err != nil {
		s.writeCloneError(w, gitURL, err)
		return
	}
	// The project's own repository must not see the folder as a change of its own. Advisory, as it
	// is for a worktree: a folder that is not a repository has nothing to ignore it in.
	_ = gitx.EnsureIgnoreBase(ctx, p.Dir)
	updated, err := s.updateProject(p.ID, func(cur *Project) { cur.Repos = append(cur.Repos, repo) })
	if errors.Is(err, errProjectGone) {
		// Deleted while the clone ran: the folder it went into is no longer anyone's.
		s.discardRepoFolder(ctx, p, repo)
		writeError(w, http.StatusNotFound, ErrProjectNotFound.Error())
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	p = updated
	for _, c := range s.sessionsOfProject(p.ID) {
		s.attachProjectRepos(ctx, p, c.workspaceDir(), c.id)
		applyRuntimeTo(c.svc, p, c.workspaceDir())
	}
	writeJSON(w, http.StatusCreated, p)
}

// errNoCatalog is catalogClone's answer when the workspace has no catalog to clone into.
var errNoCatalog = errors.New("the workspace has no catalog of repositories")

// catalogFolder is the workspace folder that holds the repositories projects share.
const catalogFolder = "repos"

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
	dir = freeCloneDir(filepath.Join(root, cmp.Or(repoNameOf("", gitURL), name)), gitURL, env)
	if _, err := cloneGitRepo(gitURL, dir, env); err != nil {
		return "", false, err
	}
	return dir, true, nil
}

// placeRepo puts the repository gitURL in the project's folder, at dest: a worktree of the
// catalog's clone on the project's own branch. A repository with nothing to branch from yet (an
// empty one), or a workspace without a catalog, gets a clone of its own there instead.
func (s *Server) placeRepo(ctx context.Context, p *Project, name, gitURL, dest string) (ProjectRepo, error) {
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
		err = renamePath(src, dest)
	case err == nil || errors.Is(err, errNoCatalog):
		_, err = cloneGitRepo(gitURL, dest, s.gitCommandEnv())
	default:
		// The catalog's clone failed: that is the answer.
	}
	if err != nil {
		return ProjectRepo{}, err
	}
	mainBranch := settleMainBranch(ctx, dest, defaultMainBranch)
	return ProjectRepo{Name: name, GitURL: gitURL, Dir: dest, MainBranch: mainBranch}, nil
}

// handleRemoveProjectRepo takes a repository out of a project: its worktree in every session, and
// the project's folder of it (the project's worktree of a shared one, which stays in the catalog
// for the others, or its own clone). Uncommitted work in any of them stops it, unless the user
// confirmed the discard with `?force=1` - the same rule a session's own checkout follows.
func (s *Server) handleRemoveProjectRepo(w http.ResponseWriter, r *http.Request) {
	defer s.lockProjectRepos(r.PathValue("id"))()
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
	worktrees := s.sessionWorktreesOf(ctx, p, repo)
	if !isForced(r) {
		if dir, dirty := firstDirtyDir(ctx, append([]string{p.repoPath(repo)}, worktrees...)); dirty {
			writeError(w, http.StatusConflict, fmt.Sprintf(
				"the repository %s has uncommitted changes in %s (or they could not be read): "+
					"commit them, or confirm the discard", repo.Name, dir))
			return
		}
		if s.deletesClone(repo) && s.refuseUnpublished(w, ctx, repo.Dir) {
			return
		}
	}
	for _, path := range worktrees {
		// Best effort: the clone goes below, and a registration left behind is pruned with it.
		_ = worktreeRemove(ctx, repo.Dir, path, true)
	}
	updated, err := s.updateProject(p.ID, func(cur *Project) {
		cur.Repos = slices.DeleteFunc(cur.Repos, func(x ProjectRepo) bool { return x.Name == repo.Name })
	})
	if errors.Is(err, errProjectGone) {
		writeError(w, http.StatusNotFound, ErrProjectNotFound.Error())
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	p = updated
	s.discardRepoFolder(ctx, p, repo)
	for _, c := range s.sessionsOfProject(p.ID) {
		applyRuntimeTo(c.svc, p, c.workspaceDir())
	}
	writeJSON(w, http.StatusOK, p)
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

// discardRepoFolder is what a repository leaves behind when it goes: the project's worktree of a
// shared one (its branch only when nothing on it is unmerged: what was committed there is work), or
// the project's own clone.
func (s *Server) discardRepoFolder(ctx context.Context, p *Project, repo ProjectRepo) {
	if repo.Shared {
		// Best effort, like the sessions' worktrees.
		_ = worktreeRemove(ctx, repo.Dir, p.repoPath(repo), true)
		_ = gitx.DeleteBranch(ctx, repo.Dir, projectBranch(p, repo))
	} else if s.deletesClone(repo) {
		_ = os.RemoveAll(repo.Dir)
	} else {
		// A clone registered from outside the workspace is the user's: it stays where it is.
	}
}

// refuseUnpublished answers 409, and returns true, when dir holds commits no remote has: the
// working tree being clean says nothing about them, and deleting the clone loses them. The user
// confirms the discard with `?force=1`, as for uncommitted work.
func (s *Server) refuseUnpublished(w http.ResponseWriter, ctx context.Context, dir string) bool {
	n, err := gitx.UnpublishedCommits(ctx, dir)
	if err == nil && n == 0 {
		return false
	}
	writeError(w, http.StatusConflict, fmt.Sprintf("the repository has commits that exist nowhere else "+
		"(or they could not be counted): push them, or confirm the discard (%s)", dir))
	return true
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

// catalogUse is a project that uses a repository of the catalog.
type catalogUse struct {
	ID    string `json:"id"`
	Title string `json:"title"`
}

// catalogRepo is one repository of the catalog, and the projects that use it.
type catalogRepo struct {
	Name     string       `json:"name"`
	GitURL   string       `json:"git_url"`
	Dir      string       `json:"dir"`
	Projects []catalogUse `json:"projects"`
	// Size is what the clone takes on disk, in bytes.
	Size int64 `json:"size"`
}

// dirSize adds up the size of the files under dir. What cannot be read counts as nothing: the
// figure is a guide to what deleting a clone frees, not an inventory.
func dirSize(dir string) int64 {
	var n int64
	_ = filepath.WalkDir(dir, func(_ string, d fs.DirEntry, err error) error {
		if err == nil && d.Type().IsRegular() {
			if info, ierr := d.Info(); ierr == nil {
				n += info.Size()
			}
		}
		return nil
	})
	return n
}

// allProjects is every project the gateway knows, none when it has no store or cannot read it.
func (s *Server) allProjects() []Project {
	if s.projects == nil {
		return nil
	}
	all, _ := s.projects.loadAll()
	return all
}

// catalogUsers are the projects whose repositories include the catalog's clone at dir.
func catalogUsers(projects []Project, dir string) []catalogUse {
	users := []catalogUse{}
	for _, p := range projects {
		if slices.ContainsFunc(p.Repos, func(r ProjectRepo) bool { return r.Shared && gitx.SamePath(r.Dir, dir) }) {
			users = append(users, catalogUse{ID: p.ID, Title: p.Title})
		}
	}
	return users
}

// handleListRepos answers the catalog: every repository cloned there, and which projects use it,
// so a project can add one that is already here without cloning it again.
func (s *Server) handleListRepos(w http.ResponseWriter, _ *http.Request) {
	out := []catalogRepo{}
	root := s.catalogDir()
	entries, _ := os.ReadDir(root)
	var projects []Project
	if len(entries) > 0 {
		projects = s.allProjects()
	}
	for _, e := range entries {
		dir := filepath.Join(root, e.Name())
		gitURL := originOf(dir, s.gitCommandEnv())
		if !e.IsDir() || gitURL == "" {
			continue
		}
		out = append(out, catalogRepo{
			Name: e.Name(), GitURL: gitURL, Dir: dir, Projects: catalogUsers(projects, dir), Size: dirSize(dir),
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"repos": out})
}

// removeAll is how a clone leaves the catalog; a variable so a test can make it fail.
var removeAll = os.RemoveAll

// handleDeleteRepo deletes a clone of the catalog that no project uses any more. Uncommitted work
// in it stops the deletion, unless the user confirmed the discard with `?force=1`.
func (s *Server) handleDeleteRepo(w http.ResponseWriter, r *http.Request) {
	s.catalogMu.Lock()
	defer s.catalogMu.Unlock()
	dir := s.catalogRepoDir(r.PathValue("name"))
	if dir == "" {
		writeError(w, http.StatusNotFound, "the workspace has no repository with that name")
		return
	}
	if users := catalogUsers(s.allProjects(), dir); len(users) > 0 {
		titles := make([]string, len(users))
		for i, u := range users {
			titles[i] = u.Title
		}
		writeError(w, http.StatusConflict, fmt.Sprintf("the repository is used by %s: remove it from "+
			"those projects first", strings.Join(titles, ", ")))
		return
	}
	if _, dirty := firstDirtyDir(r.Context(), []string{dir}); dirty && !isForced(r) {
		writeError(w, http.StatusConflict, "the repository has uncommitted changes (or they could not be read): "+
			"commit them, or confirm the discard")
		return
	}
	if !isForced(r) && s.refuseUnpublished(w, r.Context(), dir) {
		return
	}
	if err := removeAll(dir); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
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
		// Advisory: a clone with no origin, or a merge made here, has nothing to bring in.
		_ = gitx.PullFastForward(ctx, repo.Dir, cmp.Or(repo.MainBranch, defaultMainBranch))
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
		if repo.Shared {
			b.WriteString(": shared with other projects, so its main branch is theirs too")
		}
	}
	b.WriteString("\nRun git inside the repository you changed, and commit each one on its own. " +
		"The project's own repository ignores these folders.")
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

// renamePath and initRepo are what moving the main repository does to the disk; variables so a
// test can make either fail.
var (
	renamePath = os.Rename
	initRepo   = gitx.InitOn
)

// mainRepoFolder is the folder name the project's own repository takes when it becomes one of the
// others: the last element of its URL, or of its folder. "" when neither makes a usable name.
func (p *Project) mainRepoFolder() string {
	if name := repoNameOf("", p.GitURL); name != "" {
		return name
	}
	return repoNameOf(filepath.Base(p.Dir), "")
}

// freeRepoFolder is name when the project has no repository or folder by it, name with "-main"
// after it when that is free, and "" when neither is (or name is empty).
func (p *Project) freeRepoFolder(name string) string {
	taken := func(n string) bool { return p.repoIndex(n) >= 0 || pathExists(filepath.Join(p.Dir, n)) }
	switch {
	case name == "":
		return ""
	case !taken(name):
		return name
	case !taken(name + "-main"):
		return name + "-main"
	default:
		return ""
	}
}

// handleDetachProjectMain takes the project's own repository out of the project's folder, into a
// folder of its own, so the project becomes a plain folder that holds its repositories, all alike.
// Nothing is deleted: the repository, its history and its uncommitted work move as they are.
//
// It is refused while a session is running, and while a session has its own copy of the main
// repository: that copy belongs to the repository that is moving, so the session has to be merged
// or deleted first.
func (s *Server) handleDetachProjectMain(w http.ResponseWriter, r *http.Request) {
	defer s.lockProjectRepos(r.PathValue("id"))()
	p := s.projectOf(r.PathValue("id"))
	if p == nil {
		writeError(w, http.StatusNotFound, ErrProjectNotFound.Error())
		return
	}
	ctx := r.Context()
	if !s.detachAllowed(w, ctx, p) {
		return
	}
	name := p.freeRepoFolder(p.mainRepoFolder())
	if name == "" {
		writeError(w, http.StatusConflict, "the project has no free folder name for its main repository")
		return
	}
	mainBranch := mainBranchOf(ctx, p)
	if err := moveMainRepo(p, name); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if mainBranch == "" {
		mainBranch = defaultMainBranch
	}
	if err := initRepo(ctx, p.Dir, mainBranch); err != nil {
		undoMoveMainRepo(p, name)
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	moved := ProjectRepo{Name: name, GitURL: p.GitURL, Dir: filepath.Join(p.Dir, name), MainBranch: mainBranch}
	_ = gitx.EnsureIgnoreBase(ctx, p.Dir)
	updated, err := s.updateProject(p.ID, func(cur *Project) {
		cur.Repos = append([]ProjectRepo{moved}, cur.Repos...)
		cur.GitURL = ""
		cur.MainBranch = mainBranch
	})
	if err != nil {
		// The project does not declare the move, so the disk must not show it either.
		undoMoveMainRepo(p, name)
	}
	if errors.Is(err, errProjectGone) {
		writeError(w, http.StatusNotFound, ErrProjectNotFound.Error())
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	p = updated
	for _, c := range s.sessionsOfProject(p.ID) {
		applyRuntimeTo(c.svc, p, c.workspaceDir())
	}
	writeJSON(w, http.StatusOK, p)
}

// detachAllowed answers 409, and returns false, while moving the main repository would pull the
// ground from under a session.
func (s *Server) detachAllowed(w http.ResponseWriter, ctx context.Context, p *Project) bool {
	for _, c := range s.sessionsOfProject(p.ID) {
		if c.isRunning() {
			writeError(w, http.StatusConflict, "a session of the project is running: wait for it to finish")
			return false
		}
	}
	all, err := gitx.Worktrees(ctx, p.Dir)
	if err != nil {
		writeError(w, http.StatusConflict, "the project's folder is not a repository, so there is no main "+
			"repository to move: "+err.Error())
		return false
	}
	for _, wt := range all {
		if !wt.Prunable && !gitx.SamePath(wt.Path, p.Dir) {
			writeError(w, http.StatusConflict, "a session has its own copy of the main repository: "+
				"merge or delete the sessions of the project first")
			return false
		}
	}
	return true
}

// pathExists reports whether anything is at path.
func pathExists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

// moveMainRepo moves everything in the project's folder into its subfolder name, except the other
// repositories, which stay where they are. A move that fails puts back what it had moved.
func moveMainRepo(p *Project, name string) error {
	stay := map[string]bool{name: true}
	for _, repo := range p.Repos {
		stay[strings.SplitN(filepath.ToSlash(repo.relPath(p.Dir)), "/", 2)[0]] = true
	}
	entries, err := os.ReadDir(p.Dir)
	if err != nil {
		return err
	}
	dest := filepath.Join(p.Dir, name)
	if err := os.Mkdir(dest, 0o755); err != nil {
		return err
	}
	var moved []string
	for _, e := range entries {
		if stay[e.Name()] {
			continue
		}
		if err := renamePath(filepath.Join(p.Dir, e.Name()), filepath.Join(dest, e.Name())); err != nil {
			for _, m := range moved {
				_ = renamePath(filepath.Join(dest, m), filepath.Join(p.Dir, m))
			}
			_ = os.Remove(dest)
			return err
		}
		moved = append(moved, e.Name())
	}
	return nil
}

// undoMoveMainRepo puts back what moveMainRepo moved into the subfolder name, after a step that
// came later failed: the repository this folder was initialised as is dropped, and the original
// (which is in the subfolder) takes its place. Nothing is touched unless the original is there.
func undoMoveMainRepo(p *Project, name string) {
	dest := filepath.Join(p.Dir, name)
	if !pathExists(filepath.Join(dest, ".git")) {
		return
	}
	_ = os.RemoveAll(filepath.Join(p.Dir, ".git"))
	entries, _ := os.ReadDir(dest)
	for _, e := range entries {
		_ = renamePath(filepath.Join(dest, e.Name()), filepath.Join(p.Dir, e.Name()))
	}
	_ = os.Remove(dest)
}

// catalogRepoDir is the folder of the catalog's clone called name, "" when there is none.
func (s *Server) catalogRepoDir(name string) string {
	root := s.catalogDir()
	dir := filepath.Join(root, name)
	if root == "" || repoNameOf(name, "") != name || !pathExists(dir) {
		return ""
	}
	return dir
}

// handleUpdateRepo brings the catalog's clone called name up to date with its origin, on the
// branch it has out, when it can move forward without a merge.
func (s *Server) handleUpdateRepo(w http.ResponseWriter, r *http.Request) {
	dir := s.catalogRepoDir(r.PathValue("name"))
	if dir == "" {
		writeError(w, http.StatusNotFound, "the workspace has no repository with that name")
		return
	}
	ctx := r.Context()
	if err := gitx.PullFastForward(ctx, dir, gitx.Display(ctx, dir)); err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleSyncProjectRepo brings the project's folder of a shared repository up to that
// repository's main branch: the one-click answer to a folder the list marks as behind. It only
// moves forward, and never over uncommitted work.
func (s *Server) handleSyncProjectRepo(w http.ResponseWriter, r *http.Request) {
	p := s.projectOf(r.PathValue("id"))
	if p == nil {
		writeError(w, http.StatusNotFound, ErrProjectNotFound.Error())
		return
	}
	i := p.repoIndex(r.PathValue("name"))
	if i < 0 || !p.Repos[i].Shared {
		writeError(w, http.StatusNotFound, "the project has no shared repository with that name")
		return
	}
	repo := p.Repos[i]
	ctx := r.Context()
	dir := p.repoPath(repo)
	if on := gitx.Display(ctx, dir); on != projectBranch(p, repo) {
		writeError(w, http.StatusConflict, fmt.Sprintf("the folder is on %s, not on the project's branch %s: "+
			"switch it back first", on, projectBranch(p, repo)))
		return
	}
	if _, dirty := firstDirtyDir(ctx, []string{dir}); dirty {
		writeError(w, http.StatusConflict, "the folder has uncommitted changes: commit them first")
		return
	}
	mainBranch := cmp.Or(repo.MainBranch, defaultMainBranch)
	// Advisory: a clone with no origin has nothing to bring in.
	_ = gitx.PullFastForward(ctx, repo.Dir, mainBranch)
	if err := fastForward(ctx, dir, mainBranch); err != nil {
		writeError(w, http.StatusConflict, fmt.Sprintf("the folder has commits of its own that %s does not: "+
			"merge %s into it to bring it up to date", mainBranch, mainBranch))
		return
	}
	liveRepos(ctx, p)
	writeJSON(w, http.StatusOK, p)
}
