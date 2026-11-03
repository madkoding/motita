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
	body, _, err := mem.Read("shop/Lessons.md")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"- make test runs the gate", "- pnpm, not npm", "- go 1.26", "author: motita", "type: lesson"} {
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
	cases := map[string]string{"/w/shop/": "shop/Lessons", "/": "", ".": ""}
	for in, want := range cases {
		if got, ok := ProjectEntry(nil, in); got != want || ok != (want != "") {
			t.Errorf("ProjectEntry(%q) = %q %v, want %q", in, got, ok, want)
		}
	}
	// A session's worktree is named after the session: the project's own name wins.
	view := memory.Open(t.TempDir()).ForProject("Shop online", "acme/shop")
	if got, _ := ProjectEntry(view, "/ws/worktrees/s-123"); got != "Shop online/Lessons" {
		t.Errorf("ProjectEntry(view) = %q", got)
	}
}

// TestKeepLessonsNamesTheProjectAndItsRepos: in a session of a project, the lessons go under the
// project's title, never the worktree's folder, and say which repositories they are about.
func TestKeepLessonsNamesTheProjectAndItsRepos(t *testing.T) {
	work := filepath.Join(t.TempDir(), "worktrees", "s-123")
	mem := memory.Open(t.TempDir()).ForProject("Shop online", "acme/shop", "acme/api")
	if err := KeepLessons(mem, work, []string{"make test runs the gate"}); err != nil {
		t.Fatal(err)
	}
	body, _, err := mem.Read("Shop online/Lessons.md")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"project: Shop online", "repos: acme/shop, acme/api", "type: lesson"} {
		if !strings.Contains(body, want) {
			t.Errorf("entry lacks %q:\n%s", want, body)
		}
	}
}

// TestKeepLessonsWithoutAProjectName: a workspace at "/" has no name, and nothing is mirrored under
// a generic one.
func TestKeepLessonsWithoutAProjectName(t *testing.T) {
	mem := memory.Open(t.TempDir())
	if err := KeepLessons(mem, "/", []string{"x"}); err == nil && len(mem.List()) != 0 {
		t.Errorf("mirrored under a shared name: %v", mem.List())
	}
}

// TestKeepLessonsHidesSecrets: a token a model copies into a lesson is masked before it is kept.
func TestKeepLessonsHidesSecrets(t *testing.T) {
	work := filepath.Join(t.TempDir(), "shop")
	mem := memory.Open(t.TempDir())
	in := []string{"deploy with GITHUB_TOKEN=ghp_abcdefghijklmnopqrstuvwxyz0123456789"}
	if err := KeepLessons(mem, work, in); err != nil {
		t.Fatal(err)
	}
	body, _, _ := mem.Read("shop/Lessons.md")
	if strings.Contains(body, "ghp_abcdefghijklmnopqrstuvwxyz0123456789") ||
		strings.Contains(strings.Join(Lessons(work), " "), "ghp_abcdefghij") {
		t.Errorf("the token was kept:\n%s", body)
	}
	if !strings.Contains(in[0], "ghp_") {
		t.Error("the caller's lessons are not changed")
	}
}
