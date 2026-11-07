package knowledge

import (
	"errors"
	"fmt"
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
	wants := []string{"- make test runs the gate", "- pnpm, not npm", "- go 1.26", "author: motita", "type: lesson"}
	for _, want := range wants {
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

// The memory mirror holds each lesson once even after the project file rotated it out, stays
// within its cap, and keeps its header.
func TestTheMirrorDoesNotDuplicateAfterRotationAndStaysBounded(t *testing.T) {
	work := filepath.Join(t.TempDir(), "shop")
	mem := memory.Open(t.TempDir())
	for i := 0; i < maxLessons+10; i++ {
		if err := KeepLessons(mem, work, []string{fmt.Sprintf("lesson %03d", i)}); err != nil {
			t.Fatal(err)
		}
	}
	// lesson 000 rotated out of the 60 of the file; saying it again must not repeat it in the mirror.
	if err := KeepLessons(mem, work, []string{"lesson 000"}); err != nil {
		t.Fatal(err)
	}
	body, _, _ := mem.Read("shop/Lessons.md")
	if n := strings.Count(body, "- lesson 000"); n != 1 {
		t.Errorf("lesson 000 appears %d times", n)
	}
	for i := 0; i < maxMirrored; i++ {
		if err := KeepLessons(mem, work, []string{fmt.Sprintf("bulk %03d", i)}); err != nil {
			t.Fatal(err)
		}
	}
	body, _, err := mem.Read("shop/Lessons.md")
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(body, "\n- "); n > maxMirrored {
		t.Errorf("%d bullets, cap %d", n, maxMirrored)
	}
	keeps := strings.Contains(body, "type: lesson") && strings.Contains(body, "- bulk 239")
	if !keeps || strings.Contains(body, "- lesson 001") {
		t.Errorf("the header stays and the oldest go:\n%.300s", body)
	}
}

// A mirror that already outgrew the entry limit is cut back instead of failing for ever, and a
// mirror that cannot be written says so while the lessons stay saved.
func TestAMirrorTooBigIsCutBackAndAFailureIsReported(t *testing.T) {
	work := filepath.Join(t.TempDir(), "shop")
	root := t.TempDir()
	mem := memory.Open(root)
	if err := KeepLessons(mem, work, []string{"first"}); err != nil {
		t.Fatal(err)
	}
	entry := filepath.Join(root, "shop", "Lessons.md")
	line := "- old lesson that is long enough to fill the entry quickly\n"
	big := "---\ntype: lesson\n---\n" + strings.Repeat(line, 5000)
	if err := os.WriteFile(entry, []byte(big), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := KeepLessons(mem, work, []string{"second"}); err != nil {
		t.Fatalf("an oversized mirror must be cut back: %v", err)
	}
	if body, _, err := mem.Read("shop/Lessons.md"); err != nil || !strings.Contains(body, "- second") {
		t.Fatalf("read after the cut: %v", err)
	}

	// An entry that is a folder cannot be written: reported, not swallowed, lessons kept.
	if err := os.Remove(entry); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(entry, "x"), 0o755); err != nil {
		t.Fatal(err)
	}
	err := KeepLessons(mem, work, []string{"third"})
	if !errors.Is(err, ErrMirror) {
		t.Fatalf("err = %v", err)
	}
	if got := Lessons(work); got[len(got)-1] != "third" {
		t.Errorf("the lesson must stay saved in the file: %q", got)
	}
}

// A workspace that names no project (the root) has no entry to mirror into.
func TestNothingIsMirroredWithoutAProjectName(t *testing.T) {
	mem := memory.Open(t.TempDir())
	if err := mirrorLessons(mem, string(filepath.Separator), []string{"x"}); err != nil || len(mem.List()) != 0 {
		t.Fatalf("mirrored under no name: %v %v", err, mem.List())
	}
}
