package gateway

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
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

// scheduleRequest is what a client may send to create or change a task.
//
// Every field is optional on PATCH and required on POST, and that asymmetry is
// deliberate: a PATCH that had to carry the whole record would make "pause this" a
// request that can silently rewrite the task.
type scheduleRequest struct {
	Title     string `json:"title"`
	Task      string `json:"task"`
	Kind      string `json:"kind"`
	SessionID string `json:"session_id"`
	// Every is a DURATION STRING ("30m"). It is not a time.Duration, because that type
	// marshals to an integer of nanoseconds: a request that reads `"every":1800000000000`
	// is a request nobody can write by hand or review in a log.
	Every   string `json:"every"`
	Enabled *bool  `json:"enabled"`
}

// minEvery is the floor in force, defaulted when nothing was configured.
func (s *Server) minEvery() time.Duration {
	if s.opts.ScheduleMinEvery > 0 {
		return s.opts.ScheduleMinEvery
	}
	return time.Minute
}

// parseEvery reads the cadence and enforces the floor.
//
// The floor is enforced HERE and not only in the configuration, because a request body
// is a second way in: a cadence that the configuration refuses must not be reachable by
// typing it into a form.
func (s *Server) parseEvery(text string) (schedule.Duration, error) {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return 0, errors.New("the request needs 'every': a task with no cadence would never fire")
	}
	d, err := time.ParseDuration(trimmed)
	if err != nil {
		return 0, fmt.Errorf("could not read 'every' as a duration (use \"30m\", \"1h\", \"1d\" is NOT valid): %w", err)
	}
	if d < s.minEvery() {
		return 0, fmt.Errorf("'every' is %s, which is below the minimum of %s: a tighter cadence starts a task faster than an agent turn can finish", d, s.minEvery())
	}
	return schedule.Duration(d), nil
}

// handleCreateSchedule mints a task that fires on its own.
func (s *Server) handleCreateSchedule(w http.ResponseWriter, r *http.Request) {
	if s.schedules == nil {
		writeError(w, http.StatusNotImplemented, "this gateway was started without a schedule directory")
		return
	}
	var body scheduleRequest
	if !s.decodeBody(w, r, &body) {
		return
	}
	title := strings.TrimSpace(body.Title)
	if title == "" {
		writeError(w, http.StatusBadRequest, "the request needs 'title': it is the only field a person reads")
		return
	}
	task := strings.TrimSpace(body.Task)
	if task == "" {
		writeError(w, http.StatusBadRequest, "the request needs 'task': it is the text the agent will run")
		return
	}
	kind := strings.TrimSpace(body.Kind)
	switch kind {
	case "":
		kind = schedule.KindTask
	case schedule.KindTask, schedule.KindPlan:
	default:
		writeError(w, http.StatusBadRequest, fmt.Sprintf("unknown 'kind': %q (use %q or %q)", kind, schedule.KindTask, schedule.KindPlan))
		return
	}
	every, err := s.parseEvery(body.Every)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	// A task fires INTO a conversation that already exists. Creating one here would make
	// a scheduled run appear in a conversation the user never opened, and the sessions
	// the gateway holds are the only place a run can be watched.
	sessionID := strings.TrimSpace(body.SessionID)
	if sessionID == "" {
		sessionID = schedule.DefaultSessionID
	}
	if _, ok := s.lookup(sessionID); !ok {
		writeError(w, http.StatusNotFound, fmt.Sprintf("there is no session %q to fire into", sessionID))
		return
	}

	id, err := newSessionID()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	rec := schedule.Schedule{
		ID:        id,
		Title:     title,
		Task:      task,
		Kind:      kind,
		SessionID: sessionID,
		Every:     every,
		Enabled:   true,
		Created:   time.Now(),
	}
	if err := s.schedules.Save(rec); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"schedule": viewOfSchedule(rec, time.Now())})
}
