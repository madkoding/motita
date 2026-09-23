package gateway

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"sync"
	"time"
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

	// approvalMu guards the question of the run in progress IN THIS CONVERSATION.
	approvalMu sync.Mutex
	approvalID string
	approvalCh chan bool
}

func newConversation(id string, svc Service) *conversation {
	now := time.Now()
	return &conversation{id: id, svc: svc, created: now, lastUsed: now}
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

// sessionStatus is what the list and create endpoints report about one conversation.
type sessionStatus struct {
	ID       string    `json:"id"`
	Created  time.Time `json:"created"`
	LastUsed time.Time `json:"last_used"`
	Running  bool      `json:"running"`
}

func (c *conversation) status() sessionStatus {
	c.stateMu.Lock()
	defer c.stateMu.Unlock()
	return sessionStatus{ID: c.id, Created: c.created, LastUsed: c.lastUsed, Running: c.running}
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
func (s *Server) handleCreateSession(w http.ResponseWriter, _ *http.Request) {
	conv, err := s.createSession()
	switch {
	case errors.Is(err, ErrCeilingReached):
		writeError(w, http.StatusConflict, err.Error())
	case err != nil:
		writeError(w, http.StatusNotImplemented, err.Error())
	default:
		writeJSON(w, http.StatusCreated, conv.status())
	}
}

// handleListSessions answers what this gateway is holding.
//
// Sorted by id so that a client reading it twice sees the same order, and so does a test.
func (s *Server) handleListSessions(w http.ResponseWriter, _ *http.Request) {
	all := s.snapshot()
	out := make([]sessionStatus, 0, len(all))
	for _, c := range all {
		out = append(out, c.status())
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	writeJSON(w, http.StatusOK, map[string]any{"sessions": out})
}

// handleDeleteSession drops one conversation.
//
// The default one is refused: it belongs to the process that started this gateway, and closing it
// would leave that process talking to a conversation that no longer exists.
func (s *Server) handleDeleteSession(w http.ResponseWriter, r *http.Request) {
	c := convOf(r)
	if c.id == DefaultSession {
		writeError(w, http.StatusConflict,
			"the default session belongs to the process that started this gateway and cannot be closed")
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
	w.WriteHeader(http.StatusNoContent)
}
