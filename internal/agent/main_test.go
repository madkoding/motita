package agent

import (
	"fmt"
	"os"
	"testing"

	"github.com/madkoding/motita/internal/logx"
	"github.com/madkoding/motita/internal/sandbox"
)

// TestMain lets the test binary act as the sandbox's child process when it is
// invoked with the matching marker. Without this, the sandbox would re-execute
// the test binary, which would launch the whole suite again.
func TestMain(m *testing.M) {
	if sandbox.IsChildExecution(os.Args[1:]) {
		if err := sandbox.RunAsChild(os.Args[1:]); err != nil {
			fmt.Fprintf(os.Stderr, "agent[tests]: %v\n", err)
			os.Exit(126)
		}
		return
	}
	l, _ := logx.New(logx.Options{Level: logx.Error, Console: false})
	logx.Install(l)
	os.Exit(m.Run())
}
