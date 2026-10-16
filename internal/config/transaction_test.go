package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A failed transaction puts the file back as it was, mode included, and a transaction on a file
// that is not there is refused.
func TestTransactionRestoresTheFileWhenItFails(t *testing.T) {
	path := filepath.Join(t.TempDir(), "motita.yaml")
	if err := os.WriteFile(path, []byte("sandbox:\n  kind: none\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	boom := errors.New("boom")
	err := Transaction(path, func(Tx) error {
		return errors.Join(os.WriteFile(path, []byte("changed"), 0o600), boom)
	})
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v", err)
	}
	got, _ := os.ReadFile(path)
	info, _ := os.Stat(path)
	if string(got) != "sandbox:\n  kind: none\n" || info.Mode().Perm() != 0o640 {
		t.Errorf("restored %q with mode %v", got, info.Mode().Perm())
	}
	if err := Transaction(path, func(Tx) error { return nil }); err != nil {
		t.Errorf("a transaction that succeeds is not an error: %v", err)
	}
	if err := Transaction(filepath.Join(t.TempDir(), "gone.yaml"), func(Tx) error { return nil }); err == nil {
		t.Error("a missing file was held")
	}
	if err := Transaction(t.TempDir(), func(Tx) error { return nil }); err == nil {
		t.Error("a directory was held")
	}
}

// A file that cannot be put back is said so.
func TestTransactionReportsAFileItCannotRestore(t *testing.T) {
	path := filepath.Join(t.TempDir(), "motita.yaml")
	if err := os.WriteFile(path, []byte("sandbox:\n  kind: none\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	old := uiCreateTemp
	t.Cleanup(func() { uiCreateTemp = old })
	uiCreateTemp = func(string, string) (*os.File, error) { return nil, errors.New("disk full") }
	err := Transaction(path, func(Tx) error { return errors.New("boom") })
	if err == nil || !strings.Contains(err.Error(), "could not be restored") {
		t.Errorf("err = %v", err)
	}
}

// Another setter's edit made while a transaction is open waits for it and is not erased when the
// transaction fails: the restore happens before the other setter reads the file.
func TestTransactionKeepsAConcurrentEditOutOfItsUndo(t *testing.T) {
	t.Setenv("MOTITA_SANDBOX_DISK_ACCESS", "")
	path := diskFile(t, "sandbox:\n  kind: none\n")
	done := make(chan error, 1)
	err := Transaction(path, func(tx Tx) error {
		if err := tx.SetDiskAccess(DiskFull); err != nil {
			return err
		}
		go func() { done <- SetUILanguage(path, "es") }()
		time.Sleep(50 * time.Millisecond) // the other setter is waiting on the lock
		return errors.New("a later key failed")
	})
	if err == nil {
		t.Fatal("the transaction should have failed")
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(path)
	if !strings.Contains(string(got), "language: es") || strings.Contains(string(got), "disk_access") {
		t.Errorf("the other edit must stay and the transaction's must go: %q", got)
	}
}
