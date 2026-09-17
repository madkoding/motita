package template

import (
	"strings"
	"testing"
)

func TestRenderSubstitutesVariables(t *testing.T) {
	out, missing := Render("Task: {{task}} in {{workspace}}", map[string]string{
		"task":      "fix the test",
		"workspace": "/tmp/work",
	})
	if out != "Task: fix the test in /tmp/work" {
		t.Errorf("out = %q", out)
	}
	if len(missing) != 0 {
		t.Errorf("nothing should be missing: %v", missing)
	}
}

func TestRenderWithSpaces(t *testing.T) {
	out, _ := Render("{{ task }} and {{task}}", map[string]string{"task": "x"})
	if out != "x and x" {
		t.Errorf("out = %q", out)
	}
}

// TestRenderReportsMissing: a variable with no value is not silently replaced by
// an empty string; a warning is issued, because a prompt with holes makes the
// model fail in puzzling ways.
func TestRenderReportsMissing(t *testing.T) {
	out, missing := Render("{{task}} -- {{unknown}} -- {{other}}", map[string]string{"task": "x"})
	if len(missing) != 2 {
		t.Fatalf("missing = %v", missing)
	}
	if missing[0] != "other" || missing[1] != "unknown" {
		t.Errorf("missing out of order: %v", missing)
	}
	if !strings.Contains(out, "{{unknown}}") {
		t.Errorf("the unknown variable must stay visible: %q", out)
	}
}

func TestVariablesDetected(t *testing.T) {
	vars := Variables("{{a}} {{b}} {{a}} and {{ c }}")
	expected := []string{"a", "b", "c"}
	if strings.Join(vars, ",") != strings.Join(expected, ",") {
		t.Errorf("variables = %v", vars)
	}
}

func TestHistoryEmpty(t *testing.T) {
	h := History(nil)
	if !strings.Contains(h, "first attempt") {
		t.Errorf("history = %q", h)
	}
}

func TestHistoryWithAttempts(t *testing.T) {
	h := History([]string{"test 1 failed", "test 2 failed"})
	if !strings.Contains(h, "Failed attempt 1") || !strings.Contains(h, "Failed attempt 2") {
		t.Errorf("history = %q", h)
	}
	if !strings.Contains(h, "test 1 failed") {
		t.Errorf("missing the attempt contents: %q", h)
	}
}
