//go:build unix

package sandbox

import (
	"os"
	"os/exec"
	"syscall"
)

// matarGrupo envía SIGKILL a todo el grupo de procesos (pid negativo).
//
// Es lo que hace que un plazo se cumpla de verdad: si sólo muriera el hijo
// directo, sus descendientes seguirían vivos con el tubo de salida abierto y
// Wait se quedaría esperando.
func matarGrupo(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return os.ErrProcessDone
	}
	if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); err != nil {
		return os.ErrProcessDone
	}
	return nil
}
