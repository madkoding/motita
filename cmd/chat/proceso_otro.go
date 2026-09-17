//go:build !unix

package main

import (
	"os"
	"os/exec"
)

// configurarGrupo no hace nada fuera de Unix: no hay grupos de procesos POSIX.
func configurarGrupo(cmd *exec.Cmd) {}

// matarGrupo sin SysProcAttr la única opción es matar el proceso directo.
func matarGrupo(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return os.ErrProcessDone
	}
	return cmd.Process.Kill()
}
