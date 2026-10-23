package intent

import (
	"context"
	"errors"
	"testing"
)

func answer(text string, err error) Completer {
	return func(context.Context, string, string) (string, error) { return text, err }
}

func TestRefineAsksTheModelOnlyWhenTheRulesHadNothing(t *testing.T) {
	called := false
	spy := func(context.Context, string, string) (string, error) {
		called = true
		return `{"kind":"task"}`, nil
	}
	for _, text := range []string{"/plan x", "fix it", "how?"} {
		if d := Refine(context.Background(), Classify(text), spy); d.Source == SourceModel {
			t.Errorf("%q: the model overrode the rules: %+v", text, d)
		}
	}
	if called {
		t.Error("the model was asked about a message the rules had already decided")
	}
	if d := Refine(context.Background(), Classify("hola"), nil); d.Source != SourceDefault {
		t.Errorf("without a model the default stays: %+v", d)
	}
}

func TestRefineTakesTheModelsKind(t *testing.T) {
	d := Refine(context.Background(), Classify("the cache"), answer("sure: {\"kind\": \"PLAN\"} done", nil))
	if d.Kind != KindPlan || d.Source != SourceModel || d.Prompt != "the cache" {
		t.Errorf("Refine = %+v", d)
	}
	if got := d.Note(); got != "mode: plan (model)" {
		t.Errorf("Note() = %q", got)
	}
}

func TestRefineKeepsTheDefaultWhenTheModelFails(t *testing.T) {
	for name, c := range map[string]Completer{
		"error":      answer("", errors.New("down")),
		"no json":    answer("plan", nil),
		"bad json":   answer("{kind}", nil),
		"other kind": answer(`{"kind":"chat"}`, nil),
		"reversed":   answer("} {", nil),
	} {
		t.Run(name, func(t *testing.T) {
			d := Refine(context.Background(), Classify("the cache"), c)
			if d.Source != SourceDefault || d.Kind != KindTask || d.Reason != ReasonModelFailed {
				t.Errorf("Refine = %+v", d)
			}
		})
	}
}

func TestCorrectsCountsTheSameMessageSentToTheOtherMode(t *testing.T) {
	guess := Classify("how does the cache work?")
	for _, tc := range []struct {
		prev Decision
		next string
		want bool
	}{
		{guess, "/task how does the cache work?", true},
		{guess, "/plan how does the cache work?", false},
		{guess, "/task something else", false},
		{guess, "fix it", false},
		{Classify("/plan x"), "/task x", false},
		{Decision{}, "/task x", false},
	} {
		if got := Corrects(tc.prev, Classify(tc.next)); got != tc.want {
			t.Errorf("Corrects(%+v, %q) = %v, want %v", tc.prev, tc.next, got, tc.want)
		}
	}
}
