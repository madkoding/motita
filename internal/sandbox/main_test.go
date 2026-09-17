package sandbox

import (
	"fmt"
	"os"
	"testing"

	"github.com/madkoding/starlight/internal/logx"
)

// TestMain makes the test binary behave like the real binary: if it is invoked
// in sandbox child mode, it applies the limits and execs the command, instead of
// launching the suite.
//
// Without this, the sandbox would re-execute "its own executable" (which in the
// tests is the test binary), that one would ignore the marker and run all the
// tests again, which would in turn spawn more children: infinite recursion. It is
// exactly the kind of failure that only shows up when running for real.
func TestMain(m *testing.M) {
	if IsChildExecution(os.Args[1:]) {
		if err := RunAsChild(os.Args[1:]); err != nil {
			fmt.Fprintf(os.Stderr, "sandbox[tests]: %v\n", err)
			os.Exit(126)
		}
		return
	}

	l, _ := logx.New(logx.Options{Level: logx.Error, Console: false})
	logx.Install(l)
	os.Exit(m.Run())
}
