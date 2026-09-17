//go:build !linux

package sandbox

import "fmt"

// cgroup does not exist outside Linux.
type cgroup struct{}

func newCgroup(root string, l Limits) (*cgroup, error) {
	return nil, fmt.Errorf("cgroups only exist on Linux")
}

func (c *cgroup) addProcess(pid int) error { return nil }
func (c *cgroup) remove() error            { return nil }
