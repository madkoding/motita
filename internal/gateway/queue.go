package gateway

import (
	"net/http"
	"strings"

	"github.com/madkoding/motita/internal/schedule"
)

// pushQueue adds a message to the conversation's queue (at the front when first is set)
// and returns its position, counting from 1.
func (c *conversation) pushQueue(task string, first bool) int {
	c.stateMu.Lock()
	defer c.stateMu.Unlock()
	if first {
		c.queue = append([]string{task}, c.queue...)
		return 1
	}
	c.queue = append(c.queue, task)
	return len(c.queue)
}

// popQueue removes and returns the oldest queued message.
func (c *conversation) popQueue() (string, bool) {
	c.stateMu.Lock()
	defer c.stateMu.Unlock()
	if len(c.queue) == 0 {
		return "", false
	}
	next := c.queue[0]
	c.queue = c.queue[1:]
	return next, true
}

// drainQueue starts the next queued message of a conversation whose run just ended.
// It does nothing while the gateway is closing: a queued message must not start work
// on a process that is going away.
func (s *Server) drainQueue(c *conversation) {
	if s.baseCtx.Err() != nil {
		return
	}
	next, ok := c.popQueue()
	if !ok {
		return
	}
	if _, started := s.startDetachedRun(c, next, schedule.KindTask, "", s.approverFactory(c)); !started {
		// The slot was taken in between: keep the message for the run that holds it.
		c.pushQueue(next, true)
	}
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
	if c.merged {
		writeError(w, http.StatusConflict, "this session has already been integrated into the project; continue in a new session to keep working")
		return
	}
	if _, started := s.startDetachedRun(c, body.Task, schedule.KindTask, "", s.approverFactory(c)); started {
		writeJSON(w, http.StatusAccepted, map[string]any{"queued": false, "started": true})
		return
	}
	pos := c.pushQueue(body.Task, false)
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
	if !c.isRunning() {
		writeError(w, http.StatusConflict, "there is no run in progress in this session to interrupt")
		return
	}
	if strings.TrimSpace(body.Task) != "" {
		c.pushQueue(body.Task, true)
	}
	c.cancelRun()
	w.WriteHeader(http.StatusNoContent)
}
