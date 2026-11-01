package agent

import (
	"context"
	"fmt"
	"strings"

	"github.com/madkoding/motita/internal/llm"
	"github.com/madkoding/motita/internal/task"
)

// A GOAL ("/goal <what they want>") is the person handing over the whole job: decide alone, do all
// of it, and finish. A plain task already has most of the machinery; a goal differs in the places
// where a plain task STOPS for the person or settles for less:
//
//   - it is always work: the analysis cannot turn it into a chat reply or a question;
//   - a spent step budget continues on its own, up to the same ceiling a person answering "yes"
//     would reach, instead of waiting for an answer nobody is there to give;
//   - its claim of "done" is audited even with one criterion, and sent back more times;
//   - a goal split into subtasks is checked WHOLE afterwards, so what the subtasks did not cover
//     (or a subtask that failed) is finished by the same run instead of ending it;
//   - in direct mode the model is asked to check the request part by part before it may stop.
//
// The guards against a run that cannot converge stay as they are: the stall detector, the
// unusable-reply limit, max_retries and the ceiling on the step budget.

// goalPrefix is what the user types to hand over a goal: "/goal <what they want>".
const goalPrefix = "/goal"

// goalDirective is appended to a goal's description. A goal is the user saying "decide for me and
// finish it", so the agent picks the option it would recommend, goes on without asking, and does
// not stop at a part of the work.
const goalDirective = "\n\nThis is a GOAL: the user delegated every decision and wants ALL of it done. " +
	"Do NOT ask questions and do NOT offer alternatives. Where a choice is needed, take the option you " +
	"would recommend and continue. Do every part of the request, not a first step or a sample: no " +
	"TODOs, no placeholders, nothing left \"for later\". Before you report it done, go through the " +
	"request part by part and show, with a command or a file, that each part is done and works."

// goalAuditChallenges is how many times a goal's claim of "done" is sent back over unmet
// criteria (a plain task: maxAuditChallenges).
const goalAuditChallenges = 5

// goalDirectGateRetries is how many times a red gate is handed back to a goal in direct mode (a
// plain task: directGateRetries).
const goalDirectGateRetries = 5

// goalDirectChecks bounds how many times a goal in direct mode is asked to check the request part
// by part before it may stop. The first answer is always questioned; a later one only when the run
// went back to work after the previous check, so a run that is done stops at the next answer.
const goalDirectChecks = 3

// goalCheckPrompt is what a goal in direct mode is told when it first stops calling tools.
const goalCheckPrompt = "Before you finish: this is a GOAL, and all of it has to be done. Re-read the " +
	"request and go through it part by part. For each part, say whether it is done and how you checked " +
	"it. If ANY part is missing, unverified or failing, keep working on it now with the tools. Only when " +
	"every part is done and checked, answer again with the final summary."

// goalState is the goal the outermost task is working on.
type goalState struct {
	// original is the description as the person typed it ("/goal ..."); the conversation is keyed
	// by it.
	original string
	// run is the description the run works on: the goal and its directive.
	run string
	// interactive is what Agent.Interactive was before the goal turned it off.
	interactive bool
	// approveAll says the person launched it with "/goal --all": every command the policy would
	// ask about runs without asking, for this goal only. What the policy DENIES stays denied.
	approveAll bool
}

// goalAllFlag is the option, right after "/goal", that approves every command during the goal:
// a goal launched before leaving should not sit for hours on one approval.
const goalAllFlag = "--all"

// goalOf reports whether a task is a goal and returns it without the prefix.
func goalOf(description string) (string, bool) {
	goal, _, ok := parseGoal(description)
	return goal, ok
}

// parseGoal is goalOf plus whether the goal was launched with --all.
func parseGoal(description string) (goal string, all, ok bool) {
	d := strings.TrimSpace(description)
	if d == goalPrefix || !strings.HasPrefix(d, goalPrefix+" ") {
		return description, false, false
	}
	goal = strings.TrimSpace(d[len(goalPrefix):])
	if rest, found := strings.CutPrefix(goal, goalAllFlag); found && (rest == "" || rest[0] == ' ') {
		goal, all = strings.TrimSpace(rest), true
	}
	if goal == "" {
		return description, false, false
	}
	return goal, all, true
}

// goalApprovesAll reports a goal launched with --all.
func (a *Agent) goalApprovesAll() bool { return a.goal != nil && a.goal.approveAll }

// prepareGoal is the task as the outermost run works on it: a goal becomes autonomous.
func (a *Agent) prepareGoal(t task.Task) task.Task {
	a.goal = nil
	run := t
	if goal, all, ok := parseGoal(t.Description); ok {
		run.Description = goal + goalDirective
		a.goal = &goalState{original: t.Description, run: run.Description, interactive: a.Interactive,
			approveAll: all}
		if all {
			a.report("goal: every command the policy would ask about runs without asking until it ends")
		}
		// A goal is autonomous: nobody is asked, the stated assumption is the answer.
		a.Interactive = false
	}
	return run
}

// endGoal closes the goal of the task that ended: the next task is asked about as it would have
// been, instead of inheriting the goal's silence.
func (a *Agent) endGoal() {
	if a.goal != nil {
		a.Interactive = a.goal.interactive
		a.goal = nil
	}
}

// isGoal reports whether the outermost task is a goal.
func (a *Agent) isGoal() bool { return a.goal != nil }

// turnUser is the conversation's key for a task description: the run of a goal works on the goal
// and its directive, while the conversation holds what the person typed. Without it the progress
// of a goal was never written to its pending turn, and a goal resumed after a restart, or one that
// stopped, came back with none of the work it had done.
func (a *Agent) turnUser(description string) string {
	if a.goal != nil && description == a.goal.run {
		return a.goal.original
	}
	return description
}

// keepGoing decides whether a run that spent a full step budget spends another one. A goal does
// not ask: the person already said to finish it, and the ceiling in nextRound still ends a run
// that never converges.
func (a *Agent) keepGoing(ctx context.Context, rounds, maxSteps int, prefix string) (bool, error) {
	if !a.isGoal() {
		return a.continueBudget(ctx, rounds, maxSteps, prefix)
	}
	a.report("goal: %d rounds used and work left; continuing with another %d (up to %d)",
		rounds, maxSteps, maxSteps*maxBudgetExtensions)
	a.log.Info(prefix+"goal: continuing with another budget without asking", "rounds", rounds,
		"max_steps", maxSteps)
	return true, nil
}

// auditLimit is how many times one run is sent back over its criteria.
func (a *Agent) auditLimit() int {
	if a.isGoal() {
		return goalAuditChallenges
	}
	return maxAuditChallenges
}

// auditMin is how many success criteria a run needs for the audit to run: a goal is audited
// whatever it states, because a goal is exactly the request that must not end half done.
func (a *Agent) auditMin() int {
	if a.isGoal() {
		return 1
	}
	return auditMinCriteria
}

// directGateLimit is how many times a red gate is handed back in direct mode.
func (a *Agent) directGateLimit() int {
	if a.isGoal() {
		return goalDirectGateRetries
	}
	return directGateRetries
}

// workByRequest reports a turn whose request is work by itself, whatever the analysis read: the
// turn that applies accepted suggestions, and a goal.
func (r *taskRun) workByRequest() bool {
	return IsApplyingTurn(r.t.Description) || (r.depth == 0 && r.a.isGoal())
}

// Lines a goal leaves in its progress trail about its success criteria. The trail is what a goal
// resumed after a restart, or the turn after one that stopped, reads back: with the criteria and
// which of them were still not shown, it knows what is LEFT, not only which commands ran.
const (
	goalCriteriaLine = "goal criteria (what done means):"
	goalPendingLine  = "criteria not shown yet:"
	goalAllShownLine = "every goal criterion is shown"
)

// noteGoalCriteria writes the goal's criteria to its trail once, when the run starts.
func (r *taskRun) noteGoalCriteria() {
	if r.depth > 0 || !r.a.isGoal() || len(r.analysis.SuccessCrit) == 0 {
		return
	}
	for _, line := range r.trail {
		if strings.Contains(line, goalCriteriaLine) {
			return
		}
	}
	r.addTrail(goalCriteriaLine + "\n- " + strings.Join(r.analysis.SuccessCrit, "\n- "))
}

// noteCriteriaState writes to a goal's trail what the last criteria audit found.
func (r *taskRun) noteCriteriaState(unmet []auditCriterion) {
	if r.depth > 0 || !r.a.isGoal() {
		return
	}
	if len(unmet) == 0 {
		r.addTrail(goalAllShownLine)
		return
	}
	names := make([]string, 0, len(unmet))
	for _, c := range unmet {
		names = append(names, strings.TrimSpace(c.Criterion))
	}
	r.addTrail(goalPendingLine + "\n- " + strings.Join(names, "\n- "))
}

// addTrail adds a line to the run's trail and writes it to the pending turn.
func (r *taskRun) addTrail(line string) {
	r.trail = append(r.trail, line)
	r.a.trail(r.t, r.trail)
}

// goalCheck reports whether a goal in direct mode is sent back to check its work before it stops,
// and adds the request to the conversation when it is.
func (r *directRun) goalCheck() bool {
	if !r.a.isGoal() || r.goalChecks >= goalDirectChecks || (r.goalChecks > 0 && !r.workedSinceCheck) {
		return false
	}
	r.goalChecks++
	r.workedSinceCheck = false
	r.a.report("goal: checking every part of the request before finishing (%d/%d)", r.goalChecks, goalDirectChecks)
	r.msgs = append(r.msgs, llm.Message{Role: "user", Content: goalCheckPrompt})
	return true
}

// closeGoal checks a goal WHOLE after its subtasks ran. The plan's subtasks are the model's reading
// of the goal, and "every subtask passed" is not "the goal is met": a part no subtask covered, or a
// subtask that failed and stopped the others, used to end the goal there. The same run now goes on
// with what the subtasks did in front of it, through the full execution cycle (criteria audit and
// the project's gate included). A plain task, a subtask and a cancelled run are returned as they are.
func (r *taskRun) closeGoal(sub TaskResult, subs []string) TaskResult {
	if r.depth > 0 || !r.a.isGoal() || r.ctx.Err() != nil {
		return sub
	}
	r.a.report("goal: the subtasks ran; checking the whole goal and finishing what is left")
	r.a.log.Info(r.prefix+"goal: closing round after the subtasks", "subtasks", len(subs), "pass", sub.Pass)
	r.seed = &roundRecord{
		round: 0, kind: roundProgress, commands: "(the goal's subtasks)", detail: goalSubtasksNote(subs, sub),
	}
	res := r.execute()
	res.Subtasks = sub.Subtasks
	if strings.TrimSpace(res.Summary) == "" {
		res.Summary = sub.Summary
	}
	return res
}

// goalSubtasksNote is what the closing run of a goal is told about its subtasks.
func goalSubtasksNote(subs []string, sub TaskResult) string {
	var b strings.Builder
	b.WriteString("This GOAL was split into these subtasks, which have already run:\n")
	for i, s := range subs {
		fmt.Fprintf(&b, "%d. %s\n", i+1, s)
	}
	if s := strings.TrimSpace(sub.Summary); s != "" {
		b.WriteString("What they did:\n" + s + "\n")
	}
	if sub.Pass {
		b.WriteString("All of them passed. ")
	} else {
		b.WriteString("They did NOT all pass: " + strings.TrimSpace(sub.Reason) + "\n")
	}
	b.WriteString("Their changes are in the working directory: check them with `git status` and " +
		"`git diff` FIRST. Then go through the goal part by part, finish whatever is missing or failing " +
		"(including the subtasks that did not run), verify it, and report \"done\": true only when the " +
		"WHOLE goal is met.")
	return b.String()
}
