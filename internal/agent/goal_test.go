package agent

import (
	"strings"
	"testing"
)

func TestGoalOf(t *testing.T) {
	cases := []struct {
		in   string
		want string
		ok   bool
	}{
		{"/goal add a login page", "add a login page", true},
		{"  /goal   fix the build ", "fix the build", true},
		{"/goal", "/goal", false},
		{"/goalpost review", "/goalpost review", false},
		{"fix the build", "fix the build", false},
	}
	for _, c := range cases {
		got, ok := goalOf(c.in)
		if got != c.want || ok != c.ok {
			t.Errorf("goalOf(%q) = %q, %v; want %q, %v", c.in, got, ok, c.want, c.ok)
		}
	}
}

func TestDecisionsAreRecordedAndReturned(t *testing.T) {
	dir := t.TempDir()
	if err := recordDecisions(dir, "add login", nil); err != nil || ReadDecisions(dir) != "" {
		t.Fatalf("nothing decided must write nothing")
	}
	ds := []ReportDecision{{Decision: "use sessions", Why: "simplest"}, {Decision: "no OAuth"}}
	if err := recordDecisions(dir, "add login", ds); err != nil {
		t.Fatal(err)
	}
	got := ReadDecisions(dir)
	for _, want := range []string{"add login", "- use sessions — simplest", "- no OAuth"} {
		if !strings.Contains(got, want) {
			t.Errorf("decisions file missing %q:\n%s", want, got)
		}
	}
	if !strings.Contains(decisionsContext(dir), "use sessions") {
		t.Errorf("the next run must see the standing decisions")
	}
	if decisionsContext(t.TempDir()) != "" {
		t.Errorf("no file, no context")
	}
}

func TestNormalizeDropsEmptyDecisions(t *testing.T) {
	r := Report{Decisions: []ReportDecision{{Decision: " "}, {Decision: " x ", Why: " y "}}}
	r.normalize(true)
	if len(r.Decisions) != 1 || r.Decisions[0].Decision != "x" || r.Decisions[0].Why != "y" {
		t.Fatalf("decisions = %+v", r.Decisions)
	}
}

func TestRemoveDecision(t *testing.T) {
	dir := t.TempDir()
	_ = recordDecisions(dir, "a", []ReportDecision{{Decision: "one"}, {Decision: "two"}})
	_ = recordDecisions(dir, "b", []ReportDecision{{Decision: "three"}})
	if got := NumberedDecisions(dir); !strings.Contains(got, "2. two") || !strings.Contains(got, "3. three") {
		t.Fatalf("numbering:\n%s", got)
	}
	if got, err := RemoveDecision(dir, 3); err != nil || got != "three" {
		t.Fatalf("removed %q, %v", got, err)
	}
	if strings.Contains(ReadDecisions(dir), "— b") {
		t.Errorf("the emptied section's heading must go too:\n%s", ReadDecisions(dir))
	}
	if _, err := RemoveDecision(dir, 9); err == nil {
		t.Errorf("an unknown number must fail")
	}
	_, _ = RemoveDecision(dir, 1)
	_, _ = RemoveDecision(dir, 1)
	if ReadDecisions(dir) != "" {
		t.Errorf("an empty file must be removed")
	}
}
