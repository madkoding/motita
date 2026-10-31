package tui

// What the Plan conversation carries across a restart. The session lives in memory, so a gateway
// that restarts used to answer the next question with a blank conversation: no summary, no plan,
// none of the turns the person can still see on screen. The summary and the pinned plan are kept
// in the conversation's archive folder, and the turns come back from the transcript the gateway
// already restores.

import (
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/madkoding/motita/internal/agent"
	"github.com/madkoding/motita/internal/llm"
	"github.com/madkoding/motita/internal/session"
)

// carryFile is the file in the archive folder that holds what the session carries.
const carryFile = "carried.json"

// seedTurns is how many of the last turns a rebuilt session is given verbatim; older ones are in
// the summary and the archive's recall.
const seedTurns = 8

// carried is the file's shape.
type carried struct {
	Summary string `json:"summary,omitempty"`
	Pinned  string `json:"pinned,omitempty"`
	// Proposed marks a pinned plan the person had not approved yet.
	Proposed bool `json:"proposed,omitempty"`
}

// carryPath is where this conversation keeps what its session carries, or "" when it has no
// archive (the feature is off, or there is no home).
func (r *AppRunner) carryPath() string {
	store := r.contextStore()
	if store == nil {
		return ""
	}
	return filepath.Join(store.Root(), carryFile)
}

// saveCarry writes what the session carries after a turn. Nothing to carry removes the file, so a
// plan that was dropped does not come back.
func (r *AppRunner) saveCarry() {
	path := r.carryPath()
	if path == "" {
		return
	}
	r.sessionMu.Lock()
	s := r.session
	r.sessionMu.Unlock()
	if s == nil {
		return
	}
	var c carried
	c.Summary, c.Pinned = s.Carried()
	c.Proposed = s.PinState() == session.PinProposed
	if c.Summary == "" && c.Pinned == "" {
		_ = os.Remove(path)
		return
	}
	data, _ := json.Marshal(c)
	if err := os.WriteFile(path, data, 0o600); err != nil && r.Log != nil {
		r.Log.Warn("could not keep the conversation's summary and plan", "error", err.Error())
	}
}

// seedSession gives a session built for a conversation that already has turns what it had: the
// carried summary and plan from disk, and the last turns from the transcript.
func (r *AppRunner) seedSession(s *session.Session) {
	c := r.savedCarry()
	s.Restore(c.Summary, c.Pinned, c.Proposed)
	s.AppendAll(seedMessages(r.history())...)
}

// seedMessages is the last seedTurns finished turns as the user and assistant messages the
// session would have held. A turn without an answer (a failure, one still running) is left out.
func seedMessages(turns []agent.DialogueTurn) []llm.Message {
	if len(turns) > seedTurns {
		turns = turns[len(turns)-seedTurns:]
	}
	var out []llm.Message
	for _, t := range turns {
		if t.Pending || t.User == "" || t.Agent == "" {
			continue
		}
		out = append(out,
			llm.Message{Role: "user", Content: t.User},
			llm.Message{Role: "assistant", Content: t.Agent})
	}
	return out
}

// PinnedPlan is the plan the conversation keeps word for word, or "" when there is none. The
// person can read it, change it or drop it from the web (see SetPinnedPlan).
func (r *AppRunner) PinnedPlan() string {
	plan, _ := r.pinnedPlan()
	return plan
}

// PinnedPlanState is session.PinActive or session.PinProposed, or "" when no plan is pinned.
func (r *AppRunner) PinnedPlanState() string {
	_, state := r.pinnedPlan()
	return state
}

// pinnedPlan is the pinned plan and its state, from the session or, before it is built, from disk.
func (r *AppRunner) pinnedPlan() (plan, state string) {
	r.sessionMu.Lock()
	s := r.session
	r.sessionMu.Unlock()
	if s != nil {
		return s.Pinned(), s.PinState()
	}
	c := r.savedCarry()
	switch {
	case c.Pinned == "":
		return "", ""
	case c.Proposed:
		return c.Pinned, session.PinProposed
	default:
		return c.Pinned, session.PinActive
	}
}

// briefPlan is the pinned plan as a task's brief carries it: a plan the person has not approved
// yet says so, so the task does not take it for its orders.
func (r *AppRunner) briefPlan() string {
	plan, state := r.pinnedPlan()
	if state == session.PinProposed {
		return "(Proposed earlier and NOT approved yet: context only, not work to carry out.)\n\n" + plan
	}
	return plan
}

// SetPinnedPlan replaces the plan the conversation keeps, or drops it when text is blank. A plan
// the person writes or saves is one they approved, so it is pinned as active. A conversation with
// no session yet (after a restart, before the next turn) builds it, so the change is the one the
// next turn starts from, and it is kept on disk at once.
func (r *AppRunner) SetPinnedPlan(text string) {
	cfg := r.Config()
	s := r.conversation(cfg, nil)
	s.Pin(text)
	r.saveCarry()
}

// pinFollowedPlan pins the plan a task carries out when the person approved it with a go-ahead:
// from now on it is the active plan, replacing any older one. Any other task leaves the pin alone.
func (r *AppRunner) pinFollowedPlan(task string) {
	if plan, following := agent.FollowedPlan(task); following && plan != "" {
		r.SetPinnedPlan(plan)
	}
}

// releaseFollowedPlan lets go of the plan a task carried out once the task passed: a finished plan
// left pinned kept sending the agent back to work already done. A task that failed keeps it, so
// "continue" still has it. It answers whether the plan was let go.
func (r *AppRunner) releaseFollowedPlan(task string, pass bool) bool {
	plan, following := agent.FollowedPlan(task)
	if !following || !pass {
		return false
	}
	r.sessionMu.Lock()
	s := r.session
	r.sessionMu.Unlock()
	if s == nil || !s.Release(plan) {
		return false
	}
	r.saveCarry()
	return true
}

// savedCarry is what the conversation keeps on disk, empty when there is nothing or no archive.
func (r *AppRunner) savedCarry() carried {
	var c carried
	path := r.carryPath()
	if path == "" {
		return c
	}
	raw, err := os.ReadFile(path) // #nosec G304 -- the conversation's own archive file.
	if err != nil || json.Unmarshal(raw, &c) != nil {
		return carried{}
	}
	return c
}

// shareLastTurn puts the turn the Task path just finished into the Plan session, so a question
// asked next in chat sees what the work did. The two paths record separately, and the session used
// to hold only Plan turns: a task done between two questions was invisible to the second one until
// it left the window and could be recalled from the archive. A session not built yet needs nothing:
// it is seeded from the transcript when it is.
func (r *AppRunner) shareLastTurn() {
	r.sessionMu.Lock()
	s := r.session
	r.sessionMu.Unlock()
	turns := r.history()
	if s == nil || len(turns) == 0 {
		return
	}
	s.AppendAll(seedMessages(turns[len(turns)-1:])...)
}
