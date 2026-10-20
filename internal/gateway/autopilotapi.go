package gateway

import (
	"net/http"
	"time"

	"github.com/madkoding/motita/internal/config"
	"github.com/madkoding/motita/internal/schedule"
)

// THE AUTOPILOT SETTINGS, as the settings page's Upkeep tab shows and changes them: the two
// switches, the limit of open draft pull requests, and when each routine last ran. See autopilot.go
// for what the routines do and what they can never do.

// autopilotRoutine is one routine as the page draws it.
type autopilotRoutine struct {
	LastRun     string `json:"last_run"`
	NextRun     string `json:"next_run"`
	LastOutcome string `json:"last_outcome"`
	// Running says the routine is going on right now.
	Running bool `json:"running"`
}

// autopilotView is what GET and PUT /v1/autopilot answer. A routine that was never switched on has
// no record yet, and the page reads it as null.
type autopilotView struct {
	WeeklyAudit bool              `json:"weekly_audit"`
	AutoPR      bool              `json:"auto_pr"`
	MaxOpenPRs  int               `json:"max_open_prs"`
	CanSave     bool              `json:"can_save"`
	Audit       *autopilotRoutine `json:"audit"`
	Fixes       *autopilotRoutine `json:"fixes"`
}

// autopilotChange is what PUT /v1/autopilot takes: any of the fields.
type autopilotChange struct {
	WeeklyAudit *bool `json:"weekly_audit"`
	AutoPR      *bool `json:"auto_pr"`
	MaxOpenPRs  *int  `json:"max_open_prs"`
}

// autopilotRun is what POST /v1/autopilot/run takes: "audit" or "fixes".
type autopilotRun struct {
	What string `json:"what"`
}

// routineOf is the record of a routine as the page draws it, or nil when there is none.
func (s *Server) routineOf(id string) *autopilotRoutine {
	if s.schedules == nil {
		return nil
	}
	rec, err := s.schedules.Load(id)
	if err != nil || rec == nil {
		return nil
	}
	v := viewOfSchedule(*rec, time.Now())
	return &autopilotRoutine{LastRun: v.LastRun, NextRun: v.NextRun, LastOutcome: v.LastOutcome,
		Running: s.autopilotRunning(id)}
}

// writeAutopilot answers the current view, or the error that kept it from being read.
func (s *Server) writeAutopilot(w http.ResponseWriter) {
	cfg, canSave, err := s.securityConfig()
	if err != nil {
		writeError(w, http.StatusInternalServerError, errConfigUnreadable+err.Error())
		return
	}
	a := cfg.Autopilot
	writeJSON(w, http.StatusOK, autopilotView{
		WeeklyAudit: a.WeeklyAudit, AutoPR: a.AutoPR, MaxOpenPRs: a.MaxOpenPRs, CanSave: canSave,
		Audit: s.routineOf(autopilotAuditID), Fixes: s.routineOf(autopilotFixesID),
	})
}

func (s *Server) handleGetAutopilot(w http.ResponseWriter, _ *http.Request) { s.writeAutopilot(w) }

// handlePutAutopilot saves the change in the configuration file and brings the routines' records in
// line with the switches.
func (s *Server) handlePutAutopilot(w http.ResponseWriter, r *http.Request) {
	var body autopilotChange
	if !s.decodeBody(w, r, &body) {
		return
	}
	if body == (autopilotChange{}) {
		writeError(w, http.StatusBadRequest, "send at least one of weekly_audit, auto_pr, max_open_prs")
		return
	}
	if !s.needFile(w) {
		return
	}
	change := config.AutopilotChange{WeeklyAudit: body.WeeklyAudit, AutoPR: body.AutoPR, MaxOpenPRs: body.MaxOpenPRs}
	err := config.Transaction(s.opts.ConfigPath, func(tx config.Tx) error { return tx.SetAutopilot(change) })
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.info("the autopilot settings were changed from the settings page")
	s.syncAutopilot()
	s.writeAutopilot(w)
}

// handleRunAutopilot starts a routine now, without waiting for its week.
func (s *Server) handleRunAutopilot(w http.ResponseWriter, r *http.Request) {
	var body autopilotRun
	if !s.decodeBody(w, r, &body) {
		return
	}
	id := map[string]string{"audit": autopilotAuditID, "fixes": autopilotFixesID}[body.What]
	if id == "" {
		writeError(w, http.StatusBadRequest, `"what" is "audit" or "fixes"`)
		return
	}
	if code, why := s.launchAutopilot(id); code != 0 {
		writeError(w, code, why)
		return
	}
	w.WriteHeader(http.StatusAccepted)
	s.writeAutopilot(w)
}

// launchAutopilot starts a routine in the background. It answers 0 when it started, or the status
// and the reason it did not: a routine that is switched off or already running is a conflict.
func (s *Server) launchAutopilot(id string) (int, string) {
	cfg, _, err := s.securityConfig()
	if err != nil {
		return http.StatusInternalServerError, errConfigUnreadable + err.Error()
	}
	on := cfg.Autopilot.WeeklyAudit
	if id == autopilotFixesID {
		on = cfg.Autopilot.AutoPR
	}
	if !on {
		return http.StatusConflict, "this routine is switched off in Settings"
	}
	if !s.autopilotBegin(id) {
		return http.StatusConflict, errAutopilotBusy.Error()
	}
	s.scheduleWG.Go(func() {
		defer s.autopilotEnd(id)
		s.recordAutopilot(id, s.runAutopilotLocked(s.baseCtx, id, cfg))
	})
	return 0, ""
}

// recordAutopilot writes the end of a run started by hand onto the routine's record. A run by hand
// counts as the week's run, so LastRun moves: the next one is a week from now.
func (s *Server) recordAutopilot(id, outcome string) {
	if s.schedules == nil {
		return
	}
	found, err := s.schedules.Update(id, func(rec *schedule.Schedule) {
		rec.LastRun = time.Now()
		rec.RunCount++
		rec.LastOutcome = outcome
	})
	if msg := recordProblem(found, err); msg != "" {
		s.warn(msg, "id", id)
	}
}

// syncAutopilot brings the routines' records in line with the switches: a switch that is on has an
// enabled record (made when there is none), and one that is off has a disabled record or none.
func (s *Server) syncAutopilot() {
	if s.schedules == nil {
		return
	}
	cfg, _, err := s.securityConfig()
	if err != nil {
		s.warn("could not read the autopilot settings", "error", err.Error())
		return
	}
	for _, id := range autopilotIDs {
		on := cfg.Autopilot.WeeklyAudit
		if id == autopilotFixesID {
			on = cfg.Autopilot.AutoPR
		}
		s.syncRoutine(id, on)
	}
}

// syncRoutine makes the record of one routine match its switch.
func (s *Server) syncRoutine(id string, on bool) {
	found, err := s.schedules.Update(id, func(rec *schedule.Schedule) { rec.Enabled = on })
	if err != nil {
		s.warn("could not update a routine of the autopilot", "id", id, "error", err.Error())
		return
	}
	if found || !on {
		return
	}
	rec := schedule.Schedule{
		ID: id, Title: autopilotTitle(id), Task: "Managed from Settings, Upkeep.", Kind: schedule.KindPlan,
		SessionID: schedule.DefaultSessionID, Every: schedule.Duration(autopilotEvery), Enabled: true,
		Created: time.Now(),
	}
	if err := s.schedules.Save(rec); err != nil {
		s.warn("could not create a routine of the autopilot", "id", id, "error", err.Error())
	}
}
