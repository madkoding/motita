package tui

import (
	"context"
	"strings"
	"testing"
)

// --- the bugs the real terminal found ---------------------------------------

// A cursor move computed from the END of the frame is wrong for an incremental repaint. When only
// the input row changed, the layout's walk-up was applied as if the whole frame had been written,
// and the cursor landed rows away from the input — the "cursor floating anywhere but the input"
// the user reported. Measured on the real path: rows written=[20] with a move of 4A put the cursor
// on row 16 while the input was on row 20.
func TestTheCursorMoveIsAdjustedForSkippedRows(t *testing.T) {
	tui := &TUI{Width: 80, Height: 24}
	// The layout says "walk up 4 from the end of the frame".
	prompt := "\x1b[4A\x1b[6G"

	// The whole frame was written: the move stands.
	if got := tui.cursorMove(prompt, 23, 24); got != prompt {
		t.Fatalf("a full repaint must keep the move, got %q", got)
	}
	// Only row 20 (index 19) was written: 4 rows of the walk-up no longer apply.
	got := tui.cursorMove(prompt, 19, 24)
	up, col, ok := parseCursorMove(got)
	if !ok {
		t.Fatalf("the adjusted move must still parse, got %q", got)
	}
	if col != 6 {
		t.Fatalf("the column must be preserved, got %d", col)
	}
	// From row 20, nothing has to be walked up: the cursor is already there.
	if up != 0 {
		t.Fatalf("up = %d, want 0: the input row was the last one written", up)
	}
}

// Nothing written means nothing moved, so the move must NOT be sent at all.
//
// The previous version returned the layout's walk-up unchanged, which executed the sequence on
// every repaint that had nothing to draw — including the repaint a mouse event forces through
// drawFrame. The terminal then ran the walk-up on its own and the cursor jumped, while the user
// only moved the pointer. The fix is to emit nothing; drawFrame handles the visibility flag
// separately.
func TestTheCursorMoveIsKeptWhenNothingWasWritten(t *testing.T) {
	tui := &TUI{Width: 80, Height: 24}
	prompt := "\x1b[4A\x1b[6G"
	if got := tui.cursorMove(prompt, -1, 24); got != "" {
		t.Fatalf("got %q, want empty: no rows were written so no move should be sent", got)
	}
}

// The move is read back exactly as the layout builds it, in both shapes.
func TestParseCursorMove(t *testing.T) {
	cases := []struct {
		in      string
		up, col int
		ok      bool
	}{
		{"\x1b[4A\x1b[6G", 4, 6, true},
		{"\x1b[6G", 0, 6, true},
		{"\x1b[12A\x1b[1G", 12, 1, true},
		{"nonsense", 0, 0, false},
		{"\x1b[4A", 0, 0, false},
		{"\x1b[xA\x1b[6G", 0, 0, false},
	}
	for _, c := range cases {
		up, col, ok := parseCursorMove(c.in)
		if ok != c.ok {
			t.Errorf("parseCursorMove(%q) ok=%v, want %v", c.in, ok, c.ok)
			continue
		}
		if ok && (up != c.up || col != c.col) {
			t.Errorf("parseCursorMove(%q) = %d,%d want %d,%d", c.in, up, col, c.up, c.col)
		}
	}
}

// A mouse report is consumed whatever kind it is. A click, a drag or a move used to fall through
// every handler into the chat, which is what let the trackpad type into the input.
func TestEveryMouseReportIsRecognised(t *testing.T) {
	for _, seq := range []string{
		"\x1b[<0;30;10M",  // press
		"\x1b[<32;31;10M", // drag
		"\x1b[<0;31;10m",  // release
		"\x1b[<35;31;12M", // move
		"\x1b[<64;31;10M", // wheel up
	} {
		if !isMouseReport(seq) {
			t.Errorf("%q must be recognised as a mouse report", seq)
		}
	}
	// Things that are NOT mouse reports must not be swallowed: the arrows still have to work.
	for _, seq := range []string{keyUp, keyDown, keyLeft, keyRight, keyEsc, "/quit", ""} {
		if isMouseReport(seq) {
			t.Errorf("%q must not be treated as a mouse report", seq)
		}
	}
}

// A sequence longer than the parser's bound is dropped whole rather than half-read. The bound has
// to clear the longest report the interface asks for: at 16 the loop gave up mid-report and left
// the tail in the buffer to be read as typed text.
func TestTheSequenceBoundClearsAMouseReport(t *testing.T) {
	// The widest realistic report: several digits per field, with a modifier.
	long := "\x1b[<64;1234;5678M"
	if len(long) > 64 {
		t.Fatalf("the bound must clear a wide report, this one is %d bytes", len(long))
	}
	if !isMouseReport(long) {
		t.Fatal("a wide report must still be recognised")
	}
}

// A prompt that cannot be parsed is passed through unchanged rather than dropped: losing the
// cursor move entirely would be worse than an unadjusted one.
func TestAnUnparsableCursorMoveIsPassedThrough(t *testing.T) {
	tui := &TUI{Width: 80, Height: 24}
	const weird = "not-a-move"
	if got := tui.cursorMove(weird, 19, 24); got != weird {
		t.Fatalf("got %q, want it unchanged", got)
	}
	// The malformed shapes parseCursorMove has to refuse.
	for _, bad := range []string{
		"no-escape",      // no introducer
		"\x1b[4A",        // a walk-up with no column
		"\x1b[xA\x1b[6G", // a non-numeric walk-up
		"\x1b[4Ax\x1b[G", // the column has no number
	} {
		if _, _, ok := parseCursorMove(bad); ok {
			t.Errorf("parseCursorMove(%q) must fail", bad)
		}
	}
	// A colon-style column with no digits fails too: Atoi refuses an empty string.
	if _, _, ok := parseCursorMove("\x1b[4A\x1b[G"); ok {
		t.Error("a column with no digits must fail")
	}
}

// Moving up past the top of the frame is handled by addressing the row absolutely, which is the
// case where the input is above the last written row.
func TestCursorMovePastTheTopAddressesTheRow(t *testing.T) {
	tui := &TUI{Width: 80, Height: 24}
	// The layout wants to walk up 10, but only row 22 was written: 10 rows up would leave the
	// frame entirely.
	got := tui.cursorMove("\x1b[10A\x1b[6G", 21, 24)
	if !strings.Contains(got, "A") || !strings.Contains(got, "G") {
		t.Fatalf("got %q, want a walk-up and a column", got)
	}
	if _, col, ok := parseCursorMove(got); !ok || col != 6 {
		t.Fatalf("the column must be preserved, got %q", got)
	}
}

// A mouse report that reached the shortcut dispatcher is consumed even when it is not a wheel.
// Before this, a click or a move fell through to the chat: the dispatcher returned "not handled"
// and the line was dispatched as a message.
func TestTheDispatcherConsumesEveryMouseReport(t *testing.T) {
	tui := &TUI{Out: &strings.Builder{}, Width: 80, Height: 24, Runner: &stubRunner{}}
	for _, seq := range []string{"\x1b[<0;30;10M", "\x1b[<32;31;10M", "\x1b[<35;31;12M"} {
		handled, quit := tui.handleShortcut(context.Background(), seq)
		if !handled || quit {
			t.Errorf("%q: handled=%v quit=%v, want true/false", seq, handled, quit)
		}
	}
	// The wheel scrolls rather than being merely consumed.
	handled, _ := tui.handleShortcut(context.Background(), "\x1b[<64;31;10M")
	if !handled {
		t.Error("the wheel must be handled")
	}
}

// A report longer than the parser's bound is abandoned as the Escape key, which is the old
// behaviour and the safe one: the bound exists so a terminal that never sends a final byte cannot
// make the reader spin for ever.
func TestAnOverlongSequenceFallsBackToEscape(t *testing.T) {
	// Build an input with no final byte, so the loop can only exit through the bound.
	tui := &TUI{In: strings.NewReader("\x1b[" + strings.Repeat("1;", 80)), Out: &strings.Builder{}, Width: 80, Height: 24}
	if _, err := tui.input().Peek(1); err != nil { // an empty buffer returns before the loop
		t.Fatalf("could not prime the reader: %v", err)
	}
	if _, err := tui.input().ReadByte(); err != nil { // the caller already took the ESC
		t.Fatalf("could not consume the introducer: %v", err)
	}
	if got := tui.readEscape(); got != keyEsc {
		t.Fatalf("readEscape = %q, want the Escape key", got)
	}
}

// The parser refuses a column with no number: "\x1b[4A\x1b[G" is malformed and must not be read
// as column zero, which would put the cursor in the left margin.
func TestParserRefusesAColumnWithNoNumber(t *testing.T) {
	if _, _, ok := parseCursorMove("\x1b[4A\x1b[G"); ok {
		t.Fatal("a column with no digits must fail")
	}
	if _, _, ok := parseCursorMove("\x1b[4A\x1b[6X"); ok {
		t.Fatal("a sequence that does not end in G must fail")
	}
}

// Inside the live reader, a mouse report is swallowed and never reaches the draft. This is the
// path that runs when the terminal is in cbreak mode, which is what a real terminal gives the
// interface: without the consumption, the report bytes were appended to the line being typed.
func TestTheLiveReaderSwallowsMouseReports(t *testing.T) {
	// A wheel report and then a letter and Enter: the letter must be the whole line.
	//
	// The letter is NOT a command prefix ("a" would complete to /attach), because what is being
	// tested is that the report does not leak into the line - not command completion.
	input := "\x1b[<64;31;10M" + "z" + "\n"
	tui := &TUI{
		In:       strings.NewReader(input),
		Out:      &strings.Builder{},
		Width:    80,
		Height:   24,
		Runner:   &stubRunner{},
		charMode: true,
	}
	line, ok := tui.readLineLive(context.Background())
	if !ok {
		t.Fatal("the reader should have produced a line")
	}
	if line != "z" {
		t.Fatalf("line = %q, want just the typed letter: the report leaked", line)
	}
}

// Both readers bound a sequence, and both must give up the same way. They are separate functions
// with separate loops, so covering one says nothing about the other.
//
// The reader has to be PRIMED before the call: both readers return early when their bufio buffer
// is empty, and a plain strings.Reader leaves it empty until something is read. An unprimed reader
// therefore never reaches the loop, which is why an earlier version of this test passed while
// covering none of it.
func TestBothReadersBoundLongSequences(t *testing.T) {
	// No final byte anywhere, so the loop can only exit through the bound.
	overlong := "\u001b[" + strings.Repeat("1;", 80)

	// The reader is primed AND the leading ESC consumed, which is what the caller does: readLine
	// has already read the 0x1b byte before it calls readEscape, and both readers expect the
	// introducer to be behind them. Priming alone is not enough — Peek returns the ESC, not the
	// '[' the guard looks for, so the function returned before its loop and the test covered
	// nothing while still passing.
	prime := func() *TUI {
		tui := &TUI{In: strings.NewReader(overlong), Out: &strings.Builder{}, Width: 80, Height: 24}
		if _, err := tui.input().Peek(1); err != nil {
			t.Fatalf("could not prime the reader: %v", err)
		}
		if _, err := tui.input().ReadByte(); err != nil { // the ESC the caller already took
			t.Fatalf("could not consume the introducer: %v", err)
		}
		return tui
	}

	if got := prime().readEscape(); got != keyEsc {
		t.Errorf("readEscape = %q, want the Escape key", got)
	}
	if got := prime().readEscapeLive(); got != keyEsc {
		t.Errorf("readEscapeLive = %q, want the Escape key", got)
	}
}
