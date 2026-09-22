package gateway

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/madkoding/starlight/internal/agent"
)

// handleTask runs a task and streams the turn.
func (s *Server) handleTask(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Task string `json:"task"`
	}
	if !s.decodeBody(w, r, &body) {
		return
	}
	if strings.TrimSpace(body.Task) == "" {
		writeError(w, http.StatusBadRequest, "the task is empty")
		return
	}
	s.run(w, r, func(ctx context.Context, progress func(string, ...any)) (string, error) {
		return s.svc.RunTask(ctx, body.Task, progress)
	})
}

// handlePlan is the same shape for the read-only planner.
func (s *Server) handlePlan(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Prompt string `json:"prompt"`
	}
	if !s.decodeBody(w, r, &body) {
		return
	}
	if strings.TrimSpace(body.Prompt) == "" {
		writeError(w, http.StatusBadRequest, "the prompt is empty")
		return
	}
	s.run(w, r, func(ctx context.Context, progress func(string, ...any)) (string, error) {
		return s.svc.RunPlan(ctx, body.Prompt, progress)
	})
}

// run executes one turn and streams it.
//
// The slot is taken BEFORE the headers are written, so a refused second client gets a 409 it can
// read instead of a stream that never starts. That ordering is the whole reason this is one
// function and not two.
func (s *Server) run(w http.ResponseWriter, r *http.Request, exec func(context.Context, func(string, ...any)) (string, error)) {
	if !s.takeRunSlot() {
		writeError(w, http.StatusConflict,
			"a run is already in progress; an agent has one conversation, so runs are served one at a time")
		return
	}
	defer s.releaseRunSlot()

	rc := http.NewResponseController(w)
	if err := startStream(w, rc); err != nil {
		// The client is gone before anything was written. There is nobody to tell, and the run
		// is not started: a turn whose output has nowhere to go is work thrown away.
		return
	}

	emit := func(event string, payload any) error { return writeEvent(w, rc, event, payload) }

	// The approver is installed for THIS run, and it is the transport's job rather than the
	// agent's. A command that needs approval and has nobody to ask is REFUSED, so the gateway is
	// exactly the thing that has to become the person asking.
	s.svc.SetApprover(s.approverFor(emit))

	progress := func(format string, args ...any) {
		// A failed write means the stream is gone. Dropping the line is right: the run is
		// already cancelled through the context, and blocking here would keep this goroutine
		// alive for a reader that will never come back. The line is forwarded VERBATIM - the
		// plan view parses prefixes out of it, so reformatting it here would break the view.
		_ = emit(EventProgress, progressEvent{Text: fmt.Sprintf(format, args...)})
	}

	result, err := exec(r.Context(), progress)
	switch {
	case err != nil:
		// The 200 and the headers are already on the wire, so a failure now belongs on the
		// stream, where the client is reading.
		_ = emit(EventError, map[string]string{"error": err.Error()})
	case result == "":
		// A run that returns nothing and no error is reported as the failure it is: an empty
		// answer rendered as success is the interface lying.
		_ = emit(EventError, map[string]string{"error": "the run finished without reporting a result"})
	default:
		_ = emit(EventDone, doneEvent{Result: result, Session: s.svc.ConversationSummary()})
	}
}

// takeRunSlot reserves the one run slot, or reports that it is taken.
func (s *Server) takeRunSlot() bool {
	s.runMu.Lock()
	defer s.runMu.Unlock()
	if s.running {
		return false
	}
	s.running = true
	return true
}

// releaseRunSlot frees it. Called from a defer, so a failing or panicking run cannot leak the
// slot and leave the gateway permanently refusing everybody.
func (s *Server) releaseRunSlot() {
	s.runMu.Lock()
	s.running = false
	s.runMu.Unlock()
}

// approverFor returns the approver installed for one run.
//
// It asks the client over the SAME stream the run is already writing to, and blocks until the
// answer arrives on POST /v1/runs/approval. Every path that cannot get an answer returns false:
// a write that failed, and a cancelled context. Silence is not consent for an action the policy
// deliberately refused to decide on its own.
func (s *Server) approverFor(emit func(string, any) error) agent.Approver {
	return func(ctx context.Context, req agent.ApprovalRequest) (bool, error) {
		id, err := newApprovalID()
		if err != nil {
			// Without an id the question cannot be asked safely: a predictable one would let
			// one client answer a question another client was asked. "I cannot form the
			// question safely" already means no in this design.
			return false, err
		}
		ch := make(chan bool, 1)

		s.approvalMu.Lock()
		s.approvalID, s.approvalCh = id, ch
		s.approvalMu.Unlock()
		// Cleared on the way out whatever happened, so a late answer cannot land on the next
		// question.
		defer func() {
			s.approvalMu.Lock()
			s.approvalID, s.approvalCh = "", nil
			s.approvalMu.Unlock()
		}()

		if err := emit(EventApproval, approvalEvent{
			ID:      id,
			Command: req.Command,
			Reason:  req.Reason,
			Rule:    req.Rule,
		}); err != nil {
			return false, err
		}

		select {
		case ok := <-ch:
			return ok, nil
		case <-ctx.Done():
			// The run was cancelled while the question was open.
			return false, ctx.Err()
		}
	}
}

// handleApproval answers the question of the run in progress.
func (s *Server) handleApproval(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ID      string `json:"id"`
		Approve bool   `json:"approve"`
	}
	if !s.decodeBody(w, r, &body) {
		return
	}

	s.approvalMu.Lock()
	id, ch := s.approvalID, s.approvalCh
	s.approvalMu.Unlock()

	switch {
	case ch == nil:
		writeError(w, http.StatusConflict, "nothing is waiting for an approval right now")
	case body.ID != id:
		// An answer to a question nobody asked. Accepting it would let a stale client approve
		// the NEXT command, which is the one thing this channel exists to prevent.
		writeError(w, http.StatusConflict, "that approval id does not match the command being asked about")
	default:
		// Buffered with room for one, so this never blocks even if the run has already moved on
		// or already been cancelled. A blocking send here would hang the answering client on a
		// question that no longer exists.
		select {
		case ch <- body.Approve:
		default:
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// newApprovalID returns an unguessable id for one question.
//
// A failing source of randomness is an ERROR and not a fallback id: a predictable id would let
// one client answer a question another client was asked, and there is already a correct
// behaviour for "I cannot form the question safely" - refuse.
func newApprovalID() (string, error) {
	b := make([]byte, 16)
	if _, err := io.ReadFull(randReader, b); err != nil {
		return "", fmt.Errorf("could not form an approval id: %w", err)
	}
	return fmt.Sprintf("%x", b), nil
}
