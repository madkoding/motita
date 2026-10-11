package tui

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// The width of text on a terminal: wrapping, clipping, and the escape sequences that take no
// column, recognised in one place.

// clipLine truncates a PLAIN line (no escape sequences) to the given number of
// columns, marking the cut with an ellipsis. It is for text whose own layout must
// survive, which is why it clips instead of folding the line.
//
// No "does the rune count fit" guard is written below: it would be unreachable. For
// a string without escapes visibleLen is exactly the rune count, so once the first
// check has established that the measurement exceeds the width, the rune count does
// too. A guard there would only look like a safety net.
func clipLine(s string, width int) string {
	// No room at all means no text, not all of it. The earlier version returned the whole
	// string when the width was zero or negative, which is the opposite of clipping: on a
	// terminal too narrow for even the margins, every row would draw at full length and the
	// frame would break out of the window it was measured for.
	if width <= 0 {
		return ""
	}
	if visibleLen(s) <= width {
		return s
	}
	// One column is spent on the ellipsis, so the result still fits.
	runes := []rune(s)
	return strings.TrimRight(string(runes[:width-1]), " ") + "\u2026"
}

// wordWrap breaks text into lines of at most `width` COLUMNS.
//
// The width is measured with visibleLen, so an escape sequence costs nothing and a multi-byte
// rune costs one column — which is what keeps an accented or emoji line from being counted as
// several columns too wide.
//
// Two things are done in one pass here, and both matter on the hot path (this runs for every
// conversation block on every keystroke):
//
//   - the running width is kept as a NUMBER instead of being recovered with
//     visibleLen(cur.String()) once per word. The old form re-scanned everything accumulated so
//     far for every word added, which is O(n^2) in the length of a paragraph.
//   - a word that does not fit is hard split, whether or not the line is still empty. Hard
//     splitting only when `cur` was empty meant a long word AFTER a short one was emitted whole
//     and broke the promise this function makes: `wordWrap("one accents", 4)` returned a
//     7-column line for a 4-column width.
//
// The caller then hands that line to cell(), which clips it to the panel and marks the cut with
// an ellipsis. So the visible damage is SILENT TRUNCATION: a path or a URL in a conversation
// block loses its tail and the reader has no way to know something is missing — they see a
// plausible-looking shorter path. The width invariant is therefore asserted directly in
// TestWordWrapWidthIsMeasuredInVisibleColumns, which is the regression test for this bug.
func wordWrap(s string, width int) []string {
	if width <= 0 {
		return []string{s}
	}
	wr := &wrapper{width: width}
	for _, para := range strings.Split(s, "\n") {
		// A blank line between paragraphs is kept, once: it is what separates the steps of an
		// answer, and dropping it ran a list and the sentence after it together. Runs of blank
		// lines are collapsed, and the ones at either end are trimmed below.
		if strings.TrimSpace(para) == "" {
			wr.blankLine()
			continue
		}
		for _, word := range strings.Fields(para) {
			wr.add(word)
		}
		wr.flush()
	}
	lines := wr.lines
	if n := len(lines); n > 0 && lines[n-1] == "" {
		lines = lines[:n-1]
	}
	if len(lines) == 0 {
		lines = append(lines, "")
	}
	return lines
}

// wrapper holds the state of wordWrap: the lines done, and the one being filled with its width
// in columns.
type wrapper struct {
	width    int
	lines    []string
	cur      strings.Builder
	curWidth int
}

// blankLine keeps one blank line between paragraphs, never two in a row nor one at the top.
func (wr *wrapper) blankLine() {
	if len(wr.lines) > 0 && wr.lines[len(wr.lines)-1] != "" {
		wr.lines = append(wr.lines, "")
	}
}

// flush ends the line being filled, if it holds anything.
func (wr *wrapper) flush() {
	if wr.cur.Len() > 0 {
		wr.lines = append(wr.lines, strings.TrimSpace(wr.cur.String()))
		wr.cur.Reset()
		wr.curWidth = 0
	}
}

// add places one word: on the current line when it fits, else on a new one, hard split when it
// is wider than a whole line.
func (wr *wrapper) add(word string) {
	w := visibleLen(word)
	// The separator costs a column when something is already on the line.
	need := w
	if wr.curWidth > 0 {
		need++
	}
	if wr.curWidth+need <= wr.width {
		if wr.curWidth > 0 {
			wr.cur.WriteByte(' ')
			wr.curWidth++
		}
		wr.cur.WriteString(word)
		wr.curWidth += w
		return
	}
	// The word does not fit on the current line.
	wr.flush()
	if w <= wr.width {
		wr.cur.WriteString(word)
		wr.curWidth = w
		return
	}
	wr.hardSplit(word, w)
}

// hardSplit cuts a word wider than a whole line into width-sized pieces, keeping the remainder
// on the current line so the next word can share it.
func (wr *wrapper) hardSplit(word string, w int) {
	for w > wr.width {
		cut, piece := splitAtWidth(word, wr.width)
		wr.lines = append(wr.lines, piece)
		word = word[cut:]
		w = visibleLen(word)
	}
	if w > 0 {
		wr.cur.WriteString(word)
		wr.curWidth = w
	}
}

// splitAtWidth cuts s after the first `width` COLUMNS and returns the byte offset of the cut
// together with the piece before it.
//
// It walks runes rather than taking s[:width]: a rune that is not ASCII occupies one column but
// several bytes, so cutting by byte offset would slice an encoding in half, and the halves of a
// broken UTF-8 sequence are not text — the terminal draws them as replacement characters.
//
// The offset returned is always > 0 for a non-empty s — a rune is at least one byte, and an
// invalid byte is decoded as a one-byte rune — which is what makes the caller's split loop
// terminate instead of spinning on a zero-length piece.
//
// Callers pass uncoloured text: wordWrap runs BEFORE t.color, so there are no escape sequences
// here to step over. If that ever changes, this needs the same state machine scanEscapes uses.
func splitAtWidth(s string, width int) (int, string) {
	cols := 0
	for i := 0; i < len(s); {
		if cols == width {
			return i, s[:i]
		}
		_, size := utf8.DecodeRuneInString(s[i:])
		i += size
		cols++
	}
	return len(s), s
}

// Escapes are parsed with a state machine instead of "skip until a byte in
// 0x40..0x7E": in a CSI sequence the introducer itself, '[' (0x5b), already falls
// inside that range, so the naive rule stops one byte early and counts the whole
// colour sequence as text.
//
// Four shapes are recognised, which is what a terminal can be sent:
//
//	CSI  ESC [ params final            colours, cursor movement, erase
//	OSC  ESC ] … BEL or ESC \          window title, hyperlinks
//	ESC  ESC intermediate* final       charset selection: ESC ( B, ESC # 8
//	ESC  ESC <other single byte>       two-byte escapes such as ESC =
//
// The general ANSI rule is what the last two encode: after the ESC, bytes in
// 0x20..0x2F are intermediates and the first byte at or above 0x30 ends the
// sequence. Treating ESC ( B as "ESC then two visible characters" is what made
// the measured width of a decorated line wrong.
const (
	escNone = iota
	escSeen // the ESC itself was just read
	escCSI  // ESC [ … : parameters, then a final byte
	escOSC  // ESC ] … : a string, ended by BEL or by ESC \
	escOSCEsc
	escInter // ESC followed by intermediates, waiting for the final byte
)

// scanEscapes walks a string and reports, for every rune, whether it is part of
// an escape sequence. It is the single place that knows how escapes look, so the
// width measurement and the stripper can never disagree.
func scanEscapes(s string, visit func(r rune, isEscape bool)) {
	state := escNone
	for _, r := range s {
		var isEscape bool
		state, isEscape = escStep(state, r)
		visit(r, isEscape)
	}
}

// escStep reads r in the given state of the escape parser: it returns the next state and
// whether r is part of an escape sequence.
func escStep(state int, r rune) (int, bool) {
	switch state {
	case escSeen:
		return afterEsc(r), true
	case escInter:
		return inIntermediate(r), true
	case escCSI:
		next := escCSI
		if r >= 0x40 && r <= 0x7e {
			next = escNone
		}
		return next, true
	case escOSC:
		return inOSC(r), true
	case escOSCEsc:
		return escNone, true
	default:
		if r == 0x1b {
			return escSeen, true
		}
		return escNone, false
	}
}

// afterEsc is the state after the byte that follows an ESC.
func afterEsc(r rune) int {
	next := escNone
	switch {
	case r == '[':
		next = escCSI
	case r == ']':
		next = escOSC
	case r >= 0x20 && r <= 0x2f:
		// An intermediate byte: the sequence continues.
		next = escInter
	default:
		// Any other byte is a two-byte escape such as ESC =, and ends it.
	}
	return next
}

// inIntermediate is the state after a byte that follows the intermediates of an escape.
func inIntermediate(r rune) int {
	next := escInter
	switch {
	case r == 0x1b:
		// A new escape interrupts the one being read. Treating it as an
		// intermediate kept the parser in this state, and the following
		// "[31m" was then counted as four columns of text.
		next = escSeen
	case r >= 0x30:
		next = escNone
	default:
		// Another intermediate byte: the sequence goes on.
	}
	return next
}

// inOSC is the state after a byte of an OSC string.
func inOSC(r rune) int {
	next := escOSC
	switch r {
	case 0x07: // BEL ends it
		next = escNone
	case 0x1b: // ESC \ also ends it
		next = escOSCEsc
	default:
		// Part of the string.
	}
	return next
}

// visibleLen returns how many COLUMNS a line occupies, escapes excluded.
//
// No ESC byte means every rune is text, so the count is taken directly and the closure-per-rune
// state machine is skipped. That is the common case — this function is called per word while
// wrapping and per line while fitting the frame — and the machine was 36% of the per-keystroke
// profile even when there was nothing to unescape.
func visibleLen(s string) int {
	if strings.IndexByte(s, 0x1b) < 0 {
		return utf8.RuneCountInString(s)
	}
	n := 0
	scanEscapes(s, func(_ rune, isEscape bool) {
		if !isEscape {
			n++
		}
	})
	return n
}

// needsEscaping reports whether r is shown escaped by EscapeControls: a C0 control but newline
// and tab, DEL, a C1 control, or an invalid byte.
func needsEscaping(r rune) bool {
	return (r < 0x20 && r != '\n' && r != '\t') || (r >= 0x7f && r < 0xa0) || r == utf8.RuneError
}

// EscapeControls makes the control characters of UNTRUSTED text visible instead of handing them
// to the terminal: the model's words, a tool's output, a command waiting for approval. A raw ESC
// in any of them is an instruction to the terminal, not text - it can hide part of a command in
// the approval window, rewrite rows already drawn, or set the clipboard (OSC 52). Every C0 byte
// but newline and tab is shown in caret form (ESC is ^[), DEL as ^?, and a C1 control as \u0080
// to \u009f. A carriage return right before a newline is dropped: that is a CRLF line ending,
// not an instruction. An invalid UTF-8 byte becomes U+FFFD.
func EscapeControls(s string) string {
	clean := true
	for _, r := range s {
		if needsEscaping(r) {
			clean = false
			break
		}
	}
	if clean {
		return s
	}
	var b strings.Builder
	b.Grow(len(s) + 8)
	for i, r := range s {
		switch {
		case r == '\r' && strings.HasPrefix(s[i+1:], "\n"):
		case r == '\n' || r == '\t':
			b.WriteRune(r)
		case r < 0x20:
			b.WriteByte('^')
			b.WriteByte(byte(r) + '@')
		case r == 0x7f:
			b.WriteString("^?")
		case r >= 0x80 && r < 0xa0:
			fmt.Fprintf(&b, `\u%04x`, r)
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// stripANSI removes every escape sequence, for no-colour mode.
//
// A line with no ESC byte is already stripped, so it is returned as-is instead of being copied
// rune by rune through the state machine.
func stripANSI(s string) string {
	if strings.IndexByte(s, 0x1b) < 0 {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	scanEscapes(s, func(r rune, isEscape bool) {
		if !isEscape {
			b.WriteRune(r)
		}
	})
	return b.String()
}
