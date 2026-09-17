//go:build unix

package execx

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
)

// configurarGrupo aísla el comando en su propio grupo de procesos. Sin esto,
// matar sólo a `sh` deja vivos a sus hijos, que mantienen el tubo abierto y
// bloquean Wait hasta que terminan por su cuenta.
func configurarGrupo(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// matarGrupo envía SIGKILL a todo el grupo (pid negativo).
func matarGrupo(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return os.ErrProcessDone
	}
	if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); err != nil {
		return os.ErrProcessDone
	}
	return nil
}

// asExitError evita depender de errors.As en cada llamada.
func asExitError(err error, destino **exec.ExitError) bool {
	return errors.As(err, destino)
}
