package agent

import "strings"

// ReportVersion is the version of the Report shape. A front end that draws the report checks it
// before trusting the fields, and it goes up only when a field changes meaning or goes away.
const ReportVersion = 1

// Report is the final account of a finished task, as DATA rather than as prose.
//
// The model used to hand back one Markdown string, which the web view and the terminal each had
// to display as it came: neither could lay out the files, the checks or the next steps its own
// way, because they were not separate things any more. Every field here is one such thing, so a
// front end decides how to draw it. Summary stays for a client that only wants the sentence, and
// it is what the conversation records.
type Report struct {
	Version int `json:"version"`
	// Status is "done", "partial" or "failed": how much of the request was met.
	Status string `json:"status"`
	// Summary is the answer in the user's language, as Markdown.
	Summary string `json:"summary"`
	// Changes is what was created, edited or removed.
	Changes []ReportChange `json:"changes"`
	// Verification is the proof: each check that was run and what it showed.
	Verification []ReportCheck `json:"verification"`
	// Risks is what the reader should know before trusting the result.
	Risks []string `json:"risks"`
	// NextSteps is what remains, or what the agent would do next.
	NextSteps []string `json:"next_steps"`
}

// ReportChange is one thing that changed.
type ReportChange struct {
	Path string `json:"path"`
	// Kind is "added", "modified", "deleted" or "other".
	Kind        string `json:"kind"`
	Description string `json:"description"`
}

// ReportCheck is one thing that was checked.
type ReportCheck struct {
	Check string `json:"check"`
	// Result is "pass", "fail" or "skipped".
	Result   string `json:"result"`
	Evidence string `json:"evidence"`
}

// normalize makes a decoded report safe to draw: the version is set, an unknown enum falls back to
// its neutral value, and the lists are never null, so a front end can iterate without a guard.
// Entries with nothing in them are dropped; a report is worth what it says.
func (r *Report) normalize(pass bool) {
	r.Version = ReportVersion
	r.Summary = strings.TrimSpace(r.Summary)
	r.Status = strings.ToLower(strings.TrimSpace(r.Status))
	switch r.Status {
	case "done", "partial", "failed":
	default:
		// The validator's verdict is the only authority the agent has: an unknown status is read
		// from it rather than guessed.
		r.Status = "failed"
		if pass {
			r.Status = "done"
		}
	}
	changes := make([]ReportChange, 0, len(r.Changes))
	for _, c := range r.Changes {
		c.Path, c.Description = strings.TrimSpace(c.Path), strings.TrimSpace(c.Description)
		if c.Path == "" && c.Description == "" {
			continue
		}
		switch strings.ToLower(strings.TrimSpace(c.Kind)) {
		case "added", "modified", "deleted":
			c.Kind = strings.ToLower(strings.TrimSpace(c.Kind))
		default:
			c.Kind = "other"
		}
		changes = append(changes, c)
	}
	r.Changes = changes
	checks := make([]ReportCheck, 0, len(r.Verification))
	for _, v := range r.Verification {
		v.Check, v.Evidence = strings.TrimSpace(v.Check), strings.TrimSpace(v.Evidence)
		if v.Check == "" {
			continue
		}
		switch strings.ToLower(strings.TrimSpace(v.Result)) {
		case "pass", "fail", "skipped":
			v.Result = strings.ToLower(strings.TrimSpace(v.Result))
		default:
			v.Result = "skipped"
		}
		checks = append(checks, v)
	}
	r.Verification = checks
	r.Risks = cleanLines(r.Risks)
	r.NextSteps = cleanLines(r.NextSteps)
}

// cleanLines trims the entries and drops the empty ones, returning a non-nil slice.
func cleanLines(in []string) []string {
	out := make([]string, 0, len(in))
	for _, s := range in {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out
}

// Text is the report as plain text, for a front end that draws no cards (the terminal, or a client
// that predates the report). The summary comes first and unchanged, then one short block per
// non-empty section; a report that is only a summary is that summary, so nothing is added to an
// answer that had nothing more to say.
func (r *Report) Text() string {
	var b strings.Builder
	b.WriteString(r.Summary)
	block := func(title string, lines []string) {
		if len(lines) == 0 {
			return
		}
		b.WriteString("\n\n" + title + ":")
		for _, l := range lines {
			b.WriteString("\n  " + l)
		}
	}
	var changes []string
	for _, c := range r.Changes {
		line := "[" + c.Kind + "] " + c.Path
		if c.Description != "" {
			line += " - " + c.Description
		}
		changes = append(changes, strings.TrimSpace(strings.Replace(line, "]  ", "] ", 1)))
	}
	block("Changed", changes)
	marks := map[string]string{"pass": "ok  ", "fail": "FAIL", "skipped": "skip"}
	var checks []string
	for _, v := range r.Verification {
		line := marks[v.Result] + " " + v.Check
		if v.Evidence != "" {
			line += " - " + v.Evidence
		}
		checks = append(checks, line)
	}
	block("Checked", checks)
	block("Worth knowing", r.Risks)
	block("Next", r.NextSteps)
	return b.String()
}
