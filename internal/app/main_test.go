package app

import (
	"fmt"
	"os"
	"testing"

	"github.com/madkoding/starlight/internal/sandbox"
)

// TestMain lets the test binary act as the sandbox's child process when it is
// invoked with the matching marker (the sandbox re-executes "its own
// executable", which in tests is this binary).
//
// For the suite itself it also installs a HOME of its own: the configuration defaults are
// computed from HOME, so a test that runs the real flow would otherwise write into the home of
// whoever runs the suite.
func TestMain(m *testing.M) {
	if sandbox.IsChildExecution(os.Args[1:]) {
		if err := sandbox.RunAsChild(os.Args[1:]); err != nil {
			fmt.Fprintf(os.Stderr, "app[tests]: %v\n", err)
			os.Exit(126)
		}
		return
	}
	// A home of its own for the suite. Installed here rather than per test because the defaults
	// are computed from HOME and every test that runs the real flow would otherwise write into
	// the home of whoever runs the suite.
	home, err := os.MkdirTemp("", "starlight-app-home-")
	if err != nil {
		fmt.Fprintf(os.Stderr, "app[tests]: could not create a home: %v\n", err)
		os.Exit(1)
	}
	os.Setenv("HOME", home)
	code := m.Run()
	os.RemoveAll(home)
	os.Exit(code)
}
