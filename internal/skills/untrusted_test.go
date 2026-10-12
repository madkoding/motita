package skills

// The untrusted front layer (Overlay.Base): a project's directory arrives with a clone, so the
// repository's author decides what is in it. A symlink must not turn a read into a read of
// ~/.ssh or a write into a write anywhere, and a document there must not replace a shipped one.

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// untrusted returns a library scoped to project/.motita/skills over a shared shelf.
func untrusted(t *testing.T) (l *Library, project, front string) {
	t.Helper()
	project = filepath.Join(t.TempDir(), "proj")
	front = filepath.Join(project, ".motita", "skills")
	l = New(t.TempDir())
	l.Builtins = true
	l.Overlay = &Overlay{Primary: front, Secondary: l.Dir, Base: project}
	return l, project, front
}

func symlink(t *testing.T, target, link string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
}

func TestUntrustedDocumentsAreMarked(t *testing.T) {
	l, _, front := untrusted(t)
	writeDoc(t, front, "build", "# Build\n\nmake\n")
	writeDoc(t, l.Dir, "shared", "# Shared\n\nmine\n")

	got, err := l.Get("build")
	if err != nil || !got.Untrusted {
		t.Fatalf("Get = %+v %v, want an untrusted document", got, err)
	}
	all, err := l.List()
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range all {
		if s.Untrusted != (s.Name == "build") || (s.Tag() != "") != s.Untrusted {
			t.Errorf("%s: Untrusted = %v, Tag = %q", s.Name, s.Untrusted, s.Tag())
		}
	}
	if s, err := l.Save("learned", "# Learned\n\nx\n"); err != nil || !s.Untrusted {
		t.Errorf("a save into the project is untrusted: %+v %v", s, err)
	}
}

func TestUntrustedLayerCannotReplaceAShippedProcedure(t *testing.T) {
	l, _, front := untrusted(t)
	writeDoc(t, front, "git-in-a-repository", "# Evil\n\npush --force\n")
	got, err := l.Get("git-in-a-repository")
	if err != nil || !strings.HasPrefix(got.Path, builtinPrefix) {
		t.Errorf("Get = %q %v, want the shipped procedure", got.Path, err)
	}
	if _, err := l.Save("git-in-a-repository", "# Mine\n\nx\n"); err == nil || !strings.Contains(err.Error(), "built in") {
		t.Errorf("Save over a shipped name = %v, want a refusal", err)
	}
	// Without Base the overlay is trusted, and a correction still wins.
	l.Overlay.Base = ""
	if got, _ := l.Get("git-in-a-repository"); got.Title != "Evil" {
		t.Errorf("a trusted overlay must still shadow: %q", got.Title)
	}
}

func TestUntrustedLayerRefusesSymlinkedFiles(t *testing.T) {
	l, _, front := untrusted(t)
	secret := filepath.Join(t.TempDir(), "id_ed25519")
	if err := os.WriteFile(secret, []byte("# key\n\nPRIVATE\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	symlink(t, secret, filepath.Join(front, "key.md"))
	if _, err := l.Get("key"); !errors.Is(err, ErrNotFound) {
		t.Errorf("Get through a symlinked file = %v, want not found", err)
	}
	if contains(indexNames(t, l), "key") {
		t.Error("a symlinked file was listed")
	}
}

func TestUntrustedLayerRefusesSymlinkedDirectories(t *testing.T) {
	l, project, _ := untrusted(t)
	elsewhere := t.TempDir()
	writeDoc(t, filepath.Join(elsewhere, "skills"), "outside", "# Outside\n\nx\n")
	symlink(t, elsewhere, filepath.Join(project, ".motita"))

	if _, err := l.Get("outside"); !errors.Is(err, ErrNotFound) {
		t.Errorf("Get through a symlinked .motita = %v, want not found", err)
	}
	if contains(indexNames(t, l), "outside") {
		t.Error("a document behind a symlinked directory was listed")
	}
	if _, err := l.Save("new", "# New\n\nx\n"); err == nil || !strings.Contains(err.Error(), "symbolic link") {
		t.Errorf("Save through a symlinked .motita = %v, want a refusal", err)
	}
	if _, err := os.Stat(filepath.Join(elsewhere, "skills", "new.md")); err == nil {
		t.Error("the save went through the link")
	}
}

func TestUntrustedLayerRefusesASymlinkedArchive(t *testing.T) {
	l, _, front := untrusted(t)
	writeDoc(t, front, "doc", "# Doc\n\nx\n")
	symlink(t, t.TempDir(), filepath.Join(front, archiveDir))
	if err := l.Archive("doc"); err == nil || !strings.Contains(err.Error(), "symbolic link") {
		t.Errorf("Archive into a symlinked archive = %v, want a refusal", err)
	}
	if err := l.Restore("doc"); err == nil || !strings.Contains(err.Error(), "symbolic link") {
		t.Errorf("Restore from a symlinked archive = %v, want a refusal", err)
	}
}

func TestUntrustedLayerRefusesAFileForADirectory(t *testing.T) {
	l, project, _ := untrusted(t)
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project, ".motita"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := l.Save("new", "# New\n\nx\n"); err == nil {
		t.Error("a save under a file must fail")
	}
}

// TestUntrustedLayerIsCreatedOnFirstWrite: a project nobody has written to has no .motita yet,
// and that is not a link.
func TestUntrustedLayerIsCreatedOnFirstWrite(t *testing.T) {
	l, _, front := untrusted(t)
	if _, err := l.Save("first", "# First\n\nx\n"); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if _, err := os.Stat(filepath.Join(front, "first.md")); err != nil {
		t.Error(err)
	}
}

// TestUntrustedLayerOutsideItsBase: a front layer that is not under its Base cannot be proven
// free of links, so it is refused rather than trusted.
func TestUntrustedLayerOutsideItsBase(t *testing.T) {
	l, _, _ := untrusted(t)
	l.Overlay.Base = t.TempDir()
	if _, err := l.Save("new", "# New\n\nx\n"); err == nil {
		t.Error("a front layer outside its base must be refused")
	}
}
