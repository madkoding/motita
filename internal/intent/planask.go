package intent

import "strings"

// planRequestNouns name a plan of work to carry out. They are narrower than planNouns on purpose:
// a summary, a comparison or an explanation is an answer, not a plan the person will follow.
var planRequestNouns = set(
	"plan", "plans", "planning", "roadmap", "steps", "checklist", "proposal",
	"planes", "pasos", "propuesta", // spanish-fixture: input
)

// planRequestStems open a word that asks to plan: "planifica", "planea" (spanish-fixture: input).
var planRequestStems = []string{"planif", "planea", "planee", "planear"}

// AsksForPlan reports whether a message asks for a plan of work, so the plan the answer gives is
// one the person means to follow. A question whose answer happens to be a numbered list does not.
func AsksForPlan(text string) bool {
	for _, w := range strings.FieldsFunc(fold(text), notWordRune) {
		if planRequestNouns[w] {
			return true
		}
		for _, stem := range planRequestStems {
			if strings.HasPrefix(w, stem) {
				return true
			}
		}
	}
	return false
}
