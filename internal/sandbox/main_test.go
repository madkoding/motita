package sandbox

import (
	"fmt"
	"os"
	"testing"

	"github.com/madkoding/motita/internal/logx"
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

	restore := tempOutsidePrivateTrees()
	l, _ := logx.New(logx.Options{Level: logx.Error, Console: false})
	logx.Install(l)
	code := m.Run()
	restore()
	os.Exit(code)
}

// tempOutsidePrivateTrees points TMPDIR at a fresh directory outside the trees the sandbox
// keeps private, and returns the function that puts the environment back and removes it.
//
// t.TempDir follows TMPDIR, and a TMPDIR inside a tree the sandbox keeps private (the user's
// home, the tools directory) makes every temporary tree unreadable to a confined command: the
// tests that read what they just wrote fail for a reason that is not the code. The base is
// named here, not taken from the environment, so the suite gives the same answer wherever it
// runs. /tmp is named directly because os.TempDir follows the very TMPDIR this replaces.
func tempOutsidePrivateTrees() func() {
	previous, had := os.LookupEnv("TMPDIR")
	base := ""
	for _, dir := range []string{"/tmp", "/var/tmp", "/dev/shm"} {
		if made, err := os.MkdirTemp(dir, "motita-sandbox-test-"); err == nil {
			base = made
			break
		}
	}
	if base == "" {
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