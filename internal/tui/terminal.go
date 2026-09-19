package tui

import "os"

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

// mouseScroll decodes an SGR mouse report and returns how many lines to scroll:
// positive for the wheel up, negative for the wheel down. ok=false means the sequence is
// not a wheel event (a click, a drag, a plain key) and the caller ignores it.
//
// It lives here, with no build tag, because it is pure string parsing: a second
// implementation for another platform would mean the parser under test is not the parser
// that ships.
//
// The SGR form is `ESC [ < b ; x ; y M` for a press and `m` for a release, with b as
// button + modifier bits. On xterm the wheel is buttons 64 (up) and 65 (down), and a
// modifier adds 4 (shift), 8 (meta) or 16 (ctrl).
func mouseScroll(seq string) (int, bool) {
	if len(seq) < 6 || seq[0] != 0x1b || seq[1] != '[' || seq[2] != '<' {
		return 0, false
	}
	// The final byte must be M (press) — a wheel release carries no movement.
	if seq[len(seq)-1] != 'M' {
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
