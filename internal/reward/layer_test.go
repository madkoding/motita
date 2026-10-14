package reward

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

// A verdict on a turn that read a project's document and a shared one lands on each where it
// lives, with the shares taken over the whole turn; a project's complaint never reaches the shared
// document's score.
func TestALayerSplitsAVerdictByWhereEachDocumentLives(t *testing.T) {
	scope, shared := layered(t)
	if err := scope.Attribute([]string{"mine", "common"}, map[string]int{"mine": 3, "common": 1}, false,
		"wrong path in /srv/shop"); err != nil {
		t.Fatal(err)
	}
	if _, ok := shared.Scores["mine"]; ok {
		t.Fatal("the project's document was scored on the shared ledger")
	}
	if _, ok := scope.Scores["common"]; ok {
		t.Fatal("the shared document was scored on the project's ledger")
	}
	mine, _ := scope.Get("mine")
	common, ok := scope.Get("common")
	if !ok || mine.Value >= common.Value || scope.ValueOf("common") != common.Value {
		t.Errorf("the shares follow the turn: mine %+v common %+v", mine, common)
	}
	if v, ok := scope.Value("common"); !ok || v != common.Value {
		t.Error("Value routes")
	}
	if len(scope.Sorted()) != 2 || len(scope.Outstanding()) != 2 || len(shared.Sorted()) != 1 {
		t.Errorf("a layer lists both: %v", scope.Sorted())
	}
	if !scope.Addressed("common") || len(shared.Outstanding()) != 0 {
		t.Error("Addressed routes to the shared ledger")
	}
	if err := scope.Save(); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{scope.Path, shared.Path} {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("both ledgers are saved: %v", err)
		}
	}
	// A turn that only read the project's documents leaves the shared ledger alone.
	if err := scope.Attribute([]string{"mine"}, nil, true, ""); err != nil || shared.dirty {
		t.Errorf("err=%v shared dirty=%v", err, shared.dirty)
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
	_ = scope.Attribute([]string{"common"}, nil, true, "")
	if err := scope.Save(); err == nil {
		t.Error("the shared ledger's failure is the layer's")
	}
	scope.Path = filepath.Join(blocker, "project.json")
	_ = scope.Attribute([]string{"mine", "common"}, nil, true, "")
	if err := scope.Save(); err == nil {
		t.Error("the layer's own failure is reported")
	}
}

// merged of a ledger that is no layer is the ledger itself.
func TestMergedOfAPlainLedger(t *testing.T) {
	l := ledgerAt(t)
	if l.merged() != l {
		t.Error("merged")
	}
}

// See usage's TestAdoptMovesTheProjectsOldTelemetry: the same rule for scores.
func TestAdoptMovesTheProjectsOldScores(t *testing.T) {
	scope, shared := layered(t)
	_ = shared.Attribute([]string{"mine", "common"}, nil, false, "old")
	if !scope.Adopt(nil) {
		t.Fatal("the project's score moves")
	}
	if _, ok := shared.Scores["mine"]; ok {
		t.Error("the moved score left the shared ledger")
	}
	if _, ok := scope.Scores["mine"]; !ok || scope.Adopt(nil) {
		t.Error("adopted once")
	}
	if _, ok := shared.Scores["common"]; !ok {
		t.Error("a shared document's score stays")
	}
	other, _ := Open("")
	other.Over(shared, func(n string) bool { return n == "common" })
	if !other.Adopt(func(string) bool { return true }) {
		t.Error("copied")
	}
	if _, ok := shared.Scores["common"]; !ok {
		t.Error("kept shared while the shared shelf has it")
	}
	if (&Ledger{}).Adopt(nil) {
		t.Error("a ledger that is no layer adopts nothing")
	}
}
