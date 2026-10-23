package plan

import (
	"os"
	"testing"
)

// TestMain points TMPDIR at a directory of this checkout for the length of the run.
//
// t.TempDir follows TMPDIR, and a TMPDIR inside a tree the sandbox keeps private (the tools
// directory, the user's home) makes every temporary tree unreadable to a confined command: the
// tests that search or read what they just wrote fail for a reason that is not the code.
func TestMain(m *testing.M) {
	restore := tempUnderCheckout()
	code := m.Run()
	restore()
	os.Exit(code)
}

// tempUnderCheckout points TMPDIR at a directory of this checkout and returns the function that
// puts the environment back and removes it. The directory is made from the package's own
// directory, as an absolute path, so it does not depend on TMPDIR and does not move when a test
// changes the working directory.
func tempUnderCheckout() func() {
	previous, had := os.LookupEnv("TMPDIR")
	wd, err := os.Getwd()
	if err != nil {
		return func() {}
	}
	base, err := os.MkdirTemp(wd, ".testtmp-")
	if err != nil {
		return func() {}
	}
	os.Setenv("TMPDIR", base)
	return func() {
		if had {
			os.Setenv("TMPDIR", previous)
		} else {
			os.Unsetenv("TMPDIR")
		}
		os.RemoveAll(base)
	}
}
