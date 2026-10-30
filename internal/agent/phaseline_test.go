package agent

import "testing"

func TestPhaseLinesAreTheLabelsOfThePipelineOnly(t *testing.T) {
	phases := []string{
		"analysing the task...",
		"planning...",
		"plan ready: 1 step",
		"plan ready: 4 steps",
		"deciding action (round 2/8)...",
		"deciding the first step...",
		"deciding the next step...",
		"deciding the next step after: go test ./...",
		"starting with: read the failing test",
		"round 3 done; the model reports more to do",
		"validating with anchor...",
		"validation passed; running final action...",
		"synthesizing answer...",
		"task complete: the tests pass",
	}
	for _, line := range phases {
		if !IsPhaseLine(line) {
			t.Errorf("%q should be a phase line", line)
		}
	}
	work := []string{
		"running: go test ./...",
		"thinking: the plan is sound",
		"understood: fix the flaky test",
		"mode: task (auto)",
		"subtask 1/2 done",
		"assuming: the default branch is main",
		"you: also check the docs",
		"approved (always allowed in this project): git status",
		"",
	}
	for _, line := range work {
		if IsPhaseLine(line) {
			t.Errorf("%q is a step, not a phase line", line)
		}
	}
}
