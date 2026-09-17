//go:build !unix

package sandbox

import (
	"os"
	"os/exec"
)

// matarGrupo sin grupos POSIX sólo puede matar el proceso directo.
func matarGrupo(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return os.ErrProcessDone
	}
	return cmd.Process.Kill()
}
