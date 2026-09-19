//go:build unix

package tui

import "os"

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

// mouseScroll decodes an SGR mouse report and returns how many lines to scroll:
// positive for the wheel up, negative for the wheel down. ok=false means the sequence is
// not a wheel event (a click, a drag, a plain key) and the caller ignores it.
//
// The SGR form is `ESC [ < b ; x ; y M` for a press and `m` for a release, with b as
// button + modifier bits. On xterm the wheel is buttons 64 (up) and 65 (down), and a
// modifier adds 4 (shift), 8 (meta) or 16 (ctrl).
func mouseScroll(seq string) (int, bool) {
	if len(seq) < 6 || seq[0] != 0x1b || seq[1] != '[' || seq[2] != '<' {
		return 0, false
	}
	// The final byte must be M (press) — a wheel release carries no movement.
	last := seq[len(seq)-1]
	if last != 'M' {
		return 0, false
	}
	body := seq[3 : len(seq)-1]

	// b is the first field, up to the first ';'.
	semi := -1
	for i := 0; i < len(body); i++ {
		if body[i] == ';' {
			semi = i
			break
		}
	}
	if semi <= 0 {
		return 0, false
	}
	button, ok := parseSmallInt(body[:semi])
	if !ok {
		return 0, false
	}

	// Strip the modifier bits so a wheel with Shift held is still a wheel.
	switch button &^ (4 | 8 | 16) {
	case 64:
		return 1, true // wheel up: go back in the conversation
	case 65:
		return -1, true // wheel down: towards the newest line
	}
	return 0, false
}

// parseSmallInt reads a non-negative decimal without pulling in strconv on a hot path,
// and without accepting signs or spaces that would let a malformed report through.
func parseSmallInt(s string) (int, bool) {
	if s == "" || len(s) > 4 {
		return 0, false
	}
	n := 0
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return 0, false
		}
		n = n*10 + int(s[i]-'0')
	}
	return n, true
}

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

// IsTerminal reports whether a writer is a terminal.
//
// The test is "is a character device", which is the portable stand-in: the standard
// library exposes no ioctl through unsafe and this module takes none, so the cheap
// check is the honest one. It is exported because the caller that decides about colour
// needs the same answer, and two implementations of one question is how they end up
// disagreeing.
//
// Anything that is not an *os.File — a buffer, a pipe, a writer an embedder supplied —
// is not a terminal, and neither is a file that cannot be inspected.
func IsTerminal(w interface{ Write([]byte) (int, error) }) bool {
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	info, err := f.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}

// isTerminalOut is the internal spelling used by the mouse code.
func isTerminalOut(w interface{ Write([]byte) (int, error) }) bool { return IsTerminal(w) }
