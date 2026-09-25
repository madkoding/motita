package usage

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestOpenMissingFileIsNotAnError(t *testing.T) {
	dir := t.TempDir()
	l, err := Open(filepath.Join(dir, ".usage.json"))
	if err != nil {
		t.Fatalf("a missing file must be a fresh start, not an error: %v", err)
	}
	if len(l.entries) != 0 {
		t.Fatalf("a new ledger must be empty, got %d entries", len(l.entries))
	}
}

func TestOpenEmptyPath(t *testing.T) {
	l, err := Open("")
	if err != nil {
		t.Fatalf("empty path must not error: %v", err)
	}
	if len(l.entries) != 0 {
		t.Fatalf("expected empty ledger, got %d entries", len(l.entries))
	}
	if l.Path != "" {
		t.Fatalf("expected empty path, got %q", l.Path)
	}
}

func TestOpenReadErrorOnDirectory(t *testing.T) {
	dir := t.TempDir()
	// Passing a directory path — ReadFile returns a non-IsNotExist error.
	_, err := Open(dir)
	if err == nil {
		t.Fatal("expected an error when opening a directory as a ledger file")
	}
}

func TestOpenEmptyFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".usage.json")
	if err := os.WriteFile(path, []byte{}, 0o644); err != nil {
		t.Fatal(err)
	}
	l, err := Open(path)
	if err != nil {
		t.Fatalf("empty file must be a fresh start, not an error: %v", err)
	}
	if len(l.entries) != 0 {
		t.Fatalf("expected empty ledger, got %d entries", len(l.entries))
	}
}

func TestOpenCorruptJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".usage.json")
	if err := os.WriteFile(path, []byte("{not valid json"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := Open(path)
	if err == nil {
		t.Fatal("expected an error when opening a corrupt JSON ledger")
	}
}

func TestBumpViewAndUseAccumulate(t *testing.T) {
	l := &Ledger{Now: func() time.Time { return time.Unix(1000, 0) }, entries: map[string]Entry{}}
	l.BumpView("zephyr-build")
	l.BumpView("zephyr-build")
	l.BumpUse("zephyr-build")
	e := l.Get("zephyr-build")
	if e.ViewCount != 2 || e.UseCount != 1 {
		t.Fatalf("view=2 use=1, got view=%d use=%d", e.ViewCount, e.UseCount)
	}
	if e.CreatedAt.IsZero() {
		t.Fatal("CreatedAt must be set on first bump")
	}
}

func TestBumpUseSetsCreatedAtOnNewEntry(t *testing.T) {
	l := &Ledger{Now: func() time.Time { return time.Unix(3000, 0) }, entries: map[string]Entry{}}
	l.BumpUse("fresh-skill")
	e := l.Get("fresh-skill")
	if e.UseCount != 1 {
		t.Fatalf("use count must be 1, got %d", e.UseCount)
	}
	if e.CreatedAt.IsZero() {
		t.Fatal("CreatedAt must be set on first BumpUse")
	}
	if !e.LastUsedAt.Equal(time.Unix(3000, 0)) {
		t.Fatalf("LastUsedAt must be set, got %v", e.LastUsedAt)
	}
}

func TestBumpPatchSetsCreatedByAndReactivates(t *testing.T) {
	l := &Ledger{Now: func() time.Time { return time.Unix(2000, 0) }, entries: map[string]Entry{}}
	l.SetState("my-skill", StateStale)
	l.BumpPatch("my-skill", ByAgent)
	e := l.Get("my-skill")
	if e.CreatedBy != ByAgent {
		t.Fatalf("CreatedBy must be agent, got %q", e.CreatedBy)
	}
	if e.State != StateActive {
		t.Fatalf("patching must reactivate, got state=%q", e.State)
	}
}

func TestIsCuratorManaged(t *testing.T) {
	l := &Ledger{Now: time.Now, entries: map[string]Entry{}}
	l.BumpPatch("agent-skill", ByAgent)
	l.BumpPatch("user-skill", ByForeground)
	if !l.IsCuratorManaged("agent-skill") {
		t.Error("agent-skill must be curator-managed")
	}
	if l.IsCuratorManaged("user-skill") {
		t.Error("user-skill must NOT be curator-managed")
	}
	if l.IsCuratorManaged("no-such-skill") {
		t.Error("a missing skill must not be curator-managed")
	}
}

func TestPinnedBlocksCuratorManaged(t *testing.T) {
	l := &Ledger{Now: time.Now, entries: map[string]Entry{}}
	l.BumpPatch("pinned-skill", ByAgent)
	l.SetPinned("pinned-skill", true)
	if l.IsCuratorManaged("pinned-skill") {
		t.Error("a pinned skill must not be curator-managed")
	}
}

func TestSetStateArchivedSetsTimestamp(t *testing.T) {
	ts := time.Unix(5000, 0)
	l := &Ledger{Now: func() time.Time { return ts }, entries: map[string]Entry{}}
	l.SetState("old-skill", StateArchived)
	e := l.Get("old-skill")
	if e.State != StateArchived {
		t.Fatalf("state must be archived, got %q", e.State)
	}
	if e.ArchivedAt == nil || !e.ArchivedAt.Equal(ts) {
		t.Fatalf("ArchivedAt must be %v, got %v", ts, e.ArchivedAt)
	}
}

func TestSetStateNonArchivedClearsTimestamp(t *testing.T) {
	ts := time.Unix(5000, 0)
	l := &Ledger{Now: func() time.Time { return ts }, entries: map[string]Entry{}}
	l.SetState("skill", StateArchived)
	if l.Get("skill").ArchivedAt == nil {
		t.Fatal("ArchivedAt must be set after archiving")
	}
	l.SetState("skill", StateActive)
	e := l.Get("skill")
	if e.ArchivedAt != nil {
		t.Fatalf("ArchivedAt must be cleared on un-archive, got %v", e.ArchivedAt)
	}
}

func TestSaveWritesReadableJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".usage.json")
	l, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	l.BumpPatch("test-skill", ByAgent)
	if err := l.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}
	l2, err := Open(path)
	if err != nil {
		t.Fatalf("could not read back: %v", err)
	}
	e := l2.Get("test-skill")
	if e.PatchCount != 1 || e.CreatedBy != ByAgent {
		t.Fatalf("roundtrip lost data: patch=%d by=%q", e.PatchCount, e.CreatedBy)
	}
}

func TestSaveWithoutChangesDoesNotRewrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".usage.json")
	l, _ := Open(path)
	l.BumpView("x")
	if err := l.Save(); err != nil {
		t.Fatal(err)
	}
	info, _ := os.Stat(path)
	firstMod := info.ModTime()
	// Second Save with no changes should be a no-op.
	if err := l.Save(); err != nil {
		t.Fatal(err)
	}
	info2, _ := os.Stat(path)
	if !info2.ModTime().Equal(firstMod) {
		t.Fatal("Save without changes must not rewrite the file")
	}
}

func TestSaveWithEmptyPathIsNoop(t *testing.T) {
	l := &Ledger{Now: time.Now, entries: map[string]Entry{}, Path: ""}
	l.BumpView("x") // make it dirty
	if err := l.Save(); err != nil {
		t.Fatalf("Save with empty path must be a no-op, got: %v", err)
	}
}

func TestSaveMkdirAllFails(t *testing.T) {
	dir := t.TempDir()
	// Create a file where a directory is expected.
	blocker := filepath.Join(dir, "blocker")
	if err := os.WriteFile(blocker, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Saving to a path under "blocker" — MkdirAll fails because blocker is a file.
	path := filepath.Join(blocker, "sub", ".usage.json")
	l := &Ledger{Now: time.Now, entries: map[string]Entry{}, Path: path}
	l.BumpView("x") // make it dirty
	if err := l.Save(); err == nil {
		t.Fatal("expected MkdirAll to fail")
	}
}

func TestSaveCreateTempFails(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root — chmod restrictions do not apply")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, ".usage.json")
	// Pre-create the directory and make it read-only so CreateTemp fails.
	// MkdirAll is a no-op on an existing directory, so it succeeds,
	// but CreateTemp cannot write inside a read-only directory.
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })
	l := &Ledger{Now: time.Now, entries: map[string]Entry{}, Path: path}
	l.BumpView("x") // make it dirty
	if err := l.Save(); err == nil {
		t.Fatal("expected CreateTemp to fail on a read-only directory")
	}
}

func TestSaveRenameFails(t *testing.T) {
	dir := t.TempDir()
	// Create a directory at the target path — rename of a file over a
	// directory fails on Linux with ENOTDIR/EISDIR.
	target := filepath.Join(dir, ".usage.json")
	if err := os.Mkdir(target, 0o755); err != nil {
		t.Fatal(err)
	}
	l := &Ledger{Now: time.Now, entries: map[string]Entry{}, Path: target}
	l.BumpView("x") // make it dirty
	if err := l.Save(); err == nil {
		t.Fatal("expected Rename to fail when target is a directory")
	}
}

func TestAllReturnsSnapshot(t *testing.T) {
	l := &Ledger{Now: time.Now, entries: map[string]Entry{}}
	l.BumpView("a")
	l.BumpView("b")
	snap := l.All()
	if len(snap) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(snap))
	}
	// Mutating the snapshot must not affect the ledger.
	snap["a"] = Entry{UseCount: 999}
	if l.Get("a").UseCount != 0 {
		t.Fatal("All must return a copy, not the internal map")
	}
}
