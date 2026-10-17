package agent

import (
	"context"
	"testing"

	"github.com/madkoding/motita/internal/anchor"
	"github.com/madkoding/motita/internal/task"
)

// With agent.auto_apply_suggestions the synthesis stamps the report, and only a report that has
// suggestions.
func TestSynthesizePhaseStampsAutoApply(t *testing.T) {
	a := synthesisAgent(t, `{"summary":"done","suggestions":[{"title":"a"}]}`)
	a.cfg.Agent.AutoApplySuggestions = true
	rep := a.synthesizePhase(context.Background(), synthesisTask(t), "out", anchor.Result{Pass: true})
	if rep == nil || !rep.AutoApply {
		t.Fatalf("report = %+v, want auto apply", rep)
	}
	for _, it := range ProposalItems(rep) {
		if !it.AutoApply {
			t.Errorf("item %+v must carry it", it)
		}
	}

	b := synthesisAgent(t, `{"summary":"done"}`)
	b.cfg.Agent.AutoApplySuggestions = true
	if rep := b.synthesizePhase(context.Background(), synthesisTask(t), "out", anchor.Result{Pass: true}); rep.AutoApply {
		t.Error("a report with no suggestions has nothing to apply")
	}

	c := synthesisAgent(t, `{"summary":"done","suggestions":[{"title":"a"}]}`)
	if rep := c.synthesizePhase(context.Background(), synthesisTask(t), "out", anchor.Result{Pass: true}); rep.AutoApply {
		t.Error("off by default")
	}
}

// The turn that applies suggestions never auto-applies its own, in either language.
func TestApplyingTurnDoesNotChain(t *testing.T) {
	for _, lead := range []string{ProposalLead, "Aplica estas sugerencias que hiciste, y nada más:"} {
		src, err := task.NewText(lead+"\n- a", "test")
		if err != nil {
			t.Fatal(err)
		}
		tk, _ := src.Next(context.Background())
		a := synthesisAgent(t, `{"summary":"done","suggestions":[{"title":"b"}]}`)
		a.cfg.Agent.AutoApplySuggestions = true
		rep := a.synthesizePhase(context.Background(), tk, "out", anchor.Result{Pass: true})
		if rep == nil || rep.AutoApply {
			t.Errorf("%q: report = %+v, want no auto apply", lead, rep)
		}
	}
	if IsApplyingTurn("fix the parser") {
		t.Error("an ordinary task is not an applying turn")
	}
}

// SplitNeeded takes an auto-apply proposal whether or not it is needed.
func TestSplitNeededTakesAutoApply(t *testing.T) {
	items := ProposalItems(&Report{AutoApply: true, Suggestions: []ReportSuggestion{{Title: "a"}, {Title: "b"}}})
	if !AutoApplies(items) {
		t.Fatal("AutoApplies")
	}
	applied, rest := SplitNeeded(items)
	if len(applied) != 2 || len(rest) != 0 {
		t.Errorf("applied %d, rest %d", len(applied), len(rest))
	}
	if AutoApplies(ProposalItems(&Report{Suggestions: []ReportSuggestion{{Title: "a"}}})) {
		t.Error("off unless stamped")
	}
}
