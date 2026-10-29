package gateway

import (
	"context"
	"os"
	"strings"

	"github.com/madkoding/motita/internal/devbox"
	"github.com/madkoding/motita/internal/gitx"
)

// SweepBoxes removes the service containers an earlier process left behind (a crash, a kill): a
// run removes its own when it ends, so any that exist when the gateway starts are orphans. It is
// a variable, exported, so the tests of any package never touch the machine's real containers.
var SweepBoxes = devbox.Sweep

// loadPersistedSessions reads every conversation the store has on disk and
// registers each one that is not already in memory. The default session is
// skipped: it is always in memory, and its file (if any) is loaded separately
// so its transcript can be restored into the existing service.
func (s *Server) loadPersistedSessions() {
	// In the background: asking a container runtime can take seconds and nothing waits for it.
	go SweepBoxes(context.Background())
	if s.store == nil {
		return
	}
	records, err := s.store.loadAll()
	if err != nil {
		s.warn("could not load persisted sessions", "error", err.Error())
		return
	}
	// The directories of free-standing sessions that no record names any more are pruned once, on
	// the way in: nothing is running yet, so none of them can be in use.
	known := map[string]bool{}
	for _, rec := range records {
		known[rec.ID] = true
	}
	for _, c := range s.snapshot() {
		known[c.id] = true
	}
	s.pruneFreeSessionDirs(known)
	s.pruneSessionBranches(known)
	// The ceiling applies to RESTORE the same way it applies to creation: a gateway
	// configured to hold 8 that finds 60 on disk must NOT load all 60. The records
	// from loadAll are sorted by last_used DESCENDING, so the first N non-default
	// records are the most recently used ones — the ones a user is most likely to
	// return to. The rest stay on disk, where they can be re-materialised on demand.
	//
	// The default session is always in memory (it was registered at Start), so the
	// budget for restored sessions is the ceiling minus one.
	ceiling := s.maxSessions()
	restored := 0
	for _, rec := range records {
		if rec.ID == DefaultSession {
			s.restoreDefaultSession(rec)
			continue
		}
		// A non-default session needs a new service built for it. Without a
		// factory the session cannot be restored as a live conversation; it is
		// skipped rather than registered with the default's service, which would
		// be two names for one transcript.
		if s.opts.NewService == nil {
			continue
		}
		// The ceiling: once enough sessions have been restored to fill the
		// budget (ceiling minus the default that is always in memory), stop.
		// The remaining records are on disk and can be re-materialised on
		// demand; loading them all is what made a gateway with max_sessions=8
		// arrive holding 60 and then refuse a new one.
		if restored >= ceiling-1 {
			if s.opts.Log != nil {
				s.opts.Log.Info("session restore stopped at the ceiling",
					"restored", restored, "ceiling", ceiling, "remaining_on_disk", len(records)-restored-1)
			}
			break
		}
		if s.restoreSession(rec) {
			restored++
		}
	}
	s.adoptThreads()
}

// restoreDefaultSession restores a record of the default session into its existing service.
func (s *Server) restoreDefaultSession(rec sessionRecord) {
	def := s.sessions[DefaultSession]
	s.bindContext(def)
	if len(rec.Turns) > 0 {
		def.svc.RestoreTranscript(rec.Turns)
	}
	def.setCheckpoints(rec.Checkpoints)
	def.restoreQueue(rec.Queue)
	if rec.Title != "" {
		def.setTitle(rec.Title)
	}
	def.setAutoApprove(rec.AutoApprove)
	// The model is the session's own too. It was restored for every session but this one, so the
	// first conversation came back from a restart on the gateway's configured model whatever it had
	// been switched to - the one place the model still read as global.
	if rec.Provider != "" || rec.Model != "" {
		def.svc.SetLLM(rec.Provider, rec.Model)
	}
	// The level this conversation was running comes back with it, or the interface would
	// paint a level the session is not actually using.
	if rec.Reasoning != "" {
		def.svc.SetReasoning(rec.Reasoning)
	}
}

// restoreSession registers a persisted non-default session with a service built for it, and
// reports whether it did.
func (s *Server) restoreSession(rec sessionRecord) bool {
	svc, err := s.opts.NewService()
	if err != nil {
		s.warn("could not build a service for a persisted session", "id", rec.ID, "error", err.Error())
		return false
	}
	conv := newConversation(rec.ID, svc)
	s.bindContext(conv)
	conv.created = rec.Created
	conv.lastUsed = rec.LastUsed
	conv.setTitle(rec.Title)
	conv.setAutoApprove(rec.AutoApprove)
	conv.setMerged(rec.MergedSHA)
	conv.setParent(rec.ParentID)
	// A session in a project gets its worktree back. The branch outlives the
	// worktree, so re-attaching it restores the session's own work rather
	// than starting over; a failure falls back to the project's directory,
	// exactly as creation does.
	// A record from before the project's directory was saved with it still names its project:
	// the directory is read from the project, so the session goes back to it rather than to
	// wherever it happened to be.
	if rec.ProjectDir == "" && rec.ProjectID != "" {
		if p := s.projectOf(rec.ProjectID); p != nil {
			rec.ProjectDir = p.Dir
		}
	}
	workspace := s.restoredWorkspace(rec)
	projectDir := rec.ProjectDir
	conv.setProjectID(rec.ProjectID, workspace, projectDir)
	// Restore the last task/kind so the session knows what it was doing
	// before the restart. The running flag is restored separately by
	// resumeInterruptedSessions, which needs the service to be fully
	// wired before it can start a run.
	conv.stateMu.Lock()
	conv.lastTask = rec.LastTask
	conv.lastKind = rec.LastKind
	conv.stateMu.Unlock()
	if len(rec.Turns) > 0 {
		svc.RestoreTranscript(rec.Turns)
	}
	conv.setCheckpoints(rec.Checkpoints)
	conv.restoreQueue(rec.Queue)
	conv.restorePRWatch(rec.PRWatch, s.prMaxFixes(conv))
	conv.restoreRepoWatches(rec.PRRepoWatches, s.prMaxFixes(conv))
	// Restore provider/model if they were persisted and differ from the
	// defaults the factory built with.
	if rec.Provider != "" || rec.Model != "" {
		svc.SetLLM(rec.Provider, rec.Model)
	}
	// Restore the reasoning level this session was running. It is restored AFTER SetLLM
	// because a service is free to reset the level with the provider (a level one provider
	// does not honour is not a reason to keep showing it), and the session's own choice is
	// what has to win.
	if rec.Reasoning != "" {
		svc.SetReasoning(rec.Reasoning)
	}
	// Restore the workspace directory for project sessions. It is the
	// worktree when there is one, so the resumed session writes where its
	// work already is.
	if workspace != "" {
		svc.SetWorkspace(workspace)
	}
	// The scope comes back with the project. A restored session must serve the
	// SAME shelf it had before the restart, or the procedures it learned while
	// working on this project would silently disappear from its answers.
	//
	// The scope is the PROJECT's checkout and nothing else: a free session is created unscoped (see
	// placeFreeStanding), and scoping it to its own folder after a restart would put what it
	// learns in a folder that is removed with it.
	if projectDir != "" {
		scopeProceduresTo(svc, projectDir)
	}
	project := s.projectOf(rec.ProjectID)
	s.attachProjectRepos(s.baseCtx, project, workspace, rec.ID)
	applyRuntimeTo(svc, project, workspace)
	s.offerThreads(conv)
	s.sessionsMu.Lock()
	s.sessions[rec.ID] = conv
	s.sessionsMu.Unlock()
	return true
}

// restoredWorkspace is the directory a restored session runs in. A session made before projects
// became plain folders has a worktree of its own: it is re-attached to the session's branch so the
// resumed session writes where its work already is. Any other session runs in the project's folder.
func (s *Server) restoredWorkspace(rec sessionRecord) string {
	if rec.ProjectDir == "" {
		return s.restoredFreeWorkspace(rec)
	}
	if rec.Workspace == "" || gitx.SamePath(rec.Workspace, rec.ProjectDir) {
		return rec.ProjectDir
	}
	wt, err := s.sessionWorktree(s.baseCtx, rec.ProjectDir, rec.ID)
	if err != nil {
		s.warn("the restored session will run in the project directory: its worktree could not be re-created",
			"id", rec.ID, "project", rec.ProjectDir, "error", err.Error())
		return rec.ProjectDir
	}
	return wt
}

// restoredFreeWorkspace is the directory a restored session of no project runs in. A record from
// before free sessions had a folder of their own names the workspace ROOT - the parent of every
// project, worktree and session folder - and restoring it there would hand it all of them; it gets
// its own folder instead, the one a new free session gets. Any other record keeps its directory.
func (s *Server) restoredFreeWorkspace(rec sessionRecord) string {
	root := strings.TrimSpace(s.opts.WorkspaceDir)
	if root == "" || rec.Workspace == "" || !gitx.SamePath(rec.Workspace, root) {
		return rec.Workspace
	}
	dir := s.freeSessionDir(rec.ID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		s.warn("the restored session will run in the workspace root: its own directory could not be created",
			"id", rec.ID, "dir", dir, "error", err.Error())
		return rec.Workspace
	}
	return dir
}

// resumeInterruptedSessions re-submits the task or plan for every session
// that was mid-run when the gateway shut down. It is called after
// loadPersistedSessions has registered every conversation and wired its
// service, because starting a run needs a fully constructed conversation.
//
// A session that was running but has no recorded lastTask cannot be resumed
// (the task text was lost), and is left in a clean non-running state rather
// than started with an empty prompt that would produce nothing useful.
func (s *Server) resumeInterruptedSessions() {
	if s.store == nil {
		return
	}
	records, err := s.store.loadAll()
	if err != nil {
		s.warn("could not load sessions for resume", "error", err.Error())
		return
	}
	for _, rec := range records {
		s.resumePRWatch(rec)
		c, ok := s.lookup(rec.ID)
		if !ok {
			continue
		}
		if rec.Running && strings.TrimSpace(rec.LastTask) != "" {
			s.resumeRun(c, rec)
		}
		// What was queued starts now, or when the resumed run ends and drains it.
		s.drainQueue(c)
	}
}

// resumePRWatch follows again a pull request that was being followed when the gateway stopped.
func (s *Server) resumePRWatch(rec sessionRecord) {
	defer s.resumeRepoWatches(rec)
	w := rec.PRWatch
	if w == nil || !isWatchedPRStatus(w.Status) {
		return
	}
	if c, ok := s.lookup(rec.ID); ok {
		st := &prWatchState{status: w.Status, attempts: w.Attempts, fixedKey: w.FixedKey, repo: w.Repo}
		st.talk.Resume(w.Seen)
		s.startPRWatch(c, st)
	}
}

// isWatchedPRStatus reports whether a pull request in this state is still being followed. The
// agent being sent to answer a review or resolve a conflict is one of them: the watch reads the
// pull request again once that run ends, and leaving those two out stopped it for good at a
// restart, with the comments made during the downtime never answered.
func isWatchedPRStatus(status string) bool {
	switch status {
	case prFollowing, prFixing, prPassed, prReviewing, prResolving:
		return true
	default:
		return false
	}
}

// resumeRun starts again the task a session was running when the gateway stopped.
func (s *Server) resumeRun(c *conversation, rec sessionRecord) {
	// Log the resume so the operator can see it happened.
	if s.opts.Log != nil {
		s.opts.Log.Info("resuming interrupted session", "id", rec.ID, "kind", rec.LastKind, "task", rec.LastTask)
	}
	// The run is started through the SAME path the scheduler and the HTTP handlers
	// use, so the slot guard, the completion classification and the persistence are
	// one implementation and cannot drift. Nothing waits for it: the resumption is a
	// side effect of starting the gateway.
	if _, ok := s.startDetachedRun(c, rec.LastTask, rec.LastKind, "", s.approverFactory(c)); !ok {
		s.warn("could not resume the interrupted session: its conversation is already running", "id", rec.ID)
	}
}

// saveSession persists one conversation to disk. It is called after a turn
// finishes, after a rename, and after a config update. A nil store means
// persistence is not configured, and the call is a no-op.
//
// A session that has been forgotten — removed from the registry by a DELETE —
// is NOT saved. A run's goroutine holds a pointer to the conversation and its
// deferred saveSession (runs.go) can fire AFTER handleDeleteSession has already
// forgotten the session and deleted its file; without this guard, that late
// save would resurrect the session on disk and it would reappear on the next
// loadPersistedSessions.
func (s *Server) saveSession(c *conversation) { s.persist(c, false) }

// saveEndedSession persists a conversation whose run has just ended: see sessionStore.saveEnded.
func (s *Server) saveEndedSession(c *conversation) { s.persist(c, true) }

func (s *Server) persist(c *conversation, ended bool) {
	if s.store == nil {
		return
	}
	if _, ok := s.lookup(c.id); !ok {
		return
	}
	save := s.store.save
	if ended {
		save = s.store.saveEnded
	}
	if err := save(c); err != nil && s.opts.Log != nil {
		s.opts.Log.Warn("could not persist the session", "id", c.id, "error", err.Error())
	}
}

// saveAllSessions persists every conversation in memory to disk. It is called
// before a gateway restart (an upgrade) so that running sessions can be
// resumed by the new process. Sessions that are mid-run are saved with
// running=true, which is what the new process reads to know which ones to
// resume.
func (s *Server) saveAllSessions() {
	if s.store == nil {
		return
	}
	for _, c := range s.snapshot() {
		if err := s.store.save(c); err != nil && s.opts.Log != nil {
			s.opts.Log.Warn("could not persist the session before restart", "id", c.id, "error", err.Error())
		}
	}
}

// deletePersistedSession removes a conversation's file from disk, and the archive that
// belonged to it: a conversation nobody can return to has no history worth keeping.
func (s *Server) deletePersistedSession(id string) {
	s.removeArtifacts(id)
	s.deleteContext(id)
	if s.store == nil {
		return
	}
	if err := s.store.delete(id); err != nil && s.opts.Log != nil {
		s.opts.Log.Warn("could not delete the persisted session", "id", id, "error", err.Error())
	}
}
