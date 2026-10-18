package tui

import (
	"context"
	"strings"
	"testing"

	"github.com/madkoding/motita/internal/agent"
)

// In auto mode, a go-ahead typed after a plan runs that plan as a task, shown as what the
// person typed; a go-ahead after a task is an ordinary message.
func TestAGoAheadAfterAPlanRunsItInTheTUI(t *testing.T) {
	runner := &fakeRunner{planAnswer: "1. read the file\n2. fix it"}
	tui := newFakeTUI("/auto\nhow do I fix it?\n\ndale\n\nq\n", runner)
	tui.Run(context.Background())
	if !runner.planCalled || !runner.taskCalled {
		t.Fatalf("plan=%v task=%v", runner.planCalled, runner.taskCalled)
	}
	if !strings.HasPrefix(runner.lastTask, agent.PlanLead) || !strings.Contains(runner.lastTask, "2. fix it") {
		t.Errorf("the task must carry the plan: %q", runner.lastTask)
	}
	if tui.lastPlan != "" {
		t.Error("a plan is carried out once")
	}

	runner = &fakeRunner{}
	tui = newFakeTUI("/auto\nfix it\n\nok\n\nq\n", runner)
	tui.Run(context.Background())
	if runner.lastTask != "ok" {
		t.Errorf("after a task a go-ahead is the message itself, got %q", runner.lastTask)
	}
}
