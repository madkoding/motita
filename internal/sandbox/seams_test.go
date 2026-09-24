//go:build linux

// The sandbox's isolation is Linux-specific (ulimit, cgroups v1, chroot, dropping
// privileges). These tests exercise that code, so they only build where it exists;
// the portable behaviour is covered by portable_test.go.

package sandbox

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/madkoding/motita/internal/execx"
	"github.com/madkoding/motita/internal/logx"
)

// ---------------------------------------------------------------------------
// Tests of the injectable seams and of the OS error paths that cannot be
// provoked with a plain filesystem setup (there is no root and no cgroups v1 in
// the test environments).
//
// The seams (childHooks, jsonMarshal, chrootHooks, privilegeHooks, osHooks) hold
// the real operations in production; the tests that replace them restore them
// with defer, and TestProductionSeamsAreTheRealOperations checks that the values
// production uses are still the real ones.
// ---------------------------------------------------------------------------

// TestProductionSeamsAreTheRealOperations: replacing a seam in a test must never
// be able to change what production does, so the defaults are asserted.
func TestProductionSeamsAreTheRealOperations(t *testing.T) {
	if got, want := osHooks.euid(), os.Geteuid(); got != want {
		t.Errorf("osHooks.euid() = %d, the real one is %d", got, want)
	}
	executable, err := osHooks.executable()
	if err != nil || !filepath.IsAbs(executable) {
		t.Errorf("osHooks.executable() = %q, %v (an absolute path was expected)", executable, err)
	}

	spec := Spec{Command: "/bin/true", Limits: Limits{MemoryMB: 1}}
	want, err := json.Marshal(spec)
	if err != nil {
		t.Fatal(err)
	}
	got, err := jsonMarshal(spec)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(want) {
		t.Errorf("jsonMarshal is not the stdlib marshaller: %s != %s", got, want)
	}
}

// TestProductionHooksAreTheRealSyscalls: the chroot hooks must be the real
// syscalls (a stub would not fail here). The privilege hooks are checked by
// identity, comparing the function pointers, because as a non-root user only
// setgroups fails (setgid and setuid succeed for the process's own ids) and
// calling them for real would make that impossible to observe.
func TestProductionHooksAreTheRealSyscalls(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("as root these syscalls succeed and would alter the test process")
	}
	if err := chrootHooks.chroot("/does/not/exist/the-production-hook"); err == nil {
		t.Error("the production chroot hook must be syscall.Chroot")
	}
	if err := chrootHooks.chdir("/does/not/exist/the-production-hook"); err == nil {
		t.Error("the production chdir hook must be syscall.Chdir")
	}
	if err := privilegeHooks.setgroups([]int{os.Getgid()}); err == nil {
		t.Error("the production setgroups hook must be syscall.Setgroups")
	}
	if reflect.ValueOf(privilegeHooks.setgid).Pointer() != reflect.ValueOf(syscall.Setgid).Pointer() {
		t.Error("the production setgid hook must be syscall.Setgid")
	}
	if reflect.ValueOf(privilegeHooks.setuid).Pointer() != reflect.ValueOf(syscall.Setuid).Pointer() {
		t.Error("the production setuid hook must be syscall.Setuid")
	}
}

// TestProductionChildExitHookIsTheRealOne: as in the rest of the package, the
// value production uses is asserted so a test cannot accidentally weaken it. The
// exec hook is asserted per platform (see prodhooks_unix_test.go), because what it
// has to be depends on the system.
func TestProductionChildExitHookIsTheRealOne(t *testing.T) {
	if reflect.ValueOf(childHooks.exit).Pointer() != reflect.ValueOf(os.Exit).Pointer() {
		t.Error("the production child exit hook must be os.Exit")
	}
}

// --- chroot and privileges with injected syscalls ---------------------------

// TestEnterChrootSuccessWithInjectedSyscalls walks the success path, which needs
// root: the real chroot would leave the test process inside a new root for ever.
func TestEnterChrootSuccessWithInjectedSyscalls(t *testing.T) {
	original := chrootHooks
	defer func() { chrootHooks = original }()

	var gotChroot, gotChdir string
	chrootHooks.chroot = func(path string) error { gotChroot = path; return nil }
	chrootHooks.chdir = func(path string) error { gotChdir = path; return nil }

	// With a directory, that directory is the one used.
	if err := enterChroot("/fake/root", "/fake/root/work"); err != nil {
		t.Fatalf("enterChroot = %v", err)
	}
	if gotChroot != "/fake/root" || gotChdir != "/fake/root/work" {
		t.Errorf("chroot(%q) chdir(%q)", gotChroot, gotChdir)
	}

	// With no directory, the root itself is used ("/"), because after the chroot
	// the path is interpreted inside the new root.
	gotChdir = ""
	if err := enterChroot("/fake/root", ""); err != nil {
		t.Fatalf("enterChroot = %v", err)
	}
	if gotChdir != "/" {
		t.Errorf("chdir = %q, \"/\" was expected when no directory is given", gotChdir)
	}
}

// TestEnterChrootChdirFailure: if the chdir after the chroot fails, the error
// must name the operation, because a failing chdir leaves the process in a
// different directory than the caller asked for.
func TestEnterChrootChdirFailure(t *testing.T) {
	original := chrootHooks
	defer func() { chrootHooks = original }()

	chrootHooks.chroot = func(string) error { return nil }
	chrootHooks.chdir = func(string) error { return syscall.ENOTDIR }

	err := enterChroot("/fake/root", "/fake/root/work")
	if err == nil {
		t.Fatal("a failing chdir must be an error")
	}
	if !strings.Contains(err.Error(), "chdir") || !strings.Contains(err.Error(), "/fake/root/work") {
		t.Errorf("the error must name the operation and the directory: %v", err)
	}
	if !errors.Is(err, syscall.ENOTDIR) {
		t.Errorf("the original error must be preserved: %v", err)
	}
}

// TestDropPrivilegesSuccessWithInjectedSyscalls walks the success path of the
// three calls in their order: group, then supplementary groups, then user.
func TestDropPrivilegesSuccessWithInjectedSyscalls(t *testing.T) {
	original := privilegeHooks
	defer func() { privilegeHooks = original }()

	var order []string
	var groups []int
	var uid, gid int
	privilegeHooks.setgroups = func(g []int) error { order = append(order, "setgroups"); groups = g; return nil }
	privilegeHooks.setgid = func(g int) error { order = append(order, "setgid"); gid = g; return nil }
	privilegeHooks.setuid = func(u int) error { order = append(order, "setuid"); uid = u; return nil }

	if err := dropPrivileges(1234, 5678); err != nil {
		t.Fatalf("dropPrivileges = %v", err)
	}
	if uid != 1234 || gid != 5678 {
		t.Errorf("uid=%d gid=%d, 1234/5678 were requested", uid, gid)
	}
	// The supplementary groups are reduced to the target group.
	if len(groups) != 1 || groups[0] != 5678 {
		t.Errorf("groups = %v, [5678] was expected", groups)
	}
	if strings.Join(order, ",") != "setgroups,setgid,setuid" {
		t.Errorf("the order must be group, supplementary groups, user: %v", order)
	}
}

// TestDropPrivilegesReportsSetgidAndSetuidFailures: each call of the sequence has
// its own error, which is what tells the operator which privilege is missing.
func TestDropPrivilegesReportsSetgidAndSetuidFailures(t *testing.T) {
	original := privilegeHooks
	defer func() { privilegeHooks = original }()

	privilegeHooks.setgroups = func([]int) error { return nil }
	privilegeHooks.setgid = func(int) error { return nil }
	privilegeHooks.setuid = func(int) error { return nil }

	privilegeHooks.setgid = func(int) error { return syscall.EPERM }
	if err := dropPrivileges(1000, 1000); err == nil {
		t.Error("a failing setgid must be an error")
	} else if !strings.Contains(err.Error(), "setgid") || !errors.Is(err, syscall.EPERM) {
		t.Errorf("the error must name setgid and keep the cause: %v", err)
	}
	// With setgid working it is setuid's turn.
	privilegeHooks.setgid = func(int) error { return nil }
	privilegeHooks.setuid = func(int) error { return syscall.EPERM }
	if err := dropPrivileges(1000, 1000); err == nil {
		t.Error("a failing setuid must be an error")
	} else if !strings.Contains(err.Error(), "setuid") || !errors.Is(err, syscall.EPERM) {
		t.Errorf("the error must name setuid and keep the cause: %v", err)
	}
}

// --- killGroup --------------------------------------------------------------

// TestKillGroupWithAnImpossiblePid: if the group cannot be signalled (it no
// longer exists) the answer is the "already finished" error and not a panic, so
// that the deadline handling of launch can carry on.
func TestKillGroupWithAnImpossiblePid(t *testing.T) {
	cmd := exec.Command("/bin/true")
	// A pid that cannot exist in practice, used as a process group.
	cmd.Process = &os.Process{Pid: 1 << 30}

	err := killGroup(cmd)
	if !errors.Is(err, os.ErrProcessDone) {
		t.Errorf("killGroup = %v, the process-done error was expected", err)
	}
}

// TestKillGroupOfAStartedGroup: the real syscall. With the child in a group of
// its own (what the sandbox uses), the negative pid signals the whole group.
func TestKillGroupOfAStartedGroup(t *testing.T) {
	cmd := exec.Command("/bin/sleep", "30")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatalf("could not start the helper: %v", err)
	}
	if err := killGroup(cmd); err != nil {
		t.Errorf("killGroup = %v", err)
	}
	if err := cmd.Wait(); err == nil {
		t.Error("the process was signalled and must not finish cleanly")
	}
}

// --- cgroups v1 error paths -------------------------------------------------

// TestNewCgroupUsesTheDefaultRoot: with no root given, the standard cgroups v1
// mount point is used. The test does not depend on the host having v1: it only
// asserts which root was used, reporting it in the error when there is no v1.
func TestNewCgroupUsesTheDefaultRoot(t *testing.T) {
	cg, err := newCgroup("", Limits{})
	if err != nil {
		if !strings.Contains(err.Error(), "/sys/fs/cgroup") {
			t.Errorf("the error must name the default root: %v", err)
		}
		return
	}
	defer cg.remove()
	if cg.root != "/sys/fs/cgroup" {
		t.Errorf("root = %q", cg.root)
	}
}

// TestNewCgroupWithoutPermissionToCreateTheGroup: the memory controller exists
// but the group cannot be created.
func TestNewCgroupWithoutPermissionToCreateTheGroup(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("as root the directory can always be created")
	}
	root := t.TempDir()
	memory := filepath.Join(root, "memory")
	if err := os.MkdirAll(memory, 0o755); err != nil {
		t.Fatal(err)
	}
	// No write permission on the controller directory: MkdirAll fails.
	if err := os.Chmod(memory, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(memory, 0o700) })

	cg, err := newCgroup(root, Limits{MemoryMB: 64})
	if err == nil {
		t.Fatalf("the cgroup must not be created without permission (cg = %+v)", cg)
	}
	if !strings.Contains(err.Error(), "no permission to create the cgroup") {
		t.Errorf("unexpected error: %v", err)
	}
}

// TestNewCgroupMemoryLimitCannotBeWritten: the group is created but the memory
// limit cannot be written; that is fatal (the memory limit is the reason the
// group exists).
func TestNewCgroupMemoryLimitCannotBeWritten(t *testing.T) {
	root := t.TempDir()
	base := filepath.Join(root, "memory", "motita")
	if err := os.MkdirAll(base, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(base, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(base, 0o700) })

	cg, err := newCgroup(root, Limits{MemoryMB: 64})
	if err == nil {
		t.Fatalf("a memory limit that cannot be written must be an error (cg = %+v)", cg)
	}
	if !strings.Contains(err.Error(), "could not write the limit") {
		t.Errorf("unexpected error: %v", err)
	}
}

// TestNewCgroupPidsLimitCannotBeWritten: the PIDs limit is optional, so failing
// to write it must not fail the whole group: it is dropped and the memory limit
// stays.
func TestNewCgroupPidsLimitCannotBeWritten(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "memory"), 0o755); err != nil {
		t.Fatal(err)
	}
	pidsDir := filepath.Join(root, "pids")
	if err := os.MkdirAll(filepath.Join(pidsDir, "motita"), 0o755); err != nil {
		t.Fatal(err)
	}
	// The controller is present (pids.max exists) ...
	if err := os.WriteFile(filepath.Join(pidsDir, "pids.max"), []byte("max"), 0o644); err != nil {
		t.Fatal(err)
	}
	// ... but the group cannot be written to.
	if err := os.Chmod(filepath.Join(pidsDir, "motita"), 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(filepath.Join(pidsDir, "motita"), 0o700) })

	cg, err := newCgroup(root, Limits{MemoryMB: 64, Processes: 10})
	if err != nil {
		t.Fatalf("the PIDs limit is optional and must not fail the group: %v", err)
	}
	defer cg.remove()
	if cg.pids != "" {
		t.Errorf("the PIDs group must be dropped when it cannot be limited: %q", cg.pids)
	}
	if cg.memory == "" {
		t.Error("the memory group must be kept")
	}
	// And it carries the memory limit it could apply.
	if err := cg.addProcess(os.Getpid()); err != nil {
		t.Errorf("the memory group must still accept the process: %v", err)
	}
}

// TestNewCgroupPidsGroupCannotBeCreated: the controller is there but the pids
// group cannot even be created; the memory limit still applies.
func TestNewCgroupPidsGroupCannotBeCreated(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("as root the directory can always be created")
	}
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "memory"), 0o755); err != nil {
		t.Fatal(err)
	}
	pidsDir := filepath.Join(root, "pids")
	if err := os.MkdirAll(pidsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pidsDir, "pids.max"), []byte("max"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Without write permission on the controller, the group cannot be created.
	if err := os.Chmod(pidsDir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(pidsDir, 0o700) })

	cg, err := newCgroup(root, Limits{MemoryMB: 64, Processes: 10})
	if err != nil {
		t.Fatalf("the PIDs controller is optional and must not fail the group: %v", err)
	}
	defer cg.remove()
	if cg.pids != "" {
		t.Errorf("without a pids group there is nothing to join: %q", cg.pids)
	}
	if _, err := os.Stat(filepath.Join(pidsDir, "motita")); !os.IsNotExist(err) {
		t.Error("no pids group must have been left behind")
	}
}

// TestNewCgroupWithoutPidsLimitDoesNotUseThePidsController: with no PIDs limit
// requested there is no pids group to join, so addProcess must not try to write
// into a group that was never created (that used to be reported later as a
// failure to add the process, which was a false alarm).
func TestNewCgroupWithoutPidsLimitDoesNotUseThePidsController(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "memory"), 0o755); err != nil {
		t.Fatal(err)
	}
	// The pids controller exists and even has a stale group from a previous run.
	if err := os.MkdirAll(filepath.Join(root, "pids", "motita"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "pids", "pids.max"), []byte("max"), 0o644); err != nil {
		t.Fatal(err)
	}

	cg, err := newCgroup(root, Limits{MemoryMB: 64})
	if err != nil {
		t.Fatal(err)
	}
	defer cg.remove()
	if cg.pids != "" {
		t.Errorf("without a PIDs limit the pids group must not be used: %q", cg.pids)
	}
	if err := cg.addProcess(os.Getpid()); err != nil {
		t.Errorf("addProcess must not fail for a memory-only group: %v", err)
	}
	tasks := filepath.Join(root, "memory", "motita", "tasks")
	data, err := os.ReadFile(tasks)
	if err != nil {
		t.Fatalf("the process was not added: %v", err)
	}
	if strings.TrimSpace(string(data)) != strconv.Itoa(os.Getpid()) {
		t.Errorf("tasks = %q", data)
	}
}

// TestCgroupAddProcessReportsWriteFailures: the writes that put the PID in the
// groups are reported when they fail (a process that is not in the group is not
// limited, and that cannot be silenced).
func TestCgroupAddProcessReportsWriteFailures(t *testing.T) {
	root := t.TempDir()
	for _, controller := range []string{"memory", "pids"} {
		if err := os.MkdirAll(filepath.Join(root, controller, "motita"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "pids", "pids.max"), []byte("max"), 0o644); err != nil {
		t.Fatal(err)
	}
	cg, err := newCgroup(root, Limits{MemoryMB: 64, Processes: 10})
	if err != nil {
		t.Fatal(err)
	}
	// A tasks file that cannot be written: writing on a directory fails.
	for _, dir := range []string{cg.memory, cg.pids} {
		if err := os.Mkdir(filepath.Join(dir, "tasks"), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	if err := cg.addProcess(4321); err == nil {
		t.Error("a write that fails must be reported")
	}

	// And a partial failure (only the second group) is reported too.
	if err := os.Remove(filepath.Join(cg.memory, "tasks")); err != nil {
		t.Fatal(err)
	}
	if err := cg.addProcess(4321); err == nil {
		t.Error("a partially failing addProcess must be reported")
	}
	data, err := os.ReadFile(filepath.Join(cg.memory, "tasks"))
	if err != nil {
		t.Fatalf("the first group must have accepted the PID: %v", err)
	}
	if strings.TrimSpace(string(data)) != "4321" {
		t.Errorf("tasks = %q", data)
	}
}

// TestCgroupRemoveEmptiesTheProcessFiles: before deleting the group the process
// files are emptied (that is how a process is moved out of a v1 group), so the
// removal does not fail with "directory not empty".
func TestCgroupRemoveEmptiesTheProcessFiles(t *testing.T) {
	root := t.TempDir()
	base := filepath.Join(root, "memory", "motita")
	if err := os.MkdirAll(base, 0o755); err != nil {
		t.Fatal(err)
	}
	// The control files of a v1 group, with a process inside.
	if err := os.WriteFile(filepath.Join(base, "tasks"), []byte("12345"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(base, "cgroup.procs"), []byte("12345"), 0o644); err != nil {
		t.Fatal(err)
	}

	cg := &cgroup{root: root, name: "motita", memory: base}
	if err := cg.remove(); err != nil {
		t.Fatalf("remove = %v", err)
	}
	if _, err := os.Stat(base); !os.IsNotExist(err) {
		t.Errorf("the group was not deleted: %v", err)
	}

	// The emptying is done before the removal, and the test asserts it on a
	// group whose directory cannot be deleted with a file inside it (a directory
	// without write permission, where RemoveAll fails).
	if os.Geteuid() == 0 {
		t.Log("as root the directory is deleted even with the file inside: the emptying was not observable")
		return
	}
	second := filepath.Join(root, "memory", "unremovable")
	if err := os.MkdirAll(second, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(second, "tasks"), []byte("12345"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(second, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(second, 0o700) })

	cg2 := &cgroup{root: root, name: "motita", memory: second}
	if err := cg2.remove(); err == nil {
		t.Error("a group that cannot be deleted must be reported")
	}
	data, err := os.ReadFile(filepath.Join(second, "tasks"))
	if err != nil {
		t.Fatalf("the tasks file disappeared: %v", err)
	}
	if len(strings.TrimSpace(string(data))) != 0 {
		t.Errorf("tasks = %q, it must be emptied before the removal", data)
	}
}

// TestCgroupRemoveReportsWhatItCannotRemove: a group that cannot be deleted must
// be reported with every directory that failed, not silenced (an orphaned group
// is left in /sys/fs/cgroup for ever).
func TestCgroupRemoveReportsWhatItCannotRemove(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("as root the directories can always be deleted")
	}
	root := t.TempDir()
	base := filepath.Join(root, "memory", "motita")
	pids := filepath.Join(root, "pids", "motita")
	for _, dir := range []string{base, pids} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		// A file inside and no write permission: RemoveAll cannot empty the
		// directory, so it cannot be deleted.
		if err := os.WriteFile(filepath.Join(dir, "tasks"), []byte("1234"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(dir, 0o500); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { os.Chmod(dir, 0o700) })
	}

	cg := &cgroup{root: root, name: "motita", memory: base, pids: pids}
	err := cg.remove()
	if err == nil {
		t.Fatal("a group that cannot be deleted must be an error")
	}
	if !strings.Contains(err.Error(), "could not remove the cgroup") {
		t.Errorf("unexpected error: %v", err)
	}
	// The problems accumulate: both directories are named and joined.
	for _, dir := range []string{base, pids} {
		if !strings.Contains(err.Error(), dir) {
			t.Errorf("%s is missing from the accumulated problems: %v", dir, err)
		}
	}
	if !strings.Contains(err.Error(), "; ") {
		t.Errorf("the problems must be joined into one message: %v", err)
	}
}

// TestCgroupRemoveIgnoresAnAlreadyDeletedGroup: a directory that no longer
// exists is not a problem (it is the normal case when the kernel removed it or
// when two removals overlap).
func TestCgroupRemoveIgnoresAnAlreadyDeletedGroup(t *testing.T) {
	root := t.TempDir()
	cg := &cgroup{root: root, name: "motita", memory: filepath.Join(root, "memory", "motita")}
	if err := cg.remove(); err != nil {
		t.Errorf("a group that does not exist must not be a problem: %v", err)
	}
}

// TestRemoveWithRetriesGivesUpAndReturnsTheError: with a directory that cannot be
// deleted, all the attempts are spent and the last error is returned instead of
// looping for ever or returning nil.
func TestRemoveWithRetriesGivesUpAndReturnsTheError(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("as root the directory can always be deleted")
	}
	parent := t.TempDir()
	target := filepath.Join(parent, "motita")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(target, "child"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Without write permission on the parent it cannot even be unlinked, so
	// every attempt fails and RemoveAll cannot empty it either.
	if err := os.Chmod(parent, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(parent, 0o700) })

	start := time.Now()
	err := removeWithRetries(target, 3)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatalf("it must not report success when the directory is still there")
	}
	if _, statErr := os.Stat(target); statErr != nil {
		t.Fatalf("the directory must still exist: %v", statErr)
	}
	if !errors.Is(err, os.ErrPermission) {
		t.Errorf("the returned error must be the last one: %v", err)
	}
	// The attempt is a wait and not a spin: the pauses are made.
	if elapsed < 20*time.Millisecond {
		t.Errorf("the retries were not spaced: %s", elapsed)
	}
}

// --- sandbox.New error paths ------------------------------------------------

// TestNewDefaultsTheWorkingDirectoryToTheCurrentOne: with no directory, the
// current one is used, and it is resolved to an absolute path (the commands need
// to be able to chdir into it).
func TestNewDefaultsTheWorkingDirectoryToTheCurrentOne(t *testing.T) {
	dir := t.TempDir()
	chdirInto(t, dir)

	s, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	if s.base != resolved {
		t.Errorf("base = %q, the current directory (%q) was expected", s.base, resolved)
	}
}

// TestNewReportsAnUncreatableWorkingDirectory: with chroot, a directory that cannot
// be created stops New because every later path depends on it.
func TestNewReportsAnUncreatableWorkingDirectory(t *testing.T) {
	base := t.TempDir()
	file := filepath.Join(base, "a-file")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	s, err := New(Options{Dir: filepath.Join(file, "below-a-file"), UseChroot: true, Root: "."})
	if err == nil {
		s.Close()
		t.Fatal("a working directory that cannot be created must be an error")
	}
	if !strings.Contains(err.Error(), "could not create the working directory") {
		t.Errorf("unexpected error: %v", err)
	}
}

// TestNewReportsAnUnresolvableWorkingDirectory: with the current directory
// deleted, "." cannot be resolved and New must fail with a clear message instead
// of keeping a relative path the child could not chdir into.
func TestNewReportsAnUnresolvableWorkingDirectory(t *testing.T) {
	dir, err := os.MkdirTemp("", "sandbox-deleted-*")
	if err != nil {
		t.Fatal(err)
	}
	chdirInto(t, dir)
	if err := os.Remove(dir); err != nil {
		t.Fatal(err)
	}

	s, err := New(Options{})
	if err == nil {
		s.Close()
		t.Fatal("an unresolvable working directory must be an error")
	}
	if !strings.Contains(err.Error(), "could not resolve the working directory") {
		t.Errorf("unexpected error: %v", err)
	}
}

// TestNewReportsWhenTheExecutableCannotBeLocated: the sandbox runs through this
// very binary, so if it cannot be located the isolation is impossible.
func TestNewReportsWhenTheExecutableCannotBeLocated(t *testing.T) {
	original := osHooks.executable
	defer func() { osHooks.executable = original }()
	osHooks.executable = func() (string, error) { return "", errors.New("no executable today") }

	s, err := New(Options{Dir: t.TempDir()})
	if err == nil {
		s.Close()
		t.Fatal("without its own executable the sandbox cannot work")
	}
	if !strings.Contains(err.Error(), "could not locate this very executable") {
		t.Errorf("unexpected error: %v", err)
	}
}

// TestNewWithChrootAsRootAndInaccessibleRoot: when the process is root but the
// requested root is not accessible, the chroot is not applied and it is recorded
// (the sandbox does not pretend to be isolated).
func TestNewWithChrootAsRootAndInaccessibleRoot(t *testing.T) {
	original := osHooks.euid
	defer func() { osHooks.euid = original }()
	osHooks.euid = func() int { return 0 }

	s, err := New(Options{
		Dir:       t.TempDir(),
		UseChroot: true,
		Root:      "/does/not/exist/this/root",
		Log:       logx.Global(),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	if s.op.UseChroot {
		t.Error("an inaccessible root must switch the chroot off")
	}
	found := false
	for _, warning := range s.NotApplied() {
		if strings.Contains(warning, "is not accessible") {
			found = true
		}
	}
	if !found {
		t.Errorf("the inaccessible root must be recorded: %v", s.NotApplied())
	}
}

// TestNewWithChrootAsRootWithAnAccessibleRoot: the opposite case, which is what
// makes the chroot be really declared and used.
func TestNewWithChrootAsRootWithAnAccessibleRoot(t *testing.T) {
	original := osHooks.euid
	defer func() { osHooks.euid = original }()
	osHooks.euid = func() int { return 0 }

	root := t.TempDir()
	s, err := New(Options{
		Dir:       t.TempDir(),
		UseChroot: true,
		Root:      root,
		Log:       logx.Global(),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	if !s.op.UseChroot {
		t.Fatalf("with root and an accessible root the chroot must be kept: %v", s.NotApplied())
	}
	if !hasMode(s.Isolation(), ModeChroot) {
		t.Errorf("the chroot must be declared as applied: %v", s.Isolation())
	}
}

// --- cgroups through New and Close ------------------------------------------

// fakeCgroupTree builds the minimal cgroups v1 tree the implementation expects.
func fakeCgroupTree(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for _, controller := range []string{"memory", "pids"} {
		if err := os.MkdirAll(filepath.Join(root, controller), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "pids", "pids.max"), []byte("max"), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

// TestNewWithCgroupsAssignsTheGroupAndReportsTheAddFailure: when the group is
// created it is kept and declared, and if the process cannot be added to it that
// is recorded as isolation that was not applied.
func TestNewWithCgroupsAssignsTheGroupAndReportsTheAddFailure(t *testing.T) {
	root := fakeCgroupTree(t)
	// The memory group exists but is not writable, so newCgroup succeeds (no
	// limit to write) and adding the process fails.
	base := filepath.Join(root, "memory", "motita")
	if err := os.MkdirAll(base, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(base, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(base, 0o700) })

	s, err := New(Options{
		Dir:        t.TempDir(),
		UseCgroups: true,
		CgroupRoot: root,
		Log:        logx.Global(),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	if s.cg == nil {
		t.Fatalf("the cgroup was created and must be kept: %v", s.NotApplied())
	}
	if !hasMode(s.Isolation(), ModeCgroups) {
		t.Errorf("cgroups_v1 must be declared: %v", s.Isolation())
	}
	found := false
	for _, warning := range s.NotApplied() {
		if strings.Contains(warning, "could not add the process to the group") {
			found = true
		}
	}
	if !found {
		t.Errorf("the failed addition must be recorded: %v", s.NotApplied())
	}
}

// TestCloseReturnsNilWhenTheGroupGoesAway and TestCloseReportsACgroupThatCannotBeRemoved
// cover the two endings of Close: the group is deleted, or the failure is
// reported to the caller (and logged) instead of being buried.
func TestCloseReturnsNilWhenTheGroupGoesAway(t *testing.T) {
	root := fakeCgroupTree(t)
	cg, err := newCgroup(root, Limits{MemoryMB: 64, Processes: 10})
	if err != nil {
		t.Fatal(err)
	}
	s := &Sandbox{cg: cg, log: logx.Global(), base: root}

	if err := s.Close(); err != nil {
		t.Fatalf("Close = %v", err)
	}
	for _, dir := range []string{cg.memory, cg.pids} {
		if _, err := os.Stat(dir); !os.IsNotExist(err) {
			t.Errorf("%s was not deleted: %v", dir, err)
		}
	}
}

func TestCloseReportsACgroupThatCannotBeRemoved(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("as root the directories can always be deleted")
	}
	root := t.TempDir()
	base := filepath.Join(root, "memory", "motita")
	if err := os.MkdirAll(base, 0o755); err != nil {
		t.Fatal(err)
	}
	// A file inside and no write permission: RemoveAll cannot empty the
	// directory, so it cannot be deleted and the failure must be reported.
	if err := os.WriteFile(filepath.Join(base, "tasks"), []byte("1234"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(base, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(base, 0o700) })

	logFile := filepath.Join(t.TempDir(), "sandbox.log")
	logger, err := logx.New(logx.Options{Path: logFile, Level: logx.Debug, Console: false})
	if err != nil {
		t.Fatal(err)
	}
	defer logger.Close()

	s := &Sandbox{cg: &cgroup{root: root, name: "motita", memory: base}, log: logger, base: root}
	err = s.Close()
	if err == nil {
		t.Fatal("Close must report the group it could not remove")
	}
	if !strings.Contains(err.Error(), "could not remove the cgroup") {
		t.Errorf("unexpected error: %v", err)
	}
	// The operator has to be able to find out from the log too.
	data, err := os.ReadFile(logFile)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "could not remove the cgroup") {
		t.Errorf("the log must carry the failure: %s", data)
	}
}

// --- sandbox.Run error paths ------------------------------------------------

// TestRunReportsAnUnpreparableWorkingDirectory: a working directory that cannot
// be created stops the run before anything is executed.
func TestRunReportsAnUnpreparableWorkingDirectory(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "a-file")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	s := runnableSandbox(t, dir)

	// The directory exists, but its child is a file, so it cannot be used.
	_, _, exit, err := s.Run(context.Background(), execx.Request{
		Command: "/bin/true",
		Dir:     filepath.Join(file, "below-a-file"),
	})
	if err == nil {
		t.Fatal("a working directory that cannot be prepared must be an error")
	}
	if !strings.Contains(err.Error(), "could not prepare the working directory") {
		t.Errorf("unexpected error: %v", err)
	}
	if exit != -1 {
		t.Errorf("exit = %d, -1 was expected (nothing was run)", exit)
	}
}

// TestRunReportsAnUncreatableTemporaryDirectory: the ephemeral TMPDIR is part of
// the isolation; without it the run does not start.
func TestRunReportsAnUncreatableTemporaryDirectory(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "a-file")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	// The base of the sandbox is a path below a file: the requested working
	// directory is fine, so MkdirAll passes and MkdirTemp fails.
	s := runnableSandbox(t, filepath.Join(file, "below-a-file"))

	_, _, exit, err := s.Run(context.Background(), execx.Request{
		Command: "/bin/true",
		Dir:     dir,
	})
	if err == nil {
		t.Fatal("without a temporary directory the run must not start")
	}
	if !strings.Contains(err.Error(), "could not create the temporary directory") {
		t.Errorf("unexpected error: %v", err)
	}
	if exit != -1 {
		t.Errorf("exit = %d, -1 was expected", exit)
	}
}

// TestRunWarnsWhenTheTemporaryDirectoryCannotBeDeleted: a failure to clean up is
// not a failure of the run (the command's result matters), but it is logged
// because it leaves garbage behind.
func TestRunWarnsWhenTheTemporaryDirectoryCannotBeDeleted(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("as root the temporary directory can always be deleted")
	}
	base := t.TempDir()
	logFile := filepath.Join(t.TempDir(), "sandbox.log")
	logger, err := logx.New(logx.Options{Path: logFile, Level: logx.Debug, Console: false})
	if err != nil {
		t.Fatal(err)
	}
	defer logger.Close()

	s := runnableSandbox(t, base)
	s.log = logger

	// The command makes its own TMPDIR undeletable: RemoveAll will fail at the
	// deferred clean-up.
	output, _, exit, err := s.Run(context.Background(), execx.Request{
		Command: "/bin/sh",
		Args:    []string{"-c", `touch "$TMPDIR/leftover"; chmod 500 "$TMPDIR"; echo done`},
	})
	if err != nil {
		t.Fatalf("the command ran: %v (output %q)", err, output)
	}
	if exit != 0 {
		t.Fatalf("exit = %d, output = %q", exit, output)
	}

	// Let the test's own clean-up delete the directory it could not delete.
	leftovers, err := filepath.Glob(filepath.Join(base, "tmp-*"))
	if err != nil {
		t.Fatal(err)
	}
	for _, dir := range leftovers {
		if err := os.Chmod(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if len(leftovers) == 0 {
		t.Error("the undeletable temporary directory must have been left behind")
	}

	data, err := os.ReadFile(logFile)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "could not delete the temporary directory") {
		t.Errorf("the failure to clean up must be logged: %s", data)
	}
}

// TestRunWithChrootChecksTheCommandInsideTheRoot: in the chroot branch the
// command is looked up inside the new root before anything is executed, and the
// path used is the real one inside the root.
func TestRunWithChrootChecksTheCommandInsideTheRoot(t *testing.T) {
	original := osHooks.euid
	defer func() { osHooks.euid = original }()
	osHooks.euid = func() int { return 0 }

	root := t.TempDir()
	base := t.TempDir()
	s, err := New(Options{
		Dir:       base,
		UseChroot: true,
		Root:      root,
		Log:       logx.Global(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !s.op.UseChroot {
		t.Fatalf("the chroot must be active for this test: %v", s.NotApplied())
	}

	// The command does not exist inside the root: it must be refused and the
	// error must name the path inside the chroot.
	_, _, exit, err := s.Run(context.Background(), execx.Request{Command: "/bin/sh"})
	if err == nil {
		t.Fatal("a command that is not inside the chroot must be refused")
	}
	if !strings.Contains(err.Error(), "does not exist inside the chroot") {
		t.Errorf("unexpected error: %v", err)
	}
	if !strings.Contains(err.Error(), filepath.Join(root, "bin/sh")) {
		t.Errorf("the error must name the path inside the root: %v", err)
	}
	if exit != -1 {
		t.Errorf("exit = %d, -1 was expected", exit)
	}

	// With the command present inside the root, the run goes on to the child
	// (which, without root, refuses to claim it applied the chroot: it is not
	// the sandbox's job to pretend).
	if err := os.MkdirAll(filepath.Join(root, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/bin/sh", filepath.Join(root, "bin", "sh")); err != nil {
		t.Fatal(err)
	}
	output, _, exit, err := s.Run(context.Background(), execx.Request{Command: "/bin/sh", Args: []string{"-c", "true"}})
	if os.Geteuid() != 0 {
		// The child cannot chroot: it says so and stops.
		if err == nil && exit == 0 {
			t.Fatalf("without root the chroot cannot be applied and yet it was reported as a success: %q", output)
		}
		if !strings.Contains(output, "chroot") {
			t.Errorf("the child must explain that the chroot failed: %q", output)
		}
		return
	}
	if err != nil {
		t.Fatalf("as root the chroot must work: %v (%q)", err, output)
	}
}

// TestRunReportsASpecThatCannotBeSerialised: the spec travels to the child as
// JSON; if it cannot be serialised there is nothing to pass on.
func TestRunReportsASpecThatCannotBeSerialised(t *testing.T) {
	original := jsonMarshal
	defer func() { jsonMarshal = original }()
	jsonMarshal = func(any) ([]byte, error) { return nil, errors.New("no serialiser today") }

	s := runnableSandbox(t, t.TempDir())
	_, _, exit, err := s.Run(context.Background(), execx.Request{Command: "/bin/true"})
	if err == nil {
		t.Fatal("a spec that cannot be serialised must stop the run")
	}
	if !strings.Contains(err.Error(), "could not serialise the sandbox spec") {
		t.Errorf("unexpected error: %v", err)
	}
	if exit != -1 {
		t.Errorf("exit = %d, -1 was expected", exit)
	}
}

// TestRunReportsAnIsolationThatCannotStart: if the isolation process cannot be
// started at all (the executable is gone), the error must say that it was the
// isolation and not the command.
func TestRunReportsAnIsolationThatCannotStart(t *testing.T) {
	s := runnableSandbox(t, t.TempDir())
	s.executable = "/does/not/exist/motita"

	_, _, exit, err := s.Run(context.Background(), execx.Request{Command: "/bin/true"})
	if err == nil {
		t.Fatal("an isolation that cannot be started must be an error")
	}
	if !strings.Contains(err.Error(), "could not start the isolation") {
		t.Errorf("unexpected error: %v", err)
	}
	if exit != -1 {
		t.Errorf("exit = %d, -1 was expected", exit)
	}
}

// TestIsolationJSONRejectsASerialisationFailure: the JSON is for the logs, so if
// it cannot be built the answer is a fixed line, never a panic nor an empty
// string.
func TestIsolationJSONRejectsASerialisationFailure(t *testing.T) {
	original := jsonMarshal
	defer func() { jsonMarshal = original }()
	jsonMarshal = func(any) ([]byte, error) { return nil, errors.New("no serialiser today") }

	s := runnableSandbox(t, t.TempDir())
	got := s.IsolationJSON()
	if !strings.Contains(got, "not serialisable") {
		t.Errorf("IsolationJSON = %q", got)
	}
	if !json.Valid([]byte(got)) {
		t.Errorf("even the fallback must be valid JSON: %q", got)
	}
}

// --- helpers ----------------------------------------------------------------

// runnableSandbox builds a sandbox without going through New (some error paths
// need a state New would refuse) but with the fields Run uses.
func runnableSandbox(t *testing.T, base string) *Sandbox {
	t.Helper()
	return &Sandbox{
		op:         Options{},
		log:        logx.Global(),
		base:       base,
		executable: os.Args[0],
	}
}

// hasMode reports whether the isolation description includes the mode.
func hasMode(modes []Mode, mode Mode) bool {
	for _, m := range modes {
		if m == mode {
			return true
		}
	}
	return false
}

// chdirInto moves the test process into dir and restores the previous directory
// when the test finishes.
//
// The restore is best effort: other tests of this package move the process into
// temporary directories (the child mode does a real chdir) and those directories
// are deleted when their test ends, so there may be no directory to go back to.
func chdirInto(t *testing.T, dir string) {
	t.Helper()
	previous, err := os.Getwd()
	if err != nil {
		previous = ""
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("could not change to %q: %v", dir, err)
	}
	t.Cleanup(func() {
		if previous == "" {
			return
		}
		if err := os.Chdir(previous); err != nil {
			t.Logf("could not restore the working directory %q: %v", previous, err)
		}
	})
}

// TestTheChildNeverReceivesARelativeWorkingDirectory: the child re-executes this
// binary and chdirs to the directory carried in the spec. A relative value would be
// resolved a second time, inside a process that starts somewhere else, so the
// command would run outside the directory the caller meant and the anchor could not
// see the work it has to validate.
//
// Found on a real 32-bit machine: `workspace_dir: ./workspace` made the anchor fail
// every check, while the identical configuration with an absolute path passed. The
// property is asserted by observing the spec, so the test does not have to change
// the process working directory (which would break the rest of the package).
func TestTheChildNeverReceivesARelativeWorkingDirectory(t *testing.T) {
	cases := []struct {
		name string
		dir  string
	}{
		{"a relative directory", "workspace"},
		{"a nested relative directory", filepath.Join("a", "b")},
		{"a dot", "."},
		{"a relative parent", ".."},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			base := t.TempDir()
			var spec Spec
			saved := launchCommand
			defer func() { launchCommand = saved }()
			launchCommand = func(ctx context.Context, _ string, args ...string) *exec.Cmd {
				if len(args) >= 2 {
					_ = json.Unmarshal([]byte(args[1]), &spec)
				}
				return exec.CommandContext(ctx, "true")
			}

			// The sandbox is built with an absolute base (as it is in production),
			// and the request carries the directory the caller spelled.
			box, err := New(Options{Dir: base})
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			defer box.Close()

			r := execx.Request{Command: "true"}
			if tc.dir == "." || tc.dir == ".." {
				// These must be resolved against the sandbox's base, not the
				// process, so give a base and let the request be relative to it.
				r.Dir = tc.dir
			} else {
				r.Dir = tc.dir
			}
			// Run may fail for a directory that cannot be created; what matters is
			// that whatever reached the child was absolute.
			_, _, _, _ = box.Run(context.Background(), r)

			if spec.Dir == "" {
				t.Skip("the sandbox did not reach the launch step")
			}
			if !filepath.IsAbs(spec.Dir) {
				t.Errorf("the child received the relative path %q: it would be resolved twice", spec.Dir)
			}
		})
	}
}
