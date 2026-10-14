package skills

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// InScope tells the scope's own documents, archived ones included, from the shared ones.
func TestInScopeIsTheFrontLayerAndItsArchive(t *testing.T) {
	l, _, front := untrusted(t)
	writeDoc(t, front, "build", "# Build\n\nmake\n")
	writeDoc(t, filepath.Join(front, archiveDir), "old", "# Old\n\nx\n")
	writeDoc(t, l.Dir, "common", "# Common\n\nx\n")
	for name, want := range map[string]bool{"build": true, "old": true, "common": false, "": false, "../x": false} {
		if l.InScope(name) != want {
			t.Errorf("%q: want %v", name, want)
		}
	}
	if New(t.TempDir()).InScope("build") {
		t.Error("a library with no scope has nothing in one")
	}
}

// A reader layered over a project - its proposals in front, the project's documents, then the
// shared shelf - finds the shared documents too, and calls them shared.
func TestTheFallbackLayerIsReadAndShared(t *testing.T) {
	l, project, front := untrusted(t)
	shared := l.Dir
	writeDoc(t, shared, "common", "# Common\n\nx\n")
	writeDoc(t, front, "build", "# Build\n\nmake\n")
	r := New(shared)
	r.Overlay = &Overlay{Primary: filepath.Join(front, ProposedDir), Secondary: front, Fallback: shared, Base: project}
	got, err := r.Get("common")
	if err != nil || r.originOf(filepath.Join(shared, "common.md")) != "shared" {
		t.Fatalf("Get = %+v %v", got, err)
	}
	all, err := r.List()
	if err != nil || len(all) < 2 {
		t.Errorf("List = %v %v", all, err)
	}
	if _, err := r.Get("missing"); !errors.Is(err, ErrNotFound) {
		t.Errorf("a name in no layer is not found: %v", err)
	}
}

// A scoped library keeps its proposals in the project and accepts them there; a proposals folder
// the repository ships as a link holds none.
func TestProposalsOfAScopeLiveInTheProject(t *testing.T) {
	l, project, front := untrusted(t)
	writeDoc(t, filepath.Join(front, ProposedDir), "deploy", "# Deploy\n\nx\n")
	names, err := l.Proposed()
	if err != nil || len(names) != 1 || names[0] != "deploy" {
		t.Fatalf("Proposed = %v %v", names, err)
	}
	if err := l.AcceptProposal("deploy"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(front, "deploy.md")); err != nil {
		t.Error("an accepted proposal lands on the project's shelf")
	}

	elsewhere := t.TempDir()
	writeDoc(t, elsewhere, "evil", "# Evil\n\nx\n")
	if err := os.RemoveAll(filepath.Join(front, ProposedDir)); err != nil {
		t.Fatal(err)
	}
	symlink(t, elsewhere, filepath.Join(front, ProposedDir))
	if names, err := l.Proposed(); err != nil || len(names) != 0 {
		t.Errorf("a linked proposals folder lists nothing: %v %v", names, err)
	}
	if _, err := l.Proposal("evil"); !errors.Is(err, ErrNotFound) {
		t.Errorf("a linked proposal is not one: %v", err)
	}
	_ = project
}
