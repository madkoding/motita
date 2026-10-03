package tui

import (
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
