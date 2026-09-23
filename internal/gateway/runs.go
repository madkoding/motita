package gateway

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/madkoding/starlight/internal/agent"
	"github.com/madkoding/starlight/internal/session"
)

// handleTask runs a task and streams the turn.
//
// The caller becomes the run's FIRST subscriber, and that is the difference from what this used to
// be: the handler is no longer the run's destination. A client that goes away stops READING; it
// does not stop the turn, and a later client can attach and be told everything that happened in
// the meantime.
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
	c := convOf(r)
	s.startRun(w, r, c, func(ctx context.Context, progress func(string, ...any)) (string, error) {
		return c.svc.RunTask(ctx, body.Task, progress)
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
	c := convOf(r)
	s.startRun(w, r, c, func(ctx context.Context, progress func(string, ...any)) (string, error) {
		return c.svc.RunPlan(ctx, body.Prompt, progress)
	})
}

// startRun begins a turn and streams it to the caller.
//
// The slot is taken BEFORE the headers are written, so a refused second client gets a 409 it can
// read instead of a stream that never starts. That ordering is the whole reason this is one
// function and not two.
func (s *Server) startRun(w http.ResponseWriter, r *http.Request, c *conversation, exec func(context.Context, func(string, ...any)) (string, error)) {
	if !c.takeRunSlot() {
		writeError(w, http.StatusConflict,
			"a run is already in progress in this session; a conversation is served one run at a time")
		return
	}

	// The run derives its context from the PROCESS, not from this request. That is the line the
	// whole feature turns on: a run bounded by its connection is a run that dies when the client
	// walks away, and reconnecting to a dead run has nothing to reconnect to.
	runCtx, cancel := context.WithCancel(s.baseCtx)
	rn := newRun(newRunID(), runCtx, cancel)
	c.setCurrentRun(rn)

	// The approver is installed for THIS run, and it is the transport's job rather than the
	// agent's. A command that needs approval and has nobody to ask is REFUSED, so the gateway is
	// exactly the thing that has to become the person asking.
	c.svc.SetApprover(s.approverFor(c, rn))

	// The run is driven in ITS OWN goroutine, and this handler only reads its events - which is
	// what lets the turn outlive the request.
	go func() {
		defer c.releaseRunSlot()
		defer c.clearCurrentRun()
		result, err := exec(runCtx, rn.progress())
		switch {
		case errors.Is(err, context.Canceled):
			// Cancelled is its OWN outcome and not an error: it is something a person asked for,
			// and reporting it as a failure would make the interface apologise for the user's
			// own decision.
			rn.append(EventError, map[string]string{"error": "the run was cancelled"})
			rn.finish("cancelled", "", "the run was cancelled", session.Snapshot{})
		case err != nil:
			rn.append(EventError, map[string]string{"error": err.Error()})
			rn.finish("error", "", err.Error(), session.Snapshot{})
		case result == "":
			// A run that returns nothing and no error is reported as the failure it is: an empty
			// answer rendered as success is the interface lying.
			rn.append(EventError, map[string]string{"error": "the run finished without reporting a result"})
			rn.finish("error", "", "the run finished without reporting a result", session.Snapshot{})
		default:
			snap := c.svc.ConversationSummary()
			rn.append(EventDone, doneEvent{Result: result, Session: snap})
			rn.finish("done", result, "", snap)
		}
	}()

	s.attach(w, r, c, rn, 0)
}

// attach streams one run to one client, from the given sequence number onwards.
//
// It is the ONLY place that writes to a client during a run, which is what settles the
// two-writers problem: the run appends to its own log, and every attached handler writes to its own
// socket. One writer per connection - and the socket a client left behind stops being written to
// the moment its handler returns.
func (s *Server) attach(w http.ResponseWriter, r *http.Request, c *conversation, rn *run, from uint64) {
	rc := http.NewResponseController(w)
	if err := startStream(w, rc); err != nil {
		// The client is gone before anything was written. The RUN KEEPS GOING: there is nobody to
		// tell and nothing to undo - the events are in the log, waiting for whoever attaches next.
		return
	}

	// The subscription is taken FIRST, so nothing that is appended from here on can be missed. The
	// replay is then read, and the queue is drained of anything it already carried: an event
	// appended between the two is in BOTH, and sending it twice under two frames would make a
	// client render the same line twice.
	sub := rn.subscribe()
	defer rn.unsubscribe(sub)
	replay, info := rn.since(from)

	// The preamble first: what the run is, what the log can still give, and the question that is
	// pending RIGHT NOW. A client that reconnects while the run is blocked on an approval has to be
	// told about it, or it waits forever for a run that is waiting for it.
	var pending *approvalEvent
	if p := c.pendingApprovalNow(); p != nil {
		pending = &approvalEvent{ID: p.id, Command: p.command, Reason: p.reason, Rule: p.rule}
	}
	outcome, _, _, finished := rn.outcomeOf()
	if err := writeEvent(w, rc, 0, EventAttached, attachedEvent{
		RunID: rn.id, FirstSeq: info.FirstSeq, LastSeq: info.LastSeq,
		Dropped: info.Dropped, PendingApproval: pending, Outcome: outcome,
	}); err != nil {
		return
	}
	for _, e := range replay {
		if err := writeRaw(w, rc, e); err != nil {
			return
		}
	}
	// No draining here: a run that has already finished put everything it will ever emit in the log
	// (the replay above), and one still in flight is handled by the loop below, whose `done` case
	// drains. An extra drain at this point would be a second copy of the same code with an error
	// path nothing else reaches.

	// A run that has already finished has nothing more to send, and the stream ends here. Saying so
	// in the preamble first is what lets the client tell "finished" from "the connection dropped".
	if finished {
		return
	}

	for {
		select {
		case <-r.Context().Done():
			// This client is gone. Only this reader is affected; the run keeps its log.
			return
		case <-rn.done:
			// The run ended. Its last event was appended before done was closed, so it is either in
			// the replay above or in the subscriber's channel, and this drains that channel before
			// the stream closes. Returning here would risk dropping the final event.
			_ = rn.drainFrom(w, rc, sub, info.LastSeq)
			return
		case e := <-sub.ch:
			if err := writeRaw(w, rc, e); err != nil {
				return
			}
		case <-sub.lost:
			// This reader could not keep up and was cut off rather than fed events with holes. It
			// is TOLD, so it can reconnect from its last sequence number and be given the rest.
			_ = writeEvent(w, rc, 0, EventError, map[string]string{
				"error": "this reader fell behind the run and was detached; reconnect with the last sequence number you saw",
			})
			return
		}
	}
}

// drainFrom writes whatever is queued for a subscriber beyond the given sequence number.
//
// It exists because a run ending and its last event being read are two different moments: a select
// that saw `done` first would leave the final event in the channel, and the client would be told
// the turn ended without ever being shown how. Anything at or below `after` is already in the replay
// the caller just wrote, and sending it again would make the client render the same line twice.
//
// The reads are non-blocking, so a subscriber with an empty queue costs nothing.
func (r *run) drainFrom(w http.ResponseWriter, rc *http.ResponseController, sub *subscriber, after uint64) error {
	for {
		select {
		case e := <-sub.ch:
			if e.Seq <= after {
				// Already sent in the replay.
				continue
			}
			if err := writeRaw(w, rc, e); err != nil {
				return err
			}
		default:
			return nil
		}
	}
}

// progress returns the callback the agent reports through.
//
// The line is forwarded VERBATIM - the plan view parses prefixes out of it, so reformatting it here
// would break the view - and it goes into the run's LOG rather than to a socket, which is what
// makes it available to a client that was not there when it was emitted.
func (r *run) progress() func(string, ...any) {
	return func(format string, args ...any) {
		r.append(EventProgress, progressEvent{Text: fmt.Sprintf(format, args...)})
	}
}

// approverFor returns the approver for one run.
//
// It records the question on the CONVERSATION before announcing it, so a client that attaches while
// the run is blocked is told about it in its preamble - and it blocks until the answer arrives on
// POST /v1/sessions/{id}/runs/approval. Every path that cannot get an answer returns false: a run
// that was cancelled, and a question that cannot be formed. Silence is not consent for an action
// the policy deliberately refused to decide on its own.
func (s *Server) approverFor(c *conversation, rn *run) agent.Approver {
	return func(ctx context.Context, req agent.ApprovalRequest) (bool, error) {
		id, err := newApprovalID()
		if err != nil {
			// Without an id the question cannot be asked safely: a predictable one would let one
			// client answer a question another client was asked. "I cannot form the question
			// safely" already means no in this design.
			return false, err
		}
		p := &pendingApproval{id: id, ch: make(chan bool, 1), command: req.Command, reason: req.Reason, rule: req.Rule}
		c.setPendingApproval(p)
		// Cleared on the way out whatever happened, so a late answer cannot land on the next one.
		defer c.clearPendingApproval()

		rn.append(EventApproval, approvalEvent{ID: id, Command: req.Command, Reason: req.Reason, Rule: req.Rule})

		select {
		case ok := <-p.ch:
			return ok, nil
		case <-ctx.Done():
			// The run was cancelled while the question was open.
			return false, ctx.Err()
		}
	}
}

// handleApproval answers the question of the run in progress IN THIS conversation.
func (s *Server) handleApproval(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ID      string `json:"id"`
		Approve bool   `json:"approve"`
	}
	if !s.decodeBody(w, r, &body) {
		return
	}

	p := convOf(r).pendingApprovalNow()
	switch {
	case p == nil:
		writeError(w, http.StatusConflict, "nothing is waiting for an approval right now")
	case body.ID != p.id:
		// An answer to a question nobody asked. Accepting it would let a stale client approve the
		// NEXT command, which is the one thing this channel exists to prevent. It matters MORE now
		// that a client can reconnect: a client that comes back and answers a question from an
		// EARLIER run must not approve the current one, and the id is what makes that impossible.
		writeError(w, http.StatusConflict, "that approval id does not match the command being asked about")
	default:
		// Buffered with room for one, so this never blocks even if the run has already moved on or
		// already been cancelled. A blocking send here would hang the answering client on a
		// question that no longer exists.
		select {
		case p.ch <- body.Approve:
		default:
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// handleAttach lets a client join a run already in flight, from where it left off.
//
// This is the reconnect half of the design, and it exists because the run is not a connection: the
// gateway holds the events, so a client that went away asks for what follows the last one it saw.
// It keeps nothing and reconstructs nothing - which is what it means for a client to be a remote
// control.
func (s *Server) handleAttach(w http.ResponseWriter, r *http.Request) {
	c := convOf(r)
	rn, ok := c.currentRun()
	if !ok {
		writeError(w, http.StatusNotFound, "there is no run in progress in this session to attach to")
		return
	}
	s.attach(w, r, c, rn, fromParam(r))
}

// handleRunStatus answers what is running, without opening a stream.
//
// A client uses it to decide whether to attach at all, and to show the user that a turn is in
// flight - which it can know from the gateway instead of remembering it. Same rule as everything
// else here: the gateway is the source of truth.
func (s *Server) handleRunStatus(w http.ResponseWriter, r *http.Request) {
	rn, ok := convOf(r).currentRun()
	if !ok {
		writeError(w, http.StatusNotFound, "there is no run in progress in this session")
		return
	}
	outcome, _, _, _ := rn.outcomeOf()
	_, info := rn.since(0)
	writeJSON(w, http.StatusOK, map[string]any{
		"run_id": rn.id, "outcome": outcome, "first_seq": info.FirstSeq,
		"last_seq": info.LastSeq, "dropped": info.Dropped, "subscribers": rn.subscriberCount(),
	})
}

// handleCancelRun stops the run in flight in this conversation.
//
// Cancelling is addressed to the CONVERSATION rather than to a run id: there is one run per
// conversation by construction, so a run id would be a second name for the same thing - and a
// client that reconnected and remembered a stale id would cancel the wrong run, or nothing.
func (s *Server) handleCancelRun(w http.ResponseWriter, r *http.Request) {
	if !convOf(r).cancelRun() {
		writeError(w, http.StatusConflict, "there is no run in progress in this session to stop")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// fromParam reads the sequence number a client asks to resume from.
//
// A missing or unreadable value means "from the beginning", which is the safe reading: a client
// that cannot say where it got to is given everything the log still holds, and the preamble tells
// it whether that is all of it. An error would leave it with nothing at all.
func fromParam(r *http.Request) uint64 {
	v, err := strconv.ParseUint(strings.TrimSpace(r.URL.Query().Get("from")), 10, 64)
	if err != nil {
		return 0
	}
	return v
}

// newRunID returns an unguessable id for one run.
func newRunID() string {
	b := make([]byte, 12)
	if _, err := io.ReadFull(randReader, b); err != nil {
		// A run without an id cannot be distinguished from another one, but refusing to start the
		// turn over it would be worse: the id is for the client to keep track, and the TOKEN is
		// what protects this gateway.
		return fmt.Sprintf("run-%d", time.Now().UnixNano())
	}
	return fmt.Sprintf("%x", b)
}

// newApprovalID returns an unguessable id for one question.
//
// A failing source of randomness is an ERROR and not a fallback id: a predictable id would let one
// client answer a question another client was asked, and there is already a correct behaviour for
// "I cannot form the question safely" - refuse.
func newApprovalID() (string, error) {
	b := make([]byte, 16)
	if _, err := io.ReadFull(randReader, b); err != nil {
		return "", fmt.Errorf("could not form an approval id: %w", err)
	}
	return fmt.Sprintf("%x", b), nil
}
