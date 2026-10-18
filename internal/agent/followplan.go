package agent

import "strings"

// PlanLead opens the turn that carries out the plan the agent gave in the message before, when
// the person answered it with a go-ahead ("do it", "ok"). The plan travels in the task itself, so
// the run works from the whole of it rather than from the trimmed line the conversation keeps.
const PlanLead = "Carry out the plan you proposed, exactly as written:"

// planEnd separates the plan from the person's own words in a plan-following task.
const planEnd = "\n\n(The person said: "

// FollowPlanTask is the task that carries out plan after goAhead, the person's words.
func FollowPlanTask(goAhead, plan string) string {
	return PlanLead + "\n\n" + strings.TrimSpace(plan) + planEnd + strings.TrimSpace(goAhead) + ")"
}

// FollowedPlan returns the plan a plan-following task carries, and whether it is one.
func FollowedPlan(description string) (string, bool) {
	rest, ok := strings.CutPrefix(strings.TrimSpace(description), PlanLead)
	if !ok {
		return "", false
	}
	plan, _, _ := strings.Cut(rest, planEnd)
	return strings.TrimSpace(plan), true
}

// approvedPlan is the plan phase's answer for a plan-following turn: the plan already exists in
// the task and the person approved it, so asking the model to plan it again would spend a call to
// arrive at a different wording of the same thing.
func approvedPlan() Plan {
	return Plan{
		Steps:          []Step{{Number: 1, Action: "Follow the plan in the TASK, step by step, in its order"}},
		ExpectedResult: "Every step of the plan is done and shown to work",
	}
}

// quickPlan says the analysis planned the work itself and the plan phase is not needed: the
// setting is on, the analysis wrote steps, and it did not ask for subtasks, which only the plan
// phase can declare.
func (r *taskRun) quickPlan() bool {
	return r.a.cfg.Agent.QuickPlan && len(nonBlank(r.analysis.Plan)) > 0 && !r.analysis.NeedsSubtasks
}

// planFromAnalysis is the plan the analysis wrote, in the plan phase's shape.
func planFromAnalysis(an Analysis) Plan {
	var p Plan
	for i, step := range nonBlank(an.Plan) {
		p.Steps = append(p.Steps, Step{Number: i + 1, Action: step})
	}
	if len(an.SuccessCrit) > 0 {
		p.ExpectedResult = strings.Join(an.SuccessCrit, "; ")
	}
	return p
}
