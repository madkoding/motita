//go:build !unix

package tui

import (
	"os"
	"sync"
)

// There is no controlling-terminal file and no `stty` here.
//
// Windows delivers console resizes through ReadConsoleInput, which the standard
// library does not expose portably, and its console has no /dev/tty for a probe to
// open. The interface therefore uses COLUMNS/LINES, which is correct for the size in
// force when the program started — the fallback exists for exactly this.
//
// The seams are declared too, so the package's code and tests compile unchanged on
// every platform. They are inert: nothing opens a terminal and nothing runs a command,
// so the environment is always what answers.

// probeMu guards probeTTYSize on every platform, so the shared test helper can swap it
// under the same lock the Unix side takes.
var probeMu sync.RWMutex

var (
	openTTY = os.Open
	statTTY = func(f *os.File) (os.FileMode, error) {
		info, err := f.Stat()
		if err != nil {
			return 0, err
		}
		return info.Mode(), nil
	}
	runStty      = func(*os.File) (string, error) { return "", os.ErrInvalid }
	probeTTYSize = func() (int, int, bool) { return 0, 0, false }
)

func ttySize() (int, int, bool) { return probeTTYSize() }
