package evals

import "time"

// CaseResult is how one task went.
type CaseResult struct {
	ID   string `json:"id"`
	Pass bool   `json:"pass"`
	// Steps is how many rounds the agent needed to get there (or gave up after).
	Steps  int   `json:"steps"`
	Tokens int64 `json:"tokens"`
	// CostUSD is the approximate price of the tokens; CostKnown is false for a model with no price.
	CostUSD    float64 `json:"cost_usd"`
	CostKnown  bool    `json:"cost_known"`
	DurationMS int64   `json:"duration_ms"`
	Reason     string  `json:"reason,omitempty"`
	// Error is set when the task could not run at all (the model did not answer), which says
	// nothing about how good the model is.
	Error string `json:"error,omitempty"`
}

// Summary is a whole run in four numbers.
type Summary struct {
	Passed     int     `json:"passed"`
	Total      int     `json:"total"`
	Steps      int     `json:"steps"`
	Tokens     int64   `json:"tokens"`
	CostUSD    float64 `json:"cost_usd"`
	CostKnown  bool    `json:"cost_known"`
	DurationMS int64   `json:"duration_ms"`
}

// Run is one pass over the fixed tasks with one model.
type Run struct {
	ID         string       `json:"id"`
	At         time.Time    `json:"at"`
	Provider   string       `json:"provider"`
	Model      string       `json:"model"`
	PromptHash string       `json:"prompt_hash"`
	CasesHash  string       `json:"cases_hash"`
	Results    []CaseResult `json:"results"`
	Summary    Summary      `json:"summary"`
}

// Verdict statuses.
const (
	StatusFirst  = "first"
	StatusBetter = "better"
	StatusSame   = "same"
	StatusWorse  = "worse"
)

// tolerance is how much more steps or money than before still count as the same: a run is not
// deterministic, and a change of a few percent is noise.
const tolerance = 0.25

// Verdict says how a run compares with the one before it.
type Verdict struct {
	Status string `json:"status"`
	// Against is the run compared with; empty for the first.
	Against string `json:"against,omitempty"`
	// DeltaPassed, DeltaSteps and DeltaCostUSD are this run minus the earlier one.
	DeltaPassed  int     `json:"delta_passed"`
	DeltaSteps   int     `json:"delta_steps"`
	DeltaCostUSD float64 `json:"delta_cost_usd"`
	// Regressed are the tasks that passed before and fail now; Fixed the other way round.
	Regressed []string `json:"regressed"`
	Fixed     []string `json:"fixed"`
	// StepsUp and CostUp say the run needed clearly more rounds, or cost clearly more, than before.
	StepsUp bool `json:"steps_up"`
	CostUp  bool `json:"cost_up"`
}

// Compare weighs cur against prev. A nil prev, or one made on another set of tasks, leaves nothing
// to compare with.
func Compare(prev *Run, cur Run) Verdict {
	v := Verdict{Status: StatusFirst, Regressed: []string{}, Fixed: []string{}}
	if prev == nil || prev.CasesHash != cur.CasesHash {
		return v
	}
	v.Against = prev.ID
	v.DeltaPassed = cur.Summary.Passed - prev.Summary.Passed
	v.DeltaSteps = cur.Summary.Steps - prev.Summary.Steps
	before := map[string]bool{}
	for _, r := range prev.Results {
		before[r.ID] = r.Pass
	}
	for _, r := range cur.Results {
		switch was, known := before[r.ID]; {
		case known && was && !r.Pass:
			v.Regressed = append(v.Regressed, r.ID)
		case known && !was && r.Pass:
			v.Fixed = append(v.Fixed, r.ID)
		}
	}
	v.StepsUp = exceeds(float64(cur.Summary.Steps), float64(prev.Summary.Steps))
	if cur.Summary.CostKnown && prev.Summary.CostKnown {
		v.DeltaCostUSD = cur.Summary.CostUSD - prev.Summary.CostUSD
		v.CostUp = exceeds(cur.Summary.CostUSD, prev.Summary.CostUSD)
	}
	v.Status = statusOf(v)
	return v
}

// exceeds says now is clearly more than before.
func exceeds(now, before float64) bool {
	return before > 0 && now > before*(1+tolerance)
}

// statusOf turns the deltas into one word: fewer tasks passing is worse whatever else changed, and
// with the same tasks passing, needing clearly more rounds or money is worse too.
func statusOf(v Verdict) string {
	switch {
	case v.DeltaPassed < 0 || len(v.Regressed) > 0 && v.DeltaPassed <= 0:
		return StatusWorse
	case v.DeltaPassed > 0:
		return StatusBetter
	case v.StepsUp || v.CostUp:
		return StatusWorse
	case v.DeltaSteps < 0 || v.DeltaCostUSD < 0:
		return StatusBetter
	default:
		return StatusSame
	}
}

// Stale says why the last run no longer answers "is it still fine?": "" when it does, else "never"
// (no run yet), "cases" (the tasks changed), "model" or "prompt".
func Stale(last *Run, provider, model, promptHash string) string {
	switch {
	case last == nil:
		return "never"
	case last.CasesHash != CasesHash():
		return "cases"
	case last.Provider != provider || last.Model != model:
		return "model"
	case last.PromptHash != promptHash:
		return "prompt"
	default:
		return ""
	}
}
