package agent

import (
	"context"

	"github.com/madkoding/motita/internal/task"
)

// subtaskTry is the subtask being run: the parent task and analysis, the list, which one, what the
// ones before it did, and the depth it runs at.
type subtaskTry struct {
	t        task.Task
	analysis Analysis
	subs     []string
	i        int
	outcomes []string
	depth    int
}

// retrySubtask tries a subtask that failed again, up to agent.subtask_retries times, each time
// told why the last attempt failed. The first failure used to end the parent and abandon every
// subtask after it, though the failure is usually one different approach away and the work the
// attempt did is still in the working directory.
func (a *Agent) retrySubtask(ctx context.Context, st subtaskTry, last TaskResult) TaskResult {
	t, analysis, subs, i, outcomes, depth := st.t, st.analysis, st.subs, st.i, st.outcomes, st.depth
	for n := 1; !last.Pass && n <= a.cfg.Agent.SubtaskRetries && ctx.Err() == nil; n++ {
		a.report("subtask %d/%d did not pass; trying again (%d/%d): %s", i+1, len(subs), n,
			a.cfg.Agent.SubtaskRetries, truncate(last.Reason, 160))
		last = a.processTask(ctx, task.Task{
			Description: retryBrief(subtaskBrief(t.Description, analysis.Summary, subs, i, outcomes), last.Reason),
			Origin:      t.Origin + " (subtask)",
			Context:     t.Context,
		}, depth+1)
	}
	return last
}

// retryBrief is a subtask's brief with what its last attempt learned appended.
func retryBrief(brief, reason string) string {
	return brief + "\n(A previous attempt at THIS subtask failed: " + truncate(collapse(reason), 1500) +
		"\nWhat it changed is still in the working directory: run `git status` and `git diff` first. " +
		"Do not repeat what failed: find the cause and take a different approach.)\n"
}
