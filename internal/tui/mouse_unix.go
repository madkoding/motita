//go:build unix

package tui

// Mouse support, as an addition to the keyboard and never a replacement.
//
// The design guide calls mouse "additive": everything must be reachable from the
// keyboard, and the mouse may then make the common action quicker. That is exactly
// what this does — the wheel scrolls the conversation, which is the one gesture that
// beats reaching for a key in a chat.
//
// It needs the terminal in a mode that REPORTS the mouse, and that is set with an
// escape sequence rather than an ioctl:
//
//	ESC [ ? 1000 h   report button press and release
//	ESC [ ? 1006 h   report them in SGR form: ESC [ < b ; x ; y M/m
//
// SGR is the only encoding worth asking for: the older one packs the coordinates into
// single bytes and cannot express a column past 223.
//
// The catch, and the reason the keyboard remains the only requirement: reporting events
// is only delivered promptly when the terminal is in raw or cbreak mode, and this
// interface deliberately does not touch termios — no cgo, no unsafe ioctl, portable
// across linux/windows/darwin. A canonical-mode terminal holds the bytes until Enter, so
// a wheel report arrives late and in the middle of the line, where it is discarded by the
// same parser that makes it safe.
//
// So the sequences are requested, the reports are parsed correctly, and the wheel works
// on the terminals that flush regardless — while the keyboard path stays exactly as
// reliable as it was. Claiming more than that would be a promise the user breaks on
// their first drag.
//
// This file needs no ioctl, which is why it imports neither unsafe nor syscall.
const (
	mouseOn  = "\x1b[?1000h\x1b[?1006h"
	mouseOff = "\x1b[?1006l\x1b[?1000l"
)

// enableMouse turns mouse reporting on and off. It is written directly to the output
// because the frame repaint does not carry terminal modes.
//
// Failure is ignored on purpose: a terminal that does not understand the sequence
// ignores it, and one whose stdin is a pipe has no terminal to enable anything on. The
// interface must not fail because an optional feature could not be requested.
func (t *TUI) enableMouse() {
	if t.NoColor {
		// No-colour mode exists for terminals that cannot do escapes at all; asking one
		// of those for mouse reports would print the request as text.
		return
	}
	if !isTerminalOut(t.Out) {
		return
	}
	_, _ = t.Out.Write([]byte(mouseOn))
}

func (t *TUI) disableMouse() {
	if t.NoColor || !isTerminalOut(t.Out) {
		return
	}
	_, _ = t.Out.Write([]byte(mouseOff))
}
