//go:build !unix

package app

import (
	"os"
	"os/exec"
)

// termSignal is what a process is asked to stop with where there are no POSIX signals.
//
// On Windows that is os.Kill, which ends the process without the graceful path the POSIX side gets.
// That is why it is only the FALLBACK there: `gateway stop` first asks the gateway to shut down
// over its local API (stopsOverAPI), and kills it only when that request fails or the gateway is
// still answering after the wait.
func termSignal() os.Signal { return os.Kill }

// stopsOverAPI is true here: with no SIGTERM, POST /v1/shutdown is the only way to reach the
// gateway's graceful shutdown (sessions saved, runs cancelled, listener closed) from outside it.
const stopsOverAPI = true

// detach does nothing where there are no POSIX sessions. The service still works, it just shares
// the lifetime of the terminal it was started from - which is what a Windows user starting a
// console program already expects.
func detach(cmd *exec.Cmd) {
	// Nothing to do here: see the comment above.
}
