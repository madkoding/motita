package knowledge

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/madkoding/motita/internal/memory"
)

// TestKeepLessonsMirrorsNewOnesIntoTheMemory: what a task learns shows in the person's memory,
// each lesson once, under the project's entry.
func TestKeepLessonsMirrorsNewOnesIntoTheMemory(t *testing.T) {
	work := filepath.Join(t.TempDir(), "shop")
	mem := memory.Open(t.TempDir())
	if err := KeepLessons(mem, work, []string{"make test runs the gate", "", "pnpm, not npm"}); err != nil {
		t.Fatal(err)
	}
	if err := KeepLessons(mem, work, []string{"Make test runs the gate", "go 1.26"}); err != nil {
		t.Fatal(err)
	}
	body, _, err := mem.Read("Projects/shop.md")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"- make test runs the gate", "- pnpm, not npm", "- go 1.26", "author: motita"} {
		if !strings.Contains(body, want) {
			t.Errorf("entry lacks %q:\n%s", want, body)
		}
	}
	if strings.Count(strings.ToLower(body), "make test runs the gate") != 1 {
		t.Errorf("a known lesson is not added again:\n%s", body)
	}
	if got := Lessons(work); len(got) != 3 {
		t.Errorf("project file = %q", got)
	}
}

func TestKeepLessonsWithoutMemoryOrWorkspace(t *testing.T) {
	if err := KeepLessons(nil, t.TempDir(), []string{"x"}); err != nil {
		t.Fatal(err)
	}
	mem := memory.Open(t.TempDir())
	if err := KeepLessons(mem, "", []string{"x"}); err != nil || len(mem.List()) != 0 {
		t.Fatal("no workspace: nothing kept anywhere")
	}
	blocked := filepath.Join(t.TempDir(), "file")
	_ = os.WriteFile(blocked, []byte("x"), 0o600)
	if err := KeepLessons(mem, blocked, []string{"x"}); err == nil || len(mem.List()) != 0 {
		t.Error("a lesson the project could not keep is not mirrored")
	}
}

func TestProjectEntry(t *testing.T) {
	for in, want := range map[string]string{"/w/shop/": "Projects/shop", "/": "Projects/project", ".": "Projects/project"} {
		if got := ProjectEntry(in); got != want {
			t.Errorf("ProjectEntry(%q) = %q, want %q", in, got, want)
		}
	}
}
