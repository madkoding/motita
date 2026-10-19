package gateway

import (
	"context"
	"fmt"
	"maps"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/madkoding/motita/internal/gitx"
)

// handleDeleteSession drops one conversation.
//
// The default one cannot be removed — it belongs to the process that started this
// gateway — but deleting it is treated as a reset: the transcript is cleared, the
// title is restored to the "New session" placeholder, and the persisted file is
// removed. Empty again, it is dormant and leaves the list, which is what a user who
// presses "delete" on it expects.
//
// isForced reports whether the request says the user has SEEN what will be discarded and
// means to discard it.
//
// Accepted spellings are the ones a client actually sends: `force=1`, `force=true`,
// `force=yes`. Anything else - including the parameter being absent - is NOT a
// confirmation, so a malformed or truncated request can only ever be safe.
func isForced(r *http.Request) bool {
	switch strings.ToLower(strings.TrimSpace(r.URL.Query().Get("force"))) {
	case "1", "true", "yes":
		return true
	default:
		return false
	}
}

// handleDeletionPreview answers what deleting this session would DISCARD, without
// deleting anything.
//
// The confirmation dialog needs this and it cannot get it anywhere else. A count in an
// error message arrives only AFTER the user has tried to delete and been refused, which
// is too late to inform the decision; and a client that guessed from the session's
// workspace path would be guessing at the rules this package is the only authority on.
//
// `changes` is a list rather than a number for the case that motivated it: the two
// changes in a real session were a build artefact and a lock file, and the user could not
// tell that from the number 2. The list is what makes "discard these" an answerable
// question.
//
// A session with no project, or one whose checkout has nothing uncommitted, answers with
// an empty list and nothing to confirm. A checkout whose changes could NOT be read is
// reported as such and never as empty: an empty list reads as "nothing to lose".
func (s *Server) handleDeletionPreview(w http.ResponseWriter, r *http.Request) {
	c := convOf(r)
	release := s.inspectWorktree(c)
	out := map[string]any{
		"session_id": c.id,
		"title":      c.status().Title,
		"worktree":   release.Workspace,
		"changes":    []gitx.Change{},
		"count":      0,
	}
	if release.Workspace != "" {
		out["branch"] = sessionBranch(c.id)
	}
	if len(release.Changes) > 0 {
		out["changes"] = release.Changes
		out["count"] = len(release.Changes)
	}
	if release.InspectErr != nil {
		// The list could not be read. Saying so is the only honest answer: 0 would be a
		// statement that there is nothing to lose, and it was not measured.
		out["inspection_failed"] = true
		out["error"] = fmt.Sprintf("the session's checkout at %s could not be inspected: %v",
			release.Workspace, release.InspectErr)
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleDeleteSession(w http.ResponseWriter, r *http.Request) {
	c := convOf(r)
	// A run in flight is STOPPED, not refused. Deleting a conversation is a
	// decision about that conversation, and the old answer - a 409 telling the
	// user to come back after the run finishes - made the decision conditional on
	// a turn that may run for minutes, with no way to end it from here.
	//
	// The two halves have to be in this order and both have to happen: the
	// cancellation kills what the agent is RUNNING (the sandbox process group, a
	// pending approval, the model call), and the wait makes sure the goroutine
	// has finished unwinding - it still appends its final events, generates an
	// auto-title and saves the session - before the conversation is forgotten and
	// its file removed. Deleting first would leave a turn writing into a session
	// that no longer exists.
	//
	// The conversation is marked closing FIRST: the run goroutine goes on after its
	// done channel closes, and without the mark its queue drain would start the next
	// message here and its auto-title would name the conversation just reset.
	c.beginClosing()
	if stopped, settled := c.stopRunForDeletion(deleteStopTimeout); stopped && !settled {
		s.reopenAndDrain(c, false)
		writeError(w, http.StatusConflict,
			"the run in this session did not stop in time, so the session was not deleted: stop it and try again")
		return
	}
	if c.id == DefaultSession {
		c.svc.ResetConversation()
		s.forgetCheckpoints(c)
		c.setTitle(placeholderTitle)
		s.deletePersistedSession(c.id)
		s.reopenAndDrain(c, true)
		writeJSON(w, http.StatusOK, c.status())
		return
	}
	// No "was it there?" branch: withConversation already proved it is, and a concurrent second
	// DELETE of the same session would find it gone - which is the outcome both callers asked
	// for. Reporting 404 to one of them would be reporting a race, not a fact about the session.
	//
	// The session's own CHECKOUT is given back with it. A session that belonged to a git
	// project worked in a worktree of it, and forgetting the conversation left the directory
	// and its registration behind: measured on a real gateway, six orphaned worktrees of one
	// project, two of them over 800 MB, none of which any session would ever touch again.
	// The branch is NOT deleted - that is a separate decision, and the work may be worth
	// keeping - but the registration cannot be left pointing at a session nobody has.
	//
	// `?force=1` is how the user says they SAW what would be discarded and mean to discard
	// it. It is a query parameter rather than a body because DELETE with a body is the form
	// proxies and clients disagree about, and this request has to arrive intact to be worth
	// anything. The preview the user read is served by GET .../deletion-preview.
	if err := s.releaseWorktree(c, isForced(r)); err != nil {
		// The removal is refused rather than forced when the checkout holds work that is
		// not committed and the user has not confirmed discarding it. Deleting the session
		// would otherwise destroy it silently, and a worktree with uncommitted changes is
		// exactly the state the user cannot recover from. The session is kept so the
		// decision stays theirs.
		s.reopenAndDrain(c, false)
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	c.closeForGood()
	s.releaseFreeSessionDir(c)
	s.forgetCheckpoints(c)
	s.forget(c.id)
	s.promoteThreads(c.id)
	s.deletePersistedSession(c.id)
	w.WriteHeader(http.StatusNoContent)
}

// sessionsOfProject returns the live sessions that belong to a project.
func (s *Server) sessionsOfProject(id string) []*conversation {
	out := []*conversation{}
	for _, c := range s.snapshot() {
		if c.projectID == id {
			out = append(out, c)
		}
	}
	return out
}

// handleProjectDeletionPreview answers what deleting this project would DISCARD,
// without deleting anything.
//
// It exists for the same reason the session's does, one level up. The dialog has
// always SAID "this project and all its sessions will be permanently deleted",
// and until now the handler did not delete the sessions at all: measured, every
// session was left alive pointing at a project id nothing could resolve, absent
// from the sidebar (which groups sessions under their project), and still holding
// its worktree. The dialog now describes what actually happens, and it can only
// do that if it can ask first.
//
// A session whose checkout could not be read is reported, and it must never be
// reported as clean: the refusal at deletion time depends on this same answer.
func (s *Server) handleProjectDeletionPreview(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if s.projects == nil {
		writeError(w, http.StatusNotImplemented, "this gateway was started without a project directory")
		return
	}
	project := s.projectOf(id)
	if project == nil {
		writeError(w, http.StatusNotFound, ErrProjectNotFound.Error())
		return
	}
	type sessionPreview struct {
		ID      string        `json:"id"`
		Title   string        `json:"title"`
		Changes []gitx.Change `json:"changes"`
	}
	out := struct {
		ProjectID        string           `json:"project_id"`
		Sessions         []sessionPreview `json:"sessions"`
		Count            int              `json:"count"`
		InspectionFailed bool             `json:"inspection_failed,omitempty"`
		Error            string           `json:"error,omitempty"`
		// KeepsFolder is set when the project's folder is the user's own, outside the workspace:
		// deleting the project forgets it and leaves the folder and its files alone.
		KeepsFolder bool   `json:"keeps_folder,omitempty"`
		Folder      string `json:"folder,omitempty"`
	}{ProjectID: id, Sessions: []sessionPreview{}}
	if s.keepsFolder(project) {
		out.KeepsFolder, out.Folder = true, project.Dir
	}

	for _, c := range s.sessionsOfProject(id) {
		release := s.inspectWorktree(c)
		entry := sessionPreview{ID: c.id, Title: c.status().Title, Changes: []gitx.Change{}}
		if len(release.Changes) > 0 {
			entry.Changes = release.Changes
			out.Count += len(release.Changes)
		}
		if release.InspectErr != nil {
			// One unreadable checkout is enough to make the whole answer
			// unreliable: the deletion would be refused over it, so the dialog
			// must not present it as confirmable.
			out.InspectionFailed = true
			out.Error = fmt.Sprintf("the checkout of session %s could not be inspected: %v",
				c.id, release.InspectErr)
		}
		out.Sessions = append(out.Sessions, entry)
	}
	// The project's folders of its other repositories go with it too, and so does what they hold.
	repoChanges := s.discardedRepoChanges(r.Context(), s.projectOf(id))
	for _, dir := range slices.Sorted(maps.Keys(repoChanges)) {
		out.Sessions = append(out.Sessions, sessionPreview{ID: "repo:" + dir, Title: dir, Changes: repoChanges[dir]})
		out.Count += len(repoChanges[dir])
	}
	writeJSON(w, http.StatusOK, out)
}

// worktreeInspectList names the uncommitted changes in a checkout. It is a package
// VARIABLE so a test can reach the branch where the list cannot be read — the state a
// revoked mount or a broken registration leaves, which no filesystem produces on demand.
//
// It replaced a count (`gitx.WorkingTreeChanges`, held as `worktreeInspect`) as the thing
// the deletion decision is made on. The count could only warn; the list is what lets
// someone answer "do I care about these?", which is the question a deletion asks. The
// count itself is still used where a number is the right answer, for a badge.
var worktreeInspectList = gitx.WorkingTreeChangeList

// worktreeRemove gives a checkout back to git. Also a variable, for the branch where git
// refuses a checkout that was already proven clean.
var worktreeRemove = gitx.RemoveWorktree

// worktreePrune clears stale registrations. A variable for the same reason as worktreeRemove.
var worktreePrune = gitx.PruneWorktrees

// forceRemoveCheckout deletes a session's checkout as a directory, once the user has
// confirmed discarding it and git would not remove it.
//
// It only ever deletes a path INSIDE this gateway's own worktrees directory: that is where
// sessionWorktree puts every checkout it makes, and a workspace anywhere else - the
// project's own checkout, a path a restored session carried in - is not a session's to
// delete. os.RemoveAll removes the dependency links sessionWorktree made, never what they
// point at.
func (s *Server) forceRemoveCheckout(projectDir, workspace string) error {
	root := filepath.Join(s.opts.WorkspaceDir, "worktrees")
	rel, err := filepath.Rel(root, workspace)
	if strings.TrimSpace(s.opts.WorkspaceDir) == "" || err != nil || relLeavesRoot(rel) {
		return fmt.Errorf("%s is not inside this gateway's worktrees directory", workspace)
	}
	if err := os.RemoveAll(workspace); err != nil {
		return err
	}
	// The directory is gone; a registration left pointing at it is harmless (the next
	// worktree add prunes it too), so a failed prune is logged and not reported.
	if perr := worktreePrune(context.Background(), projectDir); perr != nil && s.opts.Log != nil {
		s.opts.Log.Warn("the worktree registration could not be pruned", "project", projectDir, "error", perr.Error())
	}
	return nil
}

// worktreeRelease is what a session's checkout is worth giving back, and it is the answer
// a caller needs BEFORE asking the user to confirm.
//
// It is separated from the removal so the confirmation dialog can be told the truth
// without anything being deleted: the panel has to be able to say "these two files will be
// discarded" and list them, and it can only do that if the question can be asked on its
// own. Asking it by attempting the removal is not the same thing - it would delete the
// checkout it was merely describing.
type worktreeRelease struct {
	// Workspace is the session's checkout, empty when there is nothing to give back.
	Workspace string
	// ProjectDir is the repository the checkout belongs to, needed by the removal.
	ProjectDir string
	// Repos are the worktrees of the project's other repositories inside the checkout: each
	// repository's clone and the worktree's path.
	Repos [][2]string
	// Changes names what the checkout holds and has not committed.
	Changes []gitx.Change
	// Inspected is false when the count could not be read, which is NOT the same as a
	// clean checkout and must not be confirmed past.
	Inspected bool
	// InspectErr is why the inspection failed, when it did.
	InspectErr error
}

// inspectWorktree describes what deleting this session would give back, without
// changing anything.
//
// The rules match releaseWorktree exactly, and they have to: a preview that
// described a different decision than the one the removal makes would be worse
// than no preview. A session with no project, one running in the project's own
// checkout, and one whose worktree was already removed all have nothing to give
// back, and each answers with an empty Workspace.
func (s *Server) inspectWorktree(c *conversation) worktreeRelease {
	release := worktreeRelease{}
	projectDir := c.projectDir
	workspace := c.workspace
	if projectDir == "" || workspace == "" {
		return release
	}
	// The project's own checkout is not a session's to give back: removing it would delete
	// the user's project.
	if gitx.SamePath(workspace, projectDir) {
		return release
	}
	if _, ok, err := gitx.LiveWorktreeAt(context.Background(), projectDir, workspace); err != nil || !ok {
		return release
	}
	release.Workspace = workspace
	release.ProjectDir = projectDir
	release.Repos = sessionRepoWorktrees(context.Background(), s.projectOf(c.projectID), workspace)
	changes, err := worktreeInspectList(context.Background(), workspace)
	if err != nil {
		release.InspectErr = err
		return release
	}
	// The worktrees of the project's other repositories are part of the session's checkout, and
	// their work is lost with it just the same: their changes are listed, under their folder.
	for _, wt := range release.Repos {
		more, err := worktreeInspectList(context.Background(), wt[1])
		if err != nil {
			release.InspectErr = err
			return release
		}
		for _, ch := range more {
			rel, _ := filepath.Rel(workspace, wt[1])
			ch.Path = filepath.Join(rel, ch.Path)
			changes = append(changes, ch)
		}
	}
	release.Changes = changes
	release.Inspected = true
	return release
}

// releaseWorktree gives back the checkout a session was working in.
//
// It is best-effort about everything EXCEPT a checkout with uncommitted work, which is the
// one case that must not pass silently: the worktree is a copy of the project, and once
// the session is forgotten nothing points at it again.
//
// `discard` is the user's OWN answer to the question inspectWorktree asked. When it is
// true the uncommitted changes are destroyed on purpose, by someone who was shown the list
// of what they were discarding - which is a different act from a program losing them.
// Nothing in this package sets it on the user's behalf; it comes from a request that says
// so explicitly.
//
// A session with no project, or one whose worktree was already removed, has nothing to
// give back and this is a no-op.
func (s *Server) releaseWorktree(c *conversation, discard bool) error {
	release := s.inspectWorktree(c)
	workspace := release.Workspace
	if workspace == "" {
		return nil
	}

	// Uncommitted work stops it, UNLESS the user confirmed the discard. `git worktree
	// remove` refuses a dirty checkout by itself, but by then the user has lost the session
	// that named it and has no way to find out what was in it - so the check is made here,
	// where the message can say which session and which files.
	if !discard {
		if err := refuseUnsafeRelease(release); err != nil {
			return err
		}
	}
	if len(release.Changes) > 0 && s.opts.Log != nil {
		// A discard is a deletion of work. It is logged with the files, so the act is
		// recoverable as a FACT even though the files are not.
		s.opts.Log.Warn("the user confirmed discarding uncommitted work with the session",
			"id", c.id, "worktree", workspace, "changes", len(release.Changes))
	}

	// The other repositories' worktrees go first, so the session's own is left with nothing
	// nested in it. Best effort: removing the session's worktree removes their folders anyway,
	// and the registration git keeps of one is pruned once it is gone.
	for _, wt := range release.Repos {
		_ = worktreeRemove(context.Background(), wt[0], wt[1], discard)
	}
	defer func() {
		for _, wt := range release.Repos {
			_ = gitx.PruneWorktrees(context.Background(), wt[0])
		}
	}()
	// `force` is passed when the discard was confirmed: git refuses a dirty checkout, and
	// the refusal would otherwise arrive after the user had already said yes.
	err := worktreeRemove(context.Background(), release.ProjectDir, workspace, discard)
	if err != nil && discard {
		err = s.forceRelease(c, release.ProjectDir, workspace, err)
	}
	if err != nil {
		// git refused a checkout that was proven clean. That is reported rather than
		// forced, for the same reason: this program does not delete work it cannot prove
		// is safe to delete without the user's word, and the message names the command
		// that does.
		if s.opts.Log != nil {
			s.opts.Log.Warn("the session's worktree could not be removed",
				"id", c.id, "worktree", workspace, "error", err.Error())
		}
		return fmt.Errorf("the session's checkout at %s could not be removed (%v): "+
			"remove it with `git worktree remove %s` and delete the session again", workspace, err, workspace)
	}
	dropSessionBranches(context.Background(), c.id, release)
	if s.opts.Log != nil {
		s.opts.Log.Info("the session's worktree was released with it",
			"id", c.id, "worktree", workspace, "branch", sessionBranch(c.id))
	}
	return nil
}

// dropSessionBranches deletes the session's branch from the repositories it had a worktree of, once
// that worktree is gone: a branch per opened session would otherwise pile up in the project's
// repository, and in every shared or user's repository, for ever. Only a branch with nothing
// unmerged goes (`git branch -d`): commits that exist nowhere else are work, and the branch that
// holds them stays.
func dropSessionBranches(ctx context.Context, sessionID string, release worktreeRelease) {
	branch := sessionBranch(sessionID)
	dirs := []string{release.ProjectDir}
	for _, wt := range release.Repos {
		dirs = append(dirs, wt[0])
	}
	for _, dir := range distinctDirs(dirs) {
		// Advisory: unmerged work, or a branch the repository never had, keeps it.
		_ = gitx.DeleteBranch(ctx, dir, branch)
	}
}

// refuseUnsafeRelease is why a checkout cannot be released without the user's word: its
// uncommitted changes, or a count of them that could not be read. Uncommitted work stops it:
// `git worktree remove` refuses a dirty checkout by itself, but by then the user has lost the
// session that named it and has no way to find out what was in it - so the check is made here,
// where the message can say which session and which files.
func refuseUnsafeRelease(release worktreeRelease) error {
	workspace := release.Workspace
	if release.InspectErr != nil {
		// The count could not be read, so the checkout cannot be proven clean. Refusing
		// is the answer that cannot lose work.
		return fmt.Errorf("the session's checkout at %s could not be inspected (%v), so the session was not deleted: "+
			"check it by hand and remove it with `git worktree remove %s`", workspace, release.InspectErr, workspace)
	}
	if len(release.Changes) > 0 {
		return fmt.Errorf("the session has %d uncommitted change(s) in %s, so deleting it would destroy them: "+
			"commit them, or confirm the discard and remove the checkout with `git worktree remove --force %s`",
			len(release.Changes), workspace, workspace)
	}
	return nil
}

// forceRelease removes a checkout git refused to remove although the user confirmed the discard:
// a checkout holding a submodule, a registration git no longer reads. Reported from real use as "I
// accept deleting it and it throws a git error" - the one answer the user had already given was
// overruled by a tool. With the discard confirmed, the checkout is removed as a directory and its
// registration pruned. It returns nil once the checkout is gone.
func (s *Server) forceRelease(c *conversation, projectDir, workspace string, gitErr error) error {
	if ferr := s.forceRemoveCheckout(projectDir, workspace); ferr != nil {
		return fmt.Errorf("%v; removing it as a directory failed too: %v", gitErr, ferr)
	}
	s.warn("git refused to remove the session's worktree; it was removed as a directory",
		"id", c.id, "worktree", workspace, "git_error", gitErr.Error())
	return nil
}
