package gateway

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func newTestRepoStore(t *testing.T) *repoStore {
	t.Helper()
	rs, err := newRepoStore(filepath.Join(t.TempDir(), "repositories"))
	if err != nil {
		t.Fatal(err)
	}
	return rs
}

func TestRepoStoreKeepsOneFilePerRepository(t *testing.T) {
	rs := newTestRepoStore(t)
	older := Repository{ID: "a", Name: "older", Dir: "/x", Created: time.Unix(1, 0)}
	newer := Repository{ID: "b", Name: "newer", Dir: "/y", Created: time.Unix(2, 0)}
	for _, r := range []Repository{newer, older} {
		if err := rs.save(r); err != nil {
			t.Fatal(err)
		}
	}
	// Files that are not repositories are skipped: a folder, another extension, broken JSON.
	_ = os.Mkdir(filepath.Join(rs.dir, "folder.json"), 0o755)
	_ = os.WriteFile(filepath.Join(rs.dir, "note.txt"), []byte("x"), 0o600)
	_ = os.WriteFile(filepath.Join(rs.dir, "broken.json"), []byte("{"), 0o600)

	all, err := rs.loadAll()
	if err != nil || len(all) != 2 || all[0].ID != "a" || all[1].ID != "b" {
		t.Fatalf("oldest first: %+v %v", all, err)
	}
	got, err := rs.update("a", func(r *Repository) { r.Name = "renamed" })
	if err != nil || got.Name != "renamed" {
		t.Fatalf("update: %+v %v", got, err)
	}
	if again, _ := rs.load("a"); again == nil || again.Name != "renamed" {
		t.Errorf("the change is saved: %+v", again)
	}
	if err := rs.delete("a"); err != nil {
		t.Fatal(err)
	}
	if gone, err := rs.load("a"); gone != nil || err != nil {
		t.Errorf("a deleted repository is nil, not an error: %+v %v", gone, err)
	}
	if err := rs.delete("a"); err != nil {
		t.Errorf("deleting twice is fine: %v", err)
	}
	if _, err := rs.update("a", func(*Repository) {}); err != errRepoGone {
		t.Errorf("updating a deleted repository: %v", err)
	}
}

func TestRepoStoreRefusesWhatItCannotUse(t *testing.T) {
	rs := newTestRepoStore(t)
	blocker := filepath.Join(t.TempDir(), "file")
	_ = os.WriteFile(blocker, nil, 0o600)
	if _, err := newRepoStore(filepath.Join(blocker, "sub")); err == nil {
		t.Error("a store cannot be made under a file")
	}

	for _, id := range []string{"../x", "a/b", ""} {
		if err := rs.save(Repository{ID: id}); err == nil {
			t.Errorf("save %q", id)
		}
		if _, err := rs.load(id); err == nil {
			t.Errorf("load %q", id)
		}
		if _, err := rs.update(id, func(*Repository) {}); err == nil {
			t.Errorf("update %q", id)
		}
		if err := rs.delete(id); err == nil {
			t.Errorf("delete %q", id)
		}
	}
	if err := os.WriteFile(filepath.Join(rs.dir, "bad.json"), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := rs.load("bad"); err == nil {
		t.Error("a broken file is an error")
	}
	if _, err := rs.update("bad", func(*Repository) {}); err == nil {
		t.Error("a broken file cannot be updated")
	}
}

func TestRepoStoreReportsWhatTheDiskRefuses(t *testing.T) {
	rs := newTestRepoStore(t)
	// A folder where the file should be: it cannot be read as a file, replaced, or removed.
	dir := filepath.Join(rs.dir, "stuck.json")
	if err := os.MkdirAll(filepath.Join(dir, "inside"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := rs.load("stuck"); err == nil {
		t.Error("reading a folder")
	}
	if err := rs.save(Repository{ID: "stuck"}); err == nil {
		t.Error("replacing a folder")
	}
	if err := rs.delete("stuck"); err == nil {
		t.Error("removing a full folder")
	}
	// The temporary file cannot be written when a folder has its name.
	if err := os.Mkdir(filepath.Join(rs.dir, "tmp.json.tmp"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := rs.save(Repository{ID: "tmp"}); err == nil {
		t.Error("writing over a folder")
	}
	if _, err := rs.update("stuck", func(*Repository) {}); err == nil {
		t.Error("updating a folder")
	}
	// A saved repository whose file is replaced by one that cannot be saved over again.
	if err := rs.save(Repository{ID: "ok"}); err != nil {
		t.Fatal(err)
	}
	_ = os.Mkdir(filepath.Join(rs.dir, "ok.json.tmp"), 0o755)
	if _, err := rs.update("ok", func(r *Repository) { r.Name = "x" }); err == nil {
		t.Error("an update that cannot be saved")
	}
	_ = os.RemoveAll(rs.dir)
	if _, err := rs.loadAll(); err == nil {
		t.Error("listing a store that is gone")
	}
}
