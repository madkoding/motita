package gateway

import (
	"context"
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

// handleUpdateSchedule changes one task. It is PATCH semantics: only what the body
// carries is applied, so "pause this" cannot rewrite the task by accident.
func (s *Server) handleUpdateSchedule(w http.ResponseWriter, r *http.Request) {
	rec, ok := s.scheduleOf(w, r)
	if !ok {
		return
	}
	var body scheduleRequest
	if !s.decodeBody(w, r, &body) {
		return
	}

	if body.Title != "" {
		title := strings.TrimSpace(body.Title)
		if title == "" {
			writeError(w, http.StatusBadRequest, "the title cannot be blank")
			return
		}
		rec.Title = title
	}
	if body.Task != "" {
		task := strings.TrimSpace(body.Task)
		if task == "" {
			writeError(w, http.StatusBadRequest, "the task cannot be blank")
			return
		}
		rec.Task = task
	}
	if body.Kind != "" {
		switch body.Kind {
		case schedule.KindTask, schedule.KindPlan:
			rec.Kind = body.Kind
		default:
			writeError(w, http.StatusBadRequest, fmt.Sprintf("unknown 'kind': %q (use %q or %q)", body.Kind, schedule.KindTask, schedule.KindPlan))
			return
		}
	}
	if body.Every != "" {
		every, err := s.parseEvery(body.Every)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		rec.Every = every
	}
	if body.SessionID != "" {
		if _, ok := s.lookup(body.SessionID); !ok {
			writeError(w, http.StatusNotFound, fmt.Sprintf("there is no session %q to fire into", body.SessionID))
			return
		}
		rec.SessionID = body.SessionID
	}
	if body.Enabled != nil {
		rec.Enabled = *body.Enabled
	}

	if err := s.schedules.Save(*rec); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"schedule": viewOfSchedule(*rec, time.Now())})
}

// scheduleOf resolves the {id} in the path, answering for a missing one HERE so no
// handler forgets to.
//
// A gateway with no store answers 501 rather than 404: the capability is ABSENT, not the
// record, and a client that reads 404 would go looking for a task that could never exist.
func (s *Server) scheduleOf(w http.ResponseWriter, r *http.Request) (*schedule.Schedule, bool) {
	if s.schedules == nil {
		writeError(w, http.StatusNotImplemented, "this gateway was started without a schedule directory")
		return nil, false
	}
	id := r.PathValue("id")
	rec, err := s.schedules.Load(id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return nil, false
	}
	if rec == nil {
		writeError(w, http.StatusNotFound, fmt.Sprintf("there is no scheduled task %q", id))
		return nil, false
	}
	return rec, true
}

// handleDeleteSchedule removes one task.
func (s *Server) handleDeleteSchedule(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.scheduleOf(w, r); !ok {
		return
	}
	if err := s.schedules.Delete(r.PathValue("id")); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleRunScheduleNow starts the task immediately, without touching its cadence.
//
// 202 and not 200: a run is STARTED, not finished, and the same is true of a scheduled
// firing. The cadence is deliberately left alone - "run it now" is a person overriding
// the clock once, not a decision to move the schedule.
//
// An optional body may carry {"wait": true}, which blocks until the run ends and answers
// with its outcome. It exists for a script that wants the result; the browser does not use
// it, because blocking an HTTP request on an agent turn is what the SSE stream is for.
func (s *Server) handleRunScheduleNow(w http.ResponseWriter, r *http.Request) {
	rec, ok := s.scheduleOf(w, r)
	if !ok {
		return
	}
	var body struct {
		Wait bool `json:"wait"`
	}
	if r.ContentLength > 0 {
		if !s.decodeBody(w, r, &body) {
			return
		}
	}

	rn, started := s.startDetachedRun(s.conversationOf(rec.SessionID), rec.Task, rec.Kind,
		s.unattendedApprover(rec.ID))
	if !started {
		writeError(w, http.StatusConflict,
			fmt.Sprintf("a run is already in progress in session %q, which is where this task fires", rec.SessionID))
		return
	}
	if !body.Wait {
		writeJSON(w, http.StatusAccepted, map[string]any{"schedule": viewOfSchedule(*rec, time.Now()), "run_id": rn.id})
		return
	}
	<-rn.done
	outcome, result, errText, _ := rn.outcomeOf()
	writeJSON(w, http.StatusOK, map[string]any{"outcome": outcome, "result": result, "error": errText})
}

// startScheduler runs the watcher that fires what is due, in the background, for the
// life of the gateway.
//
// It mirrors startUpdateChecker: one goroutine, cancelled with the process, and a no-op
// when the feature has no store. The firer BLOCKS until the run ends, which is what makes
// LastOutcome a fact instead of a hope; a firing that is still in flight is not joined by
// the next pass (see schedule.Watcher's overlap guard).
func (s *Server) startScheduler() {
	if s.schedules == nil {
		return
	}
	tick := s.opts.ScheduleTick
	if tick <= 0 {
		tick = defaultScheduleTick
	}
	store := s.schedules
	fire := func(ctx context.Context, sc schedule.Schedule) (string, error) {
		c := s.conversationOf(sc.SessionID)
		if c == nil {
			// Recorded rather than repaired: creating a conversation for a task whose
			// session was deleted would put a run in a place the user never opened. The
			// front end shows the outcome, and the fix is to edit the task.
			return "", fmt.Errorf("there is no session %q to fire into: edit the task or create the session again", sc.SessionID)
		}
		rn, ok := s.startDetachedRun(c, sc.Task, sc.Kind, s.unattendedApprover(sc.ID))
		if !ok {
			return "", fmt.Errorf("a run is already in progress in session %q", sc.SessionID)
		}
		<-rn.done
		outcome, result, errText, _ := rn.outcomeOf()
		switch outcome {
		case "done":
			return fmt.Sprintf("the run finished: %s", firstLine(result)), nil
		case "cancelled":
			return "the run was cancelled", nil
		default:
			return "", fmt.Errorf("the run failed: %s", errText)
		}
	}

	w := schedule.NewWatcher(store, fire, s.opts.Log)
	w.SetTick(tick)
	go w.Run(s.baseCtx)
}

// defaultScheduleTick is the resolution at which a due task is noticed when nothing is
// configured. Half a minute: fine enough that "every 5 minutes" means it, coarse enough
// that the process is not woken for nothing.
const defaultScheduleTick = 30 * time.Second

// firstLine keeps one line of a run's result for the record. A whole transcript in a
// JSON field is a field nobody reads.
func firstLine(text string) string {
	if i := strings.IndexByte(text, '\n'); i >= 0 {
		text = text[:i]
	}
	const max = 200
	if len(text) > max {
		text = text[:max] + "…"
	}
	return strings.TrimSpace(text)
}
