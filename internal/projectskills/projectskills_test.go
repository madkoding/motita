package projectskills

// Characterization tests, written BEFORE the feature exists.
//
// They pin the behaviour the change is meant to keep: a shipped procedure cannot be
// shadowed by a project document it does not name, a project document wins inside its own
// project, and a session with no project sees only what the user wrote globally.
//
// RED right now, and for the right reason: the package does not exist yet.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/madkoding/motita/internal/skills"
)

// write makes a document inside a directory, creating it if needed.
func write(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	if err := os.WriteFile(filepath.Join(dir, name+".md"), []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}

// names is the index as the MODEL sees it, sorted, which is what every assertion below
// talks about: a document that is on disk but not offered is not in the answer.
func names(t *testing.T, l *skills.Library) []string {
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

func has(list []string, want string) bool {
	for _, n := range list {
		if n == want {
			return true
		}
	}
	return false
}

// scopeDir is where a project's own procedures live, mirroring projectskills.SharedDirName.
// It is spelled here independently so the test fails if the convention moves rather than
// following it silently.
func scopeDir(projectDir string) string { return filepath.Join(projectDir, ".motita", "skills") }

// TestAProjectDocumentIsOfferedInsideItsOwnProject: the whole point. A procedure written
// while working on a project belongs to that project.
func TestAProjectDocumentIsOfferedInsideItsOwnProject(t *testing.T) {
	root := t.TempDir()
	global := filepath.Join(root, "global")
	alpha := filepath.Join(root, "alpha")

	write(t, global, "shared-note", "# Shared\n\nWritten with no project open.\n")
	write(t, scopeDir(alpha), "alpha-build", "# Alpha build\n\nHow THIS project builds.\n")

	l := skills.New(global)
	l.Builtins = true
	Scope(l, alpha)

	got := names(t, l)
	if !has(got, "alpha-build") {
		t.Errorf("a session inside the project must be offered its own procedure, got %v", got)
	}
	if !has(got, "shared-note") {
		t.Errorf("a session inside the project must still see the shared shelf, got %v", got)
	}
}

// TestAnotherProjectsDocumentIsNotOffered: and this is the failure that motivated the
// change. A procedure that describes one project must not turn up while working on
// another — which is exactly what happened when a starlight procedure outranked the
// library while the user was working on a different repository.
func TestAnotherProjectsDocumentIsNotOffered(t *testing.T) {
	root := t.TempDir()
	global := filepath.Join(root, "global")
	alpha := filepath.Join(root, "alpha")
	beta := filepath.Join(root, "beta")

	write(t, scopeDir(alpha), "alpha-only", "# Alpha only\n\nA procedure about alpha.\n")
	write(t, scopeDir(beta), "beta-only", "# Beta only\n\nA procedure about beta.\n")

	l := skills.New(global)
	l.Builtins = true
	Scope(l, beta)

	got := names(t, l)
	if has(got, "alpha-only") {
		t.Errorf("alpha's procedure leaked into beta's session: %v", got)
	}
	if !has(got, "beta-only") {
		t.Errorf("beta must see its own procedure, got %v", got)
	}

	// And it must be invisible to the SEARCH as well, which is the door the model actually
	// reaches the library through: an index-only filter would leave it findable.
	hits, err := l.Search("alpha procedure", 10)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	for _, h := range hits {
		if h.Name == "alpha-only" {
			t.Errorf("the search found another project's procedure: %v", hits)
		}
	}
}

// TestAProjectDocumentWinsOverAShippedOneOfTheSameName: the user correcting a shipped
// procedure for THIS project must have the correction take effect, without touching the
// document every other project sees.
func TestAProjectDocumentWinsOverAShippedOneOfTheSameName(t *testing.T) {
	root := t.TempDir()
	global := filepath.Join(root, "global")
	alpha := filepath.Join(root, "alpha")

	mine := "# My own version\n\nDo it alpha's way.\n"
	write(t, scopeDir(alpha), "git-in-a-repository", mine)

	l := skills.New(global)
	l.Builtins = true
	Scope(l, alpha)

	got, err := l.Get("git-in-a-repository")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Body != mine {
		t.Errorf("the project's correction must win inside its project, got:\n%s", got.Body)
	}

	// Inside ANOTHER project the shipped procedure must still be the one served.
	beta := filepath.Join(root, "beta")
	l2 := skills.New(global)
	l2.Builtins = true
	Scope(l2, beta)
	got2, err := l2.Get("git-in-a-repository")
	if err != nil {
		t.Fatalf("Get in beta: %v", err)
	}
	if got2.Body == mine {
		t.Error("alpha's correction was served inside beta")
	}
	if !strings.HasPrefix(got2.Path, "builtin:") {
		t.Errorf("beta must still get the shipped procedure, got %q", got2.Path)
	}
}

// TestASessionWithNoProjectSeesOnlyTheSharedShelf: the other half of the rule. Work done
// with no project open is shared by everyone, and nothing else is.
func TestASessionWithNoProjectSeesOnlyTheSharedShelf(t *testing.T) {
	root := t.TempDir()
	global := filepath.Join(root, "global")
	alpha := filepath.Join(root, "alpha")

	write(t, global, "shared-note", "# Shared\n\nno project\n")
	write(t, scopeDir(alpha), "alpha-only", "# Alpha\n\nalpha\n")

	l := skills.New(global)
	l.Builtins = true

	got := names(t, l)
	if has(got, "alpha-only") {
		t.Errorf("a session with no project must not see a project's procedure: %v", got)
	}
	if !has(got, "shared-note") {
		t.Errorf("a session with no project sees the shared shelf, got %v", got)
	}
}

// TestSavingInsideAProjectLandsInTheProject: a procedure written down while working on a
// project must not silently become everyone's, which is the leak the rule exists to stop.
func TestSavingInsideAProjectLandsInTheProject(t *testing.T) {
	root := t.TempDir()
	global := filepath.Join(root, "global")
	alpha := filepath.Join(root, "alpha")

	l := skills.New(global)
	l.Builtins = true
	Scope(l, alpha)

	if _, err := l.Save("learned-here", "# Learned here\n\nalpha specifics\n"); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// It is in the PROJECT's directory...
	if _, err := os.Stat(filepath.Join(scopeDir(alpha), "learned-here.md")); err != nil {
		t.Errorf("the document must be written inside the project: %v", err)
	}
	// ...and NOT in the shared one.
	if _, err := os.Stat(filepath.Join(global, "learned-here.md")); err == nil {
		t.Error("a project's document was written into the shared shelf, where every project would see it")
	}

	// A session with no project must not see it.
	plain := skills.New(global)
	plain.Builtins = true
	if has(names(t, plain), "learned-here") {
		t.Error("a procedure saved inside a project leaked into the shared shelf")
	}
}
