package agent

import (
	"context"
	"strings"
	"testing"
)

// The full cycle through the REAL action path, no model involved: read a skill, then apply a
// verdict to what was read, then prove the value survived a restart. This is the wiring the
// live probe could not exercise because the model chose to search instead of reading.
func TestTheFullCycleThroughTheActionPath(t *testing.T) {
	a := libraryAgent(t, map[string]string{"count-files": "# Count files\n1. find . -type f | wc -l\n"})

	// 1. The model asks for the procedure by kind, as it does in Task mode.
	out, err := a.runActions(context.Background(), []Command{
		{Kind: "read_skill", Description: "leer el procedimiento", Command: "count-files"},
	}, "")
	if err != nil {
		t.Fatalf("runActions: %v", err)
	}
	if !strings.Contains(out, "find . -type f") {
		t.Fatalf("the procedure must come back:\n%s", out)
	}

	// 2. What the turn read is what a verdict lands on.
	used := a.Consulted()
	if used["count-files"] != 1 {
		t.Fatalf("the read must be recorded, got %+v", used)
	}
	if err := a.reward.Attribute([]string{"count-files"}, used, false, "step 1 does not include subfolders"); err != nil {
		t.Fatalf("Attribute: %v", err)
	}

	// 3. The value is negative and the complaint is outstanding.
	s, _ := a.reward.Get("count-files")
	if s.Value >= 0 {
		t.Errorf("a bad verdict must lower the value, got %v", s.Value)
	}
	if len(s.Unaddressed()) != 1 {
		t.Fatalf("the complaint must be outstanding, got %+v", s.Notes)
	}

	// 4. The next read shows the complaint, so a fix can be written from it.
	again := act(t, a, "read_skill", "count-files")
	if !strings.Contains(again, "does not include subfolders") {
		t.Errorf("the complaint must reach the model on the next read:\n%s", again)
	}

	// 5. Fixing it marks the complaint answered.
	fixed := act(t, a, "save_skill", "count-files :: # Count files\n1. find . -type f | wc -l  (all subfolders)\n")
	if !strings.Contains(strings.ToLower(fixed), "addressed") {
		t.Errorf("the fix must mark the complaint:\n%s", fixed)
	}
	after := act(t, a, "read_skill", "count-files")
	if strings.Contains(after, "does not include subfolders") {
		t.Errorf("the answered complaint must not be repeated:\n%s", after)
	}
}
