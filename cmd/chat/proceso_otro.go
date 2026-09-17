//go:build !unix

package main

import "os/exec"

// configureGroup does nothing on systems without process groups: there is no
// equivalent concept, so the command runs as is.
func configureGroup(cmd *exec.Cmd) {}

// killGroup falls back to killing just the process.
func killGroup(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	return cmd.Process.Kill()
}
