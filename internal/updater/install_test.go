package updater

// The install() tests that build on ANY platform. The windows-tagged counterpart in
// install_windows_test.go exercises the same branch where it actually runs.

import (
	"os"
	"path/filepath"
	"testing"
)

// TestInstallReplacesTheBinaryAtomically: on linux and macOS the running binary can be
// overwritten by a rename, which is atomic - a reader either sees the old file or the new one
// and never a half-written one.
func TestInstallReplacesTheBinaryAtomically(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "motita")
	newBin := filepath.Join(dir, "downloaded")
	if err := os.WriteFile(newBin, []byte("new"), 0o644); err != nil {
		t.Fatal(err)
	}

	u := &Updater{Goos: "linux", Goarch: "amd64", ExePath: target}
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
	// The installed binary must be executable: an upgrade that lands a non-runnable file is
	// an upgrade that bricks the install.
	info, err := os.Stat(target)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0o111 == 0 {
		t.Errorf("the installed binary is not executable: %v", info.Mode().Perm())
	}
}

// TestInstallUsesTheConfiguredPlatformNotTheRunningOne: install() decides on u.Goos. A linux
// host with Goos set to windows must take the Windows path (rename the old aside), which is
// what makes that branch reachable from the CI's ubuntu runner at all - and what catches the
// bug where the type carries a Goos field that only some of its methods believe.
func TestInstallUsesTheConfiguredPlatformNotTheRunningOne(t *testing.T) {
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
	got, err := os.ReadFile(target + ".old")
	if err != nil {
		t.Fatalf("the Windows path must keep the replaced binary as .old: %v", err)
	}
	if string(got) != "old" {
		t.Errorf(".old holds %q, want the binary that was just replaced", string(got))
	}
}

// TestInstallFallsBackToACopyWhenTheFirstRenameFails: the Windows path renames the replaced
// binary aside first, and that rename can fail - an antivirus holding the file, a permission
// the process does not have. copyFile is the fallback, and the difference between "the upgrade
// failed" and "the upgrade worked anyway". It is reachable on any platform: renaming a file
// over a non-empty directory is not permitted anywhere.
func TestInstallFallsBackToACopyWhenTheFirstRenameFails(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "motita.exe")
	if err := os.WriteFile(target, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	newBin := filepath.Join(dir, "downloaded.exe")
	if err := os.WriteFile(newBin, []byte("new"), 0o755); err != nil {
		t.Fatal(err)
	}

	// A non-empty directory at <target>.old: os.Remove cannot clear it and the rename over it
	// fails, so the install must take the copy branch rather than give up.
	if err := os.Mkdir(target+".old", 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(target+".old", "keep"), []byte("x"), 0o644); err != nil {
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

// TestInstallWithoutAnExecutablePathIsReported: the caller has to have said where the binary
// is. Guessing - or replacing something else - is worse than refusing.
func TestInstallWithoutAnExecutablePathIsReported(t *testing.T) {
	u := &Updater{Goos: "linux", Goarch: "amd64"}
	err := u.install(filepath.Join(t.TempDir(), "downloaded"))
	if err == nil {
		t.Fatal("install must refuse when it does not know what to replace")
	}
}

// TestInstallWithANameButNoDirectory: a relative ExePath (just "motita", no directory part) is
// what a user gets when the binary is on the PATH and resolved by name. filepath.Dir returns
// "." for it, and the install must land next to the working directory rather than fail or
// scatter the file somewhere else.
func TestInstallWithANameButNoDirectory(t *testing.T) {
	dir := t.TempDir()
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(cwd) })

	if err := os.WriteFile("motita", []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile("downloaded", []byte("new"), 0o755); err != nil {
		t.Fatal(err)
	}

	u := &Updater{Goos: "linux", Goarch: "amd64", ExePath: "motita"}
	if err := u.install("downloaded"); err != nil {
		t.Fatalf("install: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(dir, "motita"))
	if err != nil {
		t.Fatalf("the target must exist after the install: %v", err)
	}
	if string(got) != "new" {
		t.Errorf("the target holds %q, want the downloaded binary", string(got))
	}
}
