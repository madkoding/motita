package gateway

import (
	"fmt"
	"strings"
)

// The thread action lets the agent of a project's main session decide that a request is a new or a
// different kind of work and hand it to a thread (see sessionthreads.go). The person never opens
// threads; this is how they get opened. A thread is found by its topic, which is its title, so the
// same kind of work comes back to the same thread instead of making another one.

// offerThreads gives a project's main session the thread action. Threads and sessions of no
// project are not given it, and a service that cannot take it is left as it is.
func (s *Server) offerThreads(conv *conversation) {
	conv.stateMu.Lock()
	main := conv.projectID != "" && conv.parentID == ""
	conv.stateMu.Unlock()
	if !main {
		return
	}
	if setter, ok := conv.svc.(interface{ SetThreads(func(string) string) }); ok {
		setter.SetThreads(func(command string) string { return s.routeThread(conv, command) })
	}
}

// routeThread answers one thread action of a main session: "list", or "topic :: brief".
func (s *Server) routeThread(main *conversation, command string) string {
	if main.parent() != "" {
		return "Error: this session is a thread, and a thread cannot open threads."
	}
	command = strings.TrimSpace(command)
	if strings.EqualFold(command, "list") {
		return s.listThreads(main)
	}
	topic, brief, ok := strings.Cut(command, "::")
	topic, brief = strings.TrimSpace(topic), strings.TrimSpace(brief)
	if !ok || topic == "" || brief == "" {
		return `Error: the thread action expects "list" or "topic :: the full brief".`
	}
	th, opened, err := s.threadFor(main, topic)
	if err != nil {
		return "Error: " + err.Error()
	}
	started, pos := s.startOrQueue(th, brief)
	how := "opened"
	if !opened {
		how = "reused"
	}
	if started {
		return fmt.Sprintf("Thread %q %s: it started working on the brief in its own worktree.", topic, how)
	}
	return fmt.Sprintf("Thread %q %s: it is busy, so the brief is number %d in its queue.", topic, how, pos)
}

// threadsOf are the threads of a main session, oldest first.
func (s *Server) threadsOf(main *conversation) []*conversation {
	var out []*conversation
	for _, c := range s.snapshot() {
		if c.parent() == main.id {
			out = append(out, c)
		}
	}
	oldestFirst(out)
	return out
}

func (s *Server) listThreads(main *conversation) string {
	threads := s.threadsOf(main)
	if len(threads) == 0 {
		return "This project has no threads yet."
	}
	var sb strings.Builder
	for _, th := range threads {
		st := th.status()
		state := "idle"
		if st.Running {
			state = "working"
		}
		fmt.Fprintf(&sb, "- %s (%s)\n", st.Title, state)
	}
	return sb.String()
}

// sameTopic compares two topics ignoring case and spacing.
func sameTopic(a, b string) bool {
	return strings.EqualFold(strings.Join(strings.Fields(a), " "), strings.Join(strings.Fields(b), " "))
}

// threadFor is the thread of a topic: the one that exists, or a new one in a worktree of its own.
func (s *Server) threadFor(main *conversation, topic string) (*conversation, bool, error) {
	for _, th := range s.threadsOf(main) {
		if sameTopic(th.status().Title, topic) {
			return th, false, nil
		}
	}
	main.stateMu.Lock()
	pid := main.projectID
	main.stateMu.Unlock()
	project := s.scopeOf(pid)
	if project == nil {
		return nil, false, ErrProjectNotFound
	}
	th, err := s.createSession()
	if err != nil {
		return nil, false, err
	}
	th.setTitle(topic)
	s.placeAndSave(th, pid, project, nil, s.joinThread(th, pid, main.id, true))
	return th, true, nil
}
