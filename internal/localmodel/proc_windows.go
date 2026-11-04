package localmodel

import "os/exec"

// setProcAttr has nothing to set here: Motita stops the server on its own shutdown.
func setProcAttr(*exec.Cmd) {}

// terminate ends the server: Windows has no SIGTERM to ask with.
func terminate(cmd *exec.Cmd) { _ = cmd.Process.Kill() }
