//go:build linux

package sandbox

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strings"
	"syscall"
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
// Only what changes the filesystem is - and motita's own logins and keys, which no command needs
// and a prompt-injected script would send away.
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
	accessReadFile   = 1 << 2 // handled only when something is hidden from reads
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

// mmapAnon is the anonymous mapping kernelBuffer uses, a variable only so a test can make it fail.
var mmapAnon = syscall.Mmap

// kernelBuffer copies src into memory the kernel can be pointed at, and returns its address.
//
// A system call takes the ADDRESS of a struct as a plain integer. Taking it from a Go slice needs
// the unsafe package, and from a heap or stack slice it would not be stable (a stack can move
// between taking the address and the call). An anonymous mapping lives outside the Go heap, so its
// address cannot change and the garbage collector never sees it: no unsafe is needed to hold it.
// release unmaps it once the call has returned.
func kernelBuffer(src []byte) (addr uintptr, release func(), err error) {
	buf, err := mmapAnon(-1, 0, len(src), syscall.PROT_READ|syscall.PROT_WRITE, syscall.MAP_ANON|syscall.MAP_PRIVATE)
	if err != nil {
		return 0, nil, err
	}
	copy(buf, src)
	return reflect.ValueOf(&buf[0]).Pointer(), func() { _ = syscall.Munmap(buf) }, nil
}

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
//
// The paths in hidden, motita's own logins and keys, are also made unreadable. Landlock only
// grants, so that is done by granting reads on everything around them (see readableAround); when
// that cannot be worked out, reads are left as they were rather than breaking every command.
func confineWrites(roots, hidden []string) error {
	abi := landlockABI()
	if abi == 0 {
		return fmt.Errorf("landlock is not available on this kernel")
	}
	runtime.LockOSThread()

	handled := writeAccess(abi)
	readable := readableAround(hidden)
	if readable != nil {
		handled |= accessReadFile
	}
	attr := make([]byte, 8)
	binary.NativeEndian.PutUint64(attr, handled)
	attrAddr, releaseAttr, err := kernelBuffer(attr)
	if err != nil {
		return fmt.Errorf("landlock_create_ruleset: %w", err)
	}
	fd, _, errno := landlockSyscall(sysLandlockCreateRuleset, attrAddr, uintptr(len(attr)), 0)
	releaseAttr()
	if errno != 0 {
		return fmt.Errorf("landlock_create_ruleset: %w", errno)
	}
	ruleset := int(fd)
	defer syscall.Close(ruleset)

	for _, root := range roots {
		if err := allowWrites(ruleset, root, handled&^accessReadFile); err != nil {
			return err
		}
	}
	for _, path := range readable {
		if err := allowWrites(ruleset, path, accessReadFile); err != nil {
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

// allowWrites grants the write rights to root and everything beneath it (or, for what stays
// readable around a hidden path, the right to read). /dev is granted only the right to read and
// write the files that exist (`> /dev/null`), not to create nodes there.
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
		allowed &= accessReadFile | accessWriteFile | accessTruncate
	}
	// struct landlock_path_beneath_attr is packed: u64 allowed_access, s32 parent_fd.
	rule := make([]byte, 12)
	binary.NativeEndian.PutUint64(rule, allowed)
	binary.NativeEndian.PutUint32(rule[8:], uint32(fd))
	ruleAddr, releaseRule, err := kernelBuffer(rule)
	if err != nil {
		return fmt.Errorf("landlock_add_rule(%q): %w", resolved, err)
	}
	_, _, errno := landlockSyscall(sysLandlockAddRule, uintptr(ruleset), landlockRulePathBeneath, ruleAddr)
	releaseRule()
	if errno != 0 {
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

// readDir lists a directory, a variable so a test can make a listing fail.
var readDir = os.ReadDir

// readableAround is what stays readable when the paths in hidden must not be: every entry of every
// directory on the way down to a hidden path, except the entries on that way. A grant covers what
// is beneath it, so a grant on any of those directories themselves would cover the hidden path too.
//
// An entry that is a link is granted where it points, and skipped when that is a hidden path or a
// directory on the way to one. It returns nil, hiding nothing, when there is nothing to hide or a
// directory on the way cannot be listed: what it holds could not be granted, and denying it every
// read would break the commands that only read there.
func readableAround(hidden []string) []string {
	var targets []string
	onTheWay := map[string]bool{}
	for _, h := range hidden {
		if !filepath.IsAbs(h) {
			continue
		}
		target := resolveExisting(filepath.Clean(h))
		targets = append(targets, target)
		for p := target; !onTheWay[p]; p = filepath.Dir(p) {
			onTheWay[p] = true
		}
	}
	isHidden := func(p string) bool {
		for _, t := range targets {
			if p == t || strings.HasPrefix(p, t+"/") {
				return true
			}
		}
		return false
	}
	var dirs []string
	for p := range onTheWay {
		if !isHidden(p) {
			dirs = append(dirs, p)
		}
	}
	if len(dirs) == 0 {
		return nil
	}
	sort.Strings(dirs)
	readable := []string{}
	for _, dir := range dirs {
		entries, err := readDir(dir)
		if errors.Is(err, fs.ErrNotExist) {
			continue // a directory that is not there yet holds nothing to grant
		}
		if err != nil {
			return nil
		}
		for _, entry := range entries {
			resolved, err := filepath.EvalSymlinks(filepath.Join(dir, entry.Name()))
			if err != nil || onTheWay[resolved] || isHidden(resolved) {
				continue
			}
			readable = append(readable, resolved)
		}
	}
	return readable
}

// resolveExisting is p with the links of the part of it that exists resolved: a hidden path that
// is not there yet is still hidden when it appears.
func resolveExisting(p string) string {
	var rest []string
	for {
		if resolved, err := filepath.EvalSymlinks(p); err == nil {
			return filepath.Join(append([]string{resolved}, rest...)...)
		}
		rest = append([]string{filepath.Base(p)}, rest...)
		p = filepath.Dir(p)
	}
}
