package plan

import (
	"regexp"
	"strings"

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

// pinIfPlan keeps an answer that is a plan as the session's active plan, so a long conversation
// that compacts it away still has it word for word. Any other answer leaves the pin as it was:
// a question about step 2 does not end the plan.
func pinIfPlan(sess *session.Session, answer string) {
	if looksLikePlan(answer) {
		sess.Pin(answer)
	}
}
