//go:build !unix

package llm

import "os"

// terminate ends claude. There is no SIGTERM to send here: os.Process.Signal only
// delivers os.Kill on Windows.
func terminate(p *os.Process) error { return p.Kill() }
