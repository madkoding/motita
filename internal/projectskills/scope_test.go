package projectskills

// Coverage for the paths the behavioural tests above do not reach: the helpers a caller uses to
// decide WHERE a scope is, the directory it creates on the way in, and the degenerate cases a
// library with no overlay hits. The package's gate is 100% per package, so a branch nobody runs
// is a branch nobody has decided the behaviour of.

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/madkoding/motita/internal/skills"
)

// TestProjectDirPrefersTheProjectsOwnCheckout: a session runs in a git WORKTREE of its project,
// and a worktree is removed when the session ends. Anchoring the scope to the workspace would
// put every procedure learned there on a path that gets deleted, so the project's checkout has
// to win.
func TestProjectDirPrefersTheProjectsOwnCheckout(t *testing.T) {
	if got := ProjectDirFor("/proj/checkout", "/proj/worktrees/s1"); got != "/proj/checkout" {
		t.Errorf("the project's checkout must win over the session workspace, got %q", got)
	}
	// A session with no project of its own still has a workspace, and that is where its
	// procedures belong.
	if got := ProjectDirFor("", "/some/worktree"); got != "/some/worktree" {
		t.Errorf("with no project the workspace is the scope, got %q", got)
	}
	// Neither: the shared shelf only.
	if got := ProjectDirFor("", ""); got != "" {
		t.Errorf("with neither, the scope is empty, got %q", got)
	}
	// Whitespace is not a path, and treating it as one would create a directory named " ".
	if got := ProjectDirFor("   ", "\t"); got != "" {
		t.Errorf("blank input must read as no project, got %q", got)
	}
}

// TestScopeWithNoProjectClearsTheScope: the "no project" case must be the unscoped library, so
// that a session with no project behaves exactly as it did before this package existed.
func TestScopeWithNoProjectClearsTheScope(t *testing.T) {
	l := skills.New(t.TempDir())
	l.Builtins = true

	// Scoped, then unscoped: the overlay must come off rather than stay pointing somewhere.
	Scope(l, "/tmp/some-project")
	if l.Overlay == nil {
		t.Fatal("scoping a project must install an overlay")
	}
	Scope(l, "")
	if l.Overlay != nil {
		t.Error("an empty project must clear the scope, so nothing is layered")
	}

	// And a nil library is tolerated rather than panicking: several callers assert on a
	// service interface before they know there is a library behind it.
	Scope(nil, "/tmp/x")
}

// TestPrepareCreatesTheProjectsDirectory: the write path needs the directory to exist, and it
// is created on demand because a scope nobody has written to yet is a legal state.
func TestPrepareCreatesTheProjectsDirectory(t *testing.T) {
	dir := t.TempDir()
	project := filepath.Join(dir, "alpha")

	if err := Prepare(project); err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if fi, err := os.Stat(filepath.Join(project, ".motita", "skills")); err != nil || !fi.IsDir() {
		t.Fatalf("the project's procedure directory must exist: %v", err)
	}
	// Idempotent: calling it again is how a second write in the same session happens.
	if err := Prepare(project); err != nil {
		t.Errorf("Prepare must be idempotent: %v", err)
	}
	// And a blank project is a no-op rather than an error, since nothing should be created.
	if err := Prepare("  "); err != nil {
		t.Errorf("a blank project has no directory to create, got %v", err)
	}
}

// TestScopedDocumentsAreUntrustedAndNeverReachedThroughALink: a project's .motita comes with a
// clone, so its documents are marked, and a repository that ships .motita (or .motita/skills)
// as a symbolic link gets neither a read nor a write through it.
func TestScopedDocumentsAreUntrustedAndNeverReachedThroughALink(t *testing.T) {
	project := filepath.Join(t.TempDir(), "alpha")
	l := skills.New(t.TempDir())
	Scope(l, project)
	if s, err := l.Save("doc", "# Doc\n\nx\n"); err != nil || !s.Untrusted {
		t.Fatalf("Save = %+v %v, want an untrusted document", s, err)
	}

	for _, link := range []string{".motita", ".motita/skills"} {
		project := filepath.Join(t.TempDir(), "beta")
		if err := os.MkdirAll(filepath.Dir(filepath.Join(project, link)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(t.TempDir(), filepath.Join(project, link)); err != nil {
			t.Skipf("symlinks unavailable: %v", err)
		}
		if err := Prepare(project); err == nil {
			t.Errorf("Prepare through a symlinked %s must be refused", link)
		}
		l := skills.New(t.TempDir())
		Scope(l, project)
		if _, err := l.Save("doc", "# Doc\n\nx\n"); err == nil {
			t.Errorf("a save through a symlinked %s must be refused", link)
		}
	}
}

// TestScopingTwiceKeepsTheSameSharedShelf: the overlay captures the library's ROOT as the layer
// behind it. If a second call took the CURRENT directory instead, the shared shelf would become
// the project's own directory and the two would collapse into one — every project seeing every
// other project's procedures.
func TestScopingTwiceKeepsTheSameSharedShelf(t *testing.T) {
	shared := t.TempDir()
	l := skills.New(shared)
	l.Builtins = true

	Scope(l, "/tmp/alpha")
	first := l.Overlay.Secondary
	Scope(l, "/tmp/beta")
	second := l.Overlay.Secondary

	if first != shared || second != shared {
		t.Errorf("the shared layer must stay %q across scoping, got %q then %q", shared, first, second)
	}
	if l.Overlay.Primary != filepath.Join("/tmp/beta", SharedDirName) {
		t.Errorf("the second scope must win, got %q", l.Overlay.Primary)
	}
	if l.Root() != shared {
		t.Errorf("Root must stay the shared directory, got %q", l.Root())
	}
}

// TestAScopedWriteLandsInTheProjectAndAFreeWriteDoesNot: the same library, scoped and unscoped,
// to pin the one difference that decides where knowledge ends up.
func TestAScopedWriteLandsInTheProjectAndAFreeWriteDoesNot(t *testing.T) {
	shared := t.TempDir()
	project := filepath.Join(t.TempDir(), "alpha")

	l := skills.New(shared)
	l.Builtins = true
	Scope(l, project)

	if _, err := l.Save("in-project", "# In project\n\nbody\n"); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if _, err := os.Stat(filepath.Join(project, SharedDirName, "in-project.md")); err != nil {
		t.Errorf("a scoped save must land in the project: %v", err)
	}
	if _, err := os.Stat(filepath.Join(shared, "in-project.md")); err == nil {
		t.Error("a scoped save must NOT land in the shared shelf")
	}

	// Unscoped, on the same shelf: the document goes to the root, which is the behaviour every
	// caller that never scopes anything depends on.
	free := skills.New(shared)
	free.Builtins = true
	if _, err := free.Save("shared-note", "# Shared\n\nbody\n"); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if _, err := os.Stat(filepath.Join(shared, "shared-note.md")); err != nil {
		t.Errorf("an unscoped save must land in the library root: %v", err)
	}
}

// TestAProjectSeesTheSharedShelfAndItsOwn: both layers, at once, through each door — the
// index, a read by name, and a correction that only takes effect in the project that wrote it.
func TestAProjectSeesTheSharedShelfAndItsOwn(t *testing.T) {
	root := t.TempDir()
	shared := filepath.Join(root, "global")
	alpha := filepath.Join(root, "alpha")
	beta := filepath.Join(root, "beta")

	write(t, shared, "shared-note", "# Shared\n\nshared body\n")
	write(t, shared, "corrected", "# Corrected (shared)\n\nshared version\n")
	write(t, scopeDir(alpha), "corrected", "# Corrected (alpha)\n\nalpha version\n")

	l := skills.New(shared)
	l.Builtins = true
	Scope(l, alpha)

	got := names(t, l)
	for _, want := range []string{"shared-note", "corrected"} {
		if !has(got, want) {
			t.Errorf("a project must see the shared shelf, missing %q: %v", want, got)
		}
	}

	// The correction is the project's, and only there.
	doc, err := l.Get("corrected")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if doc.Body != "# Corrected (alpha)\n\nalpha version\n" {
		t.Errorf("the project's own document must win: %q", doc.Body)
	}
	if doc.Origin != "project" {
		t.Errorf("a document from the scope must report its origin, got %q", doc.Origin)
	}

	other := skills.New(shared)
	other.Builtins = true
	Scope(other, beta)
	doc2, err := other.Get("corrected")
	if err != nil {
		t.Fatalf("Get in beta: %v", err)
	}
	if doc2.Body != "# Corrected (shared)\n\nshared version\n" {
		t.Errorf("another project must see the shared version: %q", doc2.Body)
	}
	if doc2.Origin != "shared" {
		t.Errorf("a document from the shelf must report its origin, got %q", doc2.Origin)
	}
}

// TestDeletingInAProjectKeepsTheSharedCopy: a project's document is the one deleted, and the
// shelf it was shadowing survives. The reverse would let one project's cleanup empty another
// project's library.
func TestDeletingInAProjectKeepsTheSharedCopy(t *testing.T) {
	root := t.TempDir()
	shared := filepath.Join(root, "global")
	alpha := filepath.Join(root, "alpha")

	write(t, shared, "note", "# Shared\n\nshared body\n")
	write(t, scopeDir(alpha), "note", "# Alpha\n\nalpha body\n")

	l := skills.New(shared)
	l.Builtins = true
	Scope(l, alpha)

	if err := l.Delete("note"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	// The project's copy is gone, and the shared one is intact: deleting in a scope removes the
	// scope's document, not the shelf's.
	if _, err := os.Stat(filepath.Join(scopeDir(alpha), "note.md")); err == nil {
		t.Error("the project's document must be the one deleted")
	}
	if _, err := os.Stat(filepath.Join(shared, "note.md")); err != nil {
		t.Errorf("the shared document must survive a delete inside a project: %v", err)
	}
	got, err := l.Get("note")
	if err != nil {
		t.Fatalf("Get after delete: %v", err)
	}
	if got.Body != "# Shared\n\nshared body\n" {
		t.Errorf("the shared document must be served again: %q", got.Body)
	}
}

// TestArchivingInAProjectArchivesBesideIt: the archive belongs to the layer the document came
// from, so a project's archive does not hold the shelf's documents and restoring puts the
// document back where it was.
func TestArchivingInAProjectArchivesBesideIt(t *testing.T) {
	root := t.TempDir()
	shared := filepath.Join(root, "global")
	alpha := filepath.Join(root, "alpha")

	write(t, scopeDir(alpha), "alpha-note", "# Alpha\n\nbody\n")

	l := skills.New(shared)
	l.Builtins = true
	Scope(l, alpha)

	if err := l.Archive("alpha-note"); err != nil {
		t.Fatalf("Archive: %v", err)
	}
	if _, err := os.Stat(filepath.Join(scopeDir(alpha), ".archive", "alpha-note.md")); err != nil {
		t.Errorf("the archive must sit beside the document: %v", err)
	}
	if _, err := os.Stat(filepath.Join(shared, ".archive", "alpha-note.md")); err == nil {
		t.Error("a project's archive must not be written into the shared shelf")
	}

	// Archived lists it, from whichever layer holds it.
	archived, err := l.Archived()
	if err != nil {
		t.Fatalf("Archived: %v", err)
	}
	if !has(archived, "alpha-note") {
		t.Errorf("the archive must list it: %v", archived)
	}

	// And restore brings it back into the project.
	if err := l.Restore("alpha-note"); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if _, err := os.Stat(filepath.Join(scopeDir(alpha), "alpha-note.md")); err != nil {
		t.Errorf("restore must put it back in the project: %v", err)
	}
}
