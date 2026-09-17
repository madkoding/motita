//go:build windows

package execx

import (
	"context"
	"os"
	"os/exec"
	"strings"
)

// shellCommand builds the process that runs a line the way a person would type it
// in the terminal. There is no `sh` on Windows: the interpreter is the command
// processor, taken from ComSpec so a custom one (or a different drive) is honoured.
//
// It takes the context because the caller installs a Cancel on the returned
// command, and Go rejects that on a command that was not created with
// CommandContext.
func shellCommand(ctx context.Context, command string) *exec.Cmd {
	shell := os.Getenv("ComSpec")
	if shell == "" {
		// cmd.exe is the system default and lives in the system directory.
		shell = "cmd.exe"
	}

	// cmd.exe does NOT strip the surrounding quotes of a command given with /c the
	// way sh does, so a line wrapped in quotes arrives with them and fails.
	// Removing one surrounding pair is what `cmd /c "..."` does anyway.
	command = strings.TrimSpace(command)
	if len(command) >= 2 && strings.HasPrefix(command, `"`) && strings.HasSuffix(command, `"`) {
		command = command[1 : len(command)-1]
	}

	return exec.CommandContext(ctx, shell, "/c", command)
}

// shellName is the interpreter the commands are written for. It is reported so a
// caller can tell the user which dialect their command has to be valid in.
const shellName = "cmd.exe"
