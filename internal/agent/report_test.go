package agent

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/madkoding/motita/internal/anchor"
)

// TestSynthesizePhaseDecodesTheStructuredReport: the model's JSON arrives as fields, each in the
// place a front end will read it from, and the version is stamped by the program, not the model.
func TestSynthesizePhaseDecodesTheStructuredReport(t *testing.T) {
	a := synthesisAgent(t, `{"status":"done","summary":"added Foo","changes":[{"path":"a.go","kind":"added","description":"Foo"}],
	"verification":[{"check":"go test ./...","result":"pass","evidence":"12 passed"}],"risks":["none known"],"next_steps":["wire it up"]}`)
	rep := a.synthesizePhase(context.Background(), synthesisTask(t), "out", anchor.Result{Pass: true})
	if rep == nil {
		t.Fatal("a well-formed report must decode")
	}
	want := Report{Version: ReportVersion, Status: "done", Summary: "added Foo",
		Changes:      []ReportChange{{Path: "a.go", Kind: "added", Description: "Foo"}},
		Verification: []ReportCheck{{Check: "go test ./...", Result: "pass", Evidence: "12 passed"}},
		Risks:        []string{"none known"}, NextSteps: []string{"wire it up"}}
	if !reflect.DeepEqual(*rep, want) {
		t.Errorf("report = %+v\nwant     %+v", *rep, want)
	}
}

// TestAReportWithOnlyASummaryStillDraws: an older or lazier model returns just {"summary"}. It
// must still come out as a complete report, with lists that encode as [] and never as null, so a
// front end can iterate without a guard.
func TestAReportWithOnlyASummaryStillDraws(t *testing.T) {
	a := synthesisAgent(t, `{"summary":"hi"}`)
	rep := a.synthesizePhase(context.Background(), synthesisTask(t), "out", anchor.Result{Pass: true})
	if rep == nil || rep.Status != "done" {
		t.Fatalf("status must come from the validator when the model gives none, got %+v", rep)
	}
	raw, _ := json.Marshal(rep)
	var m map[string]json.RawMessage
	_ = json.Unmarshal(raw, &m)
	for _, k := range []string{"changes", "verification", "risks", "next_steps"} {
		if string(m[k]) != "[]" {
			t.Errorf("%s encodes as %s, want []", k, m[k])
		}
	}
}

// TestReportNormalizeRepairsWhatItCannotTrust: unknown enums fall back, empty entries go, and a
// status the validator contradicts is not invented as success.
func TestReportNormalizeRepairsWhatItCannotTrust(t *testing.T) {
	r := Report{Status: "GREAT", Summary: " s ",
		Changes:      []ReportChange{{Path: " x ", Kind: "Weird"}, {}, {Kind: "DELETED", Path: "y"}},
		Verification: []ReportCheck{{Check: "c", Result: "maybe"}, {Result: "pass"}, {Check: "d", Result: "FAIL"}},
		Risks:        []string{" ", "r"}}
	r.normalize(false)
	if r.Status != "failed" || r.Summary != "s" {
		t.Errorf("status/summary = %q/%q", r.Status, r.Summary)
	}
	if len(r.Changes) != 2 || r.Changes[0].Kind != "other" || r.Changes[0].Path != "x" || r.Changes[1].Kind != "deleted" {
		t.Errorf("changes = %+v", r.Changes)
	}
	if len(r.Verification) != 2 || r.Verification[0].Result != "skipped" || r.Verification[1].Result != "fail" {
		t.Errorf("verification = %+v", r.Verification)
	}
	if !reflect.DeepEqual(r.Risks, []string{"r"}) || r.NextSteps == nil {
		t.Errorf("risks/next = %+v / %+v", r.Risks, r.NextSteps)
	}
	r2 := Report{Status: "partial"}
	r2.normalize(true)
	if r2.Status != "partial" {
		t.Errorf("a valid status must be kept, got %q", r2.Status)
	}
}

// TestReportTextIsThePlainViewForTheTerminal: the summary first, then only the sections that have
// something in them, and a report that is just a summary adds nothing to it.
func TestReportTextIsThePlainViewForTheTerminal(t *testing.T) {
	r := Report{Summary: "added Foo",
		Changes:      []ReportChange{{Path: "a.go", Kind: "added", Description: "Foo"}, {Kind: "other", Description: "config"}},
		Verification: []ReportCheck{{Check: "go test", Result: "pass", Evidence: "3 passed"}, {Check: "lint", Result: "fail"}, {Check: "shot", Result: "skipped"}},
		Risks:        []string{"r"}, NextSteps: []string{"n"}}
	want := "added Foo\n\nChanged:\n  [added] a.go - Foo\n  [other] - config\n\nChecked:\n  ok   go test - 3 passed\n  FAIL lint\n  skip shot\n\nWorth knowing:\n  r\n\nNext:\n  n"
	if got := r.Text(); got != want {
		t.Errorf("Text() =\n%s\nwant\n%s", got, want)
	}
	if got := (&Report{Summary: "only this"}).Text(); got != "only this" {
		t.Errorf("a bare summary must stay bare, got %q", got)
	}
}
