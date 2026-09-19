//go:build unix

package tui

import (
	"bytes"
	"os"
	"os/exec"
)

// Character-at-a-time input, without cgo and without unsafe.
//
// A terminal in its default (canonical) mode does not hand over a character until the line
// is finished: the driver keeps the buffer and does the editing itself. That is why a live
// completion popup, and any control key, cannot work as things stand — the program never
// sees the keystrokes. Measured on the target machine: Ctrl+F never arrived.
//
// The fix is to ask the driver for cbreak mode, which delivers each byte as it is typed, and
// to turn off echo so the program can draw the line itself. Both are `stty` flags, so this is
// the same route as the size probe: the ioctl is performed for us, in pure Go, with no cgo
// and no unsafe.
//
// The stakes are higher here than for a size reading. A terminal left in cbreak with echo off
// is unusable — the shell receives no visible input — so the restore runs from a deferred
// call AND from a panic handler, and it is idempotent.
var (
	// runSttyMode is a seam, like runStty: the failure branches (no stty, a terminal that
	// refuses the mode, a restore that fails) cannot be provoked from a test any other way.
	runSttyMode = func(f *os.File, args ...string) error {
		cmd := exec.Command("stty", args...)
		cmd.Stdin = f
		var out bytes.Buffer
		cmd.Stdout = &out
		cmd.Stderr = &out
		return cmd.Run()
	}
	openTTYMode = os.OpenFile
)

// terminalMode is what the program asked for, so it can be undone exactly once.
type terminalMode struct {
	tty    *os.File
	active bool
}

// enterRaw puts the terminal into character-at-a-time input with no echo.
//
//	cbreak  deliver each byte as it is typed, but keep signal generation (Ctrl+C still works)
//	-echo   stop the driver echoing, because the interface draws the line itself
//
// It returns a handle whose restore must be called; a handle that never became active is
// safe to restore, so the caller does not have to branch.
func enterRaw() *terminalMode {
	m := &terminalMode{}
	tty, err := openTTYMode("/dev/tty", os.O_RDWR, 0)
	if err != nil {
		// No controlling terminal: piped input, a cron job, a test. Nothing to change.
		return m
	}
	mode, err := statTTY(tty)
	if err != nil || mode&os.ModeCharDevice == 0 {
		tty.Close()
		return m
	}
	if err := runSttyMode(tty, "cbreak", "-echo"); err != nil {
		// A terminal that refuses the mode leaves us with the behaviour we had: whole lines,
		// no live completion, and a working interface. Degrading is right; failing is not.
		tty.Close()
		return m
	}
	m.tty = tty
	m.active = true
	return m
}

// restore puts the terminal back. It is idempotent, because it runs both from the deferred
// call and from the panic handler and only one of them should do the work.
func (m *terminalMode) restore() {
	if m == nil || !m.active {
		return
	}
	m.active = false
	// "sane" is the reset every stty understands, and it is more robust than replaying the
	// inverse flags: whatever went wrong, this puts the terminal into a state a shell can use.
	_ = runSttyMode(m.tty, "sane")
	m.tty.Close()
}
