//go:build linux

package sandbox

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"time"
)

// runGroup is a cgroup v2 group one run's child is started in, so everything the command starts
// can be killed when it ends: the process group alone misses a descendant that called setsid,
// which outlives the run with its limits and its temporary directory.
//
// It needs a writable cgroup v2 tree (root, or a delegated user slice). Without one newRunGroup
// answers nil, and the run is contained by its process group as before.
type runGroup struct {
	dir string
	fd  int
}

var (
	// cgroupV2Mounts are where the unified tree is mounted: on its own, or beside v1 (hybrid).
	cgroupV2Mounts = []string{"/sys/fs/cgroup", "/sys/fs/cgroup/unified"}
	// selfCgroupFile says which group this process is in.
	selfCgroupFile = "/proc/self/cgroup"
	// runGroupSeq tells apart the groups of the runs of one process.
	runGroupSeq atomic.Uint64
	// openRunGroup opens the group's directory, for clone3 to start the child in.
	openRunGroup = func(dir string) (int, error) {
		return syscall.Open(dir, syscall.O_RDONLY|syscall.O_DIRECTORY|syscall.O_CLOEXEC, 0)
	}
	// runGroupRemoveTries bounds the wait for killed members to be gone before the group can go.
	runGroupRemoveTries = 50
)

// newRunGroup creates a group under the one this process is in, or answers nil.
func newRunGroup() *runGroup {
	base := ownCgroupV2Dir()
	if base == "" {
		return nil
	}
	dir := filepath.Join(base, fmt.Sprintf("motita-run-%d-%d", os.Getpid(), runGroupSeq.Add(1)))
	if err := os.Mkdir(dir, 0o755); err != nil {
		return nil
	}
	fd, err := openRunGroup(dir)
	if err != nil {
		_ = os.Remove(dir)
		return nil
	}
	return &runGroup{dir: dir, fd: fd}
}

// ownCgroupV2Dir is the directory of this process's cgroup v2 group, or "" when there is none.
func ownCgroupV2Dir() string {
	data, err := os.ReadFile(selfCgroupFile)
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(data), "\n") {
		path, ok := strings.CutPrefix(line, "0::")
		if !ok {
			continue
		}
		for _, mount := range cgroupV2Mounts {
			dir := filepath.Join(mount, path)
			if fileExists(filepath.Join(dir, "cgroup.procs")) {
				return dir
			}
		}
	}
	return ""
}

// attach is attr with the child started inside the group (clone3's CLONE_INTO_CGROUP): it is a
// member from its first instruction, so nothing it starts can leave before it joined.
func (g *runGroup) attach(attr *syscall.SysProcAttr) *syscall.SysProcAttr {
	if g == nil {
		return attr
	}
	var a syscall.SysProcAttr
	if attr != nil {
		a = *attr
	}
	a.UseCgroupFD = true
	a.CgroupFD = g.fd
	return &a
}

// kill ends every member of the group, the ones that left the process group too.
func (g *runGroup) kill() {
	if g == nil {
		return
	}
	f, err := os.OpenFile(filepath.Join(g.dir, "cgroup.kill"), os.O_WRONLY, 0)
	if err == nil {
		_, err = f.WriteString("1")
		_ = f.Close()
	}
	if err == nil {
		return
	}
	// Before Linux 5.14 there is no cgroup.kill: the members are killed one by one, and the list
	// read again, since one may have forked while it was read.
	for i := 0; i < 10; i++ {
		data, err := os.ReadFile(filepath.Join(g.dir, "cgroup.procs"))
		pids := strings.Fields(string(data))
		if err != nil || len(pids) == 0 {
			return
		}
		for _, p := range pids {
			if pid, err := strconv.Atoi(p); err == nil {
				_ = syscall.Kill(pid, syscall.SIGKILL)
			}
		}
	}
}

// remove deletes the group. Members killed a moment ago may not be gone yet, and the kernel
// refuses to remove a group that has any: it is retried for a while.
func (g *runGroup) remove() error {
	if g == nil {
		return nil
	}
	_ = syscall.Close(g.fd)
	var err error
	for i := 0; i < runGroupRemoveTries; i++ {
		if err = syscall.Rmdir(g.dir); err == nil || errors.Is(err, syscall.ENOENT) {
			return nil
		}
		time.Sleep(10 * time.Millisecond)
	}
	return fmt.Errorf("could not remove the run's cgroup %s: %w", g.dir, err)
}
