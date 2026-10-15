package gateway

import (
	"net/http"
	"strings"

	"github.com/madkoding/motita/internal/schedule"
)

// pushQueue adds a message to the end of the conversation's queue and returns its position,
// counting from 1. It is for a conversation that cannot run now at all (integrated): the message
// is kept, and nothing is started.
func (c *conversation) pushQueue(task string) int {
	c.stateMu.Lock()
	defer c.stateMu.Unlock()
	c.queue = append(c.queue, task)
	return len(c.queue)
}

// restoreQueue puts back the queue a persisted session was saved with.
func (c *conversation) restoreQueue(queue []string) {
	c.stateMu.Lock()
	defer c.stateMu.Unlock()
	c.queue = append([]string(nil), queue...)
}

// takeSlotOrEnqueue is "start this message or queue it" as ONE step under the lock that guards
// both the run slot and the queue. It reports true when the caller now holds the slot and must
// start the run itself; otherwise the message was queued and its position is returned.
//
// The two halves used to be separate calls - try to start, and queue on failure - and a run that
// ended between them lost the message: its drain found the queue empty, the push landed after
// it, and nothing would ever look at the queue again. Deciding under the lock closes that window,
// because a holder of the slot always drains AFTER it gives the slot back (see drainQueue), so a
// message queued while the slot was held is always seen by that drain.
//
// A free slot with messages still queued (a run that ended and has not drained yet) does not let
// the new message jump ahead of them: it is queued behind them, and the caller drains.
func (c *conversation) takeSlotOrEnqueue(task string) (bool, int) {
	c.stateMu.Lock()
	defer c.stateMu.Unlock()
	if !c.running && !c.closing && len(c.queue) == 0 {
		c.running = true
		return true, 0
	}
	c.queue = append(c.queue, task)
	return false, len(c.queue)
}

// takeQueued pops the oldest queued message AND takes the run slot for it, in one step. The pop and
// the take used to be apart: a slot taken between them sent the message back to the front of the
// queue, with the same lost-wakeup window handleQueue had.
func (c *conversation) takeQueued() (string, bool) {
	c.stateMu.Lock()
	defer c.stateMu.Unlock()
	if c.running || c.closing || len(c.queue) == 0 {
		return "", false
	}
	next := c.queue[0]
	c.queue = c.queue[1:]
	c.running = true
	return next, true
}

// queueBehindRun puts a message at the front of the queue while the slot is held, and returns the
// run in flight so the caller cancels exactly it. The check and the push are one step: a run that
// ended between them used to leave the message queued with nobody left to drain it, and
// cancelling "the current run" afterwards could stop the very run that had just started with it.
//
// It reports false, and queues nothing, when nothing holds the slot. The run is nil when the slot
// is held by something that is not a run (a WebSocket query), which has nothing to cancel.
func (c *conversation) queueBehindRun(task string) (*run, bool) {
	c.stateMu.Lock()
	defer c.stateMu.Unlock()
	if !c.running {
		return nil, false
	}
	if strings.TrimSpace(task) != "" {
		c.queue = append([]string{task}, c.queue...)
	}
	return c.current, true
}

// drainQueue starts the next queued message of a conversation whose slot was just given back.
// Whoever holds the slot calls it after releasing it (a run, a WebSocket query, a shell command,
// a checkpoint restore): that is the rule takeSlotOrEnqueue depends on.
//
// It does nothing while the gateway is closing: a queued message must not start work on a process
// that is going away. A message whose session cannot run (integrated) stays queued.
func (s *Server) drainQueue(c *conversation) {
	if s.baseCtx.Err() != nil || !runnable(c) {
		return
	}
	next, ok := c.takeQueued()
	if !ok {
		return
	}
	s.launchRun(c, next, schedule.KindTask, "", "", s.approverFactory(c))
}

// releaseAndDrain gives back a slot taken with takeRunSlot and starts whatever was queued while it
// was held.
func (s *Server) releaseAndDrain(c *conversation) {
	c.releaseRunSlot()
	s.drainQueue(c)
}

// afterQueued and afterQueuedBehindRun are test seams: they run where the old code had its window,
// right after a message was queued because the slot was busy, so a test can end the run holding
// the slot at exactly that point.
var (
	afterQueued = func() {
		// Nothing outside tests: a test replaces it to act inside the window.
	}
	afterQueuedBehindRun = func() {
		// Nothing outside tests: a test replaces it to act inside the window.
	}
)

// startOrQueue starts a message as a task when the conversation is idle and queues it when it is
// busy. It reports whether it started, and the queue position when it did not.
func (s *Server) startOrQueue(c *conversation, task string) (bool, int) {
	if !runnable(c) {
		pos := c.pushQueue(task)
		s.saveSession(c)
		return false, pos
	}
	taken, pos := c.takeSlotOrEnqueue(task)
	if !taken {
		afterQueued()
		// Saved, so a restart before it runs does not lose it; then drained, in case the slot was
		// free behind older messages.
		s.saveSession(c)
		s.drainQueue(c)
		return false, pos
	}
	s.launchRun(c, task, schedule.KindTask, "", "", s.approverFactory(c))
	return true, 0
}

// handleQueue sends a message to a conversation without waiting for its turn. When the
// agent is idle the message starts a run; when it is busy the message is queued and runs
// when the current turn ends.
func (s *Server) handleQueue(w http.ResponseWriter, r *http.Request) {
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
	if c.isMerged() {
		writeError(w, http.StatusConflict, msgSessionIntegrated)
		return
	}
	started, pos := s.startOrQueue(c, body.Task)
	if started {
		writeJSON(w, http.StatusAccepted, map[string]any{"queued": false, "started": true})
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"queued": true, "started": false, "position": pos})
}

// handleInterrupt stops the run in flight in this conversation. With a task, that message
// goes to the front of the queue and runs right after the interruption.
func (s *Server) handleInterrupt(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Task string `json:"task"`
	}
	if !s.decodeBody(w, r, &body) {
		return
	}
	c := convOf(r)
	rn, busy := c.queueBehindRun(body.Task)
	if !busy {
		writeError(w, http.StatusConflict, "there is no run in progress in this session to interrupt")
		return
	}
	afterQueuedBehindRun()
	if strings.TrimSpace(body.Task) != "" {
		s.saveSession(c)
	}
	if rn != nil {
		rn.cancel()
	}
	w.WriteHeader(http.StatusNoContent)
}
