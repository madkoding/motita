//go:build linux

package sandbox

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// In cgroups v1, `tasks` takes a thread id and moves only that thread. Written with motita's
// pid, it moved the main thread alone, and the commands, forked from other threads, ran outside
// the limits. `cgroup.procs` moves the whole process.
func TestAddProcessMovesTheWholeProcess(t *testing.T) {
	root := fakeCgroupTree(t)
	for _, controller := range []string{"memory", "pids"} {
		dir := filepath.Join(root, controller, "motita")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		for _, file := range []string{"tasks", "cgroup.procs"} {
			if err := os.WriteFile(filepath.Join(dir, file), nil, 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	cg, err := newCgroup(root, Limits{Processes: 10})
	if err != nil {
		t.Fatal(err)
	}
	if err := cg.addProcess(1234); err != nil {
		t.Fatalf("addProcess: %v", err)
	}
	for _, dir := range []string{cg.memory, cg.pids} {
		procs, _ := os.ReadFile(filepath.Join(dir, "cgroup.procs"))
		tasks, _ := os.ReadFile(filepath.Join(dir, "tasks"))
		if strings.TrimSpace(string(procs)) != "1234" || len(tasks) != 0 {
			t.Errorf("%s: cgroup.procs = %q, tasks = %q; want the pid in cgroup.procs", dir, procs, tasks)
		}
	}
}
