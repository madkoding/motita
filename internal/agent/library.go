package agent

import (
	"errors"
	"fmt"
	"strings"

	"github.com/madkoding/motita/internal/skills"
)

// --- Execution --------------------------------------------------------------

// runActions runs every action in the sandbox and returns their combined output.
// runLibraryAction answers one library operation, and reports whether it was one.
//
// An unknown kind returns handled=false so it falls through to the command path, where it is
// reported as a command with no command — the honest outcome for a kind nothing recognises.
func (a *Agent) runLibraryAction(kind string, action Command) (bool, string) {
	switch kind {
	case "read_skill", "search_skills", "list_skills", "save_skill":
	default:
		return false, ""
	}
	if a.library == nil {
		return true, "Error: no procedure library is configured."
	}
	// The kind is validated above, so this switch is exhaustive over what can arrive: a final
	// "not handled" return would be unreachable. Adding an operation means adding it to both
	// switches, and the compiler will not let the second one fall through.
	switch kind {
	case "list_skills":
		return true, a.listSkills()
	case "search_skills":
		return true, a.searchSkills(action.Command)
	case "read_skill":
		return true, a.readSkill(action.Command)
	default: // save_skill
		return true, a.saveSkill(action.Command)
	}
}

// listSkills reports the index, with each skill's accumulated value when there is one.
func (a *Agent) listSkills() string {
	all, err := a.library.List()
	if err != nil {
		return fmt.Sprintf("Error: could not read the library: %v", err)
	}
	if len(all) == 0 {
		return "The library is empty. Work from your own knowledge, and save what you learn."
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%d skill(s). Use read_skill with a name to read one in full.\n", len(all))
	for _, s := range all {
		fmt.Fprintf(&b, "\n- %s: %s%s\n  %s%s%s", s.Name, s.Title, s.Tag(), s.Summary,
			a.historySuffix(s.Name), a.feedbackSuffix(s.Name))
	}
	return b.String()
}

// searchSkills finds procedures by what the work is about.
func (a *Agent) searchSkills(query string) string {
	query = strings.TrimSpace(query)
	if query == "" {
		return "Error: search_skills needs what the work is about, in plain words."
	}
	hits, err := a.library.Search(query, 10)
	if err != nil {
		return fmt.Sprintf("Error: could not search the library: %v", err)
	}
	if len(hits) == 0 {
		return fmt.Sprintf("No skill matches %q. Work from your own knowledge, "+
			"and save a skill afterwards if what you work out is worth keeping.", query)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%d skill(s) match %q.\n", len(hits), query)
	// Same obligation as the planner's search, and for the same measured reason: a summary is
	// one line, and a model that treats the list as the answer acts on a title. See the comment
	// in internal/plan/plan.go for the numbers that made this wording.
	b.WriteString("A summary above is NOT the procedure: read the one that fits with read_skill BEFORE you act.\n")
	for _, s := range hits {
		fmt.Fprintf(&b, "\n- %s: %s%s\n  %s%s", s.Name, s.Title, s.Tag(), s.Summary, a.historySuffix(s.Name))
	}
	for _, s := range hits {
		b.WriteString(a.feedbackSuffix(s.Name))
	}
	return b.String()
}

// readSkill returns one procedure in full, and records that it was relied on.
func (a *Agent) readSkill(name string) string {
	s, err := a.library.Get(strings.TrimSpace(name))
	if err != nil {
		if errors.Is(err, skills.ErrNotFound) {
			return fmt.Sprintf("No skill named %q. Use list_skills to see what the library holds.", name)
		}
		return fmt.Sprintf("Error: %v", err)
	}
	// The read is the moment the credit becomes knowable, and the only one.
	a.consult(s.Name)
	if a.usage != nil {
		a.usage.BumpView(s.Name)
		a.usage.BumpUse(s.Name)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "# skill: %s\n(source: %s)\n", s.Name, s.Path)
	if s.Untrusted {
		b.WriteString(skills.UntrustedNote)
	}
	b.WriteString("\n")
	b.WriteString(s.Body)
	b.WriteString(a.feedbackSuffix(s.Name))
	return b.String()
}

// saveSkill writes a procedure. The argument is "name :: body", because this mode's actions
// carry a single string rather than structured arguments.
func (a *Agent) saveSkill(arg string) string {
	name, body, ok := strings.Cut(arg, "::")
	if !ok {
		return "Error: save_skill expects \"name :: the document in markdown\"."
	}
	s, err := a.library.Save(strings.TrimSpace(name), strings.TrimSpace(body))
	if err != nil {
		return fmt.Sprintf("Error: %v", err)
	}
	// A save that replaces a skill with an outstanding complaint is the fix being attempted.
	// Marking it here is what stops the same complaint being handed out on every later turn.
	marked := ""
	if a.reward != nil && a.reward.Addressed(s.Name) {
		if err := a.reward.Save(); err == nil {
			marked = " The complaint that was recorded against this skill is now marked as addressed."
		}
	}
	return fmt.Sprintf("Saved %q (%d bytes).%s", s.Name, len(body), marked)
}

// historySuffix renders a skill's accumulated value, or nothing when it has no history.
//
// Numbers and counts, with no adjective: "RELIABLE" would be this program's interpretation
// presented as evidence. An unconsulted skill shows nothing rather than "0.0", which would
// read as "this failed".
func (a *Agent) historySuffix(name string) string {
	if a.reward == nil {
		return ""
	}
	s, ok := a.reward.Get(name)
	if !ok || s.Uses() == 0 {
		return ""
	}
	return fmt.Sprintf("  [used %d, value %+.2f]", s.Uses(), s.Value)
}

// feedbackSuffix states the user's outstanding complaints for a skill, quoted verbatim.
//
// The number says a skill failed; the user's own words say what was wrong with it, which is
// the only form of the complaint a repair can be written from. Only complaints with no fix
// attempted are shown: once it has been rewritten the complaint is answered.
func (a *Agent) feedbackSuffix(name string) string {
	if a.reward == nil {
		return ""
	}
	s, ok := a.reward.Get(name)
	if !ok {
		return ""
	}
	notes := s.Unaddressed()
	if len(notes) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n  !! the user reported this skill failing, and the procedure has NOT been revised since:")
	for i, n := range notes {
		fmt.Fprintf(&b, "\n     %d. %s", i+1, n.Text)
	}
	b.WriteString("\n     Read the procedure again, work out which step the report is about, and save the")
	b.WriteString("\n     corrected version with save_skill. Fixing it is worth more than avoiding it.")
	return b.String()
}
