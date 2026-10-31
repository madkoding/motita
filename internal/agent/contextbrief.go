package agent

import (
	"context"
	"strings"

	"github.com/madkoding/motita/internal/brief"
	"github.com/madkoding/motita/internal/task"
)

// recentTurns and recentWidth bound the conversation the working rounds carry: the analysis reads
// the last DialogueTurns in full, and the rounds only need what a short request refers to.
const (
	recentTurns = 4
	recentWidth = 300
)

// contextBrief is what a task is given beyond its request, as one block under one budget: the
// plan the conversation pinned, earlier turns of this conversation retrieved from the archive, and
// what the person's memory says about the task. Empty when there is none.
func (a *Agent) contextBrief(ctx context.Context, t task.Task, depth int) string {
	return brief.Compose(brief.Budget,
		brief.Part{Title: "The plan this conversation follows (pinned)", Body: a.pinnedPlan(t)},
		brief.Part{Title: "Earlier in this conversation", Body: a.archived(ctx, t, depth)},
		brief.Part{Title: "The person's memory", Body: a.remembered(t)},
	)
}

// pinnedShare is the most of the brief the pinned plan may take: the rest is for the archive and
// the memory, which a long plan used to crowd out.
const pinnedShare = brief.Budget / 2

// pinnedPlan is the conversation's pinned plan, unless the request already carries it (a task that
// carries out the plan has it word for word), cut to its share of the brief.
func (a *Agent) pinnedPlan(t task.Task) string {
	if a.pinned == nil {
		return ""
	}
	if _, following := FollowedPlan(t.Description); following {
		return ""
	}
	plan := strings.TrimSpace(a.pinned())
	if plan == "" || strings.Contains(t.Description, plan) {
		return ""
	}
	return brief.Cut(plan, pinnedShare)
}

// archived is what earlier turns of this conversation said about the task, retrieved from the
// archive rather than carried in the prompt.
func (a *Agent) archived(ctx context.Context, t task.Task, depth int) string {
	if a.recall == nil {
		return ""
	}
	// The prompt already carries the last DialogueTurns turns, and a recall must not repeat
	// them. The count is the real one, so a short conversation does not hide archived turns
	// the prompt never showed. On a top-level task the turn in progress is one of those (it
	// was recorded when the task started); a subtask has none.
	live := min(len(a.Transcript()), DialogueTurns)
	if depth == 0 && live > 0 {
		live--
	}
	block, n := a.recall(ctx, t.Description, live)
	if n > 0 {
		a.report("[recalled %d passages from earlier]", n)
	}
	return block
}

// remembered is what the person's memory already says about the task, looked up for the agent: a
// model that is not told what is in it does not ask.
func (a *Agent) remembered(t task.Task) string {
	if a.memory == nil {
		return ""
	}
	block, n := a.memory.Brief(t.Description, brief.Budget)
	if n > 0 {
		a.report("[recalled %d passages from memory]", n)
	}
	return block
}

// recentDialogue is the tail of the conversation the working rounds carry. The turn in progress is
// left out: it is the task itself, shown above, and its pending line would read as a request that
// was cut off.
func (a *Agent) recentDialogue(t task.Task) string {
	turns := a.Transcript()
	if n := len(turns); n > 0 && turns[n-1].Pending && turns[n-1].User == t.Description {
		turns = turns[:n-1]
	}
	return dialogueOf(turns, recentTurns, recentWidth)
}
