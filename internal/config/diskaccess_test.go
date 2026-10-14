package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func diskFile(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "motita.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// The workspace is the default, read from no setting at all, and only workspace or full load.
func TestDiskAccessDefaultsToTheWorkspace(t *testing.T) {
	t.Setenv("MOTITA_SANDBOX_DISK_ACCESS", "")
	cfg, err := LoadWithoutKey(diskFile(t, "sandbox:\n  kind: none\n"))
	if err != nil || cfg.Sandbox.DiskAccess != DiskWorkspace || cfg.Sandbox.FullDisk() {
		t.Fatalf("default = %q, %v", cfg.Sandbox.DiskAccess, err)
	}
	cfg, err = LoadWithoutKey(diskFile(t, "sandbox:\n  disk_access: \"\"\n"))
	if err != nil || cfg.Sandbox.DiskAccess != DiskWorkspace {
		t.Fatalf("an empty value is the workspace: %q, %v", cfg.Sandbox.DiskAccess, err)
	}
	if Default().Sandbox.FullDisk() {
		t.Error("the built-in default must be the workspace")
	}
	cfg, err = LoadWithoutKey(diskFile(t, "sandbox:\n  disk_access: \" FULL \"\n"))
	if err != nil || !cfg.Sandbox.FullDisk() {
		t.Fatalf("full = %q, %v", cfg.Sandbox.DiskAccess, err)
	}
	if _, err := LoadWithoutKey(diskFile(t, "sandbox:\n  disk_access: everything\n")); err == nil ||
		!strings.Contains(err.Error(), "sandbox.disk_access") {
		t.Errorf("an unknown value must be refused: %v", err)
	}
	t.Setenv("MOTITA_SANDBOX_DISK_ACCESS", "full")
	if cfg, _ := LoadWithoutKey(diskFile(t, "sandbox:\n  kind: none\n")); !cfg.Sandbox.FullDisk() {
		t.Error("the environment sets it too")
	}
	if !ValidDiskAccess(DiskWorkspace) || !ValidDiskAccess(DiskFull) || ValidDiskAccess("") {
		t.Error("only workspace and full are valid")
	}
}

// SetDiskAccess edits the one line in place, keeping the rest of the file and its comments.
func TestSetDiskAccessEditsInPlace(t *testing.T) {
	t.Setenv("MOTITA_SANDBOX_DISK_ACCESS", "")
	path := diskFile(t, "# mine\nsandbox:\n  kind: none # keep\n  disk_access: workspace # chosen in the settings\n")
	if err := SetDiskAccess(path, DiskFull); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(path)
	want := "# mine\nsandbox:\n  kind: none # keep\n  disk_access: \"full\" # chosen in the settings\n"
	if string(got) != want {
		t.Fatalf("got %q\nwant %q", got, want)
	}
	added := diskFile(t, "llm:\n  provider: openai\n")
	if err := SetDiskAccess(added, DiskFull); err != nil {
		t.Fatal(err)
	}
	if cfg, _ := LoadWithoutKey(added); !cfg.Sandbox.FullDisk() {
		t.Error("a missing block must be added")
	}
}

func TestSetDiskAccessRefusesWhatItCannotSave(t *testing.T) {
	t.Setenv("MOTITA_SANDBOX_DISK_ACCESS", "")
	if err := SetDiskAccess(diskFile(t, ""), "everything"); err == nil {
		t.Error("an unknown mode was written")
	}
	if err := SetDiskAccess(filepath.Join(t.TempDir(), "absent.yaml"), DiskFull); err == nil {
		t.Error("a missing file was accepted")
	}
	if err := SetDiskAccess(t.TempDir(), DiskFull); err == nil {
		t.Error("a directory was read as a file")
	}
	if err := SetDiskAccess(diskFile(t, "sandbox: {kind: none}\n"), DiskFull); err == nil ||
		!strings.Contains(err.Error(), "inline") {
		t.Errorf("an inline block: %v", err)
	}
	const broken = "sandbox:\n  kind: nonsense\n"
	path := diskFile(t, broken)
	if err := SetDiskAccess(path, DiskFull); err == nil {
		t.Error("a file that does not load was saved")
	}
	if got, _ := os.ReadFile(path); string(got) != broken {
		t.Errorf("the file must be restored: %q", got)
	}

	t.Setenv("MOTITA_SANDBOX_DISK_ACCESS", "workspace")
	path = diskFile(t, "sandbox:\n  kind: none\n")
	if err := SetDiskAccess(path, DiskFull); err == nil || !strings.Contains(err.Error(), "MOTITA_SANDBOX_DISK_ACCESS") {
		t.Errorf("an environment override must be reported: %v", err)
	}
	if got, _ := os.ReadFile(path); string(got) != "sandbox:\n  kind: none\n" {
		t.Errorf("an overridden change must be undone: %q", got)
	}
}

func TestSetDiskAccessReportsWriteFailures(t *testing.T) {
	t.Setenv("MOTITA_SANDBOX_DISK_ACCESS", "")
	old := uiCreateTemp
	t.Cleanup(func() { uiCreateTemp = old })
	uiCreateTemp = func(string, string) (*os.File, error) { return nil, errors.New("disk full") }
	if err := SetDiskAccess(diskFile(t, "sandbox:\n  kind: none\n"), DiskFull); err == nil {
		t.Error("a write that fails is an error")
	}
	calls := 0
	uiCreateTemp = func(dir, pattern string) (*os.File, error) {
		calls++
		if calls > 1 {
			return nil, errors.New("disk full")
		}
		return old(dir, pattern)
	}
	err := SetDiskAccess(diskFile(t, "sandbox:\n  kind: nonsense\n"), DiskFull)
	if err == nil || !strings.Contains(err.Error(), "could not be restored") {
		t.Errorf("err = %v", err)
	}
}
