package gateway

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/madkoding/motita/internal/agent"
	"github.com/madkoding/motita/internal/gitx"
	"github.com/madkoding/motita/internal/intent"
)

// deleteStopTimeout is how long a deletion waits for a cancelled run to finish
// unwinding before giving up and reporting it.
//
// It is a var rather than a const so a test can set it small: reaching the timeout
// with the real value would mean a ten-second test, and the branch would go
// unexercised. The repository already uses this shape for its other seams
// (launchCommand, listenAndServe).
//
// Generous on purpose: the wait is normally milliseconds, because the cancellation
// kills the sandbox's whole process group and there is no command left to wait for.
// Ten seconds is long enough that a slow-but-honest unwind is never mistaken for a
// stuck one, and short enough that a user pressing Delete is not left staring at a
// request that never returns.
var deleteStopTimeout = 10 * time.Second

// DefaultSession is the conversation every gateway has, and the one an embedded client uses.
//
// It exists because the common case is one conversation, not zero: the terminal that started this
// process talks to this id, and a client that only ever wanted to talk to "the agent" should not
// have to open a conversation first in order to do it.
const DefaultSession = "default"

// defaultMaxSessions bounds how many conversations one process holds IN MEMORY.
//
// A conversation that is idle costs very little by itself (measured: ~14 kB for the
// struct and its service shell), so the ceiling is NOT a memory-leak guard for the
// struct — it is a bound on how many TRANSCRIPTS are resident, because a transcript
// can be megabytes and the restore used to load every one on disk without checking.
// Sixty-four is far more than the handful of front ends this serves and costs under
// 1 MB of struct overhead, while keeping the recent conversations a user is likely
// to return to hot.
const defaultMaxSessions = 64

// conversation is ONE agent conversation: the service that speaks for it, the right to run in
// it, and the question waiting to be answered in it.
//
// The run slot and the approval slot live HERE rather than on the Server, and that move is the
// whole feature. They used to live on the Server, with a comment saying why: an agent has one
// conversation, so two runs at once would interleave two tasks into one transcript. That reason
// is about a CONVERSATION and not about a process - so with several conversations there are
// several slots, and two front ends can work at the same time without sharing a transcript.
type conversation struct {
	id  string
	svc Service

	// stateMu guards the three small facts the list endpoint reads and the run slot. One lock
	// for all of them because they are always read together, and a lock per field would be
	// three chances to forget one.
	stateMu  sync.Mutex
	created  time.Time
	lastUsed time.Time
	running  bool
	// titling is set while a title is being generated for this conversation (see autoTitle).
	titling bool
	// prWatch is what the gateway is doing about the session's pull request, nil when nothing.
	prWatch    *PRWatchView
	prFixedKey string // the push whose failure was last handed to the agent
	// prSeen is the comment IDs the watch has already taken in (see prWatchRecord.Seen).
	prSeen []string
	// ciRev is the push whose CI was last read, and ciRevAt when it was first seen (see awaitedCI).
	ciRev   string
	ciRevAt time.Time
	// lastTask is the most recent task or plan prompt submitted to this
	// conversation. It is saved so that a session interrupted by a gateway
	// restart (an upgrade) can be resumed automatically: the new process
	// reads it from the persisted record and re-submits it.
	lastTask string
	// lastKind is "task" or "plan", recording which mode the last run was
	// in. An interrupted plan and an interrupted task resume differently.
	lastKind string
	// lastAuto is how auto mode routed the last message it classified, so the next one can be
	// recognised as the person correcting it.
	lastAuto intent.Decision
	// agents is the latest snapshot of the agents of the run in flight, or of the last run. It is
	// kept here and not on the run so a session whose agents finished still says what they did.
	agents []agent.AgentInfo
	// title is the human-readable label a front end draws for this conversation. It is empty
	// until the first turn completes and an auto-title is derived from it, and it may be
	// changed by the user at any time through the rename endpoint.
	title string
	// projectID is the project this conversation belongs to, or empty when it
	// is a free-standing session. A session that belongs to a project runs
	// with its workspace set to the project's directory.
	projectID string
	// workspace is the directory the agent works in. It is empty for a
	// free-standing session. For a session in a git project it is the
	// session's OWN worktree, and projectDir below is the project it branches
	// from; for one that belongs to a non-git project the two are the same.
	// It is read to report the directory and the branch a session is on, so a
	// front end can show them without another round-trip.
	workspace string
	// projectDir is the project's own checkout, and it is what a session's
	// branch is compared against when deciding whether there is work to
	// integrate. Empty for a free-standing session.
	//
	// It is tracked separately from workspace because they differ exactly when
	// the feature is working: a session with its own worktree reports that
	// worktree as its workspace, and comparing its branch against ITSELF would
	// always report "nothing to merge".
	projectDir string

	// current is the run in flight, and nil when there is none.
	//
	// It replaces the loose context.CancelFunc the run used to be: a run is now an object that
	// owns its events, its subscribers and its pending approval, and the conversation only has
	// to know WHICH one is live so a client that reconnects can find it. Guarded by stateMu,
	// the same lock as `running`, because the two are read together: the run slot being taken
	// IS a run being current.
	current *run
	// lastRun is the run that ended last, kept so a client that lost its connection just before
	// the end and reattaches by the run's id is given its outcome instead of a 404. Guarded by
	// stateMu, like current.
	lastRun *run
	// pending is the approval waiting to be answered, and nil when nothing is being asked.
	//
	// It lives on the CONVERSATION rather than inside the run so that a client which reconnects
	// can be told about it: the question was asked while it was away, and a client that is not
	// told will sit forever watching a run that is waiting for the answer it will never give.
	pending *pendingApproval
	// autoApprove is the user's "allow all commands for this session": while it is set, every
	// command the policy would ASK about is approved without asking. It never reaches what the
	// policy DENIES - that is decided before any approver is consulted. It is saved with the
	// session (sessionRecord.AutoApprove), so a restart or an upgrade does not make the user
	// give the same answer again; the pill in the interface says it is on and takes it back.
	// Guarded by stateMu.
	autoApprove bool

	// merged marks a session whose work has already been integrated back into
	// the project. A merged session is read-only: it still appears in the list,
	// but tasks and plans are refused because continuing would write on a branch
	// whose history is already part of the project.
	merged bool
	// mergedSha is the commit produced by the integration, if any.
	mergedSha string

	// checkpoints is one record per user input, oldest first: where to go back to, and what
	// the agent did on the way. See checkpoints.go. Guarded by cpMu, a lock of its own because
	// a run appends a step for every progress line and must not contend with stateMu readers.
	cpMu        sync.Mutex
	checkpoints []*checkpoint

	// queue holds messages sent while a run was in flight, oldest first. Guarded by stateMu.
	// It is per conversation, so a busy session never holds back another one.
	queue []string

	// closing is set while the conversation is being deleted (or reset, for the default one) and
	// stays set once it is gone: no run starts in it and no title is written to it. Guarded by
	// stateMu. epoch counts deletions and resets, so work begun before one (a title being
	// generated) can tell that the conversation it was for is no longer there.
	closing bool
	epoch   uint64
}

// setAutoApprove turns "allow all commands for this session" on or off.
func (c *conversation) setAutoApprove(on bool) {
	c.stateMu.Lock()
	defer c.stateMu.Unlock()
	c.autoApprove = on
}

// autoApproving reports whether the user allowed every command in this session.
func (c *conversation) autoApproving() bool {
	c.stateMu.Lock()
	defer c.stateMu.Unlock()
	return c.autoApprove
}

// pendingApproval is one question waiting for a human answer.
type pendingApproval struct {
	id      string
	ch      chan bool
	command string
	reason  string
	rule    string
}

func newConversation(id string, svc Service) *conversation {
	now := time.Now()
	return &conversation{
		id:       id,
		svc:      svc,
		created:  now,
		lastUsed: now,
		title:    placeholderTitle,
	}
}

// placeholderTitle is the title a session carries until something better names
// it.
//
// It used to embed the creation time ("New session — 02/01 15:04:05"), and the
// time is not gone - it moved to the session's own metadata line, where every
// session shows when it was last used. Two places showing one timestamp meant
// the title was half date, and a title is for the name.
const placeholderTitle = "New session"

// legacyPlaceholderPrefix is the opening of the dated placeholder older builds
// wrote. Sessions persisted by one of those builds are still on disk and still
// unnamed, so they must still be recognised as placeholders - otherwise a
// session created before this change would keep its date forever instead of
// being auto-titled on its next turn.
const legacyPlaceholderPrefix = "New session — "

// isPlaceholderTitle reports whether a title is one nobody chose: the current
// placeholder, or the dated one an older build wrote.
//
// An exact match is required for the current placeholder. A prefix match would
// also swallow a title the user set themselves ("New session notes"), and
// overwriting a name the user typed is worse than leaving a session unnamed.
func isPlaceholderTitle(title string) bool {
	return title == placeholderTitle || strings.HasPrefix(title, legacyPlaceholderPrefix)
}

// touch records that this conversation was just spoken to.
func (c *conversation) touch() {
	c.stateMu.Lock()
	defer c.stateMu.Unlock()
	c.lastUsed = time.Now()
}

// takeRunSlot reserves this conversation's one run slot, or reports that it is taken.
func (c *conversation) takeRunSlot() bool {
	c.stateMu.Lock()
	defer c.stateMu.Unlock()
	if c.running || c.closing {
		return false
	}
	c.running = true
	return true
}

// releaseRunSlot frees it. Called from a defer, so a failing or panicking run cannot leak the
// slot and leave this conversation refusing everybody forever.
func (c *conversation) releaseRunSlot() {
	c.stateMu.Lock()
	defer c.stateMu.Unlock()
	c.running = false
}

// isRunning reports whether a run is in flight here.
func (c *conversation) isRunning() bool {
	c.stateMu.Lock()
	defer c.stateMu.Unlock()
	return c.running
}

// currentRun returns the run in flight, if there is one.
//
// This is what a reconnecting client is answered with, and it is why the client keeps nothing: it
// asks the gateway which run is live instead of remembering one.
func (c *conversation) currentRun() (*run, bool) {
	c.stateMu.Lock()
	defer c.stateMu.Unlock()
	if c.current == nil {
		return nil, false
	}
	return c.current, true
}

// setCurrentRun installs a run as the live one.
//
// The slot is taken by the caller first (takeRunSlot), so by the time this runs there is no other
// run to displace.
func (c *conversation) setCurrentRun(r *run) {
	c.stateMu.Lock()
	defer c.stateMu.Unlock()
	c.current = r
}

// clearCurrentRun drops the live run, and is called from a defer so a failing or panicking run
// cannot leave a finished one looking live.
func (c *conversation) clearCurrentRun() {
	c.stateMu.Lock()
	defer c.stateMu.Unlock()
	c.current = nil
}

// endRun gives back the run slot and drops rn as the live run, in one step under the lock that
// guards both.
//
// It acts only while rn IS the live run. A run calls it twice - once before its last event goes
// out (see closeRun) and once more from its defer, in case it never got that far - and by the
// second call the slot may already belong to the next run, which a plain release would free from
// under it.
func (c *conversation) endRun(rn *run) {
	c.stateMu.Lock()
	defer c.stateMu.Unlock()
	if c.current != rn {
		return
	}
	c.current = nil
	c.lastRun = rn
	c.running = false
}

// runByID finds the run a reconnecting client names: the one in flight, or the last one to end.
// The status says why there is none: 409 when another run is in flight (attaching to it would show
// this client a turn it did not start, and its result as if it were its own), 410 when the run is
// gone and nothing replaced it.
func (c *conversation) runByID(id string) (*run, int) {
	c.stateMu.Lock()
	defer c.stateMu.Unlock()
	switch {
	case c.current != nil && c.current.id == id:
		return c.current, http.StatusOK
	case c.lastRun != nil && c.lastRun.id == id:
		return c.lastRun, http.StatusOK
	case c.current != nil:
		return nil, http.StatusConflict
	default:
		return nil, http.StatusGone
	}
}

// pendingFor returns the question being asked, but only while rn is the run in flight: the
// question belongs to that run, and a client attached to the one before must not answer it.
func (c *conversation) pendingFor(rn *run) *pendingApproval {
	c.stateMu.Lock()
	defer c.stateMu.Unlock()
	if c.current != rn {
		return nil
	}
	return c.pending
}

// setPendingApproval records the question being asked.
func (c *conversation) setPendingApproval(p *pendingApproval) {
	c.stateMu.Lock()
	defer c.stateMu.Unlock()
	c.pending = p
}

// pendingApprovalNow returns the question being asked, if any.
func (c *conversation) pendingApprovalNow() *pendingApproval {
	c.stateMu.Lock()
	defer c.stateMu.Unlock()
	return c.pending
}

// clearPendingApproval drops the question, and is called from a defer so a late answer cannot land
// on the next one.
func (c *conversation) clearPendingApproval() {
	c.stateMu.Lock()
	defer c.stateMu.Unlock()
	c.pending = nil
}

// cancelRun stops the run in flight and reports whether there was one to stop.
func (c *conversation) cancelRun() bool {
	rn, ok := c.currentRun()
	if !ok {
		return false
	}
	rn.cancel()
	return true
}

// stopRunForDeletion stops the run in flight and waits for the goroutine to
// finish, so that by the time this returns nothing is still executing on behalf
// of this conversation and nothing will write to it again.
//
// A plain cancellation is not enough for a DELETION. Cancelling only asks the run
// to stop: the goroutine then unwinds from wherever it was - killing the sandbox
// process group, reaching saveSession, and appending its final events (done is
// closed only after all of that; see closeRun) - and deleting underneath that leaves a conversation being torn
// down while a turn is still writing to it. Waiting for the run's own done channel
// is what makes "stop it and then delete it" true rather than two races that
// usually happen to be ordered.
//
// It reports whether there was a run to stop. The wait is bounded: a run that
// ignores its cancellation for longer than this is reported as stopped-on-paper
// rather than blocking the request forever, and the caller decides what to say.
func (c *conversation) stopRunForDeletion(timeout time.Duration) (bool, bool) {
	rn, ok := c.currentRun()
	if !ok {
		return false, true
	}
	rn.cancel()
	select {
	case <-rn.done:
		return true, true
	case <-time.After(timeout):
		return true, false
	}
}

// SessionStatus is what the list and create endpoints report about one conversation.
type SessionStatus struct {
	ID        string    `json:"id"`
	Title     string    `json:"title"`
	ProjectID string    `json:"project_id,omitempty"`
	Branch    string    `json:"branch,omitempty"`
	Created   time.Time `json:"created"`
	LastUsed  time.Time `json:"last_used"`
	Running   bool      `json:"running"`
	// PRWatch is the state of the session's pull request while the gateway follows its CI.
	PRWatch *PRWatchView `json:"pr_watch,omitempty"`
	// Mergeable reports whether the session has work that can be integrated
	// back into the project's base branch. It is true when the session belongs
	// to a project, the session's branch (motita/<id>) exists, and it has
	// commits the base branch does not. A session that was never run in a
	// worktree, or whose work has already been merged, is not mergeable — and
	// the front end shows that as a disabled Integrate button rather than an
	// absent one, so the user knows the action exists even when it has nothing
	// to do yet.
	Mergeable bool `json:"mergeable"`
	// Merged marks a session whose work has already been integrated back into
	// the project. A merged session is read-only: it still appears in the list
	// so the user can see its commit and transcript, but it cannot be written to.
	Merged bool `json:"merged"`
	// MergedSHA is the commit the integration produced, if any. It is what the
	// front end shows when the user asks "which commit did this session make".
	MergedSHA string `json:"merged_sha,omitempty"`
	// Continuable is true when a merged session belongs to a project: the user
	// can open a fresh session from the project's up-to-date base branch.
	Continuable bool `json:"continuable"`
	// Workspace is the directory this session actually runs in. For a session
	// in a git project that is its OWN worktree, not the project's checkout,
	// which is what lets two sessions work at once without editing each
	// other's files. It is empty for a free-standing session.
	Workspace string `json:"workspace,omitempty"`
	// Changes is how many uncommitted changes this session has in its own
	// working tree: modified, staged, deleted, renamed and untracked files,
	// each counted once. It is what the sidebar draws as a count, so a session
	// that has written something does not read as idle. Absent (0) for a
	// session with no workspace, or one that is not a repository.
	Changes int `json:"changes,omitempty"`
	// Worktree is the NAME of the session's worktree directory, which is its
	// id - the last path element rather than the whole path, because the path
	// is long, mostly identical between sessions, and would be truncated to
	// nothing useful in a narrow sidebar. It is what tells two sessions of one
	// project apart at a glance. Empty for a session without its own worktree.
	Worktree string `json:"worktree,omitempty"`
	// AutoApprove is set while the user has allowed every command in this session, so the
	// interface can say so - and offer to take it back.
	AutoApprove bool `json:"auto_approve,omitempty"`
	// AgentsRunning is how many agents the session's run has working in the background right
	// now, not counting the main one. It is what the sidebar draws, so a session whose main agent
	// is waiting on others does not read as stuck.
	AgentsRunning int `json:"agents_running,omitempty"`
	// Provider and Model are what THIS session's next turn runs with. The model belongs to the
	// session, not to the gateway: each one keeps its own, and the list says which so two
	// sessions on two models can be told apart without opening them.
	Provider string `json:"provider,omitempty"`
	Model    string `json:"model,omitempty"`
}

// setAgents keeps the latest snapshot of the run's agents.
func (c *conversation) setAgents(agents []agent.AgentInfo) {
	c.stateMu.Lock()
	c.agents = agents
	c.stateMu.Unlock()
}

// agentsNow is the latest snapshot of the run's agents, never nil: a JSON reader is given [] and
// not null for a session that has none.
func (c *conversation) agentsNow() []agent.AgentInfo {
	c.stateMu.Lock()
	defer c.stateMu.Unlock()
	return append([]agent.AgentInfo{}, c.agents...)
}

// runningSubagents counts the agents of a snapshot that are still working, the main one aside.
func runningSubagents(agents []agent.AgentInfo) int {
	n := 0
	for _, a := range agents {
		if a.Parent != "" && a.State == agent.AgentRunning {
			n++
		}
	}
	return n
}

func (c *conversation) status() SessionStatus {
	c.stateMu.Lock()
	defer c.stateMu.Unlock()
	st := SessionStatus{ID: c.id, Title: c.title, ProjectID: c.projectID, Created: c.created, LastUsed: c.lastUsed,
		Running: c.running, AutoApprove: c.autoApprove, AgentsRunning: runningSubagents(c.agents)}
	st.Workspace = c.workspace
	if c.svc != nil {
		cfg := c.svc.Config()
		st.Provider, st.Model = cfg.LLM.Provider, cfg.LLM.Model
	}
	if c.prWatch != nil {
		w := *c.prWatch
		st.PRWatch = &w
	}
	if c.workspace != "" {
		c.workspaceStatus(&st)
	}
	return st
}

// workspaceStatus fills in what a session's checkout says about it: its branch, its own worktree,
// its uncommitted changes and whether it has work to integrate. Called with stateMu held.
func (c *conversation) workspaceStatus(st *SessionStatus) {
	ctx := context.Background()
	st.Branch = gitx.Display(ctx, c.workspace)
	// The worktree is a session's OWN directory, and naming it here is what
	// tells two sessions of one project apart: they share a project, a
	// branch prefix and a title format, and the only thing that differs is
	// the directory each one runs in.
	//
	// Asked of git rather than compared with the project directory: a
	// session that fell back to the project's checkout has no worktree of
	// its own, and calling that directory one would be inventing a
	// distinction the filesystem does not make. The listing also settles
	// the symlinked and relative spellings of the same path, which a string
	// comparison does not.
	//
	// The project's own checkout IS a listed worktree of its repository, so
	// "git knows this path" is not the question - the question is whether
	// this path is somewhere OTHER than the project. Without that second
	// test a session that fell back to the project's checkout reports the
	// project's directory as its worktree, which is the one thing it is not.
	st.Worktree = c.ownWorktreeName(ctx)
	// Changes is read from the session's own tree, so the count is the work
	// this session has done. An unreadable tree reports 0 rather than an
	// error: the count decorates a badge, and a badge that cannot be
	// computed should be absent rather than break the sidebar.
	if n, err := gitx.WorkingTreeChanges(ctx, c.workspace); err == nil {
		st.Changes = n
	}
	// Mergeable compares the session's branch against the PROJECT's branch,
	// not against whatever the session's own checkout is on. A session with
	// its own worktree reports its own branch (motita/<id>), so comparing
	// the two would always say "nothing ahead" - the one answer that is
	// never useful here.
	base := st.Branch
	if c.projectDir != "" {
		base = gitx.Display(ctx, c.projectDir)
	}
	if c.merged {
		st.Merged = true
		st.MergedSHA = c.mergedSha
		st.Continuable = c.projectDir != "" && c.projectID != ""
		return
	}
	// Integrate: the session's branch exists and has commits the base
	// branch does not. A session that never ran in a worktree has no
	// branch, and one whose work was already merged has none ahead.
	mergeable := c.branchAhead(ctx, base)
	// Work the agent WROTE is integrable before anything has committed it. Nothing commits
	// what an agent writes (the default final action is none), so a session's branch sits
	// exactly where it branched and a rule that asks only for commits ahead left the button
	// disabled for every session that had done its job. Integrating commits the work first
	// (see handleMergeSession); this is what lets the user reach that action.
	if st.Worktree != "" && st.Changes > 0 {
		mergeable = true
	}
	// Expose mergeable through the renamed status fields so the front end still has a
	// single word to draw the menu item with. Merged sessions do not reach here.
	st.Mergeable = mergeable
}

// ownWorktreeName is the name of the session's own worktree, or "" when it runs in the project's
// checkout or in a folder git does not list as a worktree of the project.
func (c *conversation) ownWorktreeName(ctx context.Context) string {
	if c.projectDir == "" || gitx.SamePath(c.workspace, c.projectDir) {
		return ""
	}
	if _, ok, err := gitx.LiveWorktreeAt(ctx, c.projectDir, c.workspace); err == nil && ok {
		return filepath.Base(c.workspace)
	}
	return ""
}

// branchAhead reports whether the session's branch exists and has commits base does not.
func (c *conversation) branchAhead(ctx context.Context, base string) bool {
	branch := sessionBranch(c.id)
	if !gitx.BranchExists(ctx, c.workspace, branch) {
		return false
	}
	ahead, _, err := gitx.CommitsBetween(ctx, c.workspace, base, branch)
	return err == nil && len(ahead) > 0
}

// sessionWorktree gives a session its own checkout of a project, so two
//
// It returns the directory the session should run in. When a worktree cannot be
// made it returns the PROJECT's directory and the reason, because worktrees
// need git: a project folder that is not a repository, or one with no commits
// yet, must keep working exactly as it did before rather than failing to start
// a session. Turning "this project is not a repo" into "this session cannot
// start" would be a far worse answer than running in the directory the user
// actually pointed at.
//
// It is IDEMPOTENT, and that is not a convenience: the common case is a session
// whose worktree already exists - one restored after a gateway restart, or one
// the caller asked about twice - and `git worktree add` REFUSES a directory it
// has already registered. Measured: it fails with "Preparing worktree (checking
// out 'motita/<id>')" and a non-zero status, which would turn every restored
// session into one that could not run.
//
// The session's branch is the unit of isolation, and it OUTLIVES the worktree:
// a session whose worktree is removed and recreated finds its own commits again
// rather than starting over. That is why AddWorktree attaches an existing
// branch instead of insisting on a new one.
//
// The path is derived from the workspace root and the session id alone, so the
// same session always resolves to the same directory. This is the ONLY place
// that decides where a session runs: a second copy of that rule is how a
// session ends up reporting one directory while running in another.
func (s *Server) sessionWorktree(ctx context.Context, repoDir, sessionID string) (string, error) {
	if strings.TrimSpace(s.opts.WorkspaceDir) == "" {
		// Without a workspace root there is nowhere to put a worktree, and a
		// relative path would be resolved against the process's own directory.
		// The project directory is a correct answer and needs no extra state.
		return repoDir, nil
	}
	path := filepath.Join(s.opts.WorkspaceDir, "worktrees", sessionID)
	branch := sessionBranch(sessionID)
	// Already ours: a LIVE worktree of this repository at this path is the
	// worktree asked for, whatever created it.
	//
	// The test is the PATH, deliberately, and not the branch. A session whose
	// worktree the user moved to a feature branch is still working in its own
	// tree - and asking about the branch instead answers "not ours" for it,
	// which then sends this function down the add below. Measured: that add
	// fails ("already exists", non-zero), the error propagates as the fallback
	// to the PROJECT's directory, and two sessions end up editing one checkout -
	// the exact collision worktrees exist to prevent.
	if _, ok, err := gitx.LiveWorktreeAt(ctx, repoDir, path); err == nil && ok {
		return path, nil
	}
	if err := gitx.AddWorktree(ctx, repoDir, path, branch); err != nil {
		return repoDir, err
	}
	// The checkout is complete as far as git is concerned, and that is not the same as
	// being able to RUN.
	//
	// `git worktree add` copies nothing git ignores, so a project whose dependencies live
	// in an ignored directory - `node_modules/` is the common one - gets a checkout where
	// `npm run lint`, `tsc` and `vitest` do not exist. The anchor then runs the project's
	// own gate and reports "failed checks: npm lint, npm typecheck, npm test", which
	// reads as broken CODE and is really a missing TOOLCHAIN. Measured on a real session:
	// the agent spent round after round discovering this, and the user's report was
	// "demasiado determinista?" about a gate that was simply running in an empty tree.
	//
	// Linking is what makes the checkout runnable without a fresh install per session
	// (measured: 860 MB and ~30 s for one project). It is a LINK and not a copy: the
	// dependencies are the same bytes, npm reads them without writing, and a copy per
	// session would multiply the disk by the number of sessions.
	gitx.LinkDependencyDirs(repoDir, path)
	return path, nil
}

// scopeProceduresTo points a session's procedure library at its project, when the service
// behind it can carry a scope.
//
// It is an OPTIONAL interface and a type assertion rather than a method on Service, the same
// pattern SetLibrary, SetReward and the approver already follow here: the many small services
// the tests build never run a turn and have no library to scope, and forcing every one of them
// to grow a no-op method would be noise in exchange for a guarantee they do not need. A
// service that cannot be scoped keeps the shared shelf, which is the behaviour that existed
// before this feature and is never wrong — only less specific.
//
// The directory passed is the PROJECT's checkout, never a session's worktree: a worktree is
// removed when its session ends, and a procedure written into one would go with it.
func scopeProceduresTo(svc Service, projectDir string) {
	if scoper, ok := svc.(interface{ SetProjectScope(string) }); ok {
		scoper.SetProjectScope(projectDir)
	}
}

// runtimeOf is the container runtime a project is run with, "" for none.
func runtimeOf(p *Project) string {
	if p != nil && p.Podman {
		return "podman"
	}
	return ""
}

// applyRuntimeTo tells a session how its project is run and where it works, when the service can
// take it. workspace is the directory the session runs in (its worktree of the project).
func applyRuntimeTo(svc Service, p *Project, workspace string) {
	if setter, ok := svc.(interface{ SetContainerRuntime(string) }); ok {
		setter.SetContainerRuntime(runtimeOf(p))
	}
	// The project itself, and its other repositories, so the agent knows what it works on, where
	// that is, and that each repository is committed on its own.
	if setter, ok := svc.(interface{ SetProjectNote(string) }); ok {
		home, _ := os.UserHomeDir()
		setter.SetProjectNote(projectScopeNote(p, workspace) + projectReposNote(p) + dependenciesNote(p) +
			projectInstructionsNote(workspace, home))
	}
}

// setProjectID records which project this conversation belongs to, the project's
// own checkout, and the workspace the session actually runs in. The last two
// differ for a session with its own worktree, and both are needed: the workspace
// is where the agent writes, and the project directory is what its branch gets
// compared against.
func (c *conversation) setProjectID(pid, workspace, projectDir string) {
	c.stateMu.Lock()
	defer c.stateMu.Unlock()
	c.projectID = pid
	c.workspace = workspace
	c.projectDir = projectDir
}

// setMerged marks the session as integrated and records the commit that did it.
// A blank SHA is treated as "not merged", which is the safe default for records
// that predate the field.
func (c *conversation) setMerged(sha string) {
	c.stateMu.Lock()
	defer c.stateMu.Unlock()
	if sha == "" {
		return
	}
	c.merged = true
	c.mergedSha = sha
}

// isMerged reads the merged mark under its lock: the pull request watch and the end of a run read
// it from their own goroutines while setMerged may be writing it.
func (c *conversation) isMerged() bool {
	c.stateMu.Lock()
	defer c.stateMu.Unlock()
	return c.merged
}

// setTitle sets the human-readable label for this conversation. Called after the first turn
// completes to derive an auto-title, and by the rename endpoint when the user edits one.
func (c *conversation) setTitle(t string) {
	c.stateMu.Lock()
	defer c.stateMu.Unlock()
	c.title = t
}

// conversationKeyType is the context key the resolved conversation travels under. It is an
// unexported type so that nothing outside this package can collide with it.
type conversationKeyType struct{}

var conversationKey conversationKeyType

// convOf returns the conversation a request is about.
//
// It PANICS rather than falling back, and that is deliberate: a handler reached without a
// conversation is a programming error, and a fallback would answer about the DEFAULT conversation
// - which is the one failure this design exists to prevent. A loud panic in a test is worth more
// than a quiet answer about the wrong transcript.
//
// It can only be reached with a value because every scoped route goes through withConversation.
func convOf(r *http.Request) *conversation {
	c, ok := r.Context().Value(conversationKey).(*conversation)
	if !ok {
		panic("a gateway handler was reached without a conversation: it is not registered through withConversation")
	}
	return c
}

// conversationIfAny is convOf for a handler that is ALSO reachable without one.
//
// The skill endpoints are process-wide routes ("GET /v1/skills"), so most of their callers have
// no conversation in the context — but when one comes in under a session path it does, and that
// is the case that has to be answered from the session's own library rather than the shared one.
// A nil return is therefore a normal answer here, not a programming error, which is exactly what
// convOf's panic would turn it into.
func conversationIfAny(r *http.Request) *conversation {
	c, ok := r.Context().Value(conversationKey).(*conversation)
	if !ok {
		return nil
	}
	return c
}

// withConversation resolves the {id} in the path to a live conversation and puts it in the
// request context, so that no handler has to parse a path.
//
// A request for a conversation that does not exist is answered HERE and never reaches the
// handler: a check each handler made for itself is a check one of them would forget, and the one
// that forgot would answer about the wrong conversation.
func (s *Server) withConversation(h http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		conv, ok := s.lookup(id)
		if !ok {
			writeError(w, http.StatusNotFound, fmt.Sprintf("there is no session %q", id))
			return
		}
		conv.touch()
		h(w, r.WithContext(context.WithValue(r.Context(), conversationKey, conv)))
	})
}

// lookup finds a live conversation by id.
//
// The registry lock is held for the lookup and released before any work happens, so a long run in
// one conversation never blocks a client asking for another one.
func (s *Server) lookup(id string) (*conversation, bool) {
	s.sessionsMu.Lock()
	defer s.sessionsMu.Unlock()
	c, ok := s.sessions[id]
	return c, ok
}

// snapshot copies the registry, so the list can be built without holding the lock every request
// needs in order to find its conversation.
func (s *Server) snapshot() []*conversation {
	s.sessionsMu.Lock()
	defer s.sessionsMu.Unlock()
	out := make([]*conversation, 0, len(s.sessions))
	for _, c := range s.sessions {
		out = append(out, c)
	}
	return out
}

// forget drops a conversation, and reports whether it was there.
func (s *Server) forget(id string) bool {
	s.sessionsMu.Lock()
	defer s.sessionsMu.Unlock()
	if _, ok := s.sessions[id]; !ok {
		return false
	}
	delete(s.sessions, id)
	return true
}

// sessionCount reports how many conversations the process is ACTUALLY holding in
// memory. It is what the ceiling message must report, not the configured ceiling:
// a gateway configured for 8 that restored 3 says "it holds 3", not "it holds 8".
func (s *Server) sessionCount() int {
	s.sessionsMu.Lock()
	defer s.sessionsMu.Unlock()
	return len(s.sessions)
}

// maxSessions is the ceiling in force, defaulted when none was configured.
func (s *Server) maxSessions() int {
	if s.opts.MaxSessions > 0 {
		return s.opts.MaxSessions
	}
	return defaultMaxSessions
}

// ErrCeilingReached is returned when the process is already holding as many conversations as it
// will. It is a SENTINEL rather than a message, because the status the caller answers with depends
// on which refusal this is: a gateway that cannot build conversations at all is a different thing
// from one that is full, and answering both with the same code would tell a client to give up when
// closing one session would have fixed it.
var ErrCeilingReached = errors.New("this gateway is at its ceiling of conversations")

// newSession mints one more conversation, or reports why it cannot.
//
// NewService is called while the registry lock is held. That is deliberate: the alternative is a
// capacity check that another request can invalidate between the check and the insert, and a
// factory that called back into the gateway would deadlock instead. Neither is worth the two
// lines it saves, so the contract is stated rather than worked around - NewService must not call
// back into the gateway.
func (s *Server) newSession(svc Service) (*conversation, error) {
	s.sessionsMu.Lock()
	defer s.sessionsMu.Unlock()
	held := len(s.sessions)
	if held >= s.maxSessions() {
		return nil, fmt.Errorf("%w: it holds %d, which is its ceiling, so close one first",
			ErrCeilingReached, held)
	}
	id, err := newSessionID()
	if err != nil {
		return nil, err
	}
	conv := newConversation(id, svc)
	s.sessions[id] = conv
	return conv, nil
}

// createSession mints a conversation through the configured factory.
func (s *Server) createSession() (*conversation, error) {
	if s.opts.NewService == nil {
		// Honest rather than clever: a gateway started without a way to build a conversation
		// serves one, and saying so beats minting a conversation whose service would be the
		// same one the first conversation already has - which would be two names for one
		// transcript.
		return nil, errors.New("this gateway serves one conversation: it was started without a way to build another")
	}
	svc, err := s.opts.NewService()
	if err != nil {
		return nil, fmt.Errorf("a conversation could not be started: %w", err)
	}
	s.startingLLM(svc)
	s.startingDiskAccess(svc)
	return s.newSession(svc)
}

// newSessionID returns an unguessable id for one conversation.
//
// Random rather than sequential, for the same reason the approval id is: a client that can guess
// another's id can address another's conversation. The token is what actually protects this
// gateway, so a guessable id would not be a hole - but the id is also how a client keeps itself
// from confusing two conversations, and a name that cannot collide by accident costs nothing.
func newSessionID() (string, error) {
	b := make([]byte, 12)
	if _, err := io.ReadFull(randReader, b); err != nil {
		return "", fmt.Errorf("could not form a session id: %w", err)
	}
	return "s" + hex.EncodeToString(b), nil
}

// handleCreateSession mints one more conversation and answers with it.
//
// 201 with the id, and the id is the only thing a client needs: every other endpoint is addressed
// by it, so a client that loses one opens another.
//
// The two refusals are told apart. A gateway that cannot build conversations at all is 501 - the
// capability is absent. A gateway that is full is 409 - the capability is there and the request is
// the one that cannot be served yet, and a client that reads 409 can close a session and retry
// where a 501 tells it to give up.
func (s *Server) handleCreateSession(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ProjectID string `json:"project_id"`
		// Git asks for a free-standing session's directory to be a git repository (true) or a
		// plain directory (false). Absent, the gateway's free_session_git setting decides.
		Git *bool `json:"git"`
	}
	// The body is optional: a client that sends no body gets a free-standing session.
	if r.ContentLength > 0 {
		if !s.decodeBody(w, r, &body) {
			return
		}
	}

	// When a project is named, the session runs in that project's workspace.
	// The project must exist: a session for a missing project would run in
	// the wrong directory, which is exactly what the project feature prevents.
	//
	// For a git project the session gets its OWN worktree rather than the
	// project's checkout: two sessions in one project would otherwise edit the
	// same files. The worktree is best-effort - see sessionWorktree - so a
	// project that is not a repository still gives a usable session.
	var project *Project
	if strings.TrimSpace(body.ProjectID) != "" {
		project = s.projectOf(body.ProjectID)
		if project == nil {
			writeError(w, http.StatusNotFound, ErrProjectNotFound.Error())
			return
		}
	}

	// Opening a session brings the project's main branch up to date, but in the background: the
	// person asked to work, and a fetch must not hold the session back. See refreshMainInBackground.
	s.refreshMainInBackground(project)

	conv, err := s.createSession()
	switch {
	case errors.Is(err, ErrCeilingReached):
		writeError(w, http.StatusConflict, err.Error())
	case err != nil:
		writeError(w, http.StatusNotImplemented, err.Error())
	default:
		s.placeAndSave(conv, body.ProjectID, project, body.Git)
		writeJSON(w, http.StatusCreated, conv.status())
	}
}

// placeAndSave puts a new session where it runs - in the project when one was named, in a
// directory of its own otherwise - and only then saves it.
func (s *Server) placeAndSave(conv *conversation, projectID string, project *Project, git *bool) {
	if project != nil {
		s.placeInProject(conv, projectID, project)
	} else {
		s.placeFreeStanding(conv, s.wantsGit(git))
	}
	s.saveSession(conv)
}

// placeInProject makes a new session run in the project: in a worktree of its own, made BEFORE the
// session is saved so a session that is registered is one that can actually run. A failure to
// branch falls back to the project's own directory.
func (s *Server) placeInProject(conv *conversation, projectID string, project *Project) {
	dir := project.Dir
	if wt, wtErr := s.sessionWorktree(context.Background(), project.Dir, conv.id); wtErr != nil {
		s.warn("the session will run in the project directory: its own worktree could not be created",
			"id", conv.id, "project", project.Dir, "error", wtErr.Error())
	} else {
		dir = wt
	}
	s.attachProjectRepos(context.Background(), project, dir, conv.id)
	conv.setProjectID(projectID, dir, project.Dir)
	conv.svc.SetWorkspace(dir)
	// The procedures learned here belong to THIS project. A session inside a
	// project runs its worktree, but the project's checkout is what the scope
	// is anchored to: a worktree is removed when the session ends, and a
	// procedure written into one would be lost with it.
	scopeProceduresTo(conv.svc, project.Dir)
	applyRuntimeTo(conv.svc, project, dir)
}

// freeSessionDir is where a session that belongs to no project works: a directory of its own under
// the workspace root, named after the session.
//
// Free-standing sessions used to share the workspace root itself, so two of them working at once
// wrote into one directory - and the root is also where projects and worktrees live. A directory
// per session is the same isolation a project session gets from its worktree, without needing git.
func (s *Server) freeSessionDir(id string) string {
	root := strings.TrimSpace(s.opts.WorkspaceDir)
	if root == "" {
		return ""
	}
	return filepath.Join(root, "sessions", id)
}

// placeFreeStanding gives a new session that belongs to no project its own directory. Without a
// workspace root, or when the directory cannot be made, the session keeps the configured workspace,
// which is what it always had.
//
// withGit makes the directory a git repository too. A failed `git init` is reported and the session
// still runs in its plain directory: git was a convenience, not a condition.
func (s *Server) placeFreeStanding(conv *conversation, withGit bool) {
	dir := s.freeSessionDir(conv.id)
	if dir == "" {
		return
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		s.warn("the session will run in the shared workspace: its own directory could not be created",
			"id", conv.id, "dir", dir, "error", err.Error())
		return
	}
	if withGit {
		if err := gitx.Init(context.Background(), dir); err != nil {
			s.warn("the session's directory is not a git repository: git init failed",
				"id", conv.id, "dir", dir, "error", err.Error())
		}
	}
	conv.setProjectID("", dir, "")
	conv.svc.SetWorkspace(dir)
}

// releaseFreeSessionDir removes a deleted free-standing session's directory when nothing is left in
// it. A directory that still holds files is kept: they are the user's work, and nothing here can
// tell whether they are wanted.
func (s *Server) releaseFreeSessionDir(c *conversation) {
	c.stateMu.Lock()
	ws, projectDir := c.workspace, c.projectDir
	c.stateMu.Unlock()
	dir := s.freeSessionDir(c.id)
	if dir == "" || projectDir != "" || !gitx.SamePath(ws, dir) {
		return
	}
	removeIfUnused(dir)
}

// wantsGit is the request's answer when it gave one, and the gateway's setting otherwise.
func (s *Server) wantsGit(asked *bool) bool {
	if asked != nil {
		return *asked
	}
	return s.opts.FreeSessionGit
}

// removeIfUnused removes a free-standing session's directory when nothing of the user's is in it:
// an empty directory, or one holding only the repository free_session_git made and no commit was
// ever written to. Anything else is the user's work and stays.
func removeIfUnused(dir string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	switch {
	case len(entries) == 0:
		_ = os.Remove(dir)
	case len(entries) == 1 && entries[0].Name() == ".git" && !gitx.HasCommits(context.Background(), dir):
		_ = os.RemoveAll(dir)
	default:
		// Anything else is the user's work, and it stays.
	}
}

// pruneFreeSessionDirs removes the directories under workspace/sessions that no session owns any
// more and that hold nothing of the user's (see removeIfUnused). They are left behind by a gateway
// stopped before a session's deletion finished, or by sessions deleted before directories were
// released at all. known is every session id the gateway still has a record of, loaded or not:
// a session past the restore ceiling is on disk, and its directory is still its own.
func (s *Server) pruneFreeSessionDirs(known map[string]bool) {
	root := s.freeSessionDir("")
	if root == "" {
		return
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return
	}
	for _, e := range entries {
		if !e.IsDir() || known[e.Name()] {
			continue
		}
		removeIfUnused(filepath.Join(root, e.Name()))
	}
}

// handleListSessions answers what this gateway is holding.
//
// Sorted by id so that a client reading it twice sees the same order, and so does a test.
func (s *Server) handleListSessions(w http.ResponseWriter, _ *http.Request) {
	all := s.snapshot()
	out := make([]SessionStatus, 0, len(all))
	for _, c := range all {
		if c.dormant() {
			continue
		}
		out = append(out, c.status())
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	// max_sessions is the ceiling, so a front end can say how close to it the gateway is before a
	// new session is refused with 409.
	writeJSON(w, http.StatusOK, map[string]any{"sessions": out, "max_sessions": s.maxSessions()})
}

// dormant reports whether this is the default session with nothing in it: no turn, no
// checkpoint, no run and no name of its own.
//
// The default session always exists in memory, because the terminal that started the gateway
// and the scheduler talk to it. But a conversation the user never opened is not one they have,
// and listing it is what made a fresh gateway - or one whose sessions were all deleted - show a
// "New session" nobody created. So it is left out of the list until something is said in it,
// and a front end with an empty list waits for the user to create a session.
func (c *conversation) dormant() bool {
	if c.id != DefaultSession || c.svc == nil {
		return false
	}
	c.stateMu.Lock()
	idle := !c.running && (c.title == "" || c.title == placeholderTitle)
	c.stateMu.Unlock()
	if !idle {
		return false
	}
	c.cpMu.Lock()
	marks := len(c.checkpoints)
	c.cpMu.Unlock()
	return marks == 0 && len(c.svc.Transcript()) == 0
}

// handleRenameSession changes the human-readable title of a conversation.
//
// 200 with the updated status rather than 204: a client that renamed a session draws the new
// title from the response, and a second round-trip to fetch it would be a race with any other
// client editing the same session.
func (s *Server) handleRenameSession(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Title string `json:"title"`
	}
	if !s.decodeBody(w, r, &body) {
		return
	}
	title := strings.TrimSpace(body.Title)
	if title == "" {
		writeError(w, http.StatusBadRequest, "the title cannot be empty")
		return
	}
	c := convOf(r)
	c.setTitle(title)
	s.saveSession(c)
	writeJSON(w, http.StatusOK, c.status())
}

// autoTitleTimeout bounds the model call that names a conversation. The services bound their own
// call more tightly; this is the gateway's guarantee that a title request never outlives it by much,
// whatever the service does.
const autoTitleTimeout = 30 * time.Second

// autoTitle names the conversation in the background, after a turn that answered.
//
// It is NOT part of the run, and that is the point. It used to be: the model call ran after the run
// had told its clients `done` but while it still held the run slot, so the first message a person
// sent after the first answer of every new session was refused with 409 for as long as the title
// took. It now runs on its own, after the slot is free, bounded by autoTitleTimeout and by the
// gateway's lifetime, so a shutdown stops it instead of waiting on a model.
//
// One title request at a time per conversation: a second turn that ends while the first title is
// still being asked for has nothing to add, and two answers racing to name one session would leave
// whichever landed last.
//
// A conversation being deleted is not titled, and a title asked for before a deletion or a reset
// is not written after it: the epoch it was asked in is carried to the write.
func (s *Server) autoTitle(c *conversation) {
	c.stateMu.Lock()
	if c.titling || c.closing {
		c.stateMu.Unlock()
		return
	}
	c.titling = true
	epoch := c.epoch
	c.stateMu.Unlock()

	go func() {
		defer func() {
			c.stateMu.Lock()
			c.titling = false
			c.stateMu.Unlock()
		}()
		ctx, cancel := context.WithTimeout(s.baseCtx, autoTitleTimeout)
		defer cancel()
		s.maybeAutoTitle(ctx, c, epoch)
	}()
}

// maybeAutoTitle sets a title generated by the LLM when the conversation still
// has an unchosen placeholder title. It is called after a turn
// completes, so a session that was just created gets a human-readable label
// without the user naming it themselves.
//
// The model takes its time, and the conversation does not wait for it: by the time the title
// arrives the person may have renamed the session (their title wins), or deleted the default one,
// which empties it (a title for a conversation that is gone is not set). A gateway that is closing
// writes nothing either.
func (s *Server) maybeAutoTitle(ctx context.Context, c *conversation, epoch uint64) {
	// Only replace a placeholder title, never a user-set or already-generated one.
	if !c.hasPlaceholderTitle() {
		return
	}
	first := firstUserMessage(c.svc.Transcript())
	if first == "" {
		return
	}
	title := c.svc.GenerateTitle(ctx, first)
	if title == "" || s.baseCtx.Err() != nil || firstUserMessage(c.svc.Transcript()) != first {
		return
	}
	if c.setTitleIfPlaceholder(title, epoch) {
		s.saveSession(c)
	}
}

// firstUserMessage is what the person said first in a conversation, or "" when they have said
// nothing yet. It is what a title is generated from.
func firstUserMessage(turns []agent.DialogueTurn) string {
	for _, t := range turns {
		if t.User != "" {
			return t.User
		}
	}
	return ""
}

// hasPlaceholderTitle reports whether the conversation still has the title nobody chose.
func (c *conversation) hasPlaceholderTitle() bool {
	c.stateMu.Lock()
	defer c.stateMu.Unlock()
	return isPlaceholderTitle(c.title)
}

// setTitleIfPlaceholder sets a generated title, unless somebody named the conversation while it
// was being generated, or it was deleted or reset since the title was asked for (in epoch). The
// check and the write are one step under the lock, so a rename that lands between them cannot be
// overwritten.
func (c *conversation) setTitleIfPlaceholder(t string, epoch uint64) bool {
	c.stateMu.Lock()
	defer c.stateMu.Unlock()
	if !isPlaceholderTitle(c.title) || c.closing || c.epoch != epoch {
		return false
	}
	c.title = t
	return true
}
