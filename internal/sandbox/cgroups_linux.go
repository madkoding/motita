//go:build linux

package sandbox

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// cgroup wraps a cgroups v1 group (memory + pids).
//
// Memory limits through setrlimit are approximate: RLIMIT_AS bounds the address
// space, not the resident memory, so a program that mmaps a lot and touches
// little passes the filter. cgroups is what gives a real bound. v1 is used
// because that is what appears both on old kernels (common on i386 machines) and
// in many containers; in v2 the tree is unified and this implementation detects
// that and declares itself unavailable instead of failing.
type cgroup struct {
	root   string
	name   string
	memory string
	pids   string
}

// cgroupName is unique per agent process: with one fixed name, concurrent agents
// shared one group (its limits and its accounting) and one agent's Close tore
// down the group another was still using.
var cgroupName = "starlight-" + strconv.Itoa(os.Getpid())

// newCgroup creates the group with the requested limits.
func newCgroup(root string, l Limits) (*cgroup, error) {
	if root == "" {
		root = "/sys/fs/cgroup"
	}

	// On a unified v2 system there is no per-controller tree.
	if _, err := os.Stat(filepath.Join(root, "memory")); err != nil {
		return nil, fmt.Errorf("there does not seem to be cgroups v1 in %s (the memory controller is missing)", root)
	}
	base := filepath.Join(root, "memory", cgroupName)
	if err := os.MkdirAll(base, 0o755); err != nil {
		return nil, fmt.Errorf("no permission to create the cgroup in %s: %w", base, err)
	}

	// The pids path is only set when the pids group was really created: with it
	// set but non-existent, addProcess and remove would operate on a group that
	// does not exist (which used to be reported as a failure to add the process,
	// a false alarm: the memory limit could not be applied at all).
	cg := &cgroup{
		root:   root,
		name:   cgroupName,
		memory: base,
	}

	if l.MemoryMB > 0 {
		if err := writeLimit(filepath.Join(cg.memory, "memory.limit_in_bytes"), strconv.Itoa(l.MemoryMB<<20)); err != nil {
			return nil, err
		}
	}
	if l.Processes > 0 {
		// On a unified v2 system there is no pids tree.
		if _, statErr := os.Stat(filepath.Join(root, "pids", "pids.max")); statErr == nil {
			pids := filepath.Join(root, "pids", cgroupName)
			// The PIDs limit is optional: if the group cannot be created or the
			// limit cannot be written, it carries on with the memory one.
			if err := os.MkdirAll(pids, 0o755); err == nil {
				if err := writeLimit(filepath.Join(pids, "pids.max"), strconv.Itoa(l.Processes)); err == nil {
					cg.pids = pids
				}
			}
		}
	}
	return cg, nil
}

func writeLimit(path, value string) error {
	if err := os.WriteFile(path, []byte(value), 0o644); err != nil {
		return fmt.Errorf("could not write the limit %s: %w", path, err)
	}
	return nil
}

// addProcess puts a PID into the group (the agent itself uses this so that its
// children inherit the membership).
func (c *cgroup) addProcess(pid int) error {
	paths := []string{filepath.Join(c.memory, "tasks")}
	if c.pids != "" {
		paths = append(paths, filepath.Join(c.pids, "tasks"))
	}
	var last error
	for _, p := range paths {
		if err := os.WriteFile(p, []byte(strconv.Itoa(pid)), 0o644); err != nil {
			last = err
		}
	}
	return last
}

// remove empties and deletes the group.
//
// A v1 group cannot be removed while it has members (rmdir returns EBUSY), and
// the agent itself joined it in New. Writing an empty list to tasks moves
// nothing: a task only leaves a group when its id is written into another
// group's tasks file. So every task still listed is moved to the parent group
// first, the removal is retried and, if it still cannot be done, the error is
// reported instead of silenced.
func (c *cgroup) remove() error {
	var problems []string

	for _, dir := range []string{c.pids, c.memory} {
		if dir == "" {
			continue
		}
		// 1) Move the members to the parent, one id per write (that is what
		// the kernel accepts). A task that cannot be moved (it already exited)
		// must not prevent the removal attempt.
		if data, err := os.ReadFile(filepath.Join(dir, "tasks")); err == nil {
			parent := filepath.Join(filepath.Dir(dir), "tasks")
			for _, id := range strings.Fields(string(data)) {
				_ = os.WriteFile(parent, []byte(id), 0o644)
			}
		}

		// 2) Delete the directory, retrying: the kernel may take a while to
		// delete the internal files.
		if err := removeWithRetries(dir, 3); err != nil && !os.IsNotExist(err) {
			problems = append(problems, fmt.Sprintf("%s: %v", dir, err))
		}
	}

	if len(problems) > 0 {
		return fmt.Errorf("could not remove the cgroup: %s", strings.Join(problems, "; "))
	}
	return nil
}

// removeWithRetries makes several attempts with a short wait between them.
//
// RemoveAll is used and not Remove: in a real cgroup the kernel does not expose
// the control files as directory entries, so Remove would be enough, but
// RemoveAll also cleans up any leftovers inside, which avoids the "directory not
// empty" failure that left the group orphaned for ever (the failure reproduced
// when trying the logic on a directory tree).
func removeWithRetries(dir string, attempts int) error {
	var last error
	for i := 0; i < attempts; i++ {
		last = os.RemoveAll(dir)
		if last == nil || os.IsNotExist(last) {
			return nil
		}
		time.Sleep(10 * time.Millisecond)
	}
	return last
}
