//go:build windows

package updater

// The Windows branch of install(). It only builds on Windows, like execx_windows_test.go, and
// that is deliberate: the branch is `if u.Goos == "windows"`, so a linux host CAN execute it
// (u.Goos is a field, not the running platform) - but the code it runs does not, since the
// rename-over-a-running-binary trick and the .old dance are Windows semantics. The CI runs on
// linux only, so what this file buys is the assertion that the branch is reachable and correct
// where it actually runs; the linux-side test that pins the same branch is
// TestInstallUsesTheConfiguredPlatform.
//
// Without this file install() measures 60% on Windows and the remaining statements are the ones
// a Windows user's upgrade depends on.

import (
	"os"
	"path/filepath"
	"testing"
)

// TestInstallOnWindowsRenamesTheRunningBinaryAside: Windows cannot overwrite a running binary,
// so the old one moves to <target>.old first and the new one takes its place. If the first
// rename fails, the direct copy is the fallback rather than a failed upgrade.
func TestInstallOnWindowsRenamesTheRunningBinaryAside(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "motita.exe")
	if err := os.WriteFile(target, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	newBin := filepath.Join(dir, "downloaded.exe")
	if err := os.WriteFile(newBin, []byte("new"), 0o755); err != nil {
		t.Fatal(err)
	}

	u := &Updater{Goos: "windows", Goarch: "amd64", ExePath: target}
	if err := u.install(newBin); err != nil {
		t.Fatalf("install: %v", err)
	}

	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("the target must exist after the install: %v", err)
	}
	if string(got) != "new" {
		t.Errorf("the target holds %q, want the downloaded binary", string(got))
	}
	if _, err := os.Stat(target + ".old"); err != nil {
		t.Errorf("the previous binary must be kept as .old, not deleted: %v", err)
	}
}

// TestInstallOnWindowsCleansUpALeftoverFromAPreviousUpgrade: an interrupted upgrade leaves a
// .old behind, and the next one must not fail on it. Removing it is what makes a second upgrade
// possible at all.
func TestInstallOnWindowsCleansUpALeftoverFromAPreviousUpgrade(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "motita.exe")
	if err := os.WriteFile(target, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	// A stale .old from an upgrade that did not finish.
	if err := os.WriteFile(target+".old", []byte("stale"), 0o755); err != nil {
		t.Fatal(err)
	}
	newBin := filepath.Join(dir, "downloaded.exe")
	if err := os.WriteFile(newBin, []byte("new"), 0o755); err != nil {
		t.Fatal(err)
	}

	u := &Updater{Goos: "windows", Goarch: "amd64", ExePath: target}
	if err := u.install(newBin); err != nil {
		t.Fatalf("a leftover .old must not block an upgrade: %v", err)
	}
	got, err := os.ReadFile(target + ".old")
	if err != nil {
		t.Fatalf(".old must exist: %v", err)
	}
	if string(got) != "old" {
		t.Errorf(".old holds %q, want the binary that was just replaced", string(got))
	}
}

// TestInstallOnWindowsFallsBackToACopy: the first rename can fail - an antivirus holding the
// file, a permission the process does not have - and the upgrade must still land. copyFile is
// that fallback, and it is the difference between "the upgrade failed" and "the upgrade worked
// anyway" on the platform where the running binary cannot simply be replaced.
func TestInstallOnWindowsFallsBackToACopy(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "motita.exe")
	if err := os.WriteFile(target, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	newBin := filepath.Join(dir, "downloaded.exe")
	if err := os.WriteFile(newBin, []byte("new"), 0o755); err != nil {
		t.Fatal(err)
	}

	// A DIRECTORY at <target>.old makes the first rename fail, which is the branch under test:
	// renaming a file over a directory is not permitted on any platform.
	if err := os.Mkdir(target+".old", 0o755); err != nil {
		t.Fatal(err)
	}

	u := &Updater{Goos: "windows", Goarch: "amd64", ExePath: target}
	if err := u.install(newBin); err != nil {
		t.Fatalf("the copy fallback must complete the upgrade, got %v", err)
	}
	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("the target must exist after the fallback: %v", err)
	}
	if string(got) != "new" {
		t.Errorf("the target holds %q, want the downloaded binary", string(got))
	}
}
