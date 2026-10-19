package gateway

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/madkoding/motita/internal/gitforge"
	"github.com/madkoding/motita/internal/gitx"
	"github.com/madkoding/motita/internal/projectskills"
	"github.com/madkoding/motita/internal/schedule"
)

// handleListProjects answers every project this gateway knows about.
func (s *Server) handleListProjects(w http.ResponseWriter, _ *http.Request) {
	if s.projects == nil {
		writeJSON(w, http.StatusOK, map[string]any{"projects": []Project{}})
		return
	}
	all, err := s.projects.loadAll()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if all == nil {
		all = []Project{}
	}
	// The branch, the changes and the worktrees are all read live: they change
	// while the gateway runs (the user checks out another branch, edits a file,
	// a session is created), and a value persisted at creation time would be a
	// value that used to be true. Reading them here is a fixed number of git
	// calls per project, and a project that is not a repository answers "" and
	// zero - which omitempty renders as absent.
	//
	// The session count comes from the sessions this gateway holds, not from
	// disk: what a project header shows is how many sessions are under it right
	// now, and a persisted count would drift from the list drawn below it.
	sessions := s.snapshot()
	for i := range all {
		ctx := context.Background()
		all[i].Branch = gitx.Display(ctx, all[i].Dir)
		all[i].MainBranch = mainBranchOf(ctx, &all[i])
		all[i].MainWarning = s.mainRefresh.warning(all[i].ID)
		all[i].Changes, _ = gitx.WorkingTreeChanges(ctx, all[i].Dir)
		liveRepos(ctx, &all[i])
		all[i].MissingDeps = missingDeps(&all[i])
		// Worktrees counts the session checkouts, and it is asked of git rather
		// than of the session list so that a worktree left behind by a session
		// that is gone is still counted - which is the state a user wants to
		// know about. The project's own checkout is not a session worktree and
		// is not counted.
		//
		// It is counted against the sessions this gateway still HOLDS, though:
		// a registration whose session is gone is not something the project can
		// name or clean up, and reporting it as a live worktree gives a number
		// the user cannot reconcile with anything on disk. Measured: a project
		// with every session deleted still said "worktrees: 1".
		live := liveSessionIDsFor(sessions, all[i].ID)
		if wts, err := gitx.Worktrees(ctx, all[i].Dir); err == nil {
			all[i].Worktrees = countSessionWorktrees(wts, all[i].Dir, live)
		}
		for _, c := range sessions {
			if c.projectID == all[i].ID {
				all[i].Sessions++
			}
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"projects": all})
}

// liveSessionIDsFor collects the ids of the sessions the gateway holds for one
// project, as the set the worktree count is checked against.
//
// A nil map would mean "cannot say", and this caller CAN: the snapshot it was
// given is the same list the session count above it comes from.
func liveSessionIDsFor(sessions []*conversation, projectID string) map[string]bool {
	live := map[string]bool{}
	for _, c := range sessions {
		if c.projectID == projectID {
			live[c.id] = true
		}
	}
	return live
}

// podmanInstalled reports whether podman is on the PATH. It is a variable so a
// test can say what the machine has without installing anything.
var podmanInstalled = func() bool {
	_, err := exec.LookPath("podman")
	return err == nil
}

// podmanWorks reports whether podman actually answers, which is a different question from
// whether the binary is there: a rootless setup that was never initialised, or a machine with no
// user namespaces, has the binary and cannot run anything. Offering podman there would be
// offering something that fails the first time the agent uses it.
var podmanWorks = func() bool {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return exec.CommandContext(ctx, "podman", "info").Run() == nil
}

// podmanComposeInstalled reports whether `podman compose` works, which needs a
// compose provider as well as podman. Also a variable, for the same reason.
var podmanComposeInstalled = func() bool {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return exec.CommandContext(ctx, "podman", "compose", "version").Run() == nil
}

// handleRuntimes tells the client which container runtimes this machine has, so
// the New project dialog only offers the ones that can be used.
func (s *Server) handleRuntimes(w http.ResponseWriter, _ *http.Request) {
	// Each answer is only asked when the one before it was yes: `podman info` can take seconds
	// and there is nothing to ask a binary that is not there.
	hasPodman := podmanInstalled()
	ready := hasPodman && podmanWorks()
	writeJSON(w, http.StatusOK, map[string]bool{
		"podman":         hasPodman,
		"podman_ready":   ready,
		"podman_compose": ready && podmanComposeInstalled(),
	})
}

// newProjectFolder is the absolute folder of a project being created: the absolute path chosen
// outside the workspace, or the folder name chosen inside it.
func (s *Server) newProjectFolder(w http.ResponseWriter, path, dir string) (string, bool) {
	if strings.TrimSpace(path) != "" {
		return s.newExternalFolder(w, path)
	}
	name, ok := projectFolderName(w, dir)
	if !ok {
		return "", false
	}
	if s.opts.WorkspaceDir == "" {
		writeError(w, http.StatusInternalServerError, "no workspace directory is configured")
		return "", false
	}
	return filepath.Join(s.opts.WorkspaceDir, name), true
}

// handleCreateProject mints a new project, optionally cloning a git repo.
//
// The request body carries:
//   - title: required, the human-readable name
//   - description: optional
//   - dir: a subfolder name under the workspace (relative, no path separators)
//   - git_url: optional; when set, the repo is cloned into dir
//   - use_podman: optional; the user's answer to "run this project with podman?",
//     honoured only when podman is installed
//
// When git_url is set, dir is used as the destination folder name and the
// clone happens synchronously. When git_url is empty, the folder is created
// under the workspace if it does not exist.
func (s *Server) handleCreateProject(w http.ResponseWriter, r *http.Request) {
	if s.projects == nil {
		writeError(w, http.StatusNotImplemented, "this gateway was started without a project directory")
		return
	}
	var body struct {
		Title       string `json:"title"`
		Description string `json:"description"`
		Dir         string `json:"dir"`
		// Path is an absolute folder outside the workspace, allowed only with the whole disk
		// reachable (Settings → Security). It replaces Dir.
		Path string `json:"path"`
		// ConfirmSensitive says the person knows the folder is inside the system's own files.
		ConfirmSensitive bool   `json:"confirm_sensitive"`
		GitURL           string `json:"git_url"`
		UsePodman        bool   `json:"use_podman"`
		// MainBranch, GitUserName and GitUserEmail are no longer used: a project is a plain folder,
		// and its branch and git identity belong to the repositories it links. Clients that still
		// send them are not refused.
		MainBranch   string `json:"main_branch"`
		GitUserName  string `json:"git_user_name"`
		GitUserEmail string `json:"git_user_email"`
	}
	if !s.decodeBody(w, r, &body) {
		return
	}
	title := strings.TrimSpace(body.Title)
	if title == "" {
		writeError(w, http.StatusBadRequest, "the title cannot be empty")
		return
	}
	absDir, ok := s.newProjectFolder(w, body.Path, body.Dir)
	if !ok || !sensitiveNeedsConfirm(w, absDir, body.ConfirmSensitive) {
		return
	}

	gitURL := strings.TrimSpace(body.GitURL)
	repoName := repoNameOf("", gitURL)
	if gitURL != "" && repoName == "" {
		writeError(w, http.StatusBadRequest, "the repository needs a simple folder name: letters, digits, '.', '_' or '-'")
		return
	}
	// A project is a plain folder: nothing is initialised in it, whatever git has configured.
	if !s.refuseSharedDir(w, absDir) {
		return
	}
	madeFolder := !pathExists(absDir)
	if err := os.MkdirAll(absDir, 0o755); err != nil {
		writeError(w, http.StatusInternalServerError, "could not create the project directory: "+err.Error())
		return
	}

	id, err := newSessionID()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	p := Project{
		ID:          id,
		Title:       title,
		Description: strings.TrimSpace(body.Description),
		Dir:         absDir,
		Adopted:     !madeFolder,
		Podman:      body.UsePodman && podmanInstalled(),
		Created:     time.Now(),
	}
	if err := s.projects.save(p); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	var cloneLog string
	if gitURL != "" {
		var ok bool
		if p, cloneLog, ok = s.cloneIntoNewProject(w, r, p, gitURL, repoName, madeFolder); !ok {
			return
		}
	}
	if cur := s.projectOf(p.ID); cur != nil {
		p = *cur
	}
	s.indexNewProject(p.Dir)
	writeJSON(w, http.StatusCreated, map[string]any{
		"id":          p.ID,
		"title":       p.Title,
		"description": p.Description,
		"dir":         p.Dir,
		"repos":       p.Repos,
		"podman":      p.Podman,
		"created":     p.Created,
		"clone_log":   cloneLog,
	})
}

// projectFolderName reads the folder name of a new project. The folder must be a simple name,
// not a path: a session inside a project works in that folder, and a relative path with
// separators would let the agent escape the workspace. It answers the request itself, and
// returns false, when the name is not usable.
func projectFolderName(w http.ResponseWriter, raw string) (string, bool) {
	dirName := strings.TrimSpace(raw)
	if dirName == "" {
		writeError(w, http.StatusBadRequest, "the folder name cannot be empty")
		return "", false
	}
	if strings.ContainsAny(dirName, "/\\..") || !filepath.IsLocal(dirName) {
		writeError(w, http.StatusBadRequest, "the folder name must be a simple name, not a path")
		return "", false
	}
	if reservedFolders[strings.ToLower(dirName)] {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("%q is where motita keeps its sessions and shared repositories: "+
			"choose another folder name", dirName))
		return "", false
	}
	return dirName, true
}

// reservedFolders are the workspace's own folders, which hold the worktrees and folders of every
// session of every project, and the repositories they share. A project there would run its
// sessions among all the others' work, and deleting it would delete that work.
var reservedFolders = map[string]bool{"worktrees": true, "sessions": true, catalogFolder: true}

// projectInDir is the project whose folder is dir, inside it or around it, nil when there is
// none. Two projects on one checkout would share its branches, its files and its skills, and
// deleting one would take the other's folder with it: every project is its own folder.
func (s *Server) projectInDir(dir, except string) *Project {
	if s.projects == nil || dir == "" {
		return nil
	}
	all, err := s.projects.loadAll()
	if err != nil {
		return nil
	}
	for i := range all {
		p := &all[i]
		if p.ID == except || p.Dir == "" {
			continue
		}
		if gitx.SamePath(p.Dir, dir) || pathWithin(p.Dir, dir) || pathWithin(dir, p.Dir) {
			return p
		}
	}
	return nil
}

// pathWithin reports whether path is strictly inside root.
func pathWithin(root, path string) bool {
	rel, err := filepath.Rel(filepath.Clean(root), filepath.Clean(path))
	return err == nil && !relLeavesRoot(rel) && !filepath.IsAbs(rel)
}

// refuseSharedDir answers 409, and returns false, when dir is another project's folder.
func (s *Server) refuseSharedDir(w http.ResponseWriter, dir string) bool {
	if other := s.projectInDir(dir, ""); other != nil {
		writeError(w, http.StatusConflict, fmt.Sprintf("that folder is already used by the project %q: "+
			"each project works in a folder of its own", other.Title))
		return false
	}
	return true
}

// handleUpdateProject changes the user's podman answer after the project exists.
// Turning it on needs podman on the machine, and the sessions already open under
// the project are told at once, so the next turn follows the new answer.
func (s *Server) handleUpdateProject(w http.ResponseWriter, r *http.Request) {
	p := s.projectOf(r.PathValue("id"))
	if p == nil {
		writeError(w, http.StatusNotFound, ErrProjectNotFound.Error())
		return
	}
	var body projectUpdate
	if !s.decodeBody(w, r, &body) {
		return
	}
	if !body.applyPR(w, p) || !body.applyDetails(w, p) || !s.applyFolder(w, body, p) ||
		!body.applyMainBranch(w, r, p) {
		return
	}
	// Checking the branch above can take a while; a repository added or removed meanwhile is in
	// the stored project, not in this copy, so only the fields this request changes are written
	// onto the project as it is now.
	validated := *p
	p, err := s.updateProject(validated.ID, func(cur *Project) { body.copyTo(cur, &validated) })
	if errors.Is(err, errProjectGone) {
		writeError(w, http.StatusNotFound, ErrProjectNotFound.Error())
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	for _, c := range s.sessionsOfProject(p.ID) {
		applyRuntimeTo(c.svc, p, c.workspaceDir())
	}
	// With no sessions the checkout belongs on the main branch, including a main branch just chosen.
	s.returnToMain(p)
	writeJSON(w, http.StatusOK, p)
}

// projectUpdate is the body of handleUpdateProject. Every field is optional: a client changes
// what it sends and nothing else.
type projectUpdate struct {
	Title       *string `json:"title"`
	Description *string `json:"description"`
	Podman      *bool   `json:"podman"`
	// Path points the project at another folder, outside the workspace. Nothing is moved: the
	// project is anchored to the folder, as when it is created there.
	Path *string `json:"path"`
	// ConfirmSensitive says the person knows the folder is inside the system's own files.
	ConfirmSensitive bool    `json:"confirm_sensitive"`
	MainBranch       *string `json:"main_branch"`
	MergeMethod      *string `json:"merge_method"`
	PRMaxFixes       *int    `json:"pr_max_fixes"`
	AutoMerge        *bool   `json:"auto_merge"`
	AutoContinue     *bool   `json:"auto_continue"`
}

// copyTo writes onto dst the fields this request set, taking their validated values from src.
func (u projectUpdate) copyTo(dst, src *Project) {
	if u.Title != nil {
		dst.Title = src.Title
	}
	if u.Description != nil {
		dst.Description = src.Description
	}
	if u.Podman != nil {
		dst.Podman = src.Podman
	}
	if u.Path != nil {
		dst.Dir = src.Dir
	}
	if u.MainBranch != nil {
		dst.MainBranch = src.MainBranch
	}
	if u.MergeMethod != nil {
		dst.MergeMethod = src.MergeMethod
	}
	if u.PRMaxFixes != nil {
		dst.PRMaxFixes = src.PRMaxFixes
	}
	if u.AutoMerge != nil {
		dst.AutoMerge = src.AutoMerge
	}
	if u.AutoContinue != nil {
		dst.AutoContinue = src.AutoContinue
	}
}

// applyPR applies the pull-request settings to p. It answers the request itself, and returns
// false, when one is not valid.
func (u projectUpdate) applyPR(w http.ResponseWriter, p *Project) bool {
	if u.MergeMethod != nil {
		m := strings.TrimSpace(*u.MergeMethod)
		if m != "" && !slices.Contains(gitforge.MergeMethods, m) {
			writeError(w, http.StatusBadRequest, "the merge method must be merge, squash or rebase")
			return false
		}
		p.MergeMethod = m
	}
	if u.PRMaxFixes != nil {
		if *u.PRMaxFixes < 0 || *u.PRMaxFixes > maxPRFixesLimit {
			writeError(w, http.StatusBadRequest,
				fmt.Sprintf("the attempts to fix a CI must be between 0 (the default) and %d", maxPRFixesLimit))
			return false
		}
		p.PRMaxFixes = *u.PRMaxFixes
	}
	if u.AutoMerge != nil {
		p.AutoMerge = *u.AutoMerge
	}
	if u.AutoContinue != nil {
		p.AutoContinue = *u.AutoContinue
	}
	return true
}

// applyFolder anchors p to the folder the request names. A project with sessions or with other
// repositories stays where it is: their worktrees and folders hang from the old one. It answers
// the request itself, and returns false, when the change is refused.
func (s *Server) applyFolder(w http.ResponseWriter, u projectUpdate, p *Project) bool {
	if u.Path == nil {
		return true
	}
	dir, ok := s.externalProjectFolder(w, *u.Path)
	if !ok {
		return false
	}
	if gitx.SamePath(dir, p.Dir) {
		return true
	}
	if len(s.sessionsOfProject(p.ID)) > 0 || len(p.Repos) > 0 {
		writeError(w, http.StatusConflict, "the folder of a project with sessions or other repositories "+
			"cannot change: close its sessions and remove the other repositories first")
		return false
	}
	if other := s.projectInDir(dir, p.ID); other != nil {
		writeError(w, http.StatusConflict, fmt.Sprintf("that folder belongs to the project %q", other.Title))
		return false
	}
	if !sensitiveNeedsConfirm(w, dir, u.ConfirmSensitive) {
		return false
	}
	p.Dir, p.Adopted = dir, true
	return true
}

// applyDetails applies the title, the description and the podman answer to p. It answers the
// request itself, and returns false, when one is not valid.
func (u projectUpdate) applyDetails(w http.ResponseWriter, p *Project) bool {
	if u.Title != nil {
		title := strings.TrimSpace(*u.Title)
		if title == "" {
			writeError(w, http.StatusBadRequest, "the title cannot be empty")
			return false
		}
		p.Title = title
	}
	if u.Description != nil {
		p.Description = strings.TrimSpace(*u.Description)
	}
	if u.Podman != nil {
		if *u.Podman && !podmanInstalled() {
			writeError(w, http.StatusConflict, "podman is not installed on this machine")
			return false
		}
		p.Podman = *u.Podman
	}
	return true
}

// applyMainBranch applies the main branch to p. It answers the request itself, and returns false,
// when the branch is not valid.
func (u projectUpdate) applyMainBranch(w http.ResponseWriter, r *http.Request, p *Project) bool {
	if u.MainBranch == nil {
		return true
	}
	branch := strings.TrimSpace(*u.MainBranch)
	if !gitx.ValidBranch(r.Context(), branch) {
		writeError(w, http.StatusBadRequest, "the main branch is not a valid branch name")
		return false
	}
	// In a repository the branch has to be one it has (or the one an empty repository is
	// on): the project goes back to it, and a name git cannot check out would leave it
	// stranded. A folder that is not a repository only remembers the name.
	if gitx.Repo(r.Context(), p.Dir) == nil && !branchAvailable(r.Context(), p.Dir, branch) {
		writeError(w, http.StatusBadRequest, "this project has no branch named "+branch)
		return false
	}
	p.MainBranch = branch
	return true
}

// handleProjectBranches lists what the main-branch selector can offer for a project.
func (s *Server) handleProjectBranches(w http.ResponseWriter, r *http.Request) {
	p := s.projectOf(r.PathValue("id"))
	if p == nil {
		writeError(w, http.StatusNotFound, ErrProjectNotFound.Error())
		return
	}
	ctx := r.Context()
	branches := []string{}
	if gitx.Repo(ctx, p.Dir) == nil {
		if list, err := gitx.Branches(ctx, p.Dir); err == nil {
			branches = list
		}
	}
	main := mainBranchOf(ctx, p)
	// The configured branch is always offered, even before the repository has a commit on it.
	if main != "" && !containsString(branches, main) {
		branches = append([]string{main}, branches...)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"branches":    branches,
		"main_branch": main,
		"current":     gitx.Display(ctx, p.Dir),
	})
}

// defaultMainBranch is the main branch of a project nobody chose one for.
const defaultMainBranch = "main"

func containsString(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// branchAvailable reports whether the repository at dir can be put on branch: it has it, locally
// or on origin, or it is the branch an empty repository is already on.
func branchAvailable(ctx context.Context, dir, branch string) bool {
	if list, err := gitx.Branches(ctx, dir); err == nil && containsString(list, branch) {
		return true
	}
	current, _, _ := gitx.Head(ctx, dir)
	return current == branch
}

// settleMainBranch picks the main branch of a repository that was just cloned: the requested
// one when it exists, else main, else master, else the branch the clone is on. The checkout is
// moved onto it.
func settleMainBranch(ctx context.Context, dir, want string) string {
	current, _, _ := gitx.Head(ctx, dir)
	list, _ := gitx.Branches(ctx, dir)
	for _, candidate := range []string{want, "main", "master"} {
		if containsString(list, candidate) {
			if candidate != current {
				if err := gitx.Checkout(ctx, dir, candidate); err != nil {
					return current
				}
			}
			return candidate
		}
	}
	if current != "" {
		return current
	}
	return want
}

// mainBranchOf is the project's main branch: the one saved, or - for a project from before the
// setting - the main/master branch the repository has. "" when there is none to name.
func mainBranchOf(ctx context.Context, p *Project) string {
	if p.MainBranch != "" {
		return p.MainBranch
	}
	if gitx.Repo(ctx, p.Dir) != nil {
		return ""
	}
	list, _ := gitx.Branches(ctx, p.Dir)
	for _, candidate := range []string{"main", "master"} {
		if containsString(list, candidate) {
			return candidate
		}
	}
	return ""
}

// returnToMain puts a project's checkout back on its main branch when the project has no
// sessions left (never in a folder of the user's own outside the workspace). It is deliberately
// timid: a checkout with uncommitted changes is left alone
// (switching would either fail or carry the work to another branch), and so is a project that
// still has a session, whose branch the user may be looking at. A failure is logged, not
// reported: the caller's own work (deleting a session, saving the project) already succeeded.
func (s *Server) returnToMain(p *Project) {
	if p == nil || len(s.sessionsOfProject(p.ID)) > 0 || s.usersFolder(p.Dir) {
		return
	}
	ctx := context.Background()
	main := mainBranchOf(ctx, p)
	if main == "" || gitx.Repo(ctx, p.Dir) != nil || gitx.Display(ctx, p.Dir) == main {
		return
	}
	if dirty, err := gitx.Dirty(ctx, p.Dir); err != nil || dirty {
		return
	}
	if err := gitx.Checkout(ctx, p.Dir, main); err != nil && s.opts.Log != nil {
		s.opts.Log.Warn("the project could not go back to its main branch",
			"project", p.Dir, "branch", main, "error", err.Error())
	}
}

// handleDeleteProject removes a project. Sessions that belong to it are NOT
// deleted: they remain, but their project_id becomes a dangling reference,
// which the front end renders as "no project".
//
// Their RUNS are stopped, though - and that is not the same decision. A session
// that stays behind with no project is still a session the user owns; a turn
// still executing against a project the user just removed is not. Leaving those
// running would keep writing into a workspace the user has deliberately let go of,
// and they are exactly the sessions nobody can reach afterwards, because the panel
// they were started from is gone.
func (s *Server) handleDeleteProject(w http.ResponseWriter, r *http.Request) {
	if s.projects == nil {
		writeError(w, http.StatusNotImplemented, "this gateway was started without a project directory")
		return
	}
	id := r.PathValue("id")
	if !validProjectID(id) {
		writeError(w, http.StatusNotFound, "there is no project with that id")
		return
	}
	// Stop every run that belongs to this project BEFORE its file is removed,
	// for the same reason a session deletion stops its own: the run's goroutine
	// still unwinds and saves. It also means a client that was watching one of
	// those sessions sees it end rather than freeze.
	//
	// The sessions are also DELETED, which is what the dialog has always promised
	// ("this project and all its sessions will be permanently deleted") and what
	// the handler did not do. Measured: every session survived its project,
	// pointing at an id nothing could resolve, invisible in the sidebar - which
	// groups sessions under their project - and still holding its worktree. No
	// user action could reach them again.
	//
	// Their checkouts follow the same rule as a single session's: uncommitted work
	// stops the whole deletion unless the user confirmed the discard with
	// `?force=1`. The work belongs to the user, not to the project record, and
	// deleting the record is not a decision to destroy it.
	if !isForced(r) {
		if dir := s.unpublishedInProject(r.Context(), s.projectOf(id)); dir != "" {
			writeError(w, http.StatusConflict, "the project has commits that exist nowhere else: push them, "+
				"or confirm the discard ("+dir+")")
			return
		}
		for dir := range s.discardedRepoChanges(r.Context(), s.projectOf(id)) {
			writeError(w, http.StatusConflict, "the project's repository "+dir+" has uncommitted changes: "+
				"commit them, or confirm the discard")
			return
		}
	}
	sessions := s.sessionsOfProject(id)
	if err := s.closeProjectSessions(sessions, isForced(r)); err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	// The project's own folder goes with it, so a later project of the same name does not find
	// the old files in the way. The deletion dialog is the confirmation.
	var projectDir string
	// A folder another project also stands on (a record from before folders were refused twice)
	// stays: it is that project's too.
	p := s.projectOf(id)
	if p != nil && !p.Adopted && s.ownsProjectDir(p.Dir) && s.projectInDir(p.Dir, id) == nil {
		projectDir = p.Dir
	}
	// Only now that every checkout has been proven safe to give back are the
	// sessions forgotten and the project removed. A partial deletion would leave
	// the sessions unreachable AND their worktrees gone.
	for _, c := range sessions {
		c.closeForGood()
		s.forgetCheckpoints(c)
		s.forget(c.id)
		s.deletePersistedSession(c.id)
	}
	if err := s.projects.delete(id); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.removeProjectArtifacts(id)
	s.releaseProjectFolder(r.Context(), p, projectDir)
	w.WriteHeader(http.StatusNoContent)
}

// unpublishedInProject is the first clone deleting p would remove that holds commits no remote
// has. A clean working tree says nothing about them. Only the clones added from a remote count:
// the project's own folder is meant to be removed, the deletion dialog being its confirmation,
// and a folder created locally has no remote to have published to.
func (s *Server) unpublishedInProject(ctx context.Context, p *Project) string {
	if p == nil {
		return ""
	}
	for _, repo := range p.Repos {
		if !s.deletesClone(repo) || gitx.Repo(ctx, repo.Dir) != nil {
			continue
		}
		if n, err := gitx.UnpublishedCommits(ctx, repo.Dir); err != nil || n != 0 {
			return repo.Dir
		}
	}
	return ""
}

// ownsProjectDir reports whether dir is a folder strictly inside the workspace, which is
// where this gateway creates project folders. Anything else (the workspace itself, a path
// registered from elsewhere) is never removed with its project.
func (s *Server) ownsProjectDir(dir string) bool {
	root := s.opts.WorkspaceDir
	if root == "" || dir == "" {
		return false
	}
	rel, err := filepath.Rel(root, dir)
	return err == nil && !relLeavesRoot(rel) && !filepath.IsAbs(rel)
}

// usersFolder reports whether dir is a folder of the user's own outside the workspace: one this
// gateway registered but did not create, whose branch it does not switch or update by itself.
func (s *Server) usersFolder(dir string) bool {
	return s.opts.WorkspaceDir != "" && dir != "" && !s.ownsProjectDir(dir)
}

// keepsFolder reports whether deleting p leaves its folder alone: it is outside the workspace, or
// it was already there when the project was made.
func (s *Server) keepsFolder(p *Project) bool {
	return p != nil && (p.Adopted || s.usersFolder(p.Dir))
}

// relLeavesRoot reports whether a path relative to a root names the root itself or a place
// outside it.
func relLeavesRoot(rel string) bool {
	return rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// projectOf returns the project a request is about, or nil when it does not exist.
func (s *Server) projectOf(id string) *Project {
	if s.projects == nil || id == "" {
		return nil
	}
	p, err := s.projects.load(id)
	if err != nil || p == nil {
		return nil
	}
	return p
}

// ErrProjectNotFound is returned when a session is created for a project that
// does not exist. It is a sentinel so the handler can answer 404 rather than 500.
var ErrProjectNotFound = errors.New("there is no project with that id")

// handleMergeSession starts an agent turn that integrates the session's work
// back into the project's base branch using git.
//
// Integration is now a task the LLM carries out, not a mechanical git operation:
// the agent receives the session and project context, commits any pending work
// to the session branch, and merges that branch into the project. When the run
// finishes and the project's HEAD has moved, the session is marked as merged.
//
// The caller receives an SSE stream exactly like /task, so the UI can show the
// agent's progress and the final summary in the chat.
func (s *Server) handleMergeSession(w http.ResponseWriter, r *http.Request) {
	c := convOf(r)
	if c.isMerged() {
		writeError(w, http.StatusConflict, "this session has already been integrated into the project")
		return
	}
	if c.workspace == "" || c.projectID == "" {
		writeError(w, http.StatusConflict, "this session does not belong to a project, so there is nothing to merge")
		return
	}
	p := s.projectOf(c.projectID)
	if p == nil {
		writeError(w, http.StatusNotFound, ErrProjectNotFound.Error())
		return
	}
	branch := sessionBranch(c.id)
	base := gitx.Display(r.Context(), p.Dir)
	if base == "" {
		writeError(w, http.StatusConflict, "the project has no base branch to merge into")
		return
	}
	// The project's checkout must not be sitting ON the session's branch. Git accepts
	// `merge --no-ff <branch>` when the checkout is already on that branch and answers
	// "Already up to date" with exit 0, so the merge reports success while nothing was
	// integrated - measured.
	if base == branch {
		writeError(w, http.StatusConflict,
			"the project's checkout is on this session's branch ("+branch+"), so there is nothing to integrate "+
				"and the merge would report a success that changed nothing; check the project out on its own branch first")
		return
	}
	// With nothing ahead and no uncommitted work, there is nothing to integrate. This check
	// avoids burning an LLM turn on a session that has no changes.
	ahead, _, _ := gitx.CommitsBetween(r.Context(), p.Dir, base, branch)
	changes, _ := gitx.WorkingTreeChanges(r.Context(), c.workspace)
	if len(ahead) == 0 && changes == 0 {
		writeError(w, http.StatusConflict, "this session has no work to integrate: its branch has no commits "+
			"the project's branch lacks and its worktree has no uncommitted changes")
		return
	}

	task := fmt.Sprintf(`Integrate this session's work into the project.

Follow the git-in-a-repository skill. Commit any pending changes on the session branch, `+
		`then merge the session branch into the project's base branch with a merge commit whose subject is `+
		`"chore(motita): integrate session %s".

Session ID: %s
Session branch: %s
Session worktree: %s
Project directory: %s
Project base branch: %s

Report the merge commit SHA and the files changed. If there is a conflict, abort the merge `+
		`and explain what needs to be resolved first.`, c.id, c.id, branch, c.workspace, p.Dir, base) +
		otherReposMergeNote(p, c.workspace, branch)

	s.startRunWithIntent(w, r, c, task, schedule.KindTask, runIntentMerge)
}

// handleContinueSession creates a fresh session from a merged one. It pulls the
// project's base branch and gives the new session its own worktree, so work can
// keep going without rewriting history that is already part of the project.
func (s *Server) handleContinueSession(w http.ResponseWriter, r *http.Request) {
	next, status, err := s.continueFrom(r.Context(), convOf(r))
	if err != nil {
		writeError(w, status, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, next.status())
}

// continueFrom opens a new session from a merged one, in a worktree of the project's updated base
// branch. The status is what to answer when it cannot.
func (s *Server) continueFrom(ctx context.Context, c *conversation) (*conversation, int, error) {
	if !c.isMerged() {
		return nil, http.StatusConflict, errors.New("this session has not been integrated yet; merge it first")
	}
	if c.workspace == "" || c.projectID == "" {
		return nil, http.StatusConflict,
			errors.New("this session does not belong to a project, so there is nothing to continue from")
	}
	p := s.projectOf(c.projectID)
	if p == nil {
		return nil, http.StatusNotFound, ErrProjectNotFound
	}
	base := gitx.Display(ctx, p.Dir)
	if base == "" {
		return nil, http.StatusConflict, errors.New("the project has no base branch to pull into")
	}
	// Bring the project's checkout up to date before branching from it. A session
	// that continues from stale code would build on top of a state the project has
	// already left behind, and the merge later would be harder for no reason.
	// A project with no origin has nothing to bring in, and a folder of the user's own is not
	// updated by itself: neither refuses the continuation.
	if gitx.HasRemote(ctx, p.Dir) && !s.usersFolder(p.Dir) {
		if err := gitx.PullFastForward(ctx, p.Dir, base); err != nil {
			return nil, http.StatusConflict, err
		}
	}
	newConv, err := s.createSession()
	if err != nil {
		if errors.Is(err, ErrCeilingReached) {
			return nil, http.StatusConflict, err
		}
		return nil, http.StatusNotImplemented, err
	}
	dir := p.Dir
	if !gitx.SamePath(c.workspace, c.projectDir) {
		dir = s.isolatedDir(ctx, p, newConv.id)
	}
	newConv.setProjectID(c.projectID, dir, p.Dir)
	newConv.svc.SetWorkspace(dir)
	// The title says where it came from, but only if the old session had a real one.
	if !isPlaceholderTitle(c.title) {
		newConv.setTitle("continue: " + c.title)
	}
	scopeProceduresTo(newConv.svc, projectskills.ProjectDirFor(p.Dir, dir))
	applyRuntimeTo(newConv.svc, p, dir)
	s.saveSession(newConv)
	return newConv, http.StatusCreated, nil
}

// isolatedDir gives a continued session the worktree its predecessor had: an isolated session
// continues isolated. A worktree that cannot be made falls back to the project's folder.
func (s *Server) isolatedDir(ctx context.Context, p *Project, id string) string {
	dir := p.Dir
	if wt, err := s.sessionWorktree(ctx, p.Dir, id); err != nil {
		if s.opts.Log != nil {
			s.opts.Log.Warn("the continued session will run in the project directory: its own worktree could not be created",
				"id", id, "project", p.Dir, "error", err.Error())
		}
	} else {
		dir = wt
	}
	s.attachProjectRepos(ctx, p, dir, id)
	return dir
}
