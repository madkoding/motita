//go:build !unix

package execx

import (
	"errors"
	"os"
	"os/exec"
)

// configurarGrupo no hace nada fuera de Unix: no hay grupos POSIX.
func configurarGrupo(cmd *exec.Cmd) {}

// matarGrupo sin SysProcAttr sólo puede matar el proceso directo.
func matarGrupo(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return os.ErrProcessDone
	}
	return cmd.Process.Kill()
}

func asExitError(err error, destino **exec.ExitError) bool {
	return errors.As(err, destino)
}
