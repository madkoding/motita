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
		all[i].Changes, _ = gitx.WorkingTreeChanges(ctx, all[i].Dir)
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
		GitURL      string `json:"git_url"`
		UsePodman   bool   `json:"use_podman"`
		// MainBranch is the branch the project treats as its main line: "main" unless the user
		// picked another (typically "master").
		MainBranch string `json:"main_branch"`
		// GitUserName and GitUserEmail answer the "git_identity_required" refusal:
		// they are saved as the user's global git identity.
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
	dirName := strings.TrimSpace(body.Dir)
	if dirName == "" {
		writeError(w, http.StatusBadRequest, "the folder name cannot be empty")
		return
	}
	// The folder must be a simple name, not a path: a session inside a project
	// works in that folder, and a relative path with separators would let the
	// agent escape the workspace.
	if strings.ContainsAny(dirName, "/\\..") {
		writeError(w, http.StatusBadRequest, "the folder name must be a simple name, not a path")
		return
	}

	// The default needs no check, and not asking git for it keeps a machine without git failing
	// where it always did (initialising the repository) rather than here.
	mainBranch := strings.TrimSpace(body.MainBranch)
	if mainBranch == "" {
		mainBranch = defaultMainBranch
	} else if !gitx.ValidBranch(r.Context(), mainBranch) {
		writeError(w, http.StatusBadRequest, "the main branch is not a valid branch name")
		return
	}

	// Resolve the absolute directory under the workspace.
	workspace := s.opts.WorkspaceDir
	if workspace == "" {
		writeError(w, http.StatusInternalServerError, "no workspace directory is configured")
		return
	}
	absDir := filepath.Join(workspace, dirName)

	gitURL := strings.TrimSpace(body.GitURL)
	var cloneLog string
	if gitURL != "" {
		// A folder that holds something else is not overwritten or refused: the clone goes to
		// the next free "<name>-2", "<name>-3"... and the project records that path.
		absDir = freeCloneDir(absDir, gitURL, s.gitCommandEnv())
		// Clone the repo into the directory.
		var err error
		cloneLog, err = cloneGitRepo(gitURL, absDir, s.gitCommandEnv())
		if err != nil {
			s.writeCloneError(w, gitURL, err)
			return
		}
		// A clone arrives on the remote's default branch, which may not be the one asked for:
		// the main branch becomes the requested one when the repository has it, and otherwise
		// whichever of main/master it does have.
		mainBranch = settleMainBranch(r.Context(), absDir, mainBranch)
	} else {
		// The folder is created in the workspace when it does not exist, and a
		// folder that is new or empty becomes a git repository. A folder that
		// already holds files is somebody's work and is not turned into one.
		if willInit(absDir) && !s.ensureGitIdentity(w, r, body.GitUserName, body.GitUserEmail) {
			return
		}
		if err := os.MkdirAll(absDir, 0o755); err != nil {
			writeError(w, http.StatusInternalServerError, "could not create the project directory: "+err.Error())
			return
		}
		if entries, err := os.ReadDir(absDir); err == nil && len(entries) == 0 {
			if err := gitx.InitOn(r.Context(), absDir, mainBranch); err != nil {
				writeError(w, http.StatusInternalServerError, err.Error())
				return
			}
		}
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
		GitURL:      gitURL,
		Podman:      body.UsePodman && podmanInstalled(),
		MainBranch:  mainBranch,
		Created:     time.Now(),
	}
	if err := s.projects.save(p); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"id":          p.ID,
		"title":       p.Title,
		"description": p.Description,
		"dir":         p.Dir,
		"git_url":     p.GitURL,
		"podman":      p.Podman,
		"main_branch": p.MainBranch,
		"created":     p.Created,
		"clone_log":   cloneLog,
	})
}

// errGitIdentityRequired is the machine-readable reason a project was not
// created: git has no user, and the client has to ask for one.
const errGitIdentityRequired = "git_identity_required"

// willInit reports whether creating a project in dir will make it a repository:
// the folder is missing or holds nothing.
func willInit(dir string) bool {
	entries, err := os.ReadDir(dir)
	return errors.Is(err, os.ErrNotExist) || (err == nil && len(entries) == 0)
}

// ensureGitIdentity makes sure git has a user before a repository is created.
//
// The global configuration is what a commit is made under, so that is what is
// checked. When it is incomplete and the request brought no name and email, the
// answer is a 409 carrying errGitIdentityRequired - the client opens a dialog
// and repeats the request with them. It returns false once it has answered.
func (s *Server) ensureGitIdentity(w http.ResponseWriter, r *http.Request, name, email string) bool {
	if n, e := gitx.GlobalIdentity(r.Context()); n != "" && e != "" {
		return true
	}
	name, email = strings.TrimSpace(name), strings.TrimSpace(email)
	if name == "" && email == "" {
		writeJSON(w, http.StatusConflict, map[string]string{
			"error": "git has no user configured: a name and an email are needed to create the repository",
			"code":  errGitIdentityRequired,
		})
		return false
	}
	if name == "" || !strings.Contains(email, "@") || strings.ContainsAny(name+email, "\r\n") {
		writeError(w, http.StatusBadRequest, "the git name cannot be empty and the email must be an address")
		return false
	}
	if err := gitx.SetGlobalIdentity(r.Context(), name, email); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
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
	// Every field is optional: a client changes what it sends and nothing else.
	var body struct {
		Title        *string `json:"title"`
		Description  *string `json:"description"`
		Podman       *bool   `json:"podman"`
		MainBranch   *string `json:"main_branch"`
		MergeMethod  *string `json:"merge_method"`
		PRMaxFixes   *int    `json:"pr_max_fixes"`
		AutoMerge    *bool   `json:"auto_merge"`
		AutoContinue *bool   `json:"auto_continue"`
	}
	if !s.decodeBody(w, r, &body) {
		return
	}
	if body.MergeMethod != nil {
		m := strings.TrimSpace(*body.MergeMethod)
		if m != "" && !slices.Contains(gitforge.MergeMethods, m) {
			writeError(w, http.StatusBadRequest, "the merge method must be merge, squash or rebase")
			return
		}
		p.MergeMethod = m
	}
	if body.PRMaxFixes != nil {
		if *body.PRMaxFixes < 0 || *body.PRMaxFixes > maxPRFixesLimit {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("the attempts to fix a CI must be between 0 (the default) and %d", maxPRFixesLimit))
			return
		}
		p.PRMaxFixes = *body.PRMaxFixes
	}
	if body.AutoMerge != nil {
		p.AutoMerge = *body.AutoMerge
	}
	if body.AutoContinue != nil {
		p.AutoContinue = *body.AutoContinue
	}
	if body.Title != nil {
		title := strings.TrimSpace(*body.Title)
		if title == "" {
			writeError(w, http.StatusBadRequest, "the title cannot be empty")
			return
		}
		p.Title = title
	}
	if body.Description != nil {
		p.Description = strings.TrimSpace(*body.Description)
	}
	if body.Podman != nil {
		if *body.Podman && !podmanInstalled() {
			writeError(w, http.StatusConflict, "podman is not installed on this machine")
			return
		}
		p.Podman = *body.Podman
	}
	if body.MainBranch != nil {
		branch := strings.TrimSpace(*body.MainBranch)
		if !gitx.ValidBranch(r.Context(), branch) {
			writeError(w, http.StatusBadRequest, "the main branch is not a valid branch name")
			return
		}
		// In a repository the branch has to be one it has (or the one an empty repository is
		// on): the project goes back to it, and a name git cannot check out would leave it
		// stranded. A folder that is not a repository only remembers the name.
		if gitx.Repo(r.Context(), p.Dir) == nil && !branchAvailable(r.Context(), p.Dir, branch) {
			writeError(w, http.StatusBadRequest, "this project has no branch named "+branch)
			return
		}
		p.MainBranch = branch
	}
	if err := s.projects.save(*p); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	for _, c := range s.sessionsOfProject(p.ID) {
		applyRuntimeTo(c.svc, p)
	}
	// With no sessions the checkout belongs on the main branch, including a main branch just chosen.
	s.returnToMain(p)
	writeJSON(w, http.StatusOK, p)
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
// sessions left. It is deliberately timid: a checkout with uncommitted changes is left alone
// (switching would either fail or carry the work to another branch), and so is a project that
// still has a session, whose branch the user may be looking at. A failure is logged, not
// reported: the caller's own work (deleting a session, saving the project) already succeeded.
func (s *Server) returnToMain(p *Project) {
	if p == nil || len(s.sessionsOfProject(p.ID)) > 0 {
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
	for _, c := range s.snapshot() {
		if c.projectID != id {
			continue
		}
		if stopped, settled := c.stopRunForDeletion(deleteStopTimeout); stopped && !settled {
			writeError(w, http.StatusConflict,
				"a run in one of this project's sessions did not stop in time, so the project was not deleted: stop it and try again")
			return
		}
	}
	discard := isForced(r)
	for _, c := range s.sessionsOfProject(id) {
		if err := s.releaseWorktree(c, discard); err != nil {
			// Refused, not forced: the message names the session and its files, so
			// the decision stays with the user. Nothing is forgotten yet, which is
			// why this runs BEFORE any session is dropped.
			writeError(w, http.StatusConflict, err.Error())
			return
		}
	}
	// The project's own folder goes with it, so a later project of the same name does not find
	// the old files in the way. The deletion dialog is the confirmation.
	var projectDir string
	if p := s.projectOf(id); p != nil && s.ownsProjectDir(p.Dir) {
		projectDir = p.Dir
	}
	// Only now that every checkout has been proven safe to give back are the
	// sessions forgotten and the project removed. A partial deletion would leave
	// the sessions unreachable AND their worktrees gone.
	for _, c := range s.sessionsOfProject(id) {
		s.forget(c.id)
		s.deletePersistedSession(c.id)
	}
	if err := s.projects.delete(id); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.removeProjectArtifacts(id)
	if projectDir != "" {
		// Best effort: the project is already gone, and a folder that resists (a file in use)
		// is left for the user rather than failing a deletion that has otherwise happened.
		_ = os.RemoveAll(projectDir)
	}
	w.WriteHeader(http.StatusNoContent)
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
	return err == nil && rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
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
	if c.merged {
		writeError(w, http.StatusConflict, "this session has already been integrated into the project")
		return
	}
	if c.workspace == "" {
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
			"the project's checkout is on this session's branch ("+branch+"), so there is nothing to integrate and the merge would report a success that changed nothing; check the project out on its own branch first")
		return
	}
	// With nothing ahead and no uncommitted work, there is nothing to integrate. This check
	// avoids burning an LLM turn on a session that has no changes.
	ahead, _, _ := gitx.CommitsBetween(r.Context(), p.Dir, base, branch)
	changes, _ := gitx.WorkingTreeChanges(r.Context(), c.workspace)
	if len(ahead) == 0 && changes == 0 {
		writeError(w, http.StatusConflict, "this session has no work to integrate: its branch has no commits the project's branch lacks and its worktree has no uncommitted changes")
		return
	}

	task := fmt.Sprintf(`Integrate this session's work into the project.

Follow the git-in-a-repository skill. Commit any pending changes on the session branch, then merge the session branch into the project's base branch with a merge commit whose subject is "chore(motita): integrate session %s".

Session ID: %s
Session branch: %s
Session worktree: %s
Project directory: %s
Project base branch: %s

Report the merge commit SHA and the files changed. If there is a conflict, abort the merge and explain what needs to be resolved first.`, c.id, c.id, branch, c.workspace, p.Dir, base)

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
	if !c.merged {
		return nil, http.StatusConflict, errors.New("this session has not been integrated yet; merge it first")
	}
	if c.workspace == "" {
		return nil, http.StatusConflict, errors.New("this session does not belong to a project, so there is nothing to continue from")
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
	if err := gitx.PullFastForward(ctx, p.Dir, base); err != nil {
		return nil, http.StatusConflict, err
	}
	newConv, err := s.createSession()
	if err != nil {
		if errors.Is(err, ErrCeilingReached) {
			return nil, http.StatusConflict, err
		}
		return nil, http.StatusNotImplemented, err
	}
	dir := p.Dir
	if wt, wtErr := s.sessionWorktree(ctx, p.Dir, newConv.id); wtErr != nil {
		if s.opts.Log != nil {
			s.opts.Log.Warn("the continued session will run in the project directory: its own worktree could not be created",
				"id", newConv.id, "project", p.Dir, "error", wtErr.Error())
		}
	} else {
		dir = wt
	}
	newConv.setProjectID(c.projectID, dir, p.Dir)
	newConv.svc.SetWorkspace(dir)
	// The title says where it came from, but only if the old session had a real one.
	if !isPlaceholderTitle(c.title) {
		newConv.setTitle("continue: " + c.title)
	}
	scopeProceduresTo(newConv.svc, projectskills.ProjectDirFor(p.Dir, dir))
	applyRuntimeTo(newConv.svc, p)
	s.saveSession(newConv)
	return newConv, http.StatusCreated, nil
}

// sessionBranch is the branch a session works on. The prefix is what makes the
// branches findable: `git branch --list 'motita/*'` lists exactly the sessions.
func sessionBranch(sessionID string) string {
	return "motita/" + sessionID
}

// countSessionWorktrees counts the checkouts in a worktree listing that are a
// SESSION's worktree of the project at projectDir.
//
// The project's own checkout is in the same listing and is not one: it is where
// the user works, not a session. It is told apart by path, which is the only
// thing that distinguishes the two - every entry in this listing is a checkout
// of the same repository, and a branch-based rule would both count the project's
// own branch and miss a session whose worktree has been moved.
//
// A prunable registration is NOT counted. Its directory is gone, so there is no
// checkout to speak of, and reporting it as a live worktree would give the user
// a number they cannot reconcile with anything on disk.
//
// The count is also bounded by what the PROJECT can still reach, and this is the
// half that was missing. Measured on a real gateway: a project whose sessions had
// all been deleted still reported "worktrees: 1", because a registration from a
// run whose checkout lived outside the workspace was counted like any other. A
// number the user cannot reconcile with anything they can see is worse than no
// number: it says work is out there and offers no way to find it.
//
// `live` are the session ids the gateway still holds. A registration whose path
// does not end in one of them is not a session's worktree any more - whatever it
// is, it is not something this project can name or clean up.
func countSessionWorktrees(all []gitx.Worktree, projectDir string, live map[string]bool) int {
	n := 0
	for _, w := range all {
		if w.Prunable {
			continue
		}
		if gitx.SamePath(w.Path, projectDir) {
			continue
		}
		if !isLiveSessionWorktree(w.Path, live) {
			continue
		}
		n++
	}
	return n
}

// isLiveSessionWorktree reports whether a checkout's path is a session of this
// gateway's worktree.
//
// The path is `<workspace>/worktrees/<session id>`, so the LAST element carries
// the id and that is what is asked about. The id has to be one the gateway still
// holds: a registration left by a deleted session, or by a session of a project
// that no longer exists, would otherwise keep the count up for ever.
//
// A nil map means the caller cannot say which sessions are live, and then the
// answer is "yes" for every checkout: hiding a real one is the worse mistake, and
// the number is decoration either way.
func isLiveSessionWorktree(path string, live map[string]bool) bool {
	if live == nil {
		return true
	}
	id := filepath.Base(filepath.Clean(path))
	return live[id]
}
