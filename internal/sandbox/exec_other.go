//go:build !unix

package sandbox

import (
	"errors"
	"os"
	"os/exec"
)

// execCommand runs the command and ends this process with the command's own exit
// code. It does not return when the command ran.
//
// It cannot replace the process image: syscall.Exec only ever returns EWINDOWS on
// Windows, so the Unix approach would make **every** sandboxed command fail while
// still compiling cleanly. Running a child and forwarding its exit code preserves
// what the parent depends on — the output and the exit status — which is what
// decides PASS/FAIL.
//
// It exits through os.Exit and not through childHooks: the hook points at this
// function, so using it here would be an initialisation cycle. The tests cover the
// decisions instead by driving the parts that do not end the process.
func execCommand(argv0 string, argv []string, envv []string) error {
	var args []string
	if len(argv) > 1 {
		args = argv[1:]
	}

	cmd := exec.Command(argv0, args...)
	cmd.Env = envv
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	err := cmd.Run()
	if err == nil {
		os.Exit(0)
		return nil
	}

	// A command that ran and failed is not an error of the sandbox: the exit code
	// is what the caller inspects (that is how the anchor decides PASS/FAIL).
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		code := exitErr.ExitCode()
		if code < 0 {
			code = 1
		}
		os.Exit(code)
		return nil
	}

	// The command could not start: the caller reports the reserved code 127, as on
	// Unix.
	return err
}
