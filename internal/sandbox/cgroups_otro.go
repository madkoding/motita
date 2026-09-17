//go:build !linux

package sandbox

import "fmt"

// cgroup no existe fuera de Linux.
type cgroup struct{}

func nuevoCgroup(raiz string, l Limites) (*cgroup, error) {
	return nil, fmt.Errorf("cgroups sólo existen en Linux")
}

func (c *cgroup) agregarProceso(pid int) error { return nil }
func (c *cgroup) eliminar() error              { return nil }
