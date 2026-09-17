//go:build !windows

package execx

import (
	"context"
	"os/exec"
)

// shellCommand builds the process that runs a line the way a person would type it
// in the terminal (pipes, redirections, quotes). On Unix that is `sh -c`.
//
// It takes the context because the caller installs a Cancel on the returned
// command to kill the whole process group, and Go rejects that on a command that
// was not created with CommandContext.
func shellCommand(ctx context.Context, command string) *exec.Cmd {
	return exec.CommandContext(ctx, "sh", "-c", command)
}

// shellName is the interpreter the commands are written for. It is reported so a
// caller can tell the user which dialect their command has to be valid in.
const shellName = "sh"
