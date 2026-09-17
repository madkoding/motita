//go:build !unix

package main

import (
	"context"
	"os"
	"os/exec"
	"strings"
)

// shellCommand builds the process that runs a line the way a person would type it
// in the terminal. There is no `sh` outside Unix: on Windows the interpreter is the
// command processor, taken from ComSpec so a custom one is honoured.
func shellCommand(ctx context.Context, command string) *exec.Cmd {
	shell := os.Getenv("ComSpec")
	if shell == "" {
		shell = "cmd.exe"
	}

	// cmd.exe does not strip one surrounding pair of quotes the way `sh -c` does,
	// so a line written for a shell arrives with the quotes and fails. Removing
	// them is what `cmd /c "..."` does anyway.
	command = strings.TrimSpace(command)
	if len(command) >= 2 && strings.HasPrefix(command, `"`) && strings.HasSuffix(command, `"`) {
		command = command[1 : len(command)-1]
	}

	return exec.CommandContext(ctx, shell, "/c", command)
}

// configureGroup does nothing on systems without process groups: there is no
// equivalent concept, so the command runs as is.
func configureGroup(cmd *exec.Cmd) {}

// killGroup falls back to killing just the process. On Windows this is the whole
// command processor and, with it, the console process tree it created.
func killGroup(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	return cmd.Process.Kill()
}
