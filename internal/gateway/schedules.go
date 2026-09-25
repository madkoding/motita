package gateway

import (
	"net/http"
	"time"

	"github.com/madkoding/motita/internal/schedule"
)

// The endpoints that answer about the tasks which fire on their own.
//
// They are PROCESS-wide rather than scoped to a conversation, and the reason is that a
// schedule is not a property of one: it exists whether or not a client is connected, and
// the conversation it fires into is a FIELD of its record. That is the same split
// /v1/projects already uses, and keeping the rule is worth more than shortening the URL.

// scheduleView is what a front end is told about one task.
//
// It is a DTO of its own rather than the record itself, for the reason configView exists:
// what goes on the wire is a decision, and a struct that is serialised by accident is a
// struct whose next field leaks. Here nothing is secret, so the two happen to have the
// same fields - but the decision stays explicit so that adding a field to the record does
// not publish it.
type scheduleView struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	Task        string `json:"task"`
	Kind        string `json:"kind"`
	SessionID   string `json:"session_id"`
	Every       string `json:"every"`
	Enabled     bool   `json:"enabled"`
	Created     string `json:"created"`
	LastRun     string `json:"last_run,omitempty"`
	LastOutcome string `json:"last_outcome,omitempty"`
	RunCount    int    `json:"run_count"`
	// NextRun is computed HERE so a front end can say "next at 14:00" without a second
	// implementation of the cadence arithmetic, which is how two answers to the same
	// question start disagreeing.
	NextRun string `json:"next_run"`
}

// viewOfSchedule reduces one record to what may leave the process.
func viewOfSchedule(s schedule.Schedule, now time.Time) scheduleView {
	v := scheduleView{
		ID:          s.ID,
		Title:       s.Title,
		Task:        s.Task,
		Kind:        s.Kind,
		SessionID:   s.SessionID,
		Every:       time.Duration(s.Every).String(),
		Enabled:     s.Enabled,
		Created:     s.Created.Format(time.RFC3339),
		LastOutcome: s.LastOutcome,
		RunCount:    s.RunCount,
		NextRun:     s.Next(now).Format(time.RFC3339),
	}
	if !s.LastRun.IsZero() {
		v.LastRun = s.LastRun.Format(time.RFC3339)
	}
	return v
}

// scheduleList is the shared body of every endpoint that answers with the whole set:
// list, create, update and delete all return it, so a client never has to reconcile a
// response with a second round trip.
func (s *Server) scheduleList() []scheduleView {
	out := []scheduleView{}
	if s.schedules == nil {
		return out
	}
	all, err := s.schedules.LoadAll()
	if err != nil {
		return out
	}
	now := time.Now()
	for _, sc := range all {
		out = append(out, viewOfSchedule(sc, now))
	}
	return out
}

// handleListSchedules answers every task this gateway knows about.
func (s *Server) handleListSchedules(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"schedules": s.scheduleList()})
}
