package agent

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/madkoding/motita/internal/config"
	"github.com/madkoding/motita/internal/gitx"
)

// independentScript is a plan of two independent subtasks, each of which writes its own file; a
// subtask's own plan has no subtasks, and anything else is a no-op step.
func independentScript() *scriptServer {
	return &scriptServer{
		plan: func(prompt string) string {
			if strings.Contains(prompt, "This is subtask") {
				return `{"plan":[{"step":1,"action":"do it","command":""}],"subtasks":[]}`
			}
			return `{"plan":[{"step":1,"action":"split","command":""}],` +
				`"subtasks":["write a.txt saying a","write b.txt saying b"],"independent":true}`
		},
		execute: func(_ int, prompt string) string {
			switch {
			case strings.Contains(prompt, "[THIS ONE] write a.txt"):
				return step(true, "echo a > a.txt")
			case strings.Contains(prompt, "[THIS ONE] write b.txt"):
				return step(true, "echo b > b.txt")
			default:
				return step(true, "true")
			}
		},
	}
}

func parallelCfg(t *testing.T) func(*config.Config) {
	return func(c *config.Config) {
		inRepo(t)(c)
		c.Agent.MaxParallel = 2
	}
}

func fileSays(t *testing.T, dir, name, want string) bool {
	t.Helper()
	body, err := os.ReadFile(filepath.Join(dir, name))
	return err == nil && strings.TrimSpace(string(body)) == want
}

// TestIndependentSubtasksRunSideBySideAndMerge: two subtasks the plan declared independent run as
// background agents in worktrees of their own, and both branches are merged into the checkout, in
// order; the parent's answer is what each one did.
func TestIndependentSubtasksRunSideBySideAndMerge(t *testing.T) {
	s := independentScript()
	e, result, sink := fleetRun(t, s, parallelCfg(t), nil)
	if !result.Pass || result.Subtasks != 2 {
		t.Fatalf("the run must pass with both subtasks: %+v", result)
	}
	if !fileSays(t, e.dir, "a.txt", "a") || !fileSays(t, e.dir, "b.txt", "b") {
		t.Fatal("both subtasks' work must be merged into the checkout")
	}
	if !strings.Contains(result.Reason, "2 independent subtasks completed") ||
		!strings.Contains(result.Summary, "**write a.txt saying a**") ||
		!strings.Contains(result.Summary, "**write b.txt saying b**") {
		t.Errorf("reason=%q summary=%q", result.Reason, result.Summary)
	}
	out, _ := exec.Command("git", "-C", e.dir, "log", "--merges", "--format=%s").CombinedOutput()
	merges := string(out)
	if !strings.Contains(merges, "motita: subtask 1 of 2") || !strings.Contains(merges, "motita: subtask 2 of 2") {
		t.Errorf("each subtask lands as its own merge:\n%s", out)
	}
	told := 0
	s.mu.Lock()
	for _, p := range s.executes {
		if strings.Contains(p, "run AT THE SAME TIME") && strings.Contains(p, "on branch motita/sub/") {
			told++
		}
	}
	s.mu.Unlock()
	if told != 2 {
		t.Errorf("each subtask is told it runs beside the others, on its own branch: %d prompts", told)
	}
	sink.mu.Lock()
	all := strings.Join(sink.lines, "\n")
	sink.mu.Unlock()
	if !strings.Contains(all, "2 independent subtasks, side by side") {
		t.Error("the run says the subtasks run side by side")
	}
}

// A subtask whose branch does not merge fails the parent and says why; the others still land.
func TestASubtaskWhoseBranchDoesNotMergeFailsTheParent(t *testing.T) {
	old := subagentMerge
	subagentMerge = func(ctx context.Context, dir, base, branch, msg string) (gitx.MergeResult, error) {
		if strings.HasSuffix(branch, "-s2") {
			return gitx.MergeResult{}, errors.New("the merge conflicted")
		}
		return old(ctx, dir, base, branch, msg)
	}
	t.Cleanup(func() { subagentMerge = old })
	e, result, _ := fleetRun(t, independentScript(), parallelCfg(t), nil)
	if result.Pass || !strings.Contains(result.Reason, "subtask 2 of 2 did not pass") ||
		!strings.Contains(result.Reason, "could not be merged: the merge conflicted") {
		t.Fatalf("result = %+v", result)
	}
	if !fileSays(t, e.dir, "a.txt", "a") || fileSays(t, e.dir, "b.txt", "b") {
		t.Error("the first subtask lands, the second does not")
	}
	if !strings.Contains(result.Summary, "did not pass: its work could not be merged") {
		t.Errorf("summary = %q", result.Summary)
	}
}

// A subtask that cannot be given a worktree is reported, not run in the shared checkout.
func TestASubtaskWithoutAWorktreeIsReportedNotRun(t *testing.T) {
	old := subagentSnapshot
	subagentSnapshot = func(context.Context, string, string) (string, string, error) {
		return "", "", errors.New("disk full")
	}
	t.Cleanup(func() { subagentSnapshot = old })
	e, result, _ := fleetRun(t, independentScript(), parallelCfg(t), nil)
	if result.Pass || !strings.Contains(result.Reason, "worktree of its own: disk full") {
		t.Fatalf("result = %+v", result)
	}
	if fileSays(t, e.dir, "a.txt", "a") {
		t.Error("nothing ran in the shared checkout")
	}
}

// Outside a repository root, or with one agent at a time, independent subtasks run one after
// another in the checkout, as every plan's subtasks did before.
func TestIndependentSubtasksRunOneAfterAnotherOutsideARepository(t *testing.T) {
	e, result, sink := fleetRun(t, independentScript(), func(c *config.Config) { c.Agent.MaxParallel = 2 }, nil)
	if !result.Pass || result.Reason != "2 subtasks completed" {
		t.Fatalf("result = %+v", result)
	}
	if !fileSays(t, e.dir, "a.txt", "a") || !fileSays(t, e.dir, "b.txt", "b") {
		t.Fatal("the subtasks wrote to the checkout, in turn")
	}
	sink.mu.Lock()
	defer sink.mu.Unlock()
	if strings.Contains(strings.Join(sink.lines, "\n"), "side by side") {
		t.Error("nothing ran side by side")
	}
	a := bareAgent(t, func(c *config.Config) { c.Agent.MaxParallel = 1 })
	a.subs = &subagents{}
	plan := Plan{Subtasks: []string{"x", "y"}, Independent: true}
	if a.parallelSubtasks(context.Background(), plan, plan.Subtasks) {
		t.Error("one agent at a time is one after another")
	}
}
