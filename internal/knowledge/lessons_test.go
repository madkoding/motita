package knowledge

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLessonsAreKeptOncePerLineAndBounded(t *testing.T) {
	dir := t.TempDir()
	if err := RecordLessons(dir, nil); err != nil || LessonsContext(dir) != "" {
		t.Fatal("nothing to record, nothing to read")
	}
	if err := RecordLessons("", []string{"x"}); err != nil {
		t.Fatal("no workspace, nothing written, no error")
	}
	lessons := []string{" make test runs the gate ", "", "Make test runs the gate", "pnpm, not npm"}
	if err := RecordLessons(dir, lessons); err != nil {
		t.Fatal(err)
	}
	if got := Lessons(dir); len(got) != 2 || got[0] != "make test runs the gate" || got[1] != "pnpm, not npm" {
		t.Fatalf("lines = %q", got)
	}
	ctx := LessonsContext(dir)
	if !strings.Contains(ctx, "earlier tasks learned") || !strings.Contains(ctx, "- pnpm, not npm") {
		t.Errorf("context = %q", ctx)
	}
	raw, _ := os.ReadFile(filepath.Join(dir, LessonsFile))
	if !strings.HasPrefix(string(raw), lessonsHeading) {
		t.Errorf("file = %q", raw)
	}
	var many []string
	for i := 0; i < maxLessons+5; i++ {
		many = append(many, fmt.Sprintf("lesson %03d %s", i, strings.Repeat("x", 80)))
	}
	if err := RecordLessons(dir, many); err != nil {
		t.Fatal(err)
	}
	lines := Lessons(dir)
	if len(lines) != maxLessons || !strings.HasPrefix(lines[0], "lesson 005") {
		t.Errorf("the oldest go: %d lines, first %q", len(lines), lines[0])
	}
	if len(LessonsContext(dir)) > maxLessonsContext+120 {
		t.Error("the context is bounded")
	}
	blocked := filepath.Join(dir, "file")
	_ = os.WriteFile(blocked, []byte("x"), 0o600)
	if err := RecordLessons(blocked, []string{"x"}); err == nil {
		t.Error("a workspace that is a file is reported")
	}
}

// TestOneLineBoundsALesson: a lesson is one line, and a long one is cut instead of wrapping the
// memory file.
func TestOneLineBoundsALesson(t *testing.T) {
	if got := oneLine("  a   lesson\nwith\tspaces "); got != "a lesson with spaces" {
		t.Errorf("oneLine = %q", got)
	}
	if got := oneLine(strings.Repeat("a ", 200)); len(got) > 125 || !strings.HasSuffix(got, "…") {
		t.Errorf("oneLine = %d bytes, %q", len(got), got)
	}
}
