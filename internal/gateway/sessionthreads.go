package gateway

import (
	"sort"
	"strings"
	"time"
)

// A project has ONE main session, and the work done in parallel inside it happens in threads.
//
// A thread is a session whose parentID names the project's main session. The model is deliberately
// that small: a thread is not a new kind of object, it is a session with a parent, so everything a
// session already does (run, queue, integrate, open a pull request, be deleted) works on it unchanged.
//
// What a thread adds is isolation. The main session works in the project's folder, the way Claude
// Code works in a directory; a thread gets a worktree of its own, so two threads never edit the same
// files. That only means something where there is a repository: in a plain folder isolation is a
// no-op (see placeInProject) and a thread shares the folder like any session did before.

// mainSession is the project's main session: the oldest one that has no parent. Nil when the
// project has no session yet.
func (s *Server) mainSession(projectID string) *conversation {
	if projectID == "" {
		return nil
	}
	var main *conversation
	for _, c := range s.snapshot() {
		c.stateMu.Lock()
		own := c.projectID == projectID && c.parentID == ""
		created := c.created
		c.stateMu.Unlock()
		if !own {
			continue
		}
		if main == nil || created.Before(main.createdAt()) {
			main = c
		}
	}
	return main
}

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

// threadParent decides where a new session of a project goes. The first session of a project is its
// main one and gets no parent; every later one is a thread of the main session and gets its own
// worktree. A client may name the parent itself; naming a session of another project, or one that
// is gone, is ignored rather than refused, so a stale client still gets a working session.
func (s *Server) threadParent(projectID, asked string) string {
	if asked = strings.TrimSpace(asked); asked != "" {
		if c, ok := s.lookup(asked); ok {
			c.stateMu.Lock()
			same, root := c.projectID == projectID, c.parentID
			c.stateMu.Unlock()
			if same {
				// A thread of a thread is a thread of the main session: the tree is one level deep.
				if root != "" {
					return root
				}
				return asked
			}
		}
	}
	if main := s.mainSession(projectID); main != nil {
		return main.id
	}
	return ""
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
		sort.Slice(roots, func(i, j int) bool {
			ci, cj := roots[i].createdAt(), roots[j].createdAt()
			if !ci.Equal(cj) {
				return ci.Before(cj)
			}
			return roots[i].id < roots[j].id
		})
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
	sort.Slice(kids, func(i, j int) bool {
		ci, cj := kids[i].createdAt(), kids[j].createdAt()
		if !ci.Equal(cj) {
			return ci.Before(cj)
		}
		return kids[i].id < kids[j].id
	})
	kids[0].setParent("")
	s.saveSession(kids[0])
	for _, c := range kids[1:] {
		c.setParent(kids[0].id)
		s.saveSession(c)
	}
}
