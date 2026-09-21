package tui

import (
	"context"
	"strconv"
	"strings"
	"testing"
)

// --- where the cursor actually ends up, in CELLS -----------------------------
//
// Every other check on the cursor reads the escape stream or the frame's own text. Neither can
// see this bug: the stream contained a well-formed CSI sequence and the frame's text was perfectly
// correct — the input row was drawn exactly where it belongs. What was wrong was the POSITION the
// terminal was left in, and only an emulator that applies the moves can observe it.
//
// The screen emulator in screen_test.go already applies CSI A and CSI G (with the zero-means-one
// default, which is the heart of this bug), so the cursor's row comes out of it for free.

// cursorRowAfterTypes drives the TUI through the live reader with the given input, feeding every
// frame it writes into an emulator, and returns the row (0-based) the cursor ended on together
// with the emulator.
func cursorRowAfterTypes(t *testing.T, keys string, w, h int) (int, *screen) {
	t.Helper()
	var out strings.Builder
	tu := New(&fakeRunner{cfg: configWithKey("k")})
	tu.In = strings.NewReader(keys)
	tu.Out = &out
	tu.Width, tu.Height = w, h
	tu.charMode = true

	// Draw first, exactly as Run does: the frames the reader paints are incremental, and the
	// cursor arithmetic only has the rows it wrote to work from.
	tu.drawFrame()
	if _, ok := tu.readLineLive(context.Background()); !ok {
		t.Fatal("the reader should have produced a line")
	}

	scr := newScreen(w, h)
	scr.feed(out.String())
	return scr.y, scr
}

// inputRow returns the 0-based row of the input field, read from the rendered cells rather than
// assumed, so the assertion cannot drift from the layout.
func inputRow(t *testing.T, scr *screen) int {
	t.Helper()
	for i, line := range scr.rows() {
		if strings.ContainsRune(line, '\u203a') {
			return i
		}
	}
	t.Fatalf("no input row was drawn:\n%s", scr.text())
	return -1
}

// The cursor must sit on the row of the input field — the row that carries the prompt glyph —
// for an empty draft and for a draft of any length that still fits on one row.
//
// The reported bug: with one character typed the cursor left the text and sat on the blank row
// ABOVE the box, because the walk-up of zero rows was written as "\x1b[0A" and a CSI parameter
// of zero takes its default — one row up. The frame's text was right, so nothing that reads the
// text could see it.
func TestTheCursorStaysOnTheInputRowWhileTyping(t *testing.T) {
	const w, h = 100, 30
	for _, keys := range []string{"a\n", "ab\n", "abcde\n"} {
		row, scr := cursorRowAfterTypes(t, keys, w, h)
		if want := inputRow(t, scr); row != want {
			t.Errorf("keys %q: the cursor ended on row %d, the input row is %d\n%s",
				keys, row, want, scr.text())
		}
	}
}

// The cursor must not drift while the draft grows: the row for one character and the row for ten
// are the same row, which is what "the cursor moves up when I type" contradicts.
func TestTypingDoesNotWalkTheCursorUp(t *testing.T) {
	const w, h = 100, 30
	row, scr := cursorRowAfterTypes(t, "a\n", w, h)
	first := inputRow(t, scr)
	if row != first {
		t.Fatalf("one character already moved the cursor to row %d, the input is on %d", row, first)
	}
	for _, keys := range []string{"ab\n", "abcdefgh\n", "abcdefghij\n"} {
		got, scr := cursorRowAfterTypes(t, keys, w, h)
		if got != first {
			t.Errorf("keys %q: cursor row %d, but one character put it on %d\n%s",
				keys, got, first, scr.text())
		}
	}
}

// A draft long enough to WRAP puts the cursor on the row the wrap gave it — which is NOT the
// input box's first row.
//
// This is the trap the first fix for the zero walk-up fell into: re-deriving the caret's row from
// the box's geometry (rowsBelowComposer + inputRows - 1) is correct only while the draft fits on
// one row. Past the width the caret is on a later row of the box, and the re-derivation parked it
// on the FIRST row — one row above the text, the very symptom being fixed. The layout's own
// walk-up already accounts for the wrap, so it is what the adjustment has to start from.
//
// It is driven through drawFrame rather than the live reader because the reader repaints with an
// empty draft on the way out (that is what clears the input), and that final frame is a different
// question. What is measured here is the frame the user is typing INTO.
func TestTheCursorFollowsAWrappedDraftOnTheLivePath(t *testing.T) {
	const w, h = 100, 30
	var out strings.Builder
	tu := New(&fakeRunner{cfg: configWithKey("k")})
	tu.Out = &out
	tu.Width, tu.Height = w, h

	tu.drawFrame() // the full first paint, as Run does
	out.Reset()

	// Two rows' worth of text: the first fill wraps, so the caret is on the box's second row.
	tu.draft = strings.Repeat("x", w) + "yyyyy"
	tu.drawFrame() // the incremental repaint a keystroke produces

	scr := newScreen(w, h)
	scr.feed(out.String())
	first := inputRow(t, scr)
	if scr.y != first+1 {
		t.Errorf("the cursor is on row %d, want %d — the row the wrap put the caret on:\n%s",
			scr.y, first+1, scr.text())
	}
}

// The completion popup is drawn ABOVE the input box, so its rows are not rows "below the
// composer" and must not be counted as such. Counting them left the cursor popup rows too high:
// on a 30-row terminal, with "/" typed, the popup is 14 rows tall and the cursor was parked at
// row 12 while the user was typing on row 26.
func TestTheCursorStaysOnTheInputRowUnderThePopup(t *testing.T) {
	const w, h = 100, 30
	row, scr := cursorRowAfterTypes(t, "/\n", w, h)
	if want := inputRow(t, scr); row != want {
		t.Errorf("with the popup open the cursor ended on row %d, the input row is %d\n%s",
			row, want, scr.text())
	}
}

// A walk-up of zero rows is written as a column move alone, and a row above the input is reached
// by moving DOWN. Both are the same rule: the parameter zero means one, so a zero is never
// emitted, and the direction is derived rather than assumed. This is the whole bug in one
// assertion, pinned on the builder where the rule is decided.
func TestAZeroWalkUpIsNotWrittenAsASequence(t *testing.T) {
	if got := cursorMoveSeq(0, 6); got != "\x1b[6G" {
		t.Errorf("zero rows up must be a column move alone, got %q", got)
	}
	if got := cursorMoveSeq(3, 6); got != "\x1b[3A\x1b[6G" {
		t.Errorf("three rows up must say three, got %q", got)
	}
	if got := cursorDownSeq(0, 6); got != "\x1b[6G" {
		t.Errorf("zero rows down must be a column move alone, got %q", got)
	}
	if got := cursorDownSeq(2, 6); got != "\x1b[2B\x1b[6G" {
		t.Errorf("two rows down must say two, got %q", got)
	}

	// The adjusted path in cursorMove is the one the user is on. The layout's walk-up is the
	// distance from the end of the frame to the caret's row; for a 30-row frame with the popup
	// closed that row is 30-1-4 (the rule, the status bar and the two rows of the box below the
	// field), so the prompt below walks up 4.
	tu := &TUI{Width: 100, Height: 30}
	const fieldRow = 30 - 1 - (rowsBelowComposer + inputRows - 1)
	const prompt = "\x1b[4A\x1b[6G"

	// The field row was the last row written, which is the ordinary case while typing.
	if got := tu.cursorMove(prompt, fieldRow, 30); got != "\x1b[6G" {
		t.Errorf("a cursor already on its row must only be moved to a column, got %q", got)
	}
	// A row ABOVE the input as the last one written moves DOWN to it. The old absolute branch
	// moved up by a count measured from below, which put the cursor on row 0 — the top of the
	// screen, as far from the input as the frame allows.
	down, col, ok := parseDownMove(tu.cursorMove(prompt, 4, 30))
	if !ok || col != 6 {
		t.Fatalf("a cursor above the input must be moved down to it, got %q", tu.cursorMove(prompt, 4, 30))
	}
	if want := fieldRow - 4; down != want {
		t.Errorf("down = %d, want %d: the distance from the last written row to the caret's row", down, want)
	}
	// And a row BELOW the input (a repaint under it) moves UP to it.
	up, _, ok := parseCursorMove(tu.cursorMove(prompt, 28, 30))
	if !ok || up != 28-fieldRow {
		t.Errorf("a cursor below the input must be moved up to it by %d, got %q", 28-fieldRow,
			tu.cursorMove(prompt, 28, 30))
	}
}

// parseDownMove reads a move built by cursorDownSeq: an optional CSI B and the column.
func parseDownMove(s string) (down, col int, ok bool) {
	body := strings.TrimPrefix(s, "\x1b[")
	if len(body) == len(s) {
		return 0, 0, false
	}
	if i := strings.IndexByte(body, 'B'); i >= 0 {
		n, err := strconv.Atoi(body[:i])
		if err != nil {
			return 0, 0, false
		}
		down = n
		body = body[i+1:]
		if !strings.HasPrefix(body, "\x1b[") {
			return 0, 0, false
		}
		body = body[2:]
	}
	i := strings.IndexByte(body, 'G')
	if i < 0 {
		return 0, 0, false
	}
	n, err := strconv.Atoi(body[:i])
	if err != nil {
		return 0, 0, false
	}
	return down, n, true
}

// The draft that wraps keeps the cursor on the row the WRAP put it on, and at the column one
// past the last character of that row. This is the neighbouring invariant: the fix above must not
// have been bought by dropping the row arithmetic that handles the wrapped case.
func TestTheWrappedDraftKeepsTheCursorOnItsOwnRow(t *testing.T) {
	const w, h = 100, 30
	tu := &TUI{Width: w, Height: h, Out: &strings.Builder{}, Runner: &fakeRunner{cfg: configWithKey("k")}}

	// Long enough to wrap: a draft past the body width lands on a later row of the box.
	tu.draft = strings.Repeat("x", tu.bodyWidth()+5)
	lines, prompt := tu.layout(w, h)

	box := 0
	for i, l := range lines {
		if strings.ContainsRune(stripANSI(l), '\u203a') {
			box = i
			break
		}
	}
	if box == 0 {
		t.Fatal("no input row was drawn")
	}
	up, col, ok := parseCursorMove(prompt)
	if !ok {
		t.Fatalf("the prompt must parse, got %q", prompt)
	}
	// The cursor's row is the LAST row the wrap produced, which is one row below the box's first
	// row — not the first row, and not the blank row above the box.
	if want := len(lines) - 1 - (box + 1); up != want {
		t.Errorf("the cursor must walk up %d rows from the end of the frame to the field's last row, got %d",
			want, up)
	}
	// And it sits one past the last character drawn on that row.
	if drawn := stripANSI(lines[box+1]); col-1 != visibleLen(drawn) {
		t.Errorf("the cursor is at column %d but the row is %d columns wide: %q",
			col-1, visibleLen(drawn), drawn)
	}
}
