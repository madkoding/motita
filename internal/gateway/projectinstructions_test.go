package gateway

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func putFile(t *testing.T, path, text string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestInstructionsNoteIsEmptyWithoutFiles(t *testing.T) {
	if got := projectInstructionsNote(t.TempDir(), t.TempDir()); got != "" {
		t.Fatalf("got %q, want empty", got)
	}
	if got := projectInstructionsNote("", ""); got != "" {
		t.Fatalf("got %q, want empty", got)
	}
}

func TestInstructionsPreferAgentsOverClaude(t *testing.T) {
	ws := t.TempDir()
	putFile(t, filepath.Join(ws, "AGENTS.md"), "use tabs")
	putFile(t, filepath.Join(ws, "CLAUDE.md"), "use spaces")
	got := projectInstructionsNote(ws, "")
	if !strings.Contains(got, "use tabs") || strings.Contains(got, "use spaces") {
		t.Fatalf("AGENTS.md must win over CLAUDE.md: %q", got)
	}
}

func TestInstructionsFallBackToClaude(t *testing.T) {
	ws := t.TempDir()
	putFile(t, filepath.Join(ws, "CLAUDE.md"), "run make check")
	if got := projectInstructionsNote(ws, ""); !strings.Contains(got, "run make check") {
		t.Fatalf("CLAUDE.md not read: %q", got)
	}
}

func TestInstructionsWalkUpToGitRootOnly(t *testing.T) {
	outer := t.TempDir()
	putFile(t, filepath.Join(outer, "AGENTS.md"), "OUTSIDE")
	root := filepath.Join(outer, "repo")
	putFile(t, filepath.Join(root, ".git", "HEAD"), "ref")
	putFile(t, filepath.Join(root, "AGENTS.md"), "ROOT")
	sub := filepath.Join(root, "pkg", "api")
	putFile(t, filepath.Join(sub, "AGENTS.md"), "SUB")
	got := projectInstructionsNote(sub, "")
	if strings.Contains(got, "OUTSIDE") {
		t.Fatalf("read above the git root: %q", got)
	}
	r, s := strings.Index(got, "ROOT"), strings.Index(got, "SUB")
	if r < 0 || s < 0 || r > s {
		t.Fatalf("want ROOT before SUB (closest last): %q", got)
	}
}

func TestInstructionsWithoutGitReadOnlyTheWorkspace(t *testing.T) {
	outer := t.TempDir()
	putFile(t, filepath.Join(outer, "AGENTS.md"), "PARENT")
	ws := filepath.Join(outer, "ws")
	putFile(t, filepath.Join(ws, "AGENTS.md"), "HERE")
	got := projectInstructionsNote(ws, "")
	if strings.Contains(got, "PARENT") || !strings.Contains(got, "HERE") {
		t.Fatalf("got %q", got)
	}
}

func TestInstructionsGlobalComesFirst(t *testing.T) {
	ws, home := t.TempDir(), t.TempDir()
	putFile(t, filepath.Join(home, ".motita", "AGENTS.md"), "GLOBAL")
	putFile(t, filepath.Join(ws, "AGENTS.md"), "LOCAL")
	got := projectInstructionsNote(ws, home)
	if g, l := strings.Index(got, "GLOBAL"), strings.Index(got, "LOCAL"); g < 0 || l < 0 || g > l {
		t.Fatalf("global must precede the project's: %q", got)
	}
}

func TestInstructionsIgnoreSymlinks(t *testing.T) {
	ws := t.TempDir()
	secret := filepath.Join(t.TempDir(), "secret")
	putFile(t, secret, "TOPSECRET")
	if err := os.Symlink(secret, filepath.Join(ws, "AGENTS.md")); err != nil {
		t.Skip("symlinks unavailable:", err)
	}
	if got := projectInstructionsNote(ws, ""); got != "" {
		t.Fatalf("followed a symlink: %q", got)
	}
}

func TestInstructionsAreCapped(t *testing.T) {
	ws := t.TempDir()
	putFile(t, filepath.Join(ws, "AGENTS.md"), strings.Repeat("x", 3*maxInstructionBytes))
	got := projectInstructionsNote(ws, "")
	if len(got) > maxInstructionBytes+1024 {
		t.Fatalf("note is %d bytes, cap is %d", len(got), maxInstructionBytes)
	}
}

func TestInstructionsSkipEmptyAndUnreadableFiles(t *testing.T) {
	ws := t.TempDir()
	putFile(t, filepath.Join(ws, "AGENTS.md"), "  \n")
	if got := projectInstructionsNote(ws, ""); got != "" {
		t.Fatalf("an empty file produced %q", got)
	}
	if os.Getuid() == 0 {
		t.Skip("root reads any file")
	}
	locked := filepath.Join(ws, "AGENTS.md")
	putFile(t, locked, "secret")
	if err := os.Chmod(locked, 0); err != nil {
		t.Fatal(err)
	}
	if got := projectInstructionsNote(ws, ""); got != "" {
		t.Fatalf("an unreadable file produced %q", got)
	}
}

func TestInstructionsBudgetIsSharedAcrossFiles(t *testing.T) {
	ws, home := t.TempDir(), t.TempDir()
	putFile(t, filepath.Join(home, ".motita", "AGENTS.md"), strings.Repeat("g", maxInstructionBytes))
	putFile(t, filepath.Join(ws, "AGENTS.md"), "LOCAL")
	if got := projectInstructionsNote(ws, home); strings.Contains(got, "LOCAL") {
		t.Fatalf("the budget was spent, yet the project's file was added: %d bytes", len(got))
	}
}
