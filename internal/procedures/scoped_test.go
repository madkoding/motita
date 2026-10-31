package procedures

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/madkoding/motita/internal/projectskills"
	"github.com/madkoding/motita/internal/skills"
)

// A project's store layers its documents and its own ledger over the shared ones: a switch-off of
// the project's document stays in the project, the shared documents keep their one record, and
// two sessions of one project share one ledger.
func TestAScopedStoreKeepsTheProjectsTelemetryApart(t *testing.T) {
	shared := Open(cfgAt(t.TempDir()), quiet(t))
	project := t.TempDir()
	front := filepath.Join(project, projectskills.SharedDirName)
	if err := os.MkdirAll(front, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(front, "build.md"), []byte("# Build\n\nmake\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	st := Scoped(shared, project)
	if st.Usage == shared.Usage || st.Library == shared.Library {
		t.Fatal("a scoped store has its own library and ledgers")
	}
	if again := Scoped(shared, project); again.Usage != st.Usage {
		t.Error("two sessions of one project share its ledgers")
	}
	st.Usage.SetDisabled("build", true)
	st.Usage.SetDisabled("common", true)
	if shared.Usage.Disabled("build") || !shared.Usage.Disabled("common") {
		t.Error("the project's switch-off stays in the project, the shared one is shared")
	}
	if !st.Library.Hidden("build") {
		t.Error("the scoped library asks the project's ledger what is off")
	}
	if err := st.Usage.Save(); err != nil {
		t.Fatal(err)
	}
	ledger := filepath.Join(shared.Library.Root(), ProjectsDir, projectskills.Key(project), ".usage.json")
	if _, err := os.Stat(ledger); err != nil {
		t.Errorf("the project's ledger is kept in motita's state: %v", err)
	}
	if other := Scoped(shared, t.TempDir()); other.Usage.Disabled("build") {
		t.Error("another project does not see this one's switch-off")
	}
}

// No project is the shared store; a store with no library or no ledgers is scoped as far as it
// goes; a project ledger that cannot be read leaves the project on the shared one.
func TestScopedDegradesLikeOpen(t *testing.T) {
	shared := Open(cfgAt(t.TempDir()), quiet(t))
	if st := Scoped(shared, " "); st.Usage != shared.Usage {
		t.Error("no project is the shared ledgers")
	}
	if st := Scoped(&Store{}, t.TempDir()); st.Library != nil {
		t.Error("no library stays no library")
	}
	bare := &Store{Library: shared.Library}
	if st := Scoped(bare, t.TempDir()); st.Usage != nil {
		t.Error("a ledger the shared store lacks is not made up for the project")
	}
	project := t.TempDir()
	dir := filepath.Join(shared.Library.Root(), ProjectsDir, projectskills.Key(project))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".usage.json"), []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if st := Scoped(shared, project); st.Usage != shared.Usage {
		t.Error("an unreadable project ledger leaves the project on the shared one")
	}
}

// Opening a project's ledgers moves what the shared ones recorded about its documents, and keeps
// what the shared shelf still has, live or archived.
func TestAProjectAdoptsItsOldTelemetry(t *testing.T) {
	shared := Open(cfgAt(t.TempDir()), quiet(t))
	project := t.TempDir()
	front := filepath.Join(project, projectskills.SharedDirName)
	for _, dir := range []string{front, filepath.Join(shared.Library.Root(), skills.ArchiveDir)} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, p := range []string{filepath.Join(front, "build.md"), filepath.Join(front, "both.md"),
		filepath.Join(shared.Library.Root(), skills.ArchiveDir, "both.md")} {
		if err := os.WriteFile(p, []byte("# Doc\n\nx\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	shared.Usage.SetPinned("build", true)
	shared.Usage.SetPinned("both", true)
	st := Scoped(shared, project)
	if !st.Usage.Get("build").Pinned || shared.Usage.Get("build").Pinned {
		t.Error("the project's pin moved to the project")
	}
	if !shared.Usage.Get("both").Pinned || !st.Usage.Get("both").Pinned {
		t.Error("a name the shared shelf still has stays shared and is copied")
	}
}
