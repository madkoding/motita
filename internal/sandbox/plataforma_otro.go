//go:build !linux

package sandbox

import (
	"fmt"
	"os/exec"
	"syscall"
)

// Limites por proceso (fuera de Linux: sólo se aísla el directorio efímero).
type Limites struct {
	MemoriaMB        int
	CPUSegundos      int
	Procesos         int
	ArchivosAbiertos int
	TamanoArchivoMB  int
	SinRed           bool
}

func atributosHijo(l Limites, dropPrivs bool, uid, gid int) (*syscall.SysProcAttr, []string) {
	return nil, []string{"esta plataforma no soporta setrlimit, chroot ni espacios de nombres"}
}

func hayLimites(l Limites) bool { return false }

func aplicarRlimits(l Limites) error {
	return fmt.Errorf("setrlimit sólo está implementado en Linux")
}

func entrarChroot(raiz, dir string) error {
	return fmt.Errorf("chroot sólo está implementado en Linux")
}

func bajarPrivilegios(uid, gid int) error {
	return fmt.Errorf("la bajada de privilegios sólo está implementada en Linux")
}

var _ = exec.Command
