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
func TestMain(m *testing.M) {
	if sandbox.IsChildExecution(os.Args[1:]) {
		if err := sandbox.RunAsChild(os.Args[1:]); err != nil {
			fmt.Fprintf(os.Stderr, "app[tests]: %v\n", err)
			os.Exit(126)
		}
		return
	}
	os.Exit(m.Run())
}
