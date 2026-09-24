package tui

import (
	"os"
	"testing"
)

// TestMain puts every test in this package under a HOME of its own.
//
// The configuration defaults are computed from HOME, so the wizard and every runner that writes
// state would otherwise write into the home of whoever runs the suite — the tests were leaving a
// real ~/.motita/motita.yaml and a workspace behind. A test must not touch the machine it
// runs on.
func TestMain(m *testing.M) {
	home, err := os.MkdirTemp("", "motita-tui-home-")
	if err != nil {
		panic(err)
	}
	os.Setenv("HOME", home)
	// The working directory as well, for the tests that write a configuration file by a relative
	// name: the repository must not collect them.
	wd, err := os.Getwd()
	if err == nil {
		scratch, err := os.MkdirTemp("", "motita-tui-wd-")
		if err == nil {
			os.Chdir(scratch)
			defer func() {
				os.Chdir(wd)
				os.RemoveAll(scratch)
			}()
		}
	}
	code := m.Run()
	os.RemoveAll(home)
	os.Exit(code)
}
