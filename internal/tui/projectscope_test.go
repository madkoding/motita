package tui

// The project scope on the runner: a session inside a project gets the procedures of THAT
// project, and the shared shelf keeps working for everyone else.
//
// Why the tests live here rather than only in internal/projectskills: the scoping is only real
// if it survives the wiring. The gateway names a session's project when the session is created
// or restored — which can happen BEFORE any turn has run — so the two orders (scope then store,
// store then scope) are both reachable and both have to end in the same place.

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/madkoding/motita/internal/config"
	"github.com/madkoding/motita/internal/procedures"
	"github.com/madkoding/motita/internal/skills"
)

// scopedRunner builds a runner whose library is rooted at a temporary shared directory.
func scopedRunner(t *testing.T) (*AppRunner, string) {
	t.Helper()
	shared := t.TempDir()
	cfg := config.Default()
	cfg.Skills.Dir = shared
	r := NewAppRunner(os.Stdout, os.Stderr, cfg, nil, nil, nil)
	return r, shared
}

// namesOf is the index as the MODEL sees it.
func namesOf(t *testing.T, l *skills.Library) []string {
	t.Helper()
	all, err := l.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	out := make([]string, 0, len(all))
	for _, s := range all {
		out = append(out, s.Name)
	}
	return out
}

func hasName(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

// writeProc puts a document where a project's own procedures live.
func writeProc(t *testing.T, projectDir, name, body string) {
	t.Helper()
	dir := filepath.Join(projectDir, ".motita", "skills")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, name+".md"), []byte(body), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
}

// TestAScopedRunnerSeesTheProjectsProcedures: the scope applied AFTER the library exists, which
// is the ordinary path once a session has run a turn.
func TestAScopedRunnerSeesTheProjectsProcedures(t *testing.T) {
	r, _ := scopedRunner(t)
	project := t.TempDir()
	writeProc(t, project, "alpha-only", "# Alpha only\n\nbody\n")

	// Force the store to exist first, then scope it: this is the "library already built" path.
	_ = r.library()
	r.SetProjectScope(project)

	got := namesOf(t, r.library())
	if !hasName(got, "alpha-only") {
		t.Errorf("a scoped runner must see its project's procedures: %v", got)
	}
	if !hasName(got, "files-and-directories") {
		t.Errorf("the shipped procedures must still be offered: %v", got)
	}
}

// TestAScopeAppliedBeforeTheLibraryExistsIsNotForgotten: the gateway names a session's project
// when the session is created, which is before anything has run. A library built afterwards must
// still come up scoped, or the order the two happened in would silently decide the behaviour.
func TestAScopeAppliedBeforeTheLibraryExistsIsNotForgotten(t *testing.T) {
	r, _ := scopedRunner(t)
	project := t.TempDir()
	writeProc(t, project, "early-only", "# Early only\n\nbody\n")

	// Scope FIRST: no store exists yet.
	r.SetProjectScope(project)
	// Then let the library be built, which is what a first turn does.
	got := namesOf(t, r.library())

	if !hasName(got, "early-only") {
		t.Errorf("a scope set before the library existed must be applied when it is built: %v", got)
	}
}

// TestClearingTheScopeRestoresTheSharedShelf: a session moved out of a project (or one that
// never had one) must get the shared shelf back, not a stale scope.
func TestClearingTheScopeRestoresTheSharedShelf(t *testing.T) {
	r, shared := scopedRunner(t)
	project := t.TempDir()
	writeProc(t, project, "alpha-only", "# Alpha only\n\nbody\n")
	if err := os.WriteFile(filepath.Join(shared, "shared-note.md"), []byte("# Shared\n\nbody\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	r.SetProjectScope(project)
	if !hasName(namesOf(t, r.library()), "alpha-only") {
		t.Fatal("the project's procedure must be visible while scoped")
	}

	r.SetProjectScope("")
	got := namesOf(t, r.library())
	if hasName(got, "alpha-only") {
		t.Errorf("clearing the scope must drop the project's procedures: %v", got)
	}
	if !hasName(got, "shared-note") {
		t.Errorf("the shared shelf must come back: %v", got)
	}
}

// TestScopingOneRunnerDoesNotScopeAnother: the library is shared between conversations (one
// ledger, one truth), so scoping must COPY it. Mutating it in place would scope every other
// session in the process to whichever project was set last — a bug that only shows up once two
// projects are open at once, which is exactly when it is hardest to explain.
func TestScopingOneRunnerDoesNotScopeAnother(t *testing.T) {
	shared := t.TempDir()
	cfg := config.Default()
	cfg.Skills.Dir = shared

	store := procedures.Open(cfg, nil)
	a := NewAppRunner(os.Stdout, os.Stderr, cfg, nil, nil, nil)
	b := NewAppRunner(os.Stdout, os.Stderr, cfg, nil, nil, nil)
	a.UseStore(store)
	b.UseStore(store)

	alpha := t.TempDir()
	writeProc(t, alpha, "alpha-only", "# Alpha only\n\nbody\n")

	a.SetProjectScope(alpha)

	// B never got a scope, so it must not see alpha's document.
	if hasName(namesOf(t, b.library()), "alpha-only") {
		t.Error("scoping one runner leaked into another: the shared library was mutated instead of copied")
	}
	// And the shared store itself must be untouched, since it is the one the ledger lives in.
	if hasName(namesOf(t, store.Library), "alpha-only") {
		t.Error("scoping a runner mutated the shared library")
	}
	if !hasName(namesOf(t, a.library()), "alpha-only") {
		t.Error("the scoped runner lost its own scope")
	}
}
