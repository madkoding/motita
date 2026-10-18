package agent

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/madkoding/motita/internal/config"
	"github.com/madkoding/motita/internal/gitx"
	"github.com/madkoding/motita/internal/task"
)

// subagentMerge brings a subtask's branch into the workspace; a variable so a test can make the
// merge fail without inventing a conflict.
var subagentMerge = gitx.MergeInto

// workspaceIsRepoRoot says whether the run's workspace is the root of a repository with a commit:
// the one place a worktree can be taken from.
func (a *Agent) workspaceIsRepoRoot(ctx context.Context) bool {
	dir := a.cfg.Agent.WorkspaceDir
	root, err := gitx.Root(ctx, dir)
	return err == nil && gitx.SamePath(root, dir) && gitx.HasCommits(ctx, dir)
}

// parallelSubtasks says whether a plan's subtasks run side by side: the plan declared them
// independent (different files, none builds on another), there is more than one, and this run may
// start background agents in worktrees of their own. In a directory that is not a repository root
// two pieces side by side would trample each other, so they run one after another there.
func (a *Agent) parallelSubtasks(ctx context.Context, plan Plan, subs []string) bool {
	declared := plan.Independent && len(subs) > 1
	mayStartAgents := !a.isChild && a.subs != nil && a.cfg.Agent.MaxParallel > 1
	return declared && mayStartAgents && a.workspaceIsRepoRoot(ctx)
}

// runSubtasksParallel carries out independent subtasks side by side: each one is a background agent
// in a worktree of its own, at most agent.max_parallel at a time, and their branches are merged
// into the workspace in the order the plan listed them once every one has ended. A subtask that
// did not pass, or whose branch does not merge, fails the parent, but the others still land: they
// were declared independent, so their work stands on its own.
func (a *Agent) runSubtasksParallel(ctx context.Context, t task.Task, analysis Analysis, subs []string,
	depth int, start time.Time, prefix string) TaskResult {
	res := TaskResult{Task: t.Description, Attempts: 1, Subtasks: len(subs)}
	a.log.Info(prefix+"running independent subtasks side by side", "count", len(subs),
		"at_once", a.cfg.Agent.MaxParallel)
	a.report("%d independent subtasks, side by side", len(subs))

	// The worktrees are taken one at a time: a snapshot goes through the repository's index, and
	// two at once would fight over its lock. The work in them is what runs side by side.
	results := make([]TaskResult, len(subs))
	places := make([]childPlace, len(subs))
	slots := make(chan struct{}, a.cfg.Agent.MaxParallel)
	var wg sync.WaitGroup
	for i := range subs {
		place, err := a.childPlace(ctx, fmt.Sprintf("s%d", i+1), true)
		if err != nil {
			results[i] = TaskResult{Task: subs[i], Attempts: 1,
				Reason: "it could not be given a worktree of its own: " + err.Error()}
			continue
		}
		places[i] = place
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			slots <- struct{}{}
			defer func() { <-slots }()
			results[i] = a.runSubtaskAside(ctx, t, analysis, subs, i, depth, place)
		}(i)
	}
	wg.Wait()

	outcomes := make([]string, len(subs))
	base, _, _ := gitx.Head(ctx, a.cfg.Agent.WorkspaceDir)
	for i, r := range results {
		if r.Pass {
			message := fmt.Sprintf("motita: subtask %d of %d: %s", i+1, len(subs), truncate(collapse(subs[i]), 60))
			if _, err := subagentMerge(ctx, a.cfg.Agent.WorkspaceDir, base, places[i].branch, message); err != nil {
				r.Pass, r.Reason = false, "its work could not be merged: "+err.Error()
			}
		}
		res.Validation = r.Validation
		outcomes[i] = taskOutcome(r)
		if !r.Pass {
			outcomes[i] = "did not pass: " + strings.TrimSpace(r.Reason)
			if res.Reason == "" {
				res.Reason = fmt.Sprintf("subtask %d of %d did not pass (%s): %s", i+1, len(subs),
					truncate(subs[i], 120), r.Reason)
			}
		}
	}
	res.Pass = res.Reason == ""
	if res.Pass {
		res.Reason = fmt.Sprintf("%d independent subtasks completed", len(subs))
	} else {
		a.report("%s", res.Reason)
	}
	res.Summary = subtaskSummary(subs, outcomes)
	res.DurationMS = time.Since(start).Milliseconds()
	return res
}

// runSubtaskAside runs one independent subtask as a background agent in the worktree it was given
// and returns its result; its work stays on the worktree's branch.
func (a *Agent) runSubtaskAside(ctx context.Context, t task.Task, analysis Analysis, subs []string,
	i, depth int, place childPlace) TaskResult {
	id := fmt.Sprintf("s%d", i+1)
	a.report("subtask %d/%d: %s", i+1, len(subs), truncate(subs[i], 160))
	child := a.newChild(place, config.TaskCode, "")
	a.armChild(child, id, id, config.TaskCode, truncate(collapse(subs[i]), 120), place.branch)
	// The parent's context is the one that cancels a subtask: there is nothing of its own to stop.
	noCancel := func() { /* cancelled with the parent's context */ }
	c := &subagent{id: id, purpose: subs[i], role: config.TaskCode, branch: place.branch,
		cancel: noCancel, done: make(chan struct{})}
	desc := subtaskBrief(t.Description, analysis.Summary, subs, i, nil) +
		fmt.Sprintf("\nThe subtasks are independent and run AT THE SAME TIME, each in a git worktree of "+
			"its own; yours is on branch %s, started from the current tree. Touch only the files this "+
			"subtask needs. When it is written and checked, commit it on this branch "+
			"(git add -A && git commit -m \"...\"); the branches are merged once every subtask has ended.",
			place.branch)
	r := child.processTask(ctx, task.Task{Description: desc, Origin: t.Origin + " (subtask)", Context: t.Context},
		depth+1)
	a.finishChild(c, child, place, r, ctx.Err() != nil)
	return r
}
