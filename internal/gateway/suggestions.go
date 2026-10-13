package gateway

import (
	"net/http"
	"slices"
	"strconv"
	"strings"
)

// The answers a suggestion takes, as the client sends them (web/src/proposals.ts).
var (
	suggestionPicks    = []string{"this", "rest", "none", ""}
	suggestionVerdicts = []string{"apply", "skip"}
)

// errAnswered is the refusal of a second answer: the first one may already have started a turn.
const errAnswered = "the suggestions of that turn were already answered"

// handleSuggestionAnswers keeps how the user answered the suggestions of one turn's report: the
// button pressed on each and the turn sent to apply them, if any. The client sends it before
// that turn, so a conversation opened again draws the same marks and labels that turn by name.
func (s *Server) handleSuggestionAnswers(w http.ResponseWriter, r *http.Request) {
	c := convOf(r)
	turn, err := strconv.Atoi(r.PathValue("turn"))
	if err != nil || turn < 0 {
		writeError(w, http.StatusBadRequest, "the checkpoint number is not valid")
		return
	}
	var body suggestionAnswers
	if !s.decodeBody(w, r, &body) {
		return
	}
	status, msg := c.recordAnswers(turn, body)
	if status != http.StatusOK {
		writeError(w, status, msg)
		return
	}
	s.saveSession(c)
	writeJSON(w, http.StatusOK, map[string]any{"answers": body})
}

// recordAnswers sets the answers of a turn's suggestions once, and says why not when it cannot.
func (c *conversation) recordAnswers(turn int, a suggestionAnswers) (int, string) {
	c.cpMu.Lock()
	defer c.cpMu.Unlock()
	cp := c.checkpointLocked(turn)
	if cp == nil || cp.Outcome == nil || cp.Outcome.Report == nil ||
		len(cp.Outcome.Report.Suggestions) == 0 {
		return http.StatusNotFound, "that turn made no suggestions"
	}
	if cp.Answers != nil {
		return http.StatusConflict, errAnswered
	}
	if msg := validAnswers(a, len(cp.Outcome.Report.Suggestions)); msg != "" {
		return http.StatusBadRequest, msg
	}
	cp.Answers = &a
	return http.StatusOK, ""
}

// validAnswers checks an answer against the suggestions it answers: one pick and one verdict
// each, from the known values, the first one pressed, and a request only when something was
// accepted.
func validAnswers(a suggestionAnswers, n int) string {
	if len(a.Picks) != n || len(a.Verdicts) != n {
		return "there must be one answer per suggestion"
	}
	for i := range n {
		if !slices.Contains(suggestionPicks, a.Picks[i]) || !slices.Contains(suggestionVerdicts, a.Verdicts[i]) {
			return "an answer is not one the suggestions offer"
		}
	}
	if a.Picks[0] == "" {
		return "the first suggestion must have been answered with a button"
	}
	if slices.Contains(a.Verdicts, "apply") != (strings.TrimSpace(a.Request) != "") {
		return "the request must be sent exactly when a suggestion was accepted"
	}
	return ""
}
