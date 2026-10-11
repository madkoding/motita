//go:build linux

package sandbox

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/madkoding/motita/internal/execx"
)

// fakeCgroupV2 points the run groups at a directory standing in for the unified tree, with this
// process in group /self.
func fakeCgroupV2(t *testing.T) (mount string) {
	t.Helper()
	mount = t.TempDir()
	if err := os.MkdirAll(filepath.Join(mount, "self"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(mount, "self", "cgroup.procs"), "")
	self := filepath.Join(t.TempDir(), "cgroup")
	writeFile(t, self, "4:memory:/x\n0::/self\n")
	oldMounts, oldSelf := cgroupV2Mounts, selfCgroupFile
	cgroupV2Mounts, selfCgroupFile = []string{filepath.Join(mount, "missing"), mount}, self
	t.Cleanup(func() { cgroupV2Mounts, selfCgroupFile = oldMounts, oldSelf })
	return mount
}

func writeFile(t *testing.T, path, data string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestOwnCgroupV2Dir(t *testing.T) {
	mount := fakeCgroupV2(t)
	if got := ownCgroupV2Dir(); got != filepath.Join(mount, "self") {
		t.Errorf("own group: %q", got)
	}
	// v1 only: no "0::" line.
	writeFile(t, selfCgroupFile, "4:memory:/x\n")
	if got := ownCgroupV2Dir(); got != "" {
		t.Errorf("v1 only: %q", got)
	}
	// A unified group that is not mounted where it is looked for.
	writeFile(t, selfCgroupFile, "0::/elsewhere\n")
	if got := ownCgroupV2Dir(); got != "" {
		t.Errorf("not mounted: %q", got)
	}
	selfCgroupFile = filepath.Join(t.TempDir(), "missing")
	if got := ownCgroupV2Dir(); got != "" {
		t.Errorf("no /proc: %q", got)
	}
}

func TestNewRunGroup(t *testing.T) {
	mount := fakeCgroupV2(t)
	g := newRunGroup()
	if g == nil || filepath.Dir(g.dir) != filepath.Join(mount, "self") {
		t.Fatalf("a group under this process's: %+v", g)
	}
	if err := g.remove(); err != nil {
		t.Fatal(err)
	}

	// The name is taken: no group.
	next := filepath.Join(mount, "self", "motita-run-"+strconv.Itoa(os.Getpid())+"-"+strconv.FormatUint(runGroupSeq.Load()+1, 10))
	if err := os.Mkdir(next, 0o755); err != nil {
		t.Fatal(err)
	}
	if g := newRunGroup(); g != nil {
		t.Errorf("a group over an existing directory: %+v", g)
	}

	// It cannot be opened: no group, and nothing left behind.
	old := openRunGroup
	openRunGroup = func(string) (int, error) { return -1, errors.New("boom") }
	t.Cleanup(func() { openRunGroup = old })
	if g := newRunGroup(); g != nil {
		t.Errorf("a group that cannot be opened: %+v", g)
	}
	entries, _ := os.ReadDir(filepath.Join(mount, "self"))
	if len(entries) != 2 {
		t.Errorf("a group that could not be opened was left behind: %v", entries)
	}

	// No unified tree at all.
	selfCgroupFile = filepath.Join(t.TempDir(), "missing")
	if g := newRunGroup(); g != nil {
		t.Errorf("no cgroup v2: %+v", g)
	}
}

func TestAttachStartsTheChildInTheGroup(t *testing.T) {
	var none *runGroup
	if none.attach(nil) != nil {
		t.Error("no group: the attributes as they are")
	}
	g := &runGroup{fd: 7}
	if a := g.attach(nil); !a.UseCgroupFD || a.CgroupFD != 7 {
		t.Errorf("no attributes: %+v", a)
	}
	attr := &syscall.SysProcAttr{Setpgid: true}
	if a := g.attach(attr); !a.UseCgroupFD || a.CgroupFD != 7 || !a.Setpgid || attr.UseCgroupFD {
		t.Errorf("the attributes are kept and not changed in place: %+v %+v", a, attr)
	}
}

func TestKillEndsEveryMember(t *testing.T) {
	var none *runGroup
	none.kill()

	// cgroup.kill does it in one write.
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "cgroup.kill"), "")
	(&runGroup{dir: dir}).kill()
	if data, _ := os.ReadFile(filepath.Join(dir, "cgroup.kill")); string(data) != "1" {
		t.Errorf("cgroup.kill: %q", data)
	}

	// Without it (before Linux 5.14) every listed member is killed.
	sleeper := exec.Command("sleep", "60")
	if err := sleeper.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- sleeper.Wait() }()
	dir = t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "cgroup.kill"), 0o755); err != nil { // cannot be written
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(dir, "cgroup.procs"), "x\n"+strconv.Itoa(sleeper.Process.Pid)+"\n")
	(&runGroup{dir: dir}).kill()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		_ = sleeper.Process.Kill()
		t.Error("a listed member survived")
	}

	// An empty group, or one already gone: nothing to do.
	writeFile(t, filepath.Join(dir, "cgroup.procs"), "")
	(&runGroup{dir: dir}).kill()
	(&runGroup{dir: filepath.Join(dir, "gone")}).kill()
}

func TestRemoveDeletesTheGroup(t *testing.T) {
	var none *runGroup
	if err := none.remove(); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(t.TempDir(), "g")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := (&runGroup{dir: dir, fd: -1}).remove(); err != nil || fileExists(dir) {
		t.Errorf("removed: %v", err)
	}
	if err := (&runGroup{dir: dir, fd: -1}).remove(); err != nil {
		t.Errorf("already gone: %v", err)
	}

	// Members that never go: the group stays, and that is said.
	old := runGroupRemoveTries
	runGroupRemoveTries = 2
	t.Cleanup(func() { runGroupRemoveTries = old })
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(dir, "member"), "")
	if err := (&runGroup{dir: dir, fd: -1}).remove(); err == nil || !strings.Contains(err.Error(), dir) {
		t.Errorf("a group that cannot be removed: %v", err)
	}
}

// A kernel that will not start the child inside the group (here: the "group" is a plain
// directory) does not cost the run: it runs without one, and later runs do not try again.
func TestARunWithoutARunGroupStillRuns(t *testing.T) {
	fakeCgroupV2(t)
	noRunGroups.Store(false)
	t.Cleanup(func() { noRunGroups.Store(false) })
	s, err := New(Options{Dir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	out, _, exit, err := s.Run(context.Background(), execx.Request{Command: "/bin/sh", Args: []string{"-c", "echo hi"}})
	if err != nil || exit != 0 || strings.TrimSpace(out) != "hi" {
		t.Fatalf("out %q exit %d err %v", out, exit, err)
	}
	if !noRunGroups.Load() || s.newRunGroup() != nil {
		t.Error("a group that cannot hold a child is not tried again")
	}
}

// A process the command detached with setsid left its process group, and used to outlive the
// run. Where this machine lets motita make a cgroup v2 group, it is killed with the rest.
func TestARunTakesItsDetachedProcessesWithIt(t *testing.T) {
	if _, err := exec.LookPath("setsid"); err != nil {
		t.Skip("no setsid")
	}
	probe := newRunGroup()
	if probe == nil {
		t.Skip("no writable cgroup v2 here")
	}
	_ = probe.remove()
	noRunGroups.Store(false)
	t.Cleanup(func() { noRunGroups.Store(false) })

	work := t.TempDir()
	s, err := New(Options{Dir: work})
	if err != nil {
		t.Fatal(err)
	}
	pidFile := filepath.Join(work, "pid")
	script := "setsid sh -c 'echo $$ > " + pidFile + "; exec sleep 60' </dev/null >/dev/null 2>&1 & " +
		"while [ ! -s " + pidFile + " ]; do sleep 0.05; done"
	if _, _, _, err := s.Run(context.Background(), execx.Request{Command: "/bin/sh", Args: []string{"-c", script}}); err != nil {
		t.Fatal(err)
	}
	if noRunGroups.Load() {
		t.Skip("this kernel cannot start a child inside a cgroup")
	}
	data, _ := os.ReadFile(pidFile)
	pid, convErr := strconv.Atoi(strings.TrimSpace(string(data)))
	if convErr != nil {
		t.Fatalf("no pid in %q", data)
	}
	deadline := time.Now().Add(5 * time.Second)
	for running(pid) {
		if time.Now().After(deadline) {
			_ = syscall.Kill(pid, syscall.SIGKILL)
			t.Fatalf("the detached process %d survived the run", pid)
		}
		time.Sleep(20 * time.Millisecond)
	}
}
