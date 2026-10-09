package plan

import (
	"syscall"
	"testing"
)

// makeDumpable undoes the sandbox's PR_SET_DUMPABLE=0 for this test process: a
// non-dumpable process cannot even open its own /proc/self/mem, and the read-error
// test needs the open to succeed so that the read is what fails.
func makeDumpable(t *testing.T) {
	t.Helper()
	const prSetDumpable = 4
	if _, _, errno := syscall.RawSyscall(syscall.SYS_PRCTL, prSetDumpable, 1, 0); errno != 0 {
		t.Skipf("cannot make the test process dumpable: %v", errno)
	}
}
