package tui

import (
	"fmt"
	"strings"
	"unicode"
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
	if width == 1 {
		return "\u2026"
	}
	cut, _ := splitAtWidth(s, width-1)
	return strings.TrimRight(s[:cut], " ") + "\u2026"
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
		r, size := utf8.DecodeRuneInString(s[i:])
		rw := runeWidth(r)
		// A wide rune that would straddle the cut goes to the next piece, but never leaves this
		// one empty: the caller's loop needs a cut greater than zero to terminate.
		if cols+rw > width && i > 0 {
			return i, s[:i]
		}
		i += size
		cols += rw
		if cols >= width {
			// Zero-width marks that follow belong with the rune they modify.
			for i < len(s) {
				nr, nsize := utf8.DecodeRuneInString(s[i:])
				if runeWidth(nr) != 0 {
					break
				}
				i += nsize
			}
			return i, s[:i]
		}
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
		return plainWidth(s)
	}
	n := 0
	scanEscapes(s, func(r rune, isEscape bool) {
		if !isEscape {
			n += runeWidth(r)
		}
	})
	return n
}

// plainWidth is the columns of a string without escapes. ASCII text, the overwhelming case, is
// its byte length; anything else is summed rune by rune.
func plainWidth(s string) int {
	n := 0
	for i := 0; i < len(s); i++ {
		if s[i] >= utf8.RuneSelf {
			n = 0
			for _, r := range s {
				n += runeWidth(r)
			}
			return n
		}
	}
	return len(s)
}

// runeWidth is how many terminal columns r occupies: 0 for combining marks, joiners and
// variation selectors, 2 for East Asian wide and emoji, 1 for everything else.
//
// Counting every rune as one column is what let a line of CJK text or emoji reach the terminal
// twice as wide as it was measured: the terminal wrapped it onto an extra row, the frame came
// out taller than the window and the whole interface scrolled, so the input box landed at a
// different height after each request.
func runeWidth(r rune) int {
	switch {
	case r < 0x300:
		return 1
	case unicode.Is(unicode.Mn, r), unicode.Is(unicode.Me, r), unicode.Is(unicode.Cf, r):
		return 0
	case r >= 0x1100 && r <= 0x115f, // Hangul Jamo
		r >= 0x2e80 && r <= 0xa4cf && r != 0x303f, // CJK radicals .. Yi
		r >= 0xac00 && r <= 0xd7a3,                // Hangul syllables
		r >= 0xf900 && r <= 0xfaff,                // CJK compatibility ideographs
		r >= 0xfe30 && r <= 0xfe6f,                // CJK compatibility forms
		r >= 0xff00 && r <= 0xff60,                // fullwidth forms
		r >= 0xffe0 && r <= 0xffe6,
		r >= 0x1f300 && r <= 0x1f64f, // pictographs, emoticons
		r >= 0x1f900 && r <= 0x1f9ff,
		r >= 0x1f680 && r <= 0x1f6ff, // transport
		r >= 0x20000 && r <= 0x3fffd, // CJK extensions
		r == 0x2705, r == 0x274c, r == 0x2b50, r == 0x231a, r == 0x231b:
		return 2
	default:
		return 1
	}
}

// needsEscaping reports whether r is shown escaped by EscapeControls: a C0 control but newline
// and tab, DEL, a C1 control, or an invalid byte.
func needsEscaping(r rune) bool {
	return isC0Control(r) || (r >= 0x7f && r < 0xa0) || r == utf8.RuneError
}

// isC0Control reports whether r is a C0 control character other than newline and tab.
func isC0Control(r rune) bool {
	return r < 0x20 && r != '\n' && r != '\t'
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

// guardRows is the last line of defence before a frame is written: no row may hold a tab or
// reach past `cols` columns.
//
// A row wider than the terminal is wrapped by the terminal onto a row of its own, and a tab
// jumps to the next stop, which the width count cannot know. Either one makes the frame taller
// than the window, the terminal scrolls, and the input box ends up at a different height after
// each request. Every producer of rows measures its text, but they measure it separately, so
// the frame is checked once more here, where it is cheap and nothing can slip past.
func guardRows(lines []string, cols int) []string {
	for i, l := range lines {
		if strings.IndexByte(l, '\t') >= 0 {
			l = strings.ReplaceAll(l, "\t", "    ")
		}
		if visibleLen(l) > cols {
			l = clipVisible(l, cols)
		}
		lines[i] = l
	}
	return lines
}

// clipVisible cuts a line that may hold escape sequences after `cols` columns. Escapes are kept
// whole and a reset is appended when one was seen, so a cut never leaves a colour switched on.
func clipVisible(s string, cols int) string {
	var b strings.Builder
	state, used, sawEscape := escNone, 0, false
	for _, r := range s {
		var isEscape bool
		state, isEscape = escStep(state, r)
		if isEscape {
			sawEscape = true
			b.WriteRune(r)
			continue
		}
		rw := runeWidth(r)
		if used+rw > cols {
			break
		}
		used += rw
		b.WriteRune(r)
	}
	if sawEscape {
		b.WriteString("\x1b[0m")
	}
	return b.String()
}
