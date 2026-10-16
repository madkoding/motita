//go:build linux

package sandbox

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/madkoding/motita/internal/logx"
)

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
	if os.Geteuid() == 0 {
		t.Skip("as root a read-only directory can still be written")
	}
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
	if os.Geteuid() == 0 {
		t.Skip("as root a read-only directory can still be written")
	}
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
	if os.Geteuid() == 0 {
		t.Skip("as root a read-only directory can still be written")
	}
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
