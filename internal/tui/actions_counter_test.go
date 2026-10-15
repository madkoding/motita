package tui

import (
	"fmt"
	"strings"
	"testing"
)

// With ShowActions off, three tool calls collapse into one counter line.
func TestPlanStreamCountsActionsWhenDetailIsOff(t *testing.T) {
	tui := newFakeTUI("q\n", &fakeRunner{})
	tui.ShowActions = false
	tui.messages = []Message{{Author: AuthorAgent, Pending: true}}
	s := &planStream{tui: tui, pendingIdx: 0}
	for i := 0; i < 3; i++ {
		s.handle("[using tool: execute_command]")
	}
	counters, frozen := 0, 0
	for _, m := range tui.messages {
		if m.Frozen {
			frozen++
			if strings.Contains(m.Text, "3 actions") {
				counters++
			}
		}
	}
	if frozen != 1 || counters != 1 {
		t.Fatalf("want one counter line '3 actions', got frozen=%d counters=%d: %+v", frozen, counters, tui.messages)
	}
}

// With ShowActions on, every tool call keeps its own line.
func TestPlanStreamShowsEveryActionWhenDetailIsOn(t *testing.T) {
	tui := newFakeTUI("q\n", &fakeRunner{})
	tui.ShowActions = true
	tui.messages = []Message{{Author: AuthorAgent, Pending: true}}
	s := &planStream{tui: tui, pendingIdx: 0}
	for i := 0; i < 3; i++ {
		s.handle("[using tool: execute_command]")
	}
	frozen := 0
	for _, m := range tui.messages {
		if m.Frozen {
			frozen++
		}
	}
	if frozen != 3 {
		t.Fatalf("want 3 detailed lines, got %d", frozen)
	}
}

func TestActionLabel(t *testing.T) {
	if actionLabel(1) != "1 action" || actionLabel(3) != "3 actions" {
		t.Fatal("bad label")
	}
}

// TestPlanStreamTakesAPlansStepsWithoutAnsweringWithThem: a plan reports its commands and reasoning
// as a task does, for the web's drawers. Here a command is a tool line, and its output and the
// reasoning stay out of the answer.
func TestPlanStreamTakesAPlansStepsWithoutAnsweringWithThem(t *testing.T) {
	tui := newFakeTUI("q\n", &fakeRunner{})
	tui.ShowActions = true
	stream := &planStream{tui: tui, pendingIdx: 0}
	tui.messages = []Message{{Author: AuthorAgent, Pending: true}}

	stream.handle("thinking: weighing it")
	stream.handle("running: ls -la")
	stream.handle("output (exit 0):\nfile.txt")
	stream.handle("answer")
	stream.closePending()

	var texts []string
	for _, m := range tui.messages {
		texts = append(texts, m.Text)
	}
	if got := strings.Join(texts, "|"); got != "using execute_command ls -la|answer" {
		t.Errorf("conversation = %q", got)
	}
}

// TestPlanStepsAreReportedAsTaskLines: the runner turns a plan's steps into the lines the gateway
// already turns into the drawers' events.
func TestPlanStepsAreReportedAsTaskLines(t *testing.T) {
	var lines []string
	steps := planSteps(func(f string, a ...any) { lines = append(lines, fmt.Sprintf(f, a...)) })
	steps.Command("ls")
	steps.Output(2, "boom")
	steps.Thought("hm")
	steps.Live("hm so far")
	want := []string{"running: ls", "output (exit 2):\nboom", "thinking: hm", "live: hm so far"}
	if strings.Join(lines, "\n") != strings.Join(want, "\n") {
		t.Errorf("lines = %q, want %q", lines, want)
	}
}
