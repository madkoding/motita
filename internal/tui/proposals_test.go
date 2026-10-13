package tui

import (
	"context"
	"strings"
	"testing"

	"github.com/madkoding/motita/internal/agent"
	"github.com/madkoding/motita/internal/config"
	"github.com/madkoding/motita/internal/i18n"
	"github.com/madkoding/motita/internal/llm"
	"github.com/madkoding/motita/internal/logx"
	"github.com/madkoding/motita/internal/sandbox"
	"github.com/madkoding/motita/internal/task"
)

// proposalItems are three suggestions of a finished task, as the window receives them.
func proposalItems() []agent.AskItem {
	return agent.ProposalItems(&agent.Report{Suggestions: []agent.ReportSuggestion{
		{Title: "test the empty header", Detail: "Parse panics on it"},
		{Title: "share the retry helper"},
		{Title: "log the dropped rows"},
	}})
}

// A finished task with suggestions hands them to the interface as proposals, with the request
// they came from.
func TestFinishedTaskHandsItsSuggestionsToTheInterface(t *testing.T) {
	r := NewAppRunner(&strings.Builder{}, &strings.Builder{}, config.Default(),
		&llm.Client{}, &sandbox.Sandbox{}, logx.Global())
	r.newAgent = func(config.Config, *logx.Logger, *llm.Client, *sandbox.Sandbox, task.Source, bool) AgentRunner {
		return &askingAgent{res: agent.TaskResult{Pass: true, Report: &agent.Report{
			Summary:     "fixed",
			Suggestions: []agent.ReportSuggestion{{Title: "test the empty header"}},
		}}}
	}
	if _, err := r.RunTask(context.Background(), "fix the parser", func(string, ...any) {}); err != nil {
		t.Fatalf("RunTask: %v", err)
	}
	items, origin := r.TakePendingQuestions()
	if len(items) != 1 || !items[0].Proposal || items[0].Text != "test the empty header" {
		t.Fatalf("the suggestion should arrive as a proposal, got %+v", items)
	}
	if origin != "fix the parser" {
		t.Fatalf("origin = %q, want the request", origin)
	}
}

// A failed task, or a chat reply, proposes nothing: only work that passed earns suggestions.
func TestOnlyAPassingTaskProposes(t *testing.T) {
	for _, res := range []agent.TaskResult{
		{Pass: false, Report: &agent.Report{Suggestions: []agent.ReportSuggestion{{Title: "x"}}}},
		{Pass: true, Kind: agent.KindChat, Report: &agent.Report{Suggestions: []agent.ReportSuggestion{{Title: "x"}}}},
		{Pass: true, Report: &agent.Report{Summary: "done"}},
	} {
		r := NewAppRunner(&strings.Builder{}, &strings.Builder{}, config.Default(),
			&llm.Client{}, &sandbox.Sandbox{}, logx.Global())
		res := res
		r.newAgent = func(config.Config, *logx.Logger, *llm.Client, *sandbox.Sandbox, task.Source, bool) AgentRunner {
			return &askingAgent{res: res}
		}
		_, _ = r.RunTask(context.Background(), "x", func(string, ...any) {})
		if items, _ := r.TakePendingQuestions(); items != nil {
			t.Fatalf("%+v should propose nothing, got %+v", res, items)
		}
	}
}

// The window names a proposal as a suggestion, shows what it would change, and offers the three
// answers in the interface's language.
func TestProposalWindowShowsTheThreeAnswers(t *testing.T) {
	tui := newAskTUI(t, proposalItems(), "")
	got := seen(tui)
	for _, want := range []string{"suggestion 1 of 3", "test the empty header", "Parse panics on it",
		"1) Apply this one", "2) Apply this one and the following", "3) Apply none"} {
		if !strings.Contains(got, want) {
			t.Fatalf("the window should show %q, got:\n%s", want, got)
		}
	}
	tui.Lang = i18n.ES
	got = seen(tui)
	for _, want := range []string{"sugerencia 1 de 3", "1) Aplicar esta", "2) Aplicar esta y las siguientes",
		"3) No aplicar ninguna"} {
		if !strings.Contains(got, want) {
			t.Fatalf("the window should show %q in Spanish, got:\n%s", want, got)
		}
	}
}

// "This one and the following" accepts every later proposal, and "none" refuses every later one;
// the proposals before keep their own answers.
func TestProposalAnswersSettleTheOnesAfter(t *testing.T) {
	a := newAsk(proposalItems(), "")
	a.pick(0) // the first: apply it, and move to the second
	a.pick(2) // the second: none of the rest
	if a.answers[0] != agent.ProposalApply || a.answers[1] != agent.ProposalSkipAll ||
		a.answers[2] != agent.ProposalSkipAll {
		t.Fatalf("answers = %q", a.answers)
	}
	a = newAsk(proposalItems(), "")
	a.pick(1)
	if a.answers[0] != agent.ProposalApplyRest || a.answers[1] != agent.ProposalApply ||
		a.answers[2] != agent.ProposalApply {
		t.Fatalf("answers = %q", a.answers)
	}
	// An answer of the clarifying kind settles nothing else.
	q := newAsk([]agent.AskItem{{Text: "a", Options: []string{"x"}}, {Text: "b"}}, "")
	q.pick(0)
	if q.answers[1] != "" {
		t.Fatalf("a question's answer must not fill the next, got %q", q.answers)
	}
	// A typed answer on a proposal is not an option, so it settles nothing either.
	a = newAsk(proposalItems(), "")
	a.answers[0] = "only for the json reader"
	a.settleAfter(0)
	if a.answers[1] != "" {
		t.Fatalf("a typed answer must not fill the next, got %q", a.answers)
	}
}

// Confirming sends exactly the accepted proposals, with the user's typed words about the others,
// and nothing that was refused or left unanswered.
func TestConfirmingProposalsAppliesOnlyTheAccepted(t *testing.T) {
	rr := &recordingRunner{}
	tui := &TUI{Out: &strings.Builder{}, Width: 90, Height: 30, Runner: rr}
	tui.ask = newAsk(proposalItems(), "fix the parser")
	tui.ask.answers[0] = agent.ProposalApply
	tui.ask.answers[1] = "only in the json reader"
	tui.ask.answers[2] = agent.ProposalSkipAll
	tui.confirmAsk()
	if len(rr.tasks) != 1 {
		t.Fatalf("one turn should run, got %d", len(rr.tasks))
	}
	got := rr.tasks[0]
	want := "Apply these suggestions you made, and nothing else:\n- test the empty header: Parse panics on it" +
		"\n\nAbout your other suggestions:\n- share the retry helper -> only in the json reader"
	if got != want {
		t.Fatalf("prompt = %q\nwant     %q", got, want)
	}
	if strings.Contains(got, "fix the parser") || strings.Contains(got, "log the dropped rows") {
		t.Fatalf("neither the old request nor a refused proposal may be sent again: %q", got)
	}
}

// Refusing them all runs nothing: there is nothing to apply and nothing to say.
func TestRefusingEveryProposalRunsNothing(t *testing.T) {
	rr := &recordingRunner{}
	tui := &TUI{Out: &strings.Builder{}, Width: 90, Height: 30, Runner: rr}
	tui.ask = newAsk(proposalItems(), "")
	tui.ask.pick(2)
	tui.confirmAsk()
	if len(rr.tasks) != 0 || tui.asking() {
		t.Fatalf("no turn should run and the window should close, got %q", rr.tasks)
	}
}

// userLines are the user's messages on screen.
func userLines(tui *TUI) []string {
	var out []string
	for _, m := range tui.messages {
		if m.Author == AuthorUser {
			out = append(out, m.Text)
		}
	}
	return out
}

// The turn that applies the accepted proposals shows what was chosen, not the prompt it sends:
// "Applying suggestions" and each suggestion marked as applied or not.
func TestApplyingProposalsShowsTheChoicesNotThePrompt(t *testing.T) {
	rr := &recordingRunner{}
	tui := &TUI{Out: &strings.Builder{}, Width: 90, Height: 30, Runner: rr}
	tui.ask = newAsk(proposalItems(), "fix the parser")
	tui.ask.pick(0)
	tui.ask.pick(2)
	tui.confirmAsk()
	if len(rr.tasks) != 1 || !strings.HasPrefix(rr.tasks[0], "Apply these suggestions") {
		t.Fatalf("the prompt should still be sent, got %q", rr.tasks)
	}
	lines := userLines(tui)
	want := "Applying suggestions\n✓ test the empty header\n✗ share the retry helper\n✗ log the dropped rows"
	if len(lines) != 1 || lines[0] != want {
		t.Fatalf("the conversation shows %q, want %q", lines, want)
	}
}

// Refusing every proposal runs no turn, but the conversation still says what was decided.
func TestRefusingEveryProposalSaysSo(t *testing.T) {
	tui := &TUI{Out: &strings.Builder{}, Width: 90, Height: 30, Runner: &recordingRunner{}, Lang: i18n.ES}
	tui.ask = newAsk(proposalItems(), "")
	tui.ask.pick(2)
	tui.confirmAsk()
	last := tui.messages[len(tui.messages)-1]
	if last.Author != AuthorSystem || !strings.HasPrefix(last.Text, "No se aplicó ninguna sugerencia:") ||
		!strings.Contains(last.Text, "✗ log the dropped rows") {
		t.Fatalf("last message = %+v", last)
	}
}

// A conversation read back draws the turn that applied suggestions by its label, in either
// language it was sent in, and every other turn as written.
func TestAReadBackProposalTurnIsLabelled(t *testing.T) {
	tui := &TUI{Lang: i18n.ES}
	for _, sent := range []string{
		"Apply these suggestions you made, and nothing else:\n- a",
		"Aplica estas sugerencias que hiciste, y nada más:\n- a",
	} {
		if got := tui.shownTurn(sent); got != "Aplicando sugerencias" {
			t.Fatalf("shownTurn(%q) = %q", sent, got)
		}
	}
	if got := tui.shownTurn("fix the parser"); got != "fix the parser" {
		t.Fatalf("an ordinary turn changed: %q", got)
	}
}
