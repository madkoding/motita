package plan

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/madkoding/motita/internal/review"
	"github.com/madkoding/motita/internal/reward"
	"github.com/madkoding/motita/internal/skills"
	"github.com/madkoding/motita/internal/usage"
)

// The skill tools. Their results are plain text the model reads, never JSON: a tool result
// is the next thing in a conversation, and a JSON envelope is noise the model has to strip
// before it can think about the content.
//
// The wording of an empty answer matters as much as the non-empty one. "No skill matches"
// tells the model to proceed on its own knowledge, while an error would make it retry a
// search that is not going to succeed.

// WithLibrary gives the planner a procedure library, enabling the skill tools.
func (p *Planner) WithLibrary(lib *skills.Library) *Planner {
	p.library = lib
	return p
}

// WithReward installs the long-term value ledger.
//
// It is optional: a planner without one behaves exactly as before, which is what an embedder
// that does not want the feature gets. The library keeps working either way.
func (p *Planner) WithReward(l *reward.Ledger) *Planner {
	p.reward = l
	return p
}

// WithUsage installs the per-skill telemetry sidecar. When set, the skill tools
// bump view/use/patch counters on every call, and save_skill records the
// provenance marker (agent vs foreground). Nil disables telemetry: the curator
// and the background review fork are off, and the skill tools work as before.
func (p *Planner) WithUsage(u *usage.Ledger) *Planner {
	p.usage = u
	return p
}

// saveUsage writes the accumulated skill counts; a planner without a ledger has none.
func (p *Planner) saveUsage() {
	if p.usage != nil {
		_ = p.usage.Save()
	}
}

// SetUsage is the setter for the usage ledger, used by the review fork after
// construction. It is separate from WithUsage so a caller that builds a
// planner with chained With* calls can inject the usage ledger later.
func (p *Planner) SetUsage(u *usage.Ledger) { p.usage = u }

// WithReview installs the background self-improvement fork. When set, Run
// calls review.MaybeRun after the final answer is delivered. Nil disables the
// post-turn review.
func (p *Planner) WithReview(r *review.Review) *Planner {
	p.review = r
	return p
}

// Consulted returns the skills this run read, and how many times each.
//
// It is what the caller needs to turn the user's verdict into value: the skills are known
// here and nowhere else, so this is the only place they can be reported from.
func (p *Planner) Consulted() map[string]int {
	if len(p.consulted) == 0 {
		return nil
	}
	out := make(map[string]int, len(p.consulted))
	for k, v := range p.consulted {
		out[k] = v
	}
	return out
}

// ItersSinceSkill is how many tool-call rounds this run has taken since the last save_skill.
//
// It is read by a caller that owns the post-turn review: the counter is per-run, while the
// budget the review fires on spans turns, so the owner of the conversation is the only place
// that can accumulate it. A run that called save_skill reports zero, because the planner resets
// it there — a turn that has just written a skill down does not also need reviewing.
func (p *Planner) ItersSinceSkill() int { return p.itersSinceSkill }

// consult records that a skill was read.
func (p *Planner) consult(name string) {
	if p.consulted == nil {
		p.consulted = map[string]int{}
	}
	p.consulted[name]++
}

// historySuffix renders a skill's accumulated value, or nothing when it has no history.
//
// It is a NUMBER and two counts, deliberately, with no adjective attached. "used 4, value 0.7"
// is a fact the model can weigh together with what it actually reads; "RELIABLE" or "UNPROVEN"
// would be this program's interpretation, presented as if it were evidence, and a model told a
// skill is good will reach for it even when the text says otherwise. The numbers also travel
// with their counts, because 0.7 from one verdict and from forty are different claims.
//
// An unconsulted skill shows nothing rather than "0.0", which would read as "this failed".
func (p *Planner) historySuffix(name string) string {
	if p.reward == nil {
		return ""
	}
	s, ok := p.reward.Get(name)
	if !ok || s.Uses() == 0 {
		return ""
	}
	return fmt.Sprintf("  [used %d, value %+.2f]", s.Uses(), s.Value)
}

// feedbackSuffix states the user's outstanding notes for a skill, quoted verbatim.
//
// This is the point of collecting the notes at all: a number says a skill failed, and the note
// says what was wrong with it, which is the only form of the complaint that can be acted on.
// It is quoted rather than summarised because it is the user's own words about their own work,
// and every paraphrase of it loses the specific detail that makes the fix findable.
//
// Only notes with no fix attempted are shown: once the skill has been rewritten the complaint
// is answered, and repeating it would send the model to re-fix what has already been fixed.
func (p *Planner) feedbackSuffix(name string) string {
	if p.reward == nil {
		return ""
	}
	s, ok := p.reward.Get(name)
	if !ok {
		return ""
	}
	notes := s.Unaddressed()
	if len(notes) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n  !! the user reported this skill failing, and the procedure has NOT been revised since:")
	// Every note here is a complaint: Unaddressed filters out the notes that came with a good
	// verdict, which are comments rather than reports of a fault. No verdict label is needed
	// for the same reason.
	for i, n := range notes {
		fmt.Fprintf(&b, "\n     %d. %s", i+1, n.Text)
	}
	b.WriteString("\n     Read the procedure again, work out which step the report is about, and save the")
	b.WriteString("\n     corrected version with save_skill. Fixing it is worth more than avoiding it.")
	return b.String()
}

// toolListSkills reports the index: what the library holds, without any bodies.
func (p *Planner) toolListSkills() string {
	if p.library == nil {
		return errNoProcedureLibrary
	}
	all, err := p.library.List()
	if err != nil {
		return fmt.Sprintf("Error: could not read the library: %v", err)
	}
	if len(all) == 0 {
		return "The library is empty. Nothing has been written down yet, " +
			"so work from your own knowledge and save what you learn."
	}

	var b strings.Builder
	fmt.Fprintf(&b, "%d skill(s) in the library. Use read_skill for the full procedure.\n", len(all))
	for _, s := range all {
		fmt.Fprintf(&b, "\n- %s: %s%s\n  %s%s", s.Name, s.Title, s.Tag(), s.Summary, p.historySuffix(s.Name))
	}
	return b.String()
}

// toolSearchSkills finds skills by what they are about.
func (p *Planner) toolSearchSkills(args json.RawMessage) string {
	if p.library == nil {
		return errNoProcedureLibrary
	}
	var in struct {
		Query string `json:"query"`
		Limit int    `json:"limit"`
	}
	if err := decodeArgs(args, &in); err != nil {
		return fmt.Sprintf("Error: %v", err)
	}
	if strings.TrimSpace(in.Query) == "" {
		return "Error: the query is empty. Describe the work in plain words."
	}

	hits, err := p.library.Search(in.Query, in.Limit)
	if err != nil {
		return fmt.Sprintf("Error: could not search the library: %v", err)
	}
	if len(hits) == 0 {
		return fmt.Sprintf("No skill matches %q. Work from your own knowledge, "+
			"and save a skill afterwards if what you work out is worth keeping.", in.Query)
	}

	var b strings.Builder
	fmt.Fprintf(&b, "%d skill(s) match %q.\n", len(hits), in.Query)
	// The obligation, and the reason it is worded this strongly.
	//
	// Measured on a real gateway: 14 searches against 2 reads. The line used to read "Use
	// read_skill with the name to read one in full", which describes an OPTION — and a model
	// under an anchor that cannot fail takes the option that costs nothing. It searched, read
	// nothing, changed nothing, and the run was reported complete.
	//
	// What follows is the same tool with the choice removed: the summaries are explicitly NOT
	// the procedure, and reading one is stated as the next step rather than as a possibility.
	b.WriteString("A summary above is NOT the procedure: it is a title and one line, and acting on it is how the " +
		"wrong thing gets done. Read the one that fits with read_skill BEFORE you act — the full text carries the " +
		"steps and the pitfalls that the summary cannot.\n")
	for _, s := range hits {
		fmt.Fprintf(&b, "\n- %s: %s%s\n  %s%s", s.Name, s.Title, s.Tag(), s.Summary, p.historySuffix(s.Name))
	}
	// The outstanding complaints are appended after the list, so they cannot be missed by a
	// model that only skims the summaries: a skill the user reported as broken is the reason
	// this search happened.
	for _, s := range hits {
		b.WriteString(p.feedbackSuffix(s.Name))
	}
	return b.String()
}

// toolReadSkill returns one procedure in full, which is the whole point of the library: the
// index is cheap, the procedure is what changes what the agent does.
func (p *Planner) toolReadSkill(args json.RawMessage) string {
	if p.library == nil {
		return errNoProcedureLibrary
	}
	var in struct {
		Name string `json:"name"`
	}
	if err := decodeArgs(args, &in); err != nil {
		return fmt.Sprintf("Error: %v", err)
	}

	s, err := p.library.Get(in.Name)
	if err != nil {
		if errors.Is(err, skills.ErrNotFound) {
			return fmt.Sprintf("No skill named %q. Use list_skills to see what the library holds.", in.Name)
		}
		return fmt.Sprintf("Error: %v", err)
	}
	// The procedure is being read, so it is being relied on: this is the moment the credit
	// becomes knowable, and the only one.
	p.consult(s.Name)
	if p.usage != nil {
		p.usage.BumpView(s.Name)
		p.usage.BumpUse(s.Name)
	}

	var b strings.Builder
	fmt.Fprintf(&b, "# skill: %s\n(source: %s)\n", s.Name, s.Path)
	if s.Untrusted {
		b.WriteString(skills.UntrustedNote)
	}
	b.WriteString("\n")
	b.WriteString(s.Body)
	return b.String()
}

// toolSaveSkill writes a procedure to the library.
//
// The result names the file it wrote, so the model can tell the user where it went and so a
// later read can be traced to it.
func (p *Planner) toolSaveSkill(args json.RawMessage) string {
	if p.library == nil {
		return errNoProcedureLibrary
	}
	var in struct {
		Name string `json:"name"`
		Body string `json:"body"`
	}
	if err := decodeArgs(args, &in); err != nil {
		return fmt.Sprintf("Error: %v", err)
	}

	s, err := p.library.Save(in.Name, in.Body)
	if err != nil {
		return fmt.Sprintf("Error: %v", err)
	}
	if p.usage != nil {
		by := usage.ByForeground
		if p.IsReviewFork {
			by = usage.ByAgent
		}
		p.usage.BumpPatch(s.Name, by)
	}
	// Reset the iteration counter: a save_skill just happened, so the
	// background review has no reason to fire for this turn.
	p.itersSinceSkill = 0
	return fmt.Sprintf("Saved the skill %q to %s (%d bytes). It is available from now on, including to later sessions.",
		s.Name, s.Path, len(s.Body))
}
