//go:build unix

package sandbox

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"syscall"
	"testing"
	"time"

	"github.com/madkoding/starlight/internal/execx"
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
	err := execCommand("/this/does/not/exist-starlight", []string{"x"}, []string{})
	if err == nil {
		t.Fatal("executing a non-existent file must fail")
	}
}

// TestTimeoutKillsTheDescendantsOfTheCommand: on every Unix the child leads a
// process group of its own, so the deadline kills the whole tree at once. Without
// the group (darwin got no SysProcAttr) kill(-pid) found nothing: the run only
// ended after WaitDelay and a grandchild outlived it with the sandbox's
// permissions.
func TestTimeoutKillsTheDescendantsOfTheCommand(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "survived")
	box, err := New(Options{Dir: dir})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer box.Close()

	start := time.Now()
	_, _, _, err = box.Run(context.Background(), execx.Request{
		Command: "/bin/sh",
		Args:    []string{"-c", "(sleep 1; touch " + marker + ") & sleep 30"},
		Timeout: 300 * time.Millisecond,
	})
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("the deadline must be reported")
	}
	if elapsed > 1500*time.Millisecond {
		t.Errorf("the run took %s: the group was not killed at the deadline", elapsed)
	}
	time.Sleep(1500 * time.Millisecond)
	if _, err := os.Stat(marker); err == nil {
		t.Error("a grandchild outlived the timeout")
	}
}

// TestOwnProcessGroupAlsoCoversPlatformsWithoutAttributes: a nil SysProcAttr (the
// non-Linux platforms) still gets a group of its own, and existing attributes keep
// theirs.
func TestOwnProcessGroupAlsoCoversPlatformsWithoutAttributes(t *testing.T) {
	if attr := ownProcessGroup(nil); attr == nil || !attr.Setpgid {
		t.Errorf("ownProcessGroup(nil) = %+v, want Setpgid", attr)
	}
	given := &syscall.SysProcAttr{}
	if attr := ownProcessGroup(given); attr != given || !attr.Setpgid {
		t.Errorf("the given attributes must be kept and grouped: %+v", attr)
	}
}
