package usage

import (
	"os"
	"path/filepath"
	"testing"
)

// layered is a project's ledger over a shared one; the project owns "mine".
func layered(t *testing.T) (scope, shared *Ledger) {
	t.Helper()
	dir := t.TempDir()
	shared, _ = Open(filepath.Join(dir, "shared.json"))
	scope, _ = Open(filepath.Join(dir, "project.json"))
	return scope.Over(shared, func(n string) bool { return n == "mine" }), shared
}

// A project's own documents are counted, pinned and switched off on the project's ledger, and the
// shared ones it reads stay on the shared ledger; neither leaks into the other.
func TestALayerKeepsItsOwnDocumentsAndRoutesTheShared(t *testing.T) {
	scope, shared := layered(t)
	for _, n := range []string{"mine", "common"} {
		scope.BumpView(n)
		scope.BumpUse(n)
		scope.BumpPatch(n, ByAgent)
		if !scope.IsCuratorManaged(n) {
			t.Errorf("%s: the agent's document is the curator's until it is pinned", n)
		}
		scope.SetState(n, StateStale)
		scope.SetPinned(n, true)
		scope.SetDisabled(n, true)
	}
	if shared.Get("mine").ViewCount != 0 || scope.entries["common"].ViewCount != 0 {
		t.Fatal("a document's telemetry went to the other ledger")
	}
	for _, n := range []string{"mine", "common"} {
		e := scope.Get(n)
		if e.ViewCount != 1 || e.UseCount != 1 || e.PatchCount != 1 || e.State != StateStale || !e.Pinned ||
			!scope.Disabled(n) || scope.IsCuratorManaged(n) {
			t.Errorf("%s: %+v", n, e)
		}
	}
	if all := scope.All(); len(all) != 2 || len(shared.All()) != 1 {
		t.Errorf("All merges both: %v", all)
	}
	if err := scope.Save(); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{scope.Path, shared.Path} {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("both ledgers are saved: %v", err)
		}
	}
	scope.Forget("common")
	scope.Forget("mine")
	if len(scope.All()) != 0 {
		t.Error("Forget routes too")
	}
	// A name the project already has an entry for stays the project's even once owns says no.
	scope.BumpView("mine")
	scope.owns = nil
	scope.BumpView("mine")
	if scope.entries["mine"].ViewCount != 2 {
		t.Error("an entry of the scope's own answers there")
	}
}

// A layer reports the first failure of the two saves.
func TestALayerReportsAFailedSave(t *testing.T) {
	scope, shared := layered(t)
	blocker := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(blocker, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	shared.Path = filepath.Join(blocker, "shared.json")
	scope.BumpView("common")
	if err := scope.Save(); err == nil {
		t.Error("the shared ledger's failure is the layer's")
	}
	scope.Path = filepath.Join(blocker, "project.json")
	scope.BumpView("mine")
	shared.BumpView("common")
	if err := scope.Save(); err == nil {
		t.Error("the layer's own failure is reported")
	}
}
