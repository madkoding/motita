//go:build unix

package sandbox

import (
	"reflect"
	"testing"
)

// TestProductionExecHookReplacesTheImage: on Unix the child must replace its own
// image, so the hook production uses has to be the platform implementation (which
// calls syscall.Exec) and not a stub a test could have left behind.
func TestProductionExecHookReplacesTheImage(t *testing.T) {
	if reflect.ValueOf(childHooks.exec).Pointer() != reflect.ValueOf(execCommand).Pointer() {
		t.Error("the production child exec hook must be the platform implementation")
	}
}

// TestExecCommandReportsAFailedExec: the implementation on Unix is syscall.Exec, and
// its error is what the child turns into the reserved code 127. It is exercised with
// a command that cannot exist, which is the only way to observe the failure without
// the process image really being replaced.
func TestExecCommandReportsAFailedExec(t *testing.T) {
	err := execCommand("/this/does/not/exist-motita", []string{"x"}, []string{})
	if err == nil {
		t.Fatal("executing a non-existent file must fail")
	}
}
