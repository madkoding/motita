package evals

import "testing"

func TestModeOfAndPreviousOf(t *testing.T) {
	runs := []Run{{ID: "a"}, {ID: "b", Mode: ModeDirect}, {ID: "c", Mode: ModePhased}, {ID: "d", Mode: ModeDirect}}
	if ModeOf(runs[0]) != ModePhased || ModeOf(runs[1]) != ModeDirect {
		t.Fatal("ModeOf")
	}
	if p := PreviousOf(runs, 3); p == nil || p.ID != "b" {
		t.Errorf("previous direct = %+v", p)
	}
	if p := PreviousOf(runs, 2); p == nil || p.ID != "a" {
		t.Errorf("previous phased = %+v", p)
	}
	if PreviousOf(runs, 1) != nil {
		t.Error("the first direct run has nothing before it")
	}
}

func TestCompareModes(t *testing.T) {
	phased := Run{ID: "p", Model: "m", CasesHash: "h", Summary: Summary{Passed: 4, Total: 5, Steps: 20},
		Results: []CaseResult{{ID: "x", Pass: false}}}
	direct := Run{ID: "d", Model: "m", CasesHash: "h", Mode: ModeDirect, Summary: Summary{Passed: 5, Total: 5, Steps: 8},
		Results: []CaseResult{{ID: "x", Pass: true}}}
	v := CompareModes([]Run{phased, {ID: "old", Mode: ModeDirect}, direct})
	if v == nil || v.Verdict.Status != StatusBetter || v.Phased.Passed != 4 || v.Direct.Steps != 8 {
		t.Fatalf("versus = %+v", v)
	}
	if CompareModes([]Run{phased}) != nil || CompareModes([]Run{direct}) != nil {
		t.Error("one mode alone has nothing to weigh")
	}
	other := direct
	other.Model = "n"
	if CompareModes([]Run{phased, other}) != nil {
		t.Error("two models are not two modes")
	}
	moved := direct
	moved.CasesHash = "g"
	if CompareModes([]Run{phased, moved}) != nil {
		t.Error("two task sets are not two modes")
	}
}
