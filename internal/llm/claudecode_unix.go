//go:build unix

package llm

import (
	"os"
	"syscall"
)

// terminate asks claude to exit, so it removes what it created on the way out.
func terminate(p *os.Process) error { return p.Signal(syscall.SIGTERM) }
