package plan

import (
	"regexp"
	"strings"

	"github.com/madkoding/motita/internal/intent"
	"github.com/madkoding/motita/internal/session"
)

// planStep is a line that reads as a step of a plan: "1." or "1)", or a checkbox.
var planStep = regexp.MustCompile(`^\s*(?:\d+[.)]|[-*] \[[ xX]\])\s+\S`)

// minPlanSteps is how many steps an answer needs to count as a plan rather than a short list.
const minPlanSteps = 3

// looksLikePlan says whether an answer is a plan: enough numbered or checkbox steps.
func looksLikePlan(answer string) bool {
	steps := 0
	for _, line := range strings.Split(answer, "\n") {
		if planStep.MatchString(line) {
			steps++
		}
	}
	return steps >= minPlanSteps
}

// pinIfPlan keeps the plan an answer gives as the session's PROPOSED plan, when the person asked
// for a plan and the answer is one, so a long discussion of it that compacts it away still has it
// word for word. Any other answer leaves the pin as it was: a numbered list in reply to a question
// ("the differences are: 1… 2… 3…") is not a plan, and a question about step 2 does not end one.
// The plan becomes active only when the person approves it (see tui.AppRunner.pinFollowedPlan).
func pinIfPlan(sess *session.Session, request, answer string) {
	if intent.AsksForPlan(request) && looksLikePlan(answer) {
		sess.Propose(answer)
	}
}
