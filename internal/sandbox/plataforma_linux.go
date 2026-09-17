//go:build linux

package sandbox

import (
	"fmt"
	"os/exec"
	"syscall"
)

// Limites por proceso (Linux).
type Limites struct {
	MemoriaMB        int
	CPUSegundos      int
	Procesos         int
	ArchivosAbiertos int
	TamanoArchivoMB  int
	SinRed           bool // CLONE_NEWNET: sin red para el hijo
}

// atributosHijo prepara SysProcAttr y devuelve los avisos de lo que no se pudo
// aplicar, para no mentir sobre el aislamiento efectivo.
func atributosHijo(l Limites, dropPrivs bool, uid, gid int) (*syscall.SysProcAttr, []string) {
	attr := &syscall.SysProcAttr{
		// Grupo propio: permite matar a todos los descendientes de una vez.
		Setpgid: true,
	}
	var avisos []string

	if l.SinRed {
		// CLONE_NEWNET necesita CAP_SYS_ADMIN. Si falla el clone, el error del
		// exec lo dirá; no se silencia.
		attr.Cloneflags |= syscall.CLONE_NEWNET
	}
	if dropPrivs {
		attr.Credential = &syscall.Credential{Uid: uint32(uid), Gid: uint32(gid)}
	}
	return attr, avisos
}

// rlimitNPROC es RLIMIT_NPROC (valor 6 en Linux). El paquete syscall de Go no
// lo declara —sólo está en golang.org/x/sys/unix, que es una dependencia
// externa— así que se declara aquí con el valor de <asm-generic/resource.h>.
const rlimitNPROC = 6

// hayLimites indica si hace falta el proceso hijo que aplica setrlimit.
func hayLimites(l Limites) bool {
	return l.MemoriaMB > 0 || l.CPUSegundos > 0 || l.Procesos > 0 ||
		l.ArchivosAbiertos > 0 || l.TamanoArchivoMB > 0
}

// aplicarRlimits se ejecuta DENTRO del proceso hijo antes del exec definitivo.
//
// Se aplica aquí, y no con el utilitario `prlimit`, para no depender de que
// util-linux esté instalado en la máquina i386 (a menudo no lo está) ni de
// privilegios. setrlimit no requiere ninguno.
func aplicarRlimits(l Limites) error {
	if l.CPUSegundos > 0 {
		if err := syscall.Setrlimit(syscall.RLIMIT_CPU, &syscall.Rlimit{
			Cur: uint64(l.CPUSegundos), Max: uint64(l.CPUSegundos + 5),
		}); err != nil {
			return fmt.Errorf("RLIMIT_CPU: %w", err)
		}
	}
	if l.MemoriaMB > 0 {
		bytes := uint64(l.MemoriaMB) << 20
		if err := syscall.Setrlimit(syscall.RLIMIT_AS, &syscall.Rlimit{Cur: bytes, Max: bytes}); err != nil {
			return fmt.Errorf("RLIMIT_AS: %w", err)
		}
		// El volcado de núcleo de un proceso que agota la memoria puede llenar
		// el disco de una máquina pequeña: se desactiva.
		_ = syscall.Setrlimit(syscall.RLIMIT_CORE, &syscall.Rlimit{Cur: 0, Max: 0})
	}
	if l.Procesos > 0 {
		// syscall.Rlimit no está tipado para este recurso en la biblioteca
		// estándar, pero el tipo subyacente es el mismo {Cur,Max} uint64.
		lim := syscall.Rlimit{Cur: uint64(l.Procesos), Max: uint64(l.Procesos)}
		if err := syscall.Setrlimit(rlimitNPROC, &lim); err != nil {
			return fmt.Errorf("RLIMIT_NPROC: %w", err)
		}
	}
	if l.ArchivosAbiertos > 0 {
		if err := syscall.Setrlimit(syscall.RLIMIT_NOFILE, &syscall.Rlimit{
			Cur: uint64(l.ArchivosAbiertos), Max: uint64(l.ArchivosAbiertos),
		}); err != nil {
			return fmt.Errorf("RLIMIT_NOFILE: %w", err)
		}
	}
	if l.TamanoArchivoMB > 0 {
		bytes := uint64(l.TamanoArchivoMB) << 20
		if err := syscall.Setrlimit(syscall.RLIMIT_FSIZE, &syscall.Rlimit{Cur: bytes, Max: bytes}); err != nil {
			return fmt.Errorf("RLIMIT_FSIZE: %w", err)
		}
	}
	return nil
}

// entrarChroot y bajarPrivilegios requieren root; se aíslan aquí para que el
// error sea explícito cuando no lo hay.
func entrarChroot(raiz, dir string) error {
	if raiz == "" {
		return nil
	}
	if err := syscall.Chroot(raiz); err != nil {
		return fmt.Errorf("chroot(%q): %w", raiz, err)
	}
	if dir == "" {
		dir = "/"
	}
	if err := syscall.Chdir(dir); err != nil {
		return fmt.Errorf("chdir(%q) tras chroot: %w", dir, err)
	}
	return nil
}

func bajarPrivilegios(uid, gid int) error {
	// El orden importa: primero el grupo, después los grupos suplementarios,
	// por último el usuario (una vez cambiado el uid ya no se puede cambiar el
	// gid).
	if err := syscall.Setgroups([]int{gid}); err != nil {
		return fmt.Errorf("setgroups: %w", err)
	}
	if err := syscall.Setgid(gid); err != nil {
		return fmt.Errorf("setgid(%d): %w", gid, err)
	}
	if err := syscall.Setuid(uid); err != nil {
		return fmt.Errorf("setuid(%d): %w", uid, err)
	}
	return nil
}

var _ = exec.Command
