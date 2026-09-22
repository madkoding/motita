//go:build linux

package sandbox

import (
	"fmt"
	"os/exec"
	"syscall"
)

// Per-process limits (Linux).
type Limits struct {
	MemoryMB      int
	CPUSeconds    int
	Processes     int
	OpenFiles     int
	MaxFileSizeMB int
	NoNetwork     bool // CLONE_NEWNET: no network for the child
}

// childAttributes prepares SysProcAttr and returns the warnings about what
// could not be applied, so as not to lie about the effective isolation.
func childAttributes(l Limits, dropPrivs bool, uid, gid int) (*syscall.SysProcAttr, []string) {
	attr := &syscall.SysProcAttr{
		// A group of its own: allows killing all the descendants at once.
		Setpgid: true,
	}
	var warnings []string

	if l.NoNetwork {
		// CLONE_NEWNET needs CAP_SYS_ADMIN. If the clone fails, the exec error
		// will say so; it is not silenced.
		attr.Cloneflags |= syscall.CLONE_NEWNET
	}
	if dropPrivs {
		attr.Credential = &syscall.Credential{Uid: uint32(uid), Gid: uint32(gid)}
	}
	return attr, warnings
}

// hasLimits reports whether there is anything to limit (in which case the shell
// applies the limits with ulimit before the exec).
func hasLimits(l Limits) bool {
	return l.MemoryMB > 0 || l.CPUSeconds > 0 || l.Processes > 0 ||
		l.OpenFiles > 0 || l.MaxFileSizeMB > 0
}

// enterChroot and dropPrivileges require root; they are kept apart here so the
// error is explicit when there is none.
//
// chrootHooks are the syscalls that need root (or an existing root directory) and
// that, when they succeed, cannot be undone from inside the process: a real
// chroot cannot be left behind without another chroot, and real credentials
// cannot be regained without root. They are variables holding the real calls (the
// same injectable-seam pattern as childHooks in child.go) so the success path is
// exercised in the tests on any machine; the production calls are the same
// syscalls as always.
var chrootHooks = struct {
	chroot func(path string) error
	chdir  func(path string) error
}{
	chroot: syscall.Chroot,
	chdir:  syscall.Chdir,
}

func enterChroot(root, dir string) error {
	if root == "" {
		return nil
	}
	if err := chrootHooks.chroot(root); err != nil {
		return fmt.Errorf("chroot(%q): %w", root, err)
	}
	if dir == "" {
		dir = "/"
	}
	if err := chrootHooks.chdir(dir); err != nil {
		return fmt.Errorf("chdir(%q) after chroot: %w", dir, err)
	}
	return nil
}

// privilegeHooks are the three calls that drop root, for the same reason as the
// chroot ones: their success cannot be undone. In the child process they run
// before the exec replaces the image, so replacing them in a test changes
// nothing about what production does.
var privilegeHooks = struct {
	setgroups func([]int) error
	setgid    func(int) error
	setuid    func(int) error
}{
	setgroups: syscall.Setgroups,
	setgid:    syscall.Setgid,
	setuid:    syscall.Setuid,
}

func dropPrivileges(uid, gid int) error {
	// The order matters: first the group, then the supplementary groups, and
	// finally the user (once the uid has changed the gid can no longer be
	// changed).
	if err := privilegeHooks.setgroups([]int{gid}); err != nil {
		return fmt.Errorf("setgroups: %w", err)
	}
	if err := privilegeHooks.setgid(gid); err != nil {
		return fmt.Errorf("setgid(%d): %w", gid, err)
	}
	if err := privilegeHooks.setuid(uid); err != nil {
		return fmt.Errorf("setuid(%d): %w", uid, err)
	}
	return nil
}

var _ = exec.Command
