//go:build !linux

package sandbox

import (
	"fmt"
	"os/exec"
	"syscall"
)

// Per-process limits (outside Linux: only the ephemeral directory is isolated).
type Limits struct {
	MemoryMB      int
	CPUSeconds    int
	Processes     int
	OpenFiles     int
	MaxFileSizeMB int
	NoNetwork     bool
}

func childAttributes(l Limits, dropPrivs bool, uid, gid int) (*syscall.SysProcAttr, []string) {
	return nil, []string{"this platform does not support setrlimit, chroot or namespaces"}
}

func hasLimits(l Limits) bool { return false }

func enterChroot(root, dir string) error {
	return fmt.Errorf("chroot is only implemented on Linux")
}

func dropPrivileges(uid, gid int) error {
	return fmt.Errorf("dropping privileges is only implemented on Linux")
}

var _ = exec.Command
