package brief

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestComposeKeepsTheOrderAndSkipsEmptyParts(t *testing.T) {
	got := Compose(Budget, Part{"Plan", "1. a"}, Part{"Empty", "  "}, Part{"Memory", "fact"})
	if !strings.HasPrefix(got, "## WHAT THIS CONVERSATION ALREADY KNOWS") {
		t.Errorf("no header:\n%s", got)
	}
	if strings.Contains(got, "Empty") || strings.Index(got, "### Plan") > strings.Index(got, "### Memory") {
		t.Errorf("order or empty part wrong:\n%s", got)
	}
}

func TestComposeIsEmptyWithNothingToSay(t *testing.T) {
	if got := Compose(Budget, Part{"Plan", ""}); got != "" {
		t.Errorf("got %q", got)
	}
}

// The first part is never pushed out by the ones after it, and the brief stays within its budget.
func TestComposeCutsWhatDoesNotFit(t *testing.T) {
	long := strings.Repeat("ñ", 3000) // two bytes a rune, so a cut can land inside one
	got := Compose(1000, Part{"Plan", "the plan"}, Part{"Archive", long}, Part{"Memory", "never reached"})
	if len(got) > 1000 {
		t.Errorf("the brief is %d bytes, over its budget", len(got))
	}
	if !strings.Contains(got, "the plan") || !strings.Contains(got, "[…cut to fit the brief]") {
		t.Errorf("the plan or the cut mark is missing:\n%s", got)
	}
	if strings.Contains(got, "never reached") {
		t.Error("a part with no room left was added")
	}
	if !utf8.ValidString(got) {
		t.Error("the cut split a rune")
	}
}

func TestCutNeverSplitsARune(t *testing.T) {
	const mark = "\n[…cut to fit the brief]"
	got := cut("a"+strings.Repeat("ñ", 50), len(mark)+2)
	if got != "a"+mark {
		t.Errorf("cut = %q", got)
	}
}
