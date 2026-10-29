package agent

import (
	"path"
	"strings"
)

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
	// Evidence is the visual proof: screenshots of how something looked before and after the
	// change. Each image is the name of a file the agent saved in its artifacts folder.
	Evidence []ReportEvidence `json:"evidence"`
	// Risks is what the reader should know before trusting the result.
	Risks []string `json:"risks"`
	// NextSteps is what remains, or what the agent would do next.
	NextSteps []string `json:"next_steps"`
	// Suggestions are what the agent noticed while working and did NOT do: an issue next to the
	// change, a missing test, an alternative worth weighing. They are proposals, never applied
	// on their own: the interface asks the user, one by one, whether to apply each.
	Suggestions []ReportSuggestion `json:"suggestions"`
	// Lessons are facts about the project the task learned that the next task should not have to
	// rediscover: a command that works, a convention, a trap, a decision the person made. They
	// are kept in the project (see MemoryFile) and handed to later tasks.
	Lessons []string `json:"lessons"`
	// AutoApply says the configuration applies every suggestion without asking
	// (agent.auto_apply_suggestions) as of this report: the interface starts the applying turn on
	// its own. It is stamped by the synthesis, never written by the model.
	AutoApply bool `json:"auto_apply,omitempty"`
}

// ReportSuggestion is one proposal the user decides on.
type ReportSuggestion struct {
	// Title is the proposal in one line, as the user would name it.
	Title string `json:"title"`
	// Detail is why it is worth doing and what applying it would change.
	Detail string `json:"detail"`
	// Needed marks a proposal whose answer is obvious from the context: the request is not done
	// or correct without it, it stays in scope and it is safe. Auto mode applies these without
	// asking; every other proposal is still the user's call.
	Needed bool `json:"needed,omitempty"`
	// WhyNeeded is the reason a proposal is needed, shown with it so the decision can be checked.
	// A proposal marked needed without one is demoted to optional: a decision taken for the user
	// has to say why.
	WhyNeeded string `json:"why_needed,omitempty"`
}

// maxSuggestions bounds the proposals of one report. Past a few the user is reading a backlog,
// not deciding, and the ones that matter get lost among the ones that do not.
const maxSuggestions = 3

// genericSuggestions are the proposals that fit any task: with no file, symbol or behaviour in
// the title they say nothing the user could decide on, so they are dropped.
var genericSuggestions = []string{
	"add more tests", "add tests", "add unit tests", "improve test coverage", "write more tests",
	"update the documentation", "update documentation", "improve documentation", "add documentation",
	"review the documentation", "add comments", "improve error handling", "add error handling",
	"refactor the code", "refactor", "clean up the code", "improve performance", "add logging",
	"agrega más tests", "agregar más tests", "agregar tests", "agrega tests", "más pruebas",
	"añadir pruebas", "agregar pruebas", "actualizar la documentación", "mejorar la documentación",
	"revisar la documentación", "agregar comentarios", "mejorar el manejo de errores",
	"refactorizar el código", "refactorizar", "limpiar el código", "mejorar el rendimiento",
}

// isGenericSuggestion reports whether the title is one of the boilerplate proposals and names
// nothing concrete (a path, a symbol in backticks, a number).
func isGenericSuggestion(title string) bool {
	t := strings.Trim(strings.ToLower(title), " .!:;")
	if strings.ContainsAny(t, "`/\\_.0123456789") {
		return false
	}
	for _, g := range genericSuggestions {
		if t == g {
			return true
		}
	}
	return false
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
	// Result is "pass", "fail", "skipped" (not run on purpose) or "blocked" (could not run).
	Result   string `json:"result"`
	Evidence string `json:"evidence"`
}

// ReportEvidence is one before/after comparison. Before and After are artifact file names (no
// directory); either may be empty when only one side exists, e.g. a new screen has no "before".
type ReportEvidence struct {
	Title   string `json:"title"`
	Before  string `json:"before"`
	After   string `json:"after"`
	Caption string `json:"caption"`
}

// artifactImageName reduces a model-written reference to a bare file name of an image type, or
// "" when it is not one. The name is only ever looked up in the artifacts folder, so a path in it
// is cut down to its last element rather than trusted.
func artifactImageName(s string) string {
	s = strings.TrimSpace(strings.ReplaceAll(s, "\\", "/"))
	if s == "" {
		return ""
	}
	s = path.Base(s)
	switch strings.ToLower(path.Ext(s)) {
	case ".png", ".jpg", ".jpeg", ".webp", ".gif", ".svg":
		if strings.HasPrefix(s, ".") {
			return ""
		}
		return s
	default:
		// Not an image.
	}
	return ""
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
	r.Changes = normalizeChanges(r.Changes)
	r.Verification = normalizeChecks(r.Verification)
	r.Evidence = normalizeEvidence(r.Evidence)
	r.Risks = cleanLines(r.Risks)
	r.NextSteps = cleanLines(r.NextSteps)
	r.Suggestions = normalizeSuggestions(r.Suggestions)
}

// normalizeSuggestions keeps the proposals that name something concrete, up to maxSuggestions.
func normalizeSuggestions(in []ReportSuggestion) []ReportSuggestion {
	out := make([]ReportSuggestion, 0, len(in))
	for _, s := range in {
		s.Title, s.Detail = strings.TrimSpace(s.Title), strings.TrimSpace(s.Detail)
		if s.Title == "" || isGenericSuggestion(s.Title) {
			continue
		}
		s.WhyNeeded = strings.TrimSpace(s.WhyNeeded)
		s.Needed = s.Needed && s.WhyNeeded != ""
		if !s.Needed {
			s.WhyNeeded = ""
		}
		out = append(out, s)
		if len(out) == maxSuggestions {
			break
		}
	}
	return out
}

// normalizeChanges drops the empty changes and folds an unknown kind into "other".
func normalizeChanges(in []ReportChange) []ReportChange {
	changes := make([]ReportChange, 0, len(in))
	for _, c := range in {
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
	return changes
}

// normalizeChecks drops the checks with no name and folds an unknown result into "skipped".
func normalizeChecks(in []ReportCheck) []ReportCheck {
	checks := make([]ReportCheck, 0, len(in))
	for _, v := range in {
		v.Check, v.Evidence = strings.TrimSpace(v.Check), strings.TrimSpace(v.Evidence)
		if v.Check == "" {
			continue
		}
		switch strings.ToLower(strings.TrimSpace(v.Result)) {
		case "pass", "fail", "skipped", "blocked":
			v.Result = strings.ToLower(strings.TrimSpace(v.Result))
		default:
			v.Result = "skipped"
		}
		checks = append(checks, v)
	}
	return checks
}

// normalizeEvidence keeps the comparisons that name at least one image.
func normalizeEvidence(in []ReportEvidence) []ReportEvidence {
	evidence := make([]ReportEvidence, 0, len(in))
	for _, e := range in {
		e.Before, e.After = artifactImageName(e.Before), artifactImageName(e.After)
		e.Title, e.Caption = strings.TrimSpace(e.Title), strings.TrimSpace(e.Caption)
		if e.Before == "" && e.After == "" {
			continue
		}
		evidence = append(evidence, e)
	}
	return evidence
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
	block("Changed", r.changeLines())
	block("Checked", r.checkLines())
	block("Screenshots", r.screenshotLines())
	block("Worth knowing", r.Risks)
	block("Next", r.NextSteps)
	block("Suggestions (not applied)", r.suggestionLines())
	return b.String()
}

// changeLines is one line per changed file for Text: its kind, its path and what changed.
func (r *Report) changeLines() []string {
	var changes []string
	for _, c := range r.Changes {
		line := "[" + c.Kind + "] " + c.Path
		if c.Description != "" {
			line += " - " + c.Description
		}
		changes = append(changes, strings.TrimSpace(strings.Replace(line, "]  ", "] ", 1)))
	}
	return changes
}

// checkLines is one line per verification for Text, led by a fixed-width mark of its result.
func (r *Report) checkLines() []string {
	marks := map[string]string{"pass": "ok  ", "fail": "FAIL", "skipped": "skip", "blocked": "BLCK"}
	var checks []string
	for _, v := range r.Verification {
		line := marks[v.Result] + " " + v.Check
		if v.Evidence != "" {
			line += " - " + v.Evidence
		}
		checks = append(checks, line)
	}
	return checks
}

// screenshotLines is one line per piece of evidence for Text, with the before and after it has.
func (r *Report) screenshotLines() []string {
	var shots []string
	for _, e := range r.Evidence {
		line := e.Title
		if e.Before != "" {
			line += " before=" + e.Before
		}
		if e.After != "" {
			line += " after=" + e.After
		}
		shots = append(shots, strings.TrimSpace(line))
	}
	return shots
}

// suggestionLines is one line per suggestion for Text, saying why when it is needed.
func (r *Report) suggestionLines() []string {
	var proposals []string
	for _, p := range r.Suggestions {
		line := p.Title
		if p.Detail != "" {
			line += " - " + p.Detail
		}
		if p.Needed {
			line += " (needed: " + p.WhyNeeded + ")"
		}
		proposals = append(proposals, line)
	}
	return proposals
}
