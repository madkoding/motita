package skills

// The overlay: two directories layered, front one first. The POLICY that uses it (a project,
// a workspace) lives in internal/projectskills; what is tested here is the mechanism, and in
// particular the property that makes it worth having — an unscoped library behaves EXACTLY as
// it did before the overlay existed.

import (
	"os"
	"path/filepath"
	"testing"
)

func writeDoc(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, name+".md"), []byte(body), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
}

func indexNames(t *testing.T, l *Library) []string {
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

func contains(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

// TestNoOverlayBehavesExactlyAsBefore: the degenerate case is the important one, because every
// caller that does not scope anything goes through it. Dir is the only directory consulted and
// the only one written to.
func TestNoOverlayBehavesExactlyAsBefore(t *testing.T) {
	dir := t.TempDir()
	l := New(dir)
	l.Builtins = true

	if _, err := l.Save("mine", "# Mine\n\nbody\n"); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "mine.md")); err != nil {
		t.Errorf("an unscoped save must land in Dir: %v", err)
	}
	if !contains(indexNames(t, l), "mine") {
		t.Error("an unscoped library must list what is in Dir")
	}
	// The helpers must agree with each other in this case: one layer, which is the root.
	if got := l.layers(); len(got) != 1 || got[0] != dir {
		t.Errorf("an unscoped library has one layer, got %v", got)
	}
	if l.writeDir() != dir {
		t.Errorf("an unscoped library writes to Dir, got %q", l.writeDir())
	}
	if p, s, layered := l.readDirs(); layered || p != dir || s != "" {
		t.Errorf("unscoped readDirs = (%q, %q, %v), want Dir and no second layer", p, s, layered)
	}
	if l.Root() != dir {
		t.Errorf("Root must be Dir when unscoped, got %q", l.Root())
	}
	// And nothing is labelled with an origin, because there is no second layer to be from.
	doc, err := l.Get("mine")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if doc.Origin != "" {
		t.Errorf("an unscoped read has no origin to report, got %q", doc.Origin)
	}
}

// TestTheOverlayShadowsAndFallsBack: the front layer wins where it has the document, and the
// back layer still answers where it does not.
func TestTheOverlayShadowsAndFallsBack(t *testing.T) {
	base := t.TempDir()
	front := t.TempDir()

	writeDoc(t, base, "shared-only", "# Shared only\n\nbase\n")
	writeDoc(t, base, "both", "# Both (base)\n\nbase version\n")
	writeDoc(t, front, "both", "# Both (front)\n\nfront version\n")

	l := New(base)
	l.Builtins = true
	l.Overlay = &Overlay{Primary: front, Secondary: base}

	got := indexNames(t, l)
	if !contains(got, "shared-only") || !contains(got, "both") {
		t.Errorf("the index must carry both layers, got %v", got)
	}
	if n := countName(got, "both"); n != 1 {
		t.Errorf("the shadowed document must appear once, got %d in %v", n, got)
	}

	doc, err := l.Get("both")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if doc.Body != "# Both (front)\n\nfront version\n" {
		t.Errorf("the front layer must win: %q", doc.Body)
	}
	// The path is what lookup found, and originOf names the layer from that path: both halves
	// of the pair are asserted, because reporting the wrong origin is how a project's document
	// gets presented as the shared one.
	if doc.Origin != "project" {
		t.Errorf("a document from Primary must report \"project\", got %q", doc.Origin)
	}

	other, err := l.Get("shared-only")
	if err != nil {
		t.Fatalf("Get shared-only: %v", err)
	}
	if other.Origin != "shared" {
		t.Errorf("a document from Secondary must report \"shared\", got %q", other.Origin)
	}
	// An embedded procedure belongs to neither layer: it comes from the binary.
	builtin, err := l.Get("files-and-directories")
	if err != nil {
		t.Fatalf("Get builtin: %v", err)
	}
	if builtin.Origin != "" {
		t.Errorf("a shipped procedure has no layer, got %q", builtin.Origin)
	}
}

// countName is how many times a name appears, so a duplicate is caught rather than tolerated.
func countName(list []string, want string) int {
	n := 0
	for _, s := range list {
		if s == want {
			n++
		}
	}
	return n
}

// TestTheSearchSeesBothLayers: the search is the door the model actually uses, so a document
// reachable by name and not by search is a document the model will not find.
func TestTheSearchSeesBothLayers(t *testing.T) {
	base, front := t.TempDir(), t.TempDir()
	writeDoc(t, base, "from-base", "# From base\n\nkangaroo handling\n")
	writeDoc(t, front, "from-front", "# From front\n\nplatypus handling\n")

	l := New(base)
	l.Builtins = true
	l.Overlay = &Overlay{Primary: front, Secondary: base}

	for _, q := range []string{"kangaroo", "platypus"} {
		hits, err := l.Search(q, 10)
		if err != nil {
			t.Fatalf("Search(%q): %v", q, err)
		}
		if len(hits) == 0 {
			t.Errorf("the search must reach both layers, %q found nothing", q)
		}
	}
}

// TestAPendingScopeIsNotAnError: a scope nobody has written to yet has no directory, and the
// shared shelf behind it must still be served. Reporting "the library is empty" here would be
// the fresh-install bug in a new costume.
func TestAPendingScopeIsNotAnError(t *testing.T) {
	base := t.TempDir()
	writeDoc(t, base, "shared-note", "# Shared\n\nbody\n")

	l := New(base)
	l.Builtins = true
	// A front layer that does not exist yet.
	l.Overlay = &Overlay{Primary: filepath.Join(t.TempDir(), "not-created-yet"), Secondary: base}

	got := indexNames(t, l)
	if !contains(got, "shared-note") {
		t.Errorf("a scope with no directory must not hide the shelf behind it: %v", got)
	}
	if !contains(got, "files-and-directories") {
		t.Errorf("the shipped procedures must still be offered: %v", got)
	}
}

// TestAWriteCreatesTheFrontLayerAndNotTheBackOne: saving inside a scope creates the scope's
// directory on demand and leaves the shelf alone.
func TestAWriteCreatesTheFrontLayerAndNotTheBackOne(t *testing.T) {
	base := t.TempDir()
	front := filepath.Join(t.TempDir(), "scope")

	l := New(base)
	l.Builtins = true
	l.Overlay = &Overlay{Primary: front, Secondary: base}

	saved, err := l.Save("learned", "# Learned\n\nbody\n")
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	if _, err := os.Stat(filepath.Join(front, "learned.md")); err != nil {
		t.Errorf("the write must land in the front layer: %v", err)
	}
	if _, err := os.Stat(filepath.Join(base, "learned.md")); err == nil {
		t.Error("the write must not land in the shelf behind")
	}
	if saved.Origin != "project" {
		t.Errorf("a document just written into the scope reports its layer, got %q", saved.Origin)
	}
	// And it is immediately visible in the same scope.
	if !contains(indexNames(t, l), "learned") {
		t.Error("a document saved in the scope must be offered by it")
	}
}

// TestTheArchiveIsMergedFromBothLayers: the interface draws one list, so a name archived in
// either layer is reported once rather than twice or not at all.
func TestTheArchiveIsMergedFromBothLayers(t *testing.T) {
	base, front := t.TempDir(), t.TempDir()
	writeDoc(t, base, "shared-archived", "# Shared\n\nbody\n")
	writeDoc(t, front, "front-archived", "# Front\n\nbody\n")
	// The SAME name archived in BOTH layers, which is the case the de-duplication exists for:
	// the interface draws one list, and a row with an identical twin is a row the user cannot
	// tell apart from it. It is reachable because a document can be archived in a scope after
	// the shelf already held an archived document of that name.
	inBoth := "archived-twice"
	writeDoc(t, base, inBoth, "# Base copy\n\nbody\n")
	writeDoc(t, front, inBoth, "# Front copy\n\nbody\n")

	l := New(base)
	l.Builtins = true
	l.Overlay = &Overlay{Primary: front, Secondary: base}

	// Archived through the seam, so each lands beside the document it came from.
	for _, n := range []string{"shared-archived", "front-archived"} {
		if err := l.Archive(n); err != nil {
			t.Fatalf("Archive(%s): %v", n, err)
		}
	}
	// Both copies of the duplicate name, one archive per layer, written directly: Archive
	// moves the document that is VISIBLE, so the shelf's copy is reached without the overlay.
	if err := l.Archive(inBoth); err != nil {
		t.Fatalf("Archive(%s) in scope: %v", inBoth, err)
	}
	plain := New(base)
	if err := plain.Archive(inBoth); err != nil {
		t.Fatalf("Archive(%s) on the shelf: %v", inBoth, err)
	}

	archived, err := l.Archived()
	if err != nil {
		t.Fatalf("Archived: %v", err)
	}
	for _, want := range []string{"shared-archived", "front-archived", inBoth} {
		if !contains(archived, want) {
			t.Errorf("the merged archive must list %q: %v", want, archived)
		}
		if n := countName(archived, want); n != 1 {
			t.Errorf("%q appears %d times in the merged archive, want once: %v", want, n, archived)
		}
	}
}

// TestRestoringPrefersTheFrontLayer: a document archived inside a scope comes back into that
// scope, not onto the shared shelf, which is what keeps a project's cleanup local.
func TestRestoringPrefersTheFrontLayer(t *testing.T) {
	base, front := t.TempDir(), t.TempDir()

	l := New(base)
	l.Builtins = true
	l.Overlay = &Overlay{Primary: front, Secondary: base}

	if _, err := l.Save("scoped", "# Scoped\n\nbody\n"); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if err := l.Archive("scoped"); err != nil {
		t.Fatalf("Archive: %v", err)
	}
	if err := l.Restore("scoped"); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if _, err := os.Stat(filepath.Join(front, "scoped.md")); err != nil {
		t.Errorf("restore must put the document back in the layer it came from: %v", err)
	}
	if _, err := os.Stat(filepath.Join(base, "scoped.md")); err == nil {
		t.Error("restore must not move a scoped document onto the shared shelf")
	}
}

// TestRestoreFallsThroughToTheSharedArchive: the caller knows the NAME, not which layer the
// document was archived from, so a document archived on the shelf is still restorable while a
// scope is in force.
func TestRestoreFallsThroughToTheSharedArchive(t *testing.T) {
	base, front := t.TempDir(), t.TempDir()
	writeDoc(t, base, "on-the-shelf", "# On the shelf\n\nbody\n")

	l := New(base)
	l.Builtins = true
	l.Overlay = &Overlay{Primary: front, Secondary: base}

	if err := l.Archive("on-the-shelf"); err != nil {
		t.Fatalf("Archive: %v", err)
	}
	if err := l.Restore("on-the-shelf"); err != nil {
		t.Fatalf("Restore must find it in the shared archive: %v", err)
	}
	if _, err := os.Stat(filepath.Join(base, "on-the-shelf.md")); err != nil {
		t.Errorf("it must come back to the shelf it came from: %v", err)
	}
}

// TestRestoringSomethingArchivedNowhereIsRefused: the failure has to be reported, and the
// rename is what reports it rather than a pre-flight lookup.
func TestRestoringSomethingArchivedNowhereIsRefused(t *testing.T) {
	base, front := t.TempDir(), t.TempDir()

	l := New(base)
	l.Builtins = true
	l.Overlay = &Overlay{Primary: front, Secondary: base}

	if err := l.Restore("never-existed"); err == nil {
		t.Error("restoring a document that was never archived must be refused")
	}
}

// TestDeletingAScopedDocumentLeavesTheShelfIntact, at the mechanism level: the front layer's
// copy goes and the back layer's stays, so one scope's cleanup cannot empty another's shelf.
func TestDeletingAScopedDocumentLeavesTheShelfIntact(t *testing.T) {
	base, front := t.TempDir(), t.TempDir()
	writeDoc(t, base, "note", "# Shared\n\nshared\n")
	writeDoc(t, front, "note", "# Scoped\n\nscoped\n")

	l := New(base)
	l.Builtins = true
	l.Overlay = &Overlay{Primary: front, Secondary: base}

	if err := l.Delete("note"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := os.Stat(filepath.Join(front, "note.md")); err == nil {
		t.Error("the scoped copy must be the one deleted")
	}
	if _, err := os.Stat(filepath.Join(base, "note.md")); err != nil {
		t.Errorf("the shared copy must survive: %v", err)
	}
	// And it is served again, from the shelf.
	doc, err := l.Get("note")
	if err != nil {
		t.Fatalf("Get after delete: %v", err)
	}
	if doc.Origin != "shared" {
		t.Errorf("the shelf's document is what remains, got origin %q", doc.Origin)
	}
}

// TestAnUnreadableLayerIsReported: a layer that exists and cannot be read is a real problem,
// and distinguishing it from an absent one is the reason the index reports rather than skips.
func TestAnUnreadableLayerIsReported(t *testing.T) {
	base := t.TempDir()
	// A FILE where a directory is expected: the layer exists and cannot be read.
	bad := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(bad, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	l := New(base)
	l.Builtins = true
	l.Overlay = &Overlay{Primary: bad, Secondary: base}

	if _, err := l.List(); err == nil {
		t.Error("an unreadable front layer must be reported, not passed over")
	}
}

// TestAnOriginOutsideBothLayersIsUnlabelled: originOf is asked about paths from the lookup, and
// a path that belongs to neither layer must not be labelled as one of them.
func TestAnOriginOutsideBothLayersIsUnlabelled(t *testing.T) {
	base, front := t.TempDir(), t.TempDir()
	l := New(base)
	l.Overlay = &Overlay{Primary: front, Secondary: base}

	if got := l.originOf(filepath.Join(t.TempDir(), "elsewhere.md")); got != "" {
		t.Errorf("a path in neither layer has no origin, got %q", got)
	}
	// And with no overlay at all there are no layers to be from.
	if got := New(base).originOf(filepath.Join(base, "x.md")); got != "" {
		t.Errorf("an unscoped library has no layers, got %q", got)
	}
}
