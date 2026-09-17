//go:build linux

package sandbox

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// cgroup envuelve un grupo de cgroups v1 (memory + pids).
//
// Los límites de memoria por setrlimit son aproximados: RLIMIT_AS acota el
// espacio de direcciones, no la memoria residente, así que un programa que hace
// mmap de mucho y toca poco pasa el filtro. cgroups es lo que da una cota real.
// Se usa v1 porque es lo que aparece tanto en kernels antiguos (habituales en
// máquinas i386) como en muchos contenedores; en v2 el árbol es unificado y esta
// implementación lo detecta y se declara no disponible en lugar de fallar.
type cgroup struct {
	raiz    string
	nombre  string
	memoria string
	pids    string
}

// nuevoCgroup crea el grupo con los límites pedidos.
func nuevoCgroup(raiz string, l Limites) (*cgroup, error) {
	if raiz == "" {
		raiz = "/sys/fs/cgroup"
	}

	// En un sistema v2 unificado no existe el árbol por controlador.
	if _, err := os.Stat(filepath.Join(raiz, "memory")); err != nil {
		return nil, fmt.Errorf("no parece haber cgroups v1 en %s (falta el controlador memory)", raiz)
	}
	base := filepath.Join(raiz, "memory", "starlight")
	if err := os.MkdirAll(base, 0o755); err != nil {
		return nil, fmt.Errorf("sin permiso para crear el cgroup en %s: %w", base, err)
	}

	cg := &cgroup{
		raiz:    raiz,
		nombre:  "starlight",
		memoria: base,
		pids:    filepath.Join(raiz, "pids", "starlight"),
	}

	if l.MemoriaMB > 0 {
		if err := escribirLimite(filepath.Join(cg.memoria, "memory.limit_in_bytes"), strconv.Itoa(l.MemoriaMB<<20)); err != nil {
			return nil, err
		}
	}
	if l.Procesos > 0 {
		if _, err := os.Stat(filepath.Join(raiz, "pids", "pids.max")); err == nil {
			if err := os.MkdirAll(cg.pids, 0o755); err == nil {
				if err := escribirLimite(filepath.Join(cg.pids, "pids.max"), strconv.Itoa(l.Procesos)); err != nil {
					// El límite de PIDs es opcional: se sigue con el de memoria.
					cg.pids = ""
				}
			}
		} else {
			cg.pids = ""
		}
	}
	return cg, nil
}

func escribirLimite(ruta, valor string) error {
	if err := os.WriteFile(ruta, []byte(valor), 0o644); err != nil {
		return fmt.Errorf("no se pudo escribir el límite %s: %w", ruta, err)
	}
	return nil
}

// agregarProceso mete un PID en el grupo (el propio agente lo usa para que sus
// hijos hereden la pertenencia).
func (c *cgroup) agregarProceso(pid int) error {
	rutas := []string{filepath.Join(c.memoria, "tasks")}
	if c.pids != "" {
		rutas = append(rutas, filepath.Join(c.pids, "tasks"))
	}
	var ultimo error
	for _, r := range rutas {
		if err := os.WriteFile(r, []byte(strconv.Itoa(pid)), 0o644); err != nil {
			ultimo = err
		}
	}
	return ultimo
}

// eliminar vacía y borra el grupo.
func (c *cgroup) eliminar() error {
	var problemas []string
	for _, dir := range []string{c.pids, c.memoria} {
		if dir == "" {
			continue
		}
		// Los controladores se borran escribiendo en cgroup.procs/tasks vacíos
		// y luego rmdir; en v1 basta con rmdir si no queda ningún proceso.
		if err := os.Remove(dir); err != nil && !os.IsNotExist(err) {
			problemas = append(problemas, fmt.Sprintf("%s: %v", dir, err))
		}
	}
	if len(problemas) > 0 {
		return fmt.Errorf("no se pudo eliminar el cgroup: %s", strings.Join(problemas, "; "))
	}
	return nil
}
