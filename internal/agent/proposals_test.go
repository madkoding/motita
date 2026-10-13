package agent

import (
	"strings"
	"testing"
)

// A report with nothing to propose asks nothing: a task that found nothing beyond the request ends
// as it always did.
func TestProposalItemsWithNothingToPropose(t *testing.T) {
	if got := ProposalItems(nil); got != nil {
		t.Fatalf("no report, no proposals: got %+v", got)
	}
	if got := ProposalItems(&Report{Summary: "done"}); got != nil {
		t.Fatalf("a report without suggestions proposes nothing: got %+v", got)
	}
}

// Every suggestion becomes one proposal with the three fixed answers, in order, and each item gets
// its own copy of them so the interface can never change the shared list.
func TestProposalItemsCarryTheThreeAnswers(t *testing.T) {
	items := ProposalItems(&Report{Suggestions: []ReportSuggestion{
		{Title: "test the empty header", Detail: "Parse panics on it"},
		{Title: "share the retry helper"},
	}})
	if len(items) != 2 {
		t.Fatalf("one proposal per suggestion, got %d", len(items))
	}
	it := items[0]
	if !it.Proposal || it.Text != "test the empty header" || it.Detail != "Parse panics on it" {
		t.Fatalf("the proposal should carry the suggestion whole, got %+v", it)
	}
	want := []string{ProposalApply, ProposalApplyRest, ProposalSkipAll}
	if strings.Join(it.Options, "|") != strings.Join(want, "|") {
		t.Fatalf("options = %q, want %q", it.Options, want)
	}
	items[0].Options[0] = "changed"
	if ProposalOptions[0] != ProposalApply {
		t.Fatal("an item's options must be its own copy")
	}
}

// The follow-up names exactly what was accepted, title and detail, behind the lead; with nothing
// accepted there is no turn.
func TestProposalRequest(t *testing.T) {
	if got := ProposalRequest("Apply these:", nil); got != "" {
		t.Fatalf("nothing accepted is no request, got %q", got)
	}
	got := ProposalRequest(" Apply these: ", []AskItem{
		{Text: "test the empty header", Detail: "Parse panics on it"},
		{Text: "share the retry helper"},
	})
	want := "Apply these:\n- test the empty header: Parse panics on it\n- share the retry helper"
	if got != want {
		t.Fatalf("request = %q\nwant      %q", got, want)
	}
}

// Suggestions are cleaned like every other list: the empty ones go, the rest are trimmed, and
// there are never more than maxSuggestions.
func TestNormalizeSuggestionsCapsTheList(t *testing.T) {
	in := []ReportSuggestion{{Title: "  "}}
	for i := 0; i < maxSuggestions+2; i++ {
		in = append(in, ReportSuggestion{Title: " s ", Detail: " d "})
	}
	got := normalizeSuggestions(in)
	if len(got) != maxSuggestions {
		t.Fatalf("got %d suggestions, want the cap of %d", len(got), maxSuggestions)
	}
	if got[0] != (ReportSuggestion{Title: "s", Detail: "d"}) {
		t.Fatalf("a suggestion should be trimmed, got %+v", got[0])
	}
}

// The plain-text report lists the suggestions as not applied, so a reader without the window
// still sees they are proposals and not work done.
func TestReportTextListsTheSuggestions(t *testing.T) {
	r := Report{Summary: "done", Suggestions: []ReportSuggestion{
		{Title: "test the empty header", Detail: "Parse panics on it"},
		{Title: "share the retry helper", Needed: true, WhyNeeded: "two copies drift"},
	}}
	got := r.Text()
	for _, want := range []string{
		"Suggestions (not applied):",
		"  test the empty header - Parse panics on it",
		"  share the retry helper (needed: two copies drift)",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("the text should contain %q, got:\n%s", want, got)
		}
	}
}

func TestSplitNeededKeepsTheOrderAndNeverTakesAQuestion(t *testing.T) {
	items := ProposalItems(&Report{Suggestions: []ReportSuggestion{
		{Title: "a", Needed: true}, {Title: "b"}, {Title: "c", Needed: true},
	}})
	items = append(items, AskItem{Text: "which file?", Needed: true})
	needed, rest := SplitNeeded(items)
	if len(needed) != 2 || needed[0].Text != "a" || needed[1].Text != "c" {
		t.Errorf("needed = %+v", needed)
	}
	if len(rest) != 2 || rest[0].Text != "b" || rest[1].Text != "which file?" {
		t.Errorf("rest = %+v", rest)
	}
}

// A suggestion marked needed has to say why, or it is the user's call like any other.
func TestNeededWithoutAReasonIsOptional(t *testing.T) {
	got := normalizeSuggestions([]ReportSuggestion{
		{Title: "a", Needed: true, WhyNeeded: "  the test is missing "},
		{Title: "b", Needed: true, WhyNeeded: "  "},
		{Title: "c", WhyNeeded: "stray"},
	})
	if !got[0].Needed || got[0].WhyNeeded != "the test is missing" {
		t.Errorf("a = %+v", got[0])
	}
	if got[1].Needed || got[1].WhyNeeded != "" || got[2].Needed || got[2].WhyNeeded != "" {
		t.Errorf("b, c = %+v, %+v", got[1], got[2])
	}
	if items := ProposalItems(&Report{Suggestions: got}); items[0].Why != "the test is missing" {
		t.Errorf("the reason must reach the interface: %+v", items[0])
	}
}

// agent.auto_mode_ask_all_suggestions leaves every suggestion to the user.
func TestAskAllSuggestionsClearsNeeded(t *testing.T) {
	r := Report{Suggestions: []ReportSuggestion{{Title: "a", Needed: true, WhyNeeded: "x"}}}
	r.askAllSuggestions()
	if r.Suggestions[0].Needed || r.Suggestions[0].WhyNeeded != "" {
		t.Errorf("%+v", r.Suggestions[0])
	}
}
