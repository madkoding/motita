package gateway

import "errors"

// A deletion has to stop more than the run in flight. Waiting for the run's done channel used to be
// all it did, and the run goroutine goes on after done: it drains the queue (starting the next
// queued message in a conversation about to be forgotten), asks for an auto-title (which, for the
// default session, landed on the conversation the reset had just emptied) and follows a pull
// request it may have opened. The conversation is therefore marked as closing BEFORE the run is
// stopped, and every one of those paths checks the mark.

// beginClosing marks the conversation as being deleted: from now on no run starts in it, its
// queue is not drained, and a title asked for before this point is not written.
func (c *conversation) beginClosing() {
	c.stateMu.Lock()
	defer c.stateMu.Unlock()
	c.closing = true
	c.epoch++
}

// closeForGood drops what was queued in a conversation that is being forgotten. It stays closing.
func (c *conversation) closeForGood() {
	c.stateMu.Lock()
	defer c.stateMu.Unlock()
	c.queue = nil
}

// reopen ends the closing state of a conversation that was NOT forgotten: a deletion that was
// refused, or the reset of the default session. A reset drops the queue - the messages were for the
// conversation that is gone - while a refused deletion keeps it for the conversation that stays.
func (c *conversation) reopen(dropQueue bool) {
	c.stateMu.Lock()
	defer c.stateMu.Unlock()
	c.closing = false
	if dropQueue {
		c.queue = nil
	}
}

// isClosing reports whether the conversation is being deleted or is gone.
func (c *conversation) isClosing() bool {
	c.stateMu.Lock()
	defer c.stateMu.Unlock()
	return c.closing
}

// reopenAndDrain reopens a conversation and starts what it kept queued: the drain refused while it
// was closing, so whatever the run's own drain left behind is started here.
func (s *Server) reopenAndDrain(c *conversation, dropQueue bool) {
	c.reopen(dropQueue)
	s.drainQueue(c)
}

// closeProjectSessions gets a project's sessions ready to be forgotten: each is marked closing, its
// run is stopped and waited for, and its checkout given back. Any refusal reopens every one of them
// and is returned: nothing is forgotten until all of them can be, so a partial deletion cannot
// leave sessions unreachable AND their worktrees gone.
//
// A checkout with uncommitted work is refused, not forced, unless discard says the user confirmed
// it: the message names the session and its files, so the decision stays with the user.
func (s *Server) closeProjectSessions(sessions []*conversation, discard bool) error {
	for _, c := range sessions {
		c.beginClosing()
	}
	err := s.stopProjectRuns(sessions)
	if err == nil && !discard {
		// Every checkout is proven safe BEFORE any is given back: a refusal over the last one must
		// not find the first ones already removed under sessions that stay.
		err = s.refuseAnyUnsafeRelease(sessions)
	}
	for _, c := range sessions {
		if err != nil {
			break
		}
		err = s.releaseWorktree(c, discard)
	}
	if err != nil {
		for _, c := range sessions {
			s.reopenAndDrain(c, false)
		}
	}
	return err
}

// refuseAnyUnsafeRelease is the first refusal releasing one of the sessions' checkouts would meet.
func (s *Server) refuseAnyUnsafeRelease(sessions []*conversation) error {
	for _, c := range sessions {
		if release := s.inspectWorktree(c); release.Workspace != "" {
			if err := refuseUnsafeRelease(release); err != nil {
				return err
			}
		}
	}
	return nil
}

// stopProjectRuns stops the run of every session of a project being deleted and waits for each.
func (s *Server) stopProjectRuns(sessions []*conversation) error {
	for _, c := range sessions {
		if stopped, settled := c.stopRunForDeletion(deleteStopTimeout); stopped && !settled {
			return errProjectRunStuck
		}
	}
	return nil
}

// errProjectRunStuck is the refusal of a project deletion whose run would not stop.
var errProjectRunStuck = errors.New("a run in one of this project's sessions did not stop in time, " +
	"so the project was not deleted: stop it and try again")
