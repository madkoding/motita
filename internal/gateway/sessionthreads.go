package gateway

import (
	"sort"
	"strings"
	"time"
)

// A project has ONE main session, and the work done in parallel inside it happens in threads.
//
// A thread is a session whose parentID names the project's main session. The person opens one with
// the project's "+" (a project holds as many sessions as they want), and the agent of the main session
// opens them too, when a request is about something new or different (one thread per kind of work,
// reused when the work comes back). The model is deliberately
// that small: a thread is not a new kind of object, it is a session with a parent, so everything a
// session already does (run, queue, integrate, open a pull request, be deleted) works on it unchanged.
//
// What a thread adds is isolation. The main session works in the project's folder, the way Claude
// Code works in a directory; a thread gets a worktree of its own, so two threads never edit the same
// files. That only means something where there is a repository: in a plain folder isolation is a
// no-op (see placeInProject) and a thread shares the folder like any session did before.

// createdAt is when the session was made.
func (c *conversation) createdAt() (t time.Time) {
	c.stateMu.Lock()
	defer c.stateMu.Unlock()
	return c.created
}

// parent is the id of the session this thread belongs to, empty for a main session.
func (c *conversation) parent() string {
	c.stateMu.Lock()
	defer c.stateMu.Unlock()
	return c.parentID
}

func (c *conversation) setParent(id string) {
	c.stateMu.Lock()
	defer c.stateMu.Unlock()
	c.parentID = id
}

// threadParent validates the main session a new session asks to be a thread of: it must be a live
// session of the same project. A thread of a thread is a thread of the main session, so the tree
// stays one level deep. Anything else answers empty and the session is simply a main one: a stale
// client still gets a working session.
func (s *Server) threadParent(projectID, asked string) string {
	c, ok := s.lookup(strings.TrimSpace(asked))
	if !ok {
		return ""
	}
	c.stateMu.Lock()
	defer c.stateMu.Unlock()
	if projectID == "" || c.projectID != projectID {
		return ""
	}
	if c.parentID != "" {
		return c.parentID
	}
	return c.id
}

// joinThread makes a new session a thread of the one it asked for, if that is valid, and reports
// whether it must get a worktree of its own (a thread always does).
func (s *Server) joinThread(conv *conversation, projectID, asked string, isolate bool) bool {
	parent := s.threadParent(projectID, asked)
	conv.setParent(parent)
	return isolate || parent != ""
}

// oldestFirst orders sessions by creation time, ties by id so the order is stable.
func oldestFirst(cs []*conversation) {
	sort.Slice(cs, func(i, j int) bool { return cs[i].id < cs[j].id })
	sort.SliceStable(cs, func(i, j int) bool { return cs[i].createdAt().Before(cs[j].createdAt()) })
}

// adoptThreads gives the sessions saved before threads existed the shape the model expects: in each
// project the oldest session is the main one and the others become its threads. Their workspaces are
// left as they were - nothing is moved or recreated - so the migration only records who belongs to
// whom. It is idempotent: a project that already has a main session with threads is not touched.
func (s *Server) adoptThreads() {
	byProject := map[string][]*conversation{}
	for _, c := range s.snapshot() {
		c.stateMu.Lock()
		pid, parent := c.projectID, c.parentID
		c.stateMu.Unlock()
		if pid != "" && parent == "" {
			byProject[pid] = append(byProject[pid], c)
		}
	}
	for _, roots := range byProject {
		if len(roots) < 2 {
			continue
		}
		oldestFirst(roots)
		for _, c := range roots[1:] {
			c.setParent(roots[0].id)
			s.saveSession(c)
		}
	}
}

// promoteThreads runs when a session is deleted. If it was a main session its oldest thread takes
// its place and the other threads follow that one, so a project never ends up with threads of a
// session that is gone.
func (s *Server) promoteThreads(deleted string) {
	var kids []*conversation
	for _, c := range s.snapshot() {
		if c.id != deleted && c.parent() == deleted {
			kids = append(kids, c)
		}
	}
	if len(kids) == 0 {
		return
	}
	oldestFirst(kids)
	kids[0].setParent("")
	s.saveSession(kids[0])
	for _, c := range kids[1:] {
		c.setParent(kids[0].id)
		s.saveSession(c)
	}
}
