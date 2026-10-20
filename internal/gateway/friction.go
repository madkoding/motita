package gateway

import (
	"fmt"
	"net/http"
	"time"

	"github.com/madkoding/motita/internal/agent"
	"github.com/madkoding/motita/internal/friction"
	"github.com/madkoding/motita/internal/i18n"
)

// THE FRICTION JOURNAL: what got in the way, kept on this computer.
//
// A run that fails, a step that has to be tried again, an action that is refused and an answer
// the person marks as not good each leave one line in a local file (see internal/friction), and
// the settings read it back as a weekly summary. It only reads the run: nothing here changes
// what the agent does, and nothing leaves the machine.

// openFriction opens the journal, in memory when no file is configured. A file that cannot be
// read is NOT fatal: the gateway serves with a journal that lives until it stops, and says so.
func (s *Server) openFriction() {
	j, err := friction.Open(s.opts.FrictionFile)
	if err != nil {
		s.warn("could not open the friction journal; it is kept in memory only", "error", err.Error())
		j, _ = friction.Open("")
	}
	s.friction = j
}

// noteFriction writes one entry about a turn of a conversation. A disk that refuses is a warning:
// the journal is a convenience and must never fail the run it is describing.
func (s *Server) noteFriction(c *conversation, turn int, kind, text string) {
	s.recordFriction(friction.Entry{Kind: kind, Session: c.id, Project: c.projectID, Turn: turn, Text: text})
}

func (s *Server) recordFriction(e friction.Entry) {
	if err := s.friction.Record(e); err != nil {
		s.warn("could not write the friction journal", "error", err.Error())
	}
}

// watchRetries wraps a turn's progress callback so a line that reports a step being tried again
// is also written to the journal. The line itself goes on to the stream untouched.
func (s *Server) watchRetries(c *conversation, rn *run, progress func(string, ...any)) func(string, ...any) {
	return func(format string, args ...any) {
		line := fmt.Sprintf(format, args...)
		if agent.IsRetryLine(line) {
			s.noteFriction(c, rn.turn, friction.KindRetry, line)
		}
		progress("%s", line)
	}
}

// frictionView is what GET /v1/friction answers: the week as data and as plain words.
type frictionView struct {
	friction.Summary
	Text string `json:"text"`
}

// handleGetFriction answers the week in the language the page asks for (?lang=es or en: the page
// language is the browser's, which the gateway's own setting does not follow), or in the
// interface language the gateway has.
func (s *Server) handleGetFriction(w http.ResponseWriter, r *http.Request) {
	sum := friction.Summarize(s.friction.Entries(), time.Now(), friction.WeekDays)
	lang := i18n.Lang(viewOfUI(s.uiSetting()).Resolved)
	switch asked := i18n.Lang(r.URL.Query().Get("lang")); asked {
	case i18n.ES, i18n.EN:
		lang = asked
	}
	writeJSON(w, http.StatusOK, frictionView{Summary: sum, Text: sum.Text(lang)})
}

func (s *Server) handleClearFriction(w http.ResponseWriter, _ *http.Request) {
	if err := s.friction.Clear(); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleFlagAnswer is the "this was not right" button of an answer: it writes the turn's real
// context - what was asked, what was answered, how it ran and what had already gone wrong in it -
// with the person's own words, if they wrote any.
func (s *Server) handleFlagAnswer(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Turn int    `json:"turn"`
		Note string `json:"note"`
	}
	if !s.decodeBody(w, r, &body) {
		return
	}
	c := convOf(r)
	for _, cp := range c.checkpointViews() {
		if cp.Turn != body.Turn {
			continue
		}
		e := friction.Entry{
			Kind: friction.KindFlagged, Session: c.id, Project: c.projectID, Turn: cp.Turn,
			Ask: cp.Task, Note: body.Note, Mode: cp.Kind, Steps: len(cp.Steps),
			Related: s.friction.Related(c.id, cp.Turn),
		}
		if cp.Outcome != nil {
			e.Answer = cp.Outcome.Result
			e.Text = cp.Outcome.Error
		}
		s.recordFriction(e)
		w.WriteHeader(http.StatusNoContent)
		return
	}
	writeError(w, http.StatusNotFound, "that turn is not in this conversation")
}
