package agent

import "fmt"

// singleActionWarnAt is how many progress rounds in a row may carry one action before the model is
// told to batch. A round costs a whole model call, and a single look or a single command per round
// is the commonest way a task takes twice the rounds it needs.
const singleActionWarnAt = 2

// auditMinCriteria is how many success criteria a request needs for the criteria audit to be
// worth a model call and a round: with one criterion the claim of "done" and the project's gate
// already say whether it is met.
const auditMinCriteria = 2

// batchingNote tells the next round to put independent steps together, once the run has proposed
// one action per round singleActionWarnAt times in a row. A round with several actions, or none,
// starts the count again.
func (r *taskRun) batchingNote(action Action) string {
	if len(action.Actions) != 1 {
		r.singleActionRounds = 0
		return ""
	}
	r.singleActionRounds++
	if r.singleActionRounds < singleActionWarnAt {
		return ""
	}
	return fmt.Sprintf("\n!! %d rounds in a row have run ONE action each. Actions run in order inside a "+
		"round, so put everything you already know you need in this one: every read together, and the "+
		"write followed by the check that proves it, instead of waiting a round for each.",
		r.singleActionRounds)
}
