package gateway

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/madkoding/motita/internal/gitx"
)

// DefaultSession is the conversation every gateway has, and the one an embedded client uses.
//
// It exists because the common case is one conversation, not zero: the terminal that started this
// process talks to this id, and a client that only ever wanted to talk to "the agent" should not
// have to open a conversation first in order to do it.
const DefaultSession = "default"

// defaultMaxSessions bounds how many conversations one process will hold.
//
// A conversation is not free: it keeps a transcript, a session and a reward attribution alive for
// as long as it exists. The ceiling is what stops a client that forgets to close what it opened
// from turning the agent into a memory leak. Eight is far more than the handful of front ends
// this serves and far less than anything that would matter on a machine that already runs
// commands.
const defaultMaxSessions = 8

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
	// lastTask is the most recent task or plan prompt submitted to this
	// conversation. It is saved so that a session interrupted by a gateway
	// restart (an upgrade) can be resumed automatically: the new process
	// reads it from the persisted record and re-submits it.
	lastTask string
	// lastKind is "task" or "plan", recording which mode the last run was
	// in. An interrupted plan and an interrupted task resume differently.
	lastKind string
	// title is the human-readable label a front end draws for this conversation. It is empty
	// until the first turn completes and an auto-title is derived from it, and it may be
	// changed by the user at any time through the rename endpoint.
	title string
	// projectID is the project this conversation belongs to, or empty when it
	// is a free-standing session. A session that belongs to a project runs
	// with its workspace set to the project's directory.
	projectID string
	// workspace is the directory the agent works in. It is empty for a
	// free-standing session, and set to the project's directory for one that
	// belongs to a project. It is read to report the branch a session is on,
	// so a front end can show it without another round-trip.
	workspace string

	// current is the run in flight, and nil when there is none.
	//
	// It replaces the loose context.CancelFunc the run used to be: a run is now an object that
	// owns its events, its subscribers and its pending approval, and the conversation only has
	// to know WHICH one is live so a client that reconnects can find it. Guarded by stateMu,
	// the same lock as `running`, because the two are read together: the run slot being taken
	// IS a run being current.
	current *run
	// pending is the approval waiting to be answered, and nil when nothing is being asked.
	//
	// It lives on the CONVERSATION rather than inside the run so that a client which reconnects
	// can be told about it: the question was asked while it was away, and a client that is not
	// told will sit forever watching a run that is waiting for the answer it will never give.
	pending *pendingApproval
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
		title:    "New session — " + now.Format("02/01 15:04:05"),
	}
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
	if c.running {
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

// SessionStatus is what the list and create endpoints report about one conversation.
type SessionStatus struct {
	ID        string    `json:"id"`
	Title     string    `json:"title"`
	ProjectID string    `json:"project_id,omitempty"`
	Branch    string    `json:"branch,omitempty"`
	Created   time.Time `json:"created"`
	LastUsed  time.Time `json:"last_used"`
	Running   bool      `json:"running"`
	// Mergeable reports whether the session has work that can be integrated
	// back into the project's base branch. It is true when the session belongs
	// to a project, the session's branch (motita/<id>) exists, and it has
	// commits the base branch does not. A session that was never run in a
	// worktree, or whose work has already been merged, is not mergeable — and
	// the front end shows that as a disabled Integrate button rather than an
	// absent one, so the user knows the action exists even when it has nothing
	// to do yet.
	Mergeable bool `json:"mergeable,omitempty"`
}

func (c *conversation) status() SessionStatus {
	c.stateMu.Lock()
	defer c.stateMu.Unlock()
	st := SessionStatus{ID: c.id, Title: c.title, ProjectID: c.projectID, Created: c.created, LastUsed: c.lastUsed, Running: c.running}
	if c.workspace != "" {
		ctx := context.Background()
		st.Branch = gitx.Display(ctx, c.workspace)
		// Mergeable: the session's branch exists and has commits the base
		// branch does not. A session that never ran in a worktree has no
		// branch, and one whose work was already merged has none ahead.
		branch := sessionBranch(c.id)
		if gitx.BranchExists(ctx, c.workspace, branch) {
			if ahead, _, err := gitx.CommitsBetween(ctx, c.workspace, st.Branch, branch); err == nil && len(ahead) > 0 {
				st.Mergeable = true
			}
		}
	}
	return st
}

// setProjectID records which project this conversation belongs to and the
// workspace it runs in.
func (c *conversation) setProjectID(pid, ws string) {
	c.stateMu.Lock()
	defer c.stateMu.Unlock()
	c.projectID = pid
	c.workspace = ws
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
	if len(s.sessions) >= s.maxSessions() {
		return nil, fmt.Errorf("%w: it holds %d, which is its ceiling, so close one first", ErrCeilingReached, s.maxSessions())
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
	var workspace string
	if strings.TrimSpace(body.ProjectID) != "" {
		p := s.projectOf(body.ProjectID)
		if p == nil {
			writeError(w, http.StatusNotFound, ErrProjectNotFound.Error())
			return
		}
		workspace = p.Dir
	}

	conv, err := s.createSession()
	switch {
	case errors.Is(err, ErrCeilingReached):
		writeError(w, http.StatusConflict, err.Error())
	case err != nil:
		writeError(w, http.StatusNotImplemented, err.Error())
	default:
		if workspace != "" {
			conv.setProjectID(body.ProjectID, workspace)
			conv.svc.SetWorkspace(workspace)
		}
		s.saveSession(conv)
		writeJSON(w, http.StatusCreated, conv.status())
	}
}

// handleListSessions answers what this gateway is holding.
//
// Sorted by id so that a client reading it twice sees the same order, and so does a test.
func (s *Server) handleListSessions(w http.ResponseWriter, _ *http.Request) {
	all := s.snapshot()
	out := make([]SessionStatus, 0, len(all))
	for _, c := range all {
		out = append(out, c.status())
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	writeJSON(w, http.StatusOK, map[string]any{"sessions": out})
}

// handleDeleteSession drops one conversation.
//
// The default one cannot be removed — it belongs to the process that started this
// gateway — but deleting it is treated as a reset: the transcript is cleared, the
// title is restored to the "New session" placeholder, and the persisted file is
// removed. The session stays alive but empty, which is what a user who presses
// "delete" on it expects.
func (s *Server) handleDeleteSession(w http.ResponseWriter, r *http.Request) {
	c := convOf(r)
	if c.id == DefaultSession {
		if c.isRunning() {
			writeError(w, http.StatusConflict,
				"a run is in progress in this session: close it after the run finishes")
			return
		}
		c.svc.ResetConversation()
		c.setTitle("New session — " + time.Now().Format("02/01 15:04:05"))
		s.deletePersistedSession(c.id)
		writeJSON(w, http.StatusOK, c.status())
		return
	}
	// A run in flight is refused rather than killed: there is no cancel path in this design, and
	// reporting a closed session while its run is still writing would be a lie about what is
	// happening.
	if c.isRunning() {
		writeError(w, http.StatusConflict,
			"a run is in progress in this session: close it after the run finishes")
		return
	}
	// No "was it there?" branch: withConversation already proved it is, and a concurrent second
	// DELETE of the same session would find it gone - which is the outcome both callers asked
	// for. Reporting 404 to one of them would be reporting a race, not a fact about the session.
	s.forget(c.id)
	s.deletePersistedSession(c.id)
	w.WriteHeader(http.StatusNoContent)
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

// maybeAutoTitle sets a title generated by the LLM when the conversation still
// has the placeholder "Sesión nueva — …" title. It is called after a turn
// completes, so a session that was just created gets a human-readable label
// without the user naming it themselves.
func (s *Server) maybeAutoTitle(c *conversation) {
	// Only replace the placeholder title, never a user-set or already-generated one.
	current := c.status().Title
	if current != "" && !strings.HasPrefix(current, "New session —") {
		return
	}
	turns := c.svc.Transcript()
	for _, t := range turns {
		if t.User != "" {
			title := c.svc.GenerateTitle(context.Background(), t.User)
			if title != "" {
				c.setTitle(title)
				s.saveSession(c)
			}
			return
		}
	}
}
