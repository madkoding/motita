//go:build unix

package sandbox

import "syscall"

// execCommand replaces the current process image with the command, leaving no Go
// process alive: no extra process holds the output pipes, and the limits apply to
// the command itself.
//
// It never returns when the command starts. The error is what the caller reports
// with the reserved code 127.
func execCommand(argv0 string, argv []string, envv []string) error {
	return syscall.Exec(argv0, argv, envv)
}
