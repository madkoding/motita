package config

import "errors"

// DefaultPlanLoops is how many rounds of tool calls a chat turn may make when agent.plan_loops is
// not set: enough to read a codebase the way a coding agent does, with a ceiling that still ends a
// model that loops.
const DefaultPlanLoops = 40

// PlanToolRounds is how many rounds of tool calls a chat turn may make.
func (a Agent) PlanToolRounds() int {
	if a.PlanLoops > 0 {
		return a.PlanLoops
	}
	return DefaultPlanLoops
}

// validateLoops refuses a negative agent.plan_loops.
func (a Agent) validateLoops() error {
	if a.PlanLoops < 0 {
		return errors.New("agent.plan_loops cannot be negative")
	}
	return nil
}
