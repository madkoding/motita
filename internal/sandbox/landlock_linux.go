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
//
// That `.git` file is the agent's to rewrite, so nothing it names is taken on trust: pointing it at
// the home directory used to make the home directory writable. The directory it names must be a
// real `<repository>/worktrees/<id>` whose own `gitdir` file names dir back, its `commondir` must
// be that repository, and the repository must hold a HEAD and objects. Of the repository only what
// a commit writes is granted - objects, refs and logs - never its config, hooks or info, which a
// git run later outside the sandbox would obey.
func gitDirs(dir string) []string {
	data, err := os.ReadFile(filepath.Join(dir, ".git"))
	if err != nil {
		return nil
	}
	rest, ok := strings.CutPrefix(string(data), "gitdir:")
	if !ok {
		return nil
	}
	own, ok := realPath(dir, strings.TrimSpace(rest))
	if !ok || filepath.Base(filepath.Dir(own)) != "worktrees" || !isFile(filepath.Join(own, "HEAD")) {
		return nil
	}
	common := filepath.Dir(filepath.Dir(own))
	// The registration names its worktree back, and the agent cannot write another repository's.
	back, err := os.ReadFile(filepath.Join(own, "gitdir"))
	if err != nil {
		return nil
	}
	named, ok := realPath(own, strings.TrimSpace(string(back)))
	self, _ := realPath(dir, ".git")
	if !ok || named != self {
		return nil
	}
	pointer, err := os.ReadFile(filepath.Join(own, "commondir"))
	if err != nil {
		return nil
	}
	if target, _ := realPath(own, strings.TrimSpace(string(pointer))); target != common ||
		!isFile(filepath.Join(common, "HEAD")) || !isRealDir(filepath.Join(common, "objects")) {
		return nil
	}
	dirs := []string{own}
	for _, name := range []string{"objects", "refs", "logs"} {
		if sub := filepath.Join(common, name); isRealDir(sub) {
			dirs = append(dirs, sub)
		}
	}
	return dirs
}

// realPath is p, taken against base when relative, with every symbolic link resolved; "" and false
// when it does not exist.
func realPath(base, p string) (string, bool) {
	if !filepath.IsAbs(p) {
		p = filepath.Join(base, p)
	}
	resolved, err := filepath.EvalSymlinks(p)
	return resolved, err == nil
}

func isFile(p string) bool {
	info, err := os.Lstat(p)
	return err == nil && info.Mode().IsRegular()
}

// isRealDir reports a directory that is not a symbolic link: a link named `objects` in a repository
// the agent built inside its workspace would grant whatever it points at.
func isRealDir(p string) bool {
	info, err := os.Lstat(p)
	return err == nil && info.IsDir()
}
