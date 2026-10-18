package agent

import (
	"strings"
	"sync"
)

// steering is what the person said while a task ran. The run reads it at the start of its next
// round, as a note in the journal, so a message typed mid-task steers the work instead of
// stopping it (interrupt) or waiting for it to end (queue).
type steering struct {
	mu    sync.Mutex
	notes []string
}

// Steer hands the running task a message from the person. It reports whether a run was there
// to read it: a message to an idle agent is not steering, and the caller queues it instead.
func (a *Agent) Steer(text string) bool {
	text = strings.TrimSpace(text)
	if text == "" || !a.steer.open() {
		return false
	}
	a.steer.mu.Lock()
	defer a.steer.mu.Unlock()
	a.steer.notes = append(a.steer.notes, text)
	return true
}

// open says whether a task is reading its notes right now.
func (s *steering) open() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.notes != nil
}

// begin opens the notes of a starting task; end closes them so a message after the task ends is
// not swallowed by it.
func (s *steering) begin() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.notes = []string{}
}

func (s *steering) end() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.notes = nil
}

// take returns the notes written since the last round, and empties them.
func (s *steering) take() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.notes) == 0 {
		return nil
	}
	out := s.notes
	s.notes = []string{}
	return out
}

// absorbSteering puts the person's mid-task messages in front of the next round, in the journal
// the action phase reads, and in the trail the person sees.
func (r *taskRun) absorbSteering() {
	notes := r.a.steer.take()
	if len(notes) == 0 {
		return
	}
	for _, n := range notes {
		r.a.report("you: %s", n)
	}
	r.journal = append(r.journal, roundRecord{
		round:    r.round,
		kind:     roundProgress,
		commands: "(the person added: " + truncate(collapse(strings.Join(notes, " / ")), 160) + ")",
		detail: "THE PERSON WROTE THIS WHILE YOU WORKED. It is part of the task from now on, and it " +
			"wins over the plan where they differ:\n- " + strings.Join(notes, "\n- "),
	})
}
