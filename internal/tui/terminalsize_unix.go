//go:build unix

package tui

import (
	"bytes"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
)

// probeMu guards probeTTYSize, which is read while painting and swapped by tests.
var probeMu sync.RWMutex

// The terminal's real size, without unsafe.
//
// os.Getenv("COLUMNS") cannot answer this: a child's environment is copied at exec, so
// a running process keeps the value it was started with and never sees a resize.
// Measured on the target machine — the parent moved to 60 columns and the child still
// read 100 from the environment.
//
// The size lives in the terminal driver, and reading it needs a TIOCGWINSZ ioctl,
// which the standard library does not expose portably. It does not have to be written
// by hand: `stty size` exists on every Unix and performs exactly that call. Running it
// with the terminal as stdin returns the real geometry, in pure Go, with no cgo and no
// unsafe.
//
// Verified on the target machine: with a PTY sized 24x70, `stty size < /dev/tty`
// reports `24 70` while the inherited COLUMNS is stale or empty.

// The seams. The branches that matter here — no controlling terminal, no stty, an
// answer that cannot be parsed — cannot be provoked from a test any other way, and each
// of them is a real environment this program runs in.
var (
	openTTY = os.Open
	statTTY = statMode
	runStty = runSttyCommand

	// probeTTYSize is what size() consults, so a test can control the answer without
	// needing a real terminal of a given size. Replacing it is how the ORDER of the
	// sources is tested — the terminal before the environment — which is the property
	// the first version of this feature got wrong.
	probeTTYSize = askTerminalSize
)

// ttySize reports the terminal's current size, or ok=false when it cannot be trusted.
func ttySize() (int, int, bool) {
	probeMu.RLock()
	probe := probeTTYSize
	probeMu.RUnlock()
	return probe()
}

func statMode(f *os.File) (os.FileMode, error) {
	info, err := f.Stat()
	if err != nil {
		return 0, err
	}
	return info.Mode(), nil
}

// runCommand runs a command with the terminal as its input and returns what it wrote.
//
// The name and arguments are parameters rather than being baked in, for one reason: the
// success branch has to be reachable in an environment with no terminal at all. `stty`
// cannot answer there, so the branch would only ever execute on a developer's machine —
// which is how a per-package coverage gate goes red on a change that passed locally. A
// test drives this with a command that exists everywhere, and `stty` itself is validated
// separately, under a pty the test allocates.
func runCommand(tty *os.File, name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	cmd.Stdin = tty
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		return "", err
	}
	return out.String(), nil
}

func runSttyCommand(tty *os.File) (string, error) {
	return runCommand(tty, "stty", "size")
}

// askTerminalSize asks the controlling terminal how big it is.
//
// It reports ok=false whenever the answer cannot be trusted, and the caller then falls
// back to the environment. That is the right failure mode: a wrong size draws a broken
// frame, while a missing one only means the frame is built for the last size known.
func askTerminalSize() (width, height int, ok bool) {
	tty, err := openTTY("/dev/tty")
	if err != nil {
		// No controlling terminal: piped input, a cron job, a build.
		return 0, 0, false
	}
	defer tty.Close()

	// A real terminal is a character device. Anything else — a file, a pipe — will not
	// answer, and asking would only spawn a process in order to fail.
	mode, err := statTTY(tty)
	if err != nil || mode&os.ModeCharDevice == 0 {
		return 0, 0, false
	}

	out, err := runStty(tty)
	if err != nil {
		return 0, 0, false
	}

	// "rows cols", which is the opposite order from every other interface in this
	// package. Reading it as width first is the classic way to swap the axes, and it
	// only shows on a non-square terminal.
	fields := strings.Fields(out)
	if len(fields) != 2 {
		return 0, 0, false
	}
	rows, rowsErr := strconv.Atoi(fields[0])
	cols, colsErr := strconv.Atoi(fields[1])
	if rowsErr != nil || colsErr != nil || rows <= 0 || cols <= 0 {
		return 0, 0, false
	}
	return cols, rows, true
}
