package gateway

import (
	"net/http"
	"strconv"
	"strings"
)

// questionAnswers is the user's answer to the questions a turn asked, kept with the turn so the
// card draws what was chosen, and not fresh options, when the conversation is opened again. Like
// the outcome it is set once and never changed.
type questionAnswers struct {
	// Answers is what was answered for each question, index-aligned with them: one of its options
	// or the user's own words, or "" for a question left to the agent's assumption.
	Answers []string `json:"answers"`
	// Request is the turn sent with the answers. It is how the client recognises that turn and
	// draws it under a short label instead of the prompt it carries.
	Request string `json:"request"`
}

// errQuestionsAnswered is the refusal of a second answer: the first one already started a turn.
const errQuestionsAnswered = "the questions of that turn were already answered"

// handleQuestionAnswers keeps how the user answered the questions of one turn. The client sends
// it before the turn that carries the answers, the same way it does for a report's suggestions.
func (s *Server) handleQuestionAnswers(w http.ResponseWriter, r *http.Request) {
	c := convOf(r)
	turn, err := strconv.Atoi(r.PathValue("turn"))
	if err != nil || turn < 0 {
		writeError(w, http.StatusBadRequest, "the checkpoint number is not valid")
		return
	}
	var body questionAnswers
	if !s.decodeBody(w, r, &body) {
		return
	}
	status, msg := c.recordQuestionAnswers(turn, body)
	if status != http.StatusOK {
		writeError(w, status, msg)
		return
	}
	s.saveSession(c)
	writeJSON(w, http.StatusOK, map[string]any{"answers": body})
}

// recordQuestionAnswers sets the answers of a turn's questions once, and says why not when it
// cannot.
func (c *conversation) recordQuestionAnswers(turn int, a questionAnswers) (int, string) {
	c.cpMu.Lock()
	defer c.cpMu.Unlock()
	cp := c.checkpointLocked(turn)
	if cp == nil || cp.Outcome == nil || len(cp.Outcome.Questions) == 0 {
		return http.StatusNotFound, "that turn asked no questions"
	}
	if cp.QuestionAnswers != nil {
		return http.StatusConflict, errQuestionsAnswered
	}
	if len(a.Answers) != len(cp.Outcome.Questions) {
		return http.StatusBadRequest, "there must be one answer per question"
	}
	if strings.TrimSpace(a.Request) == "" {
		return http.StatusBadRequest, "the answers must come with the turn that sends them"
	}
	cp.QuestionAnswers = &a
	return http.StatusOK, ""
}
