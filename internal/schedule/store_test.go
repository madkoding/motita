package schedule

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestStoreSavesLoadsAndDeletes(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "schedules")
	st, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	s := Schedule{ID: "s1", Title: "backup", Task: "run the backup", Kind: KindTask,
		SessionID: DefaultSessionID, Every: Duration(time.Hour), Enabled: true,
		Created: time.Now().UTC().Truncate(time.Second)}
	if err := st.Save(s); err != nil {
		t.Fatalf("Save: %v", err)
	}
	// The file is one JSON per task, named by id: that is what makes a schedule
	// editable by hand and recoverable from a backup directory.
	if _, err := os.Stat(filepath.Join(dir, "s1.json")); err != nil {
		t.Fatalf("the record is not at <dir>/s1.json: %v", err)
	}

	got, err := st.Load("s1")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got == nil || got.Title != "backup" || time.Duration(got.Every) != time.Hour {
		t.Fatalf("Load returned %+v", got)
	}

	all, err := st.LoadAll()
	if err != nil {
		t.Fatalf("LoadAll: %v", err)
	}
	if len(all) != 1 {
		t.Fatalf("LoadAll returned %d records, want 1", len(all))
	}

	if err := st.Delete("s1"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if err := st.Delete("s1"); err != nil {
		t.Fatalf("deleting twice must be the outcome the caller asked for, got: %v", err)
	}
	missing, err := st.Load("s1")
	if err != nil || missing != nil {
		t.Fatalf("Load after Delete = (%+v, %v), want (nil, nil)", missing, err)
	}
}

// A file that cannot be parsed is SKIPPED by LoadAll rather than fatal: one bad
// record must not hide every good one, which is the rule the session and project
// stores already follow.
func TestLoadAllSkipsACorruptRecord(t *testing.T) {
	dir := t.TempDir()
	st, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := st.Save(Schedule{ID: "good", Title: "t", Every: Duration(time.Hour)}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "bad.json"), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	// A file that is not a record at all is skipped too (the extension is the filter).
	if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("hello"), 0o600); err != nil {
		t.Fatal(err)
	}

	all, err := st.LoadAll()
	if err != nil {
		t.Fatalf("LoadAll: %v", err)
	}
	if len(all) != 1 || all[0].ID != "good" {
		t.Fatalf("LoadAll returned %+v, want only the good record", all)
	}
}

// A single unreadable record IS an error: a caller asking about one named task and
// being told "no" would conclude the task does not exist, which is a different fact.
func TestLoadReportsACorruptRecord(t *testing.T) {
	dir := t.TempDir()
	st, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "s1.json"), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Load("s1"); err == nil {
		t.Fatal("a corrupt record was reported as absent")
	}
}

func TestOpenNeedsADirectory(t *testing.T) {
	if _, err := Open(""); err == nil {
		t.Fatal("Open(\"\") was accepted: a store with no directory would silently drop every save")
	}
}
