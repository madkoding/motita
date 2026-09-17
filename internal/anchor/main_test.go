package anchor

import (
	"fmt"
	"os"
	"testing"

	"github.com/madkoding/starlight/internal/sandbox"
)

// TestMain lets the test binary act as the sandbox's child process when it is
// invoked with the matching marker. Without this, a check that runs through a
// sandbox would re-execute the test binary, which would launch the whole suite
// again (and end up timing out).
func TestMain(m *testing.M) {
	if sandbox.IsChildExecution(os.Args[1:]) {
		if err := sandbox.RunAsChild(os.Args[1:]); err != nil {
			fmt.Fprintf(os.Stderr, "anchor[tests]: %v\n", err)
			os.Exit(126)
		}
		return
	}
	os.Exit(m.Run())
}
