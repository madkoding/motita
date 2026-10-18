package gateway

import (
	"github.com/madkoding/motita/internal/agent"
	"github.com/madkoding/motita/internal/intent"
)

// followPlan turns a go-ahead into the task that carries out the plan the conversation ended
// with. A go-ahead after anything else (a finished task, a question, nothing) is left as it is:
// the agent reads it against the conversation, as it always did.
func followPlan(turns []agent.DialogueTurn, d intent.Decision) (intent.Decision, bool) {
	if d.Reason != intent.ReasonGoAhead || len(turns) == 0 {
		return d, false
	}
	last := turns[len(turns)-1]
	if last.Kind != "plan" || last.Pending || last.Agent == "" {
		return d, false
	}
	d.Prompt = agent.FollowPlanTask(d.Prompt, last.Agent)
	d.Reason = "carrying out the plan"
	return d, true
}
