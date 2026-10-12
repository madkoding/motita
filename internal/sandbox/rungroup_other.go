//go:build !linux

package sandbox

import "syscall"

// runGroup has no counterpart outside Linux: a run is contained by its process group.
type runGroup struct{}

func newRunGroup() *runGroup { return nil }

func (g *runGroup) attach(attr *syscall.SysProcAttr) *syscall.SysProcAttr { return attr }
func (g *runGroup) kill() {
	// No cgroup to kill outside Linux: the process group is ended by the caller.
}
func (g *runGroup) remove() error { return nil }
