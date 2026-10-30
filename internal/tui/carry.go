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
	if path := r.carryPath(); path != "" {
		if raw, err := os.ReadFile(path); err == nil { // #nosec G304 -- the conversation's own archive file.
			var c carried
			if json.Unmarshal(raw, &c) == nil {
				s.Restore(c.Summary, c.Pinned)
			}
		}
	}
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
	r.sessionMu.Lock()
	s := r.session
	r.sessionMu.Unlock()
	if s == nil {
		return r.savedPin()
	}
	return s.Pinned()
}

// SetPinnedPlan replaces the plan the conversation keeps, or drops it when text is blank. A
// conversation with no session yet (after a restart, before the next turn) builds it, so the
// change is the one the next turn starts from, and it is kept on disk at once.
func (r *AppRunner) SetPinnedPlan(text string) {
	cfg := r.Config()
	s := r.conversation(cfg, nil)
	s.Pin(text)
	r.saveCarry()
}

// savedPin is the plan kept on disk, for a conversation whose session is not built yet.
func (r *AppRunner) savedPin() string {
	path := r.carryPath()
	if path == "" {
		return ""
	}
	raw, err := os.ReadFile(path) // #nosec G304 -- the conversation's own archive file.
	if err != nil {
		return ""
	}
	var c carried
	if json.Unmarshal(raw, &c) != nil {
		return ""
	}
	return c.Pinned
}
