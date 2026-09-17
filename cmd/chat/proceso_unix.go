//go:build unix

package main

import (
	"os"
	"os/exec"
	"syscall"
)

// configurarGrupo aísla el comando en su propio grupo de procesos. Sin esto,
// matar sólo a `sh` deja vivos a sus hijos (`sleep`, scripts, pipes), que
// mantienen abierto el pipe de salida y bloquean Wait hasta que terminan solos.
func configurarGrupo(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// matarGrupo envía SIGKILL a todo el grupo de procesos (pid negativo).
func matarGrupo(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return os.ErrProcessDone
	}
	// El pid negativo alcanza a `sh` y a todos sus descendientes del grupo.
	if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); err != nil {
		return os.ErrProcessDone
	}
	return nil
}
