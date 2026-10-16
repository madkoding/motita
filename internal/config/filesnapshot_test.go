package config

import (
	"os"
	"path/filepath"
	"testing"
)

// A snapshot puts the file back as it was, mode included, and refuses a file that is not there.
func TestFileSnapshotRestoresTheFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "motita.yaml")
	if err := os.WriteFile(path, []byte("sandbox:\n  kind: none\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	snap, err := SnapshotFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("changed"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := snap.Restore(); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(path)
	info, _ := os.Stat(path)
	if string(got) != "sandbox:\n  kind: none\n" || info.Mode().Perm() != 0o640 {
		t.Errorf("restored %q with mode %v", got, info.Mode().Perm())
	}
	if _, err := SnapshotFile(filepath.Join(t.TempDir(), "gone.yaml")); err == nil {
		t.Error("a missing file was snapshotted")
	}
	if _, err := SnapshotFile(t.TempDir()); err == nil {
		t.Error("a directory was snapshotted")
	}
}
