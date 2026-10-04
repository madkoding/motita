//go:build linux

package sandbox

import (
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"unsafe"
)

// WRITE CONFINEMENT with Landlock.
//
// The policy judges the TEXT of a command line, and a script inside the workspace can still `cd`
// somewhere else and write there: no reading of the line sees that. Landlock is the kernel's own
// answer: once a process restricts itself, it and everything it starts can write only under the
// directories it was granted, whatever the program does. It needs no privileges and no extra
// dependency, only three system calls that are the same on every architecture.
//
// Reads are not restricted: a build has to read the toolchain, the system and the home directory.
// Only what changes the filesystem is.
const (
	sysLandlockCreateRuleset = 444
	sysLandlockAddRule       = 445
	sysLandlockRestrictSelf  = 446

	landlockCreateRulesetVersion = 1 << 0
	landlockRulePathBeneath      = 1
	prSetNoNewPrivs              = 38
	oPath                        = 0o10000000 // O_PATH: open only to name the directory
)

// The write-side access rights, with the Landlock ABI that introduced each.
const (
	accessWriteFile  = 1 << 1
	accessRemoveDir  = 1 << 4
	accessRemoveFile = 1 << 5
	accessMakeChar   = 1 << 6
	accessMakeDir    = 1 << 7
	accessMakeReg    = 1 << 8
	accessMakeSock   = 1 << 9
	accessMakeFifo   = 1 << 10
	accessMakeBlock  = 1 << 11
	accessMakeSym    = 1 << 12
	accessRefer      = 1 << 13 // ABI 2
	accessTruncate   = 1 << 14 // ABI 3
)

// landlockSyscall is the raw system call, a variable only so a test can make each call fail.
var landlockSyscall = syscall.Syscall

// openPath opens a directory to name it in a rule, a variable so a test can make it fail.
var openPath = syscall.Open

// landlockABI is the Landlock version this kernel offers, or 0 when it offers none (not built in,
// not in the active security modules, or a seccomp filter in the way).
func landlockABI() int {
	v, _, errno := landlockSyscall(sysLandlockCreateRuleset, 0, 0, landlockCreateRulesetVersion)
	if errno != 0 {
		return 0
	}
	return int(v)
}

// writeAccess is every write right the given ABI knows about.
func writeAccess(abi int) uint64 {
	access := uint64(accessWriteFile | accessRemoveDir | accessRemoveFile | accessMakeChar | accessMakeDir |
		accessMakeReg | accessMakeSock | accessMakeFifo | accessMakeBlock | accessMakeSym)
	if abi >= 2 {
		access |= accessRefer
	}
	if abi >= 3 {
		access |= accessTruncate
	}
	return access
}

// confineWrites restricts the calling thread, and so everything it starts, to writing under roots.
// A root that does not exist is skipped: there is nothing there to grant. It must run on the
// thread that will exec the command, which is why it pins the goroutine to it.
func confineWrites(roots []string) error {
	abi := landlockABI()
	if abi == 0 {
		return fmt.Errorf("landlock is not available on this kernel")
	}
	runtime.LockOSThread()

	handled := writeAccess(abi)
	attr := make([]byte, 8)
	binary.NativeEndian.PutUint64(attr, handled)
	fd, _, errno := landlockSyscall(sysLandlockCreateRuleset, uintptr(unsafe.Pointer(&attr[0])), uintptr(len(attr)), 0)
	if errno != 0 {
		return fmt.Errorf("landlock_create_ruleset: %w", errno)
	}
	ruleset := int(fd)
	defer syscall.Close(ruleset)

	for _, root := range roots {
		if err := allowWrites(ruleset, root, handled); err != nil {
			return err
		}
	}
	if _, _, errno := landlockSyscall(syscall.SYS_PRCTL, prSetNoNewPrivs, 1, 0); errno != 0 {
		return fmt.Errorf("prctl(NO_NEW_PRIVS): %w", errno)
	}
	if _, _, errno := landlockSyscall(sysLandlockRestrictSelf, uintptr(ruleset), 0, 0); errno != 0 {
		return fmt.Errorf("landlock_restrict_self: %w", errno)
	}
	return nil
}

// allowWrites grants the write rights to root and everything beneath it. /dev is granted only the
// right to write to the files that exist (`> /dev/null`), not to create nodes there.
func allowWrites(ruleset int, root string, handled uint64) error {
	resolved, err := filepath.EvalSymlinks(root)
	if err != nil {
		return nil
	}
	fd, err := openPath(resolved, oPath|syscall.O_CLOEXEC, 0)
	if err != nil {
		return nil
	}
	defer syscall.Close(fd)

	allowed := handled
	if resolved == "/dev" {
		allowed &= accessWriteFile | accessTruncate
	}
	// struct landlock_path_beneath_attr is packed: u64 allowed_access, s32 parent_fd.
	rule := make([]byte, 12)
	binary.NativeEndian.PutUint64(rule, allowed)
	binary.NativeEndian.PutUint32(rule[8:], uint32(fd))
	if _, _, errno := landlockSyscall(sysLandlockAddRule, uintptr(ruleset), landlockRulePathBeneath,
		uintptr(unsafe.Pointer(&rule[0]))); errno != 0 {
		return fmt.Errorf("landlock_add_rule(%q): %w", resolved, errno)
	}
	return nil
}

// gitDirs are the directories a `git` command in dir writes outside it: for a worktree, `.git` is
// a file naming its own directory under the main repository's `.git`, and a commit writes objects
// and refs into the main one. Without them confining a session worktree would break `git commit`.
func gitDirs(dir string) []string {
	data, err := os.ReadFile(filepath.Join(dir, ".git"))
	if err != nil {
		return nil
	}
	rest, ok := strings.CutPrefix(string(data), "gitdir:")
	if !ok {
		return nil
	}
	own := strings.TrimSpace(rest)
	if !filepath.IsAbs(own) {
		own = filepath.Join(dir, own)
	}
	dirs := []string{own}
	if common, err := os.ReadFile(filepath.Join(own, "commondir")); err == nil {
		target := strings.TrimSpace(string(common))
		if !filepath.IsAbs(target) {
			target = filepath.Join(own, target)
		}
		dirs = append(dirs, filepath.Clean(target))
	}
	return dirs
}
