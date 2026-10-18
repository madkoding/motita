package tui

import (
	"strings"
	"testing"

	"github.com/madkoding/motita/internal/agent"
)

// pendingRunner hands over a fixed set of proposals once, as a runner does after a task.
type pendingRunner struct {
	recordingRunner
	items []agent.AskItem
}

func (p *pendingRunner) TakePendingQuestions() ([]agent.AskItem, string) {
	items := p.items
	p.items = nil
	return items, "fix the parser"
}

// mixedItems are two needed suggestions around an optional one.
func mixedItems() []agent.AskItem {
	return agent.ProposalItems(&agent.Report{Suggestions: []agent.ReportSuggestion{
		{Title: "test the empty header", Needed: true, WhyNeeded: "the new parser has no test"},
		{Title: "share the retry helper"},
		{Title: "fix the panic the change exposed", Needed: true, WhyNeeded: "the request is not correct with it"},
	}})
}

// In auto mode, when every suggestion is needed, they are applied at once and nothing is asked.
func TestAutoModeAppliesNeededSuggestionsWithoutAsking(t *testing.T) {
	rr := &pendingRunner{items: agent.ProposalItems(&agent.Report{Suggestions: []agent.ReportSuggestion{
		{Title: "test the empty header", Needed: true, WhyNeeded: "no test"},
	}})}
	tui := &TUI{Out: &strings.Builder{}, Width: 90, Height: 30, Runner: rr, autoTurn: true}
	tui.openAskIfPending()
	if tui.asking() {
		t.Fatal("a needed suggestion must not be asked about in auto mode")
	}
	want := "Apply these suggestions you made, and nothing else:\n- test the empty header"
	if len(rr.tasks) != 1 || rr.tasks[0] != want {
		t.Fatalf("tasks = %q", rr.tasks)
	}
	if tui.autoTurn {
		t.Error("the follow-up is not an auto turn: its own needed suggestions must be asked about")
	}
	found := false
	for _, m := range tui.messages {
		found = found || (m.Author == AuthorSystem && strings.Contains(m.Text, "✓ test the empty header ("))
	}
	if !found {
		t.Errorf("what is applied without asking must be said, with why: %+v", tui.messages)
	}
}

// With optional ones left, only those are asked, and the needed ones ride in the same follow-up.
func TestAutoModeAsksOnlyTheOptionalSuggestions(t *testing.T) {
	rr := &pendingRunner{items: mixedItems()}
	tui := &TUI{Out: &strings.Builder{}, Width: 90, Height: 30, Runner: rr, autoTurn: true}
	tui.openAskIfPending()
	if !tui.asking() || len(tui.ask.items) != 1 || tui.ask.items[0].Text != "share the retry helper" {
		t.Fatalf("only the optional suggestion should be asked, got %+v", tui.ask)
	}
	if last := tui.messages[len(tui.messages)-1]; !strings.Contains(last.Text,
		"✓ fix the panic the change exposed (the request is not correct with it)") {
		t.Errorf("the needed ones must be named before asking: %+v", last)
	}
	tui.ask.pick(2) // none of the optional ones
	tui.confirmAsk()
	want := "Apply these suggestions you made, and nothing else:\n" +
		"- test the empty header\n- fix the panic the change exposed"
	if len(rr.tasks) != 1 || rr.tasks[0] != want {
		t.Fatalf("tasks = %q, want %q", rr.tasks, want)
	}
	lines := userLines(tui)
	if len(lines) != 1 || !strings.Contains(lines[0], "✗ share the retry helper") ||
		!strings.Contains(lines[0], "✓ test the empty header") {
		t.Errorf("the conversation shows %q", lines)
	}
}

// Outside auto mode every suggestion is the user's call, needed or not.
func TestOutsideAutoModeEverySuggestionIsAsked(t *testing.T) {
	rr := &pendingRunner{items: mixedItems()}
	tui := &TUI{Out: &strings.Builder{}, Width: 90, Height: 30, Runner: rr}
	tui.openAskIfPending()
	if !tui.asking() || len(tui.ask.items) != 3 || len(rr.tasks) != 0 {
		t.Fatalf("all three should be asked, got %+v, tasks %q", tui.ask, rr.tasks)
	}
}

// A message sent from the Auto screen marks the turn as an auto one; any other screen does not.
func TestSubmitMarksAutoTurns(t *testing.T) {
	tui := &TUI{Out: &strings.Builder{}, Width: 90, Height: 30, Runner: &recordingRunner{}, screen: ScreenAuto}
	tui.submit(t.Context(), "fix the parser")
	if !tui.autoTurn {
		t.Error("a message from the Auto screen is an auto turn")
	}
	tui.screen = ScreenTask
	tui.submit(t.Context(), "fix the parser")
	if tui.autoTurn {
		t.Error("a message from the Task screen is not an auto turn")
	}
}

// agent.auto_apply_suggestions applies every suggestion without asking, in any mode, and says so.
func TestAutoApplyAppliesEverySuggestionOutsideAutoMode(t *testing.T) {
	rr := &pendingRunner{items: agent.ProposalItems(&agent.Report{AutoApply: true, Suggestions: []agent.ReportSuggestion{
		{Title: "test the empty header"}, {Title: "share the retry helper"},
	}})}
	tui := &TUI{Out: &strings.Builder{}, Width: 90, Height: 30, Runner: rr}
	tui.openAskIfPending()
	if tui.asking() {
		t.Fatal("nothing must be asked")
	}
	want := "Apply these suggestions you made, and nothing else:\n- test the empty header\n- share the retry helper"
	if len(rr.tasks) != 1 || rr.tasks[0] != want {
		t.Fatalf("tasks = %q", rr.tasks)
	}
	found := false
	for _, m := range tui.messages {
		found = found || (m.Author == AuthorSystem &&
			strings.Contains(m.Text, "as the settings say:\n✓ test the empty header\n✓ share"))
	}
	if !found {
		t.Errorf("what is applied without asking must be said: %+v", tui.messages)
	}
}
