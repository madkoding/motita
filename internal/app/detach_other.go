//go:build !unix

package app

import (
	"os"
	"os/exec"
)

// termSignal is what a process is asked to stop with where there are no POSIX signals.
//
// On Windows that is os.Kill, which ends the process without the graceful path the POSIX side gets.
// The service still stops - the operating system just does not deliver a request the program can
// answer - and that difference is why this is a function rather than a constant shared with the
// other build: the two sides genuinely differ.
func termSignal() os.Signal { return os.Kill }

// detach does nothing where there are no POSIX sessions. The service still works, it just shares
// the lifetime of the terminal it was started from - which is what a Windows user starting a
// console program already expects.
func detach(cmd *exec.Cmd) {}
