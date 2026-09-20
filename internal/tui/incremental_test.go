package tui

import (
	"bytes"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// Repainting the whole frame on every keystroke is what made the interface feel slow.
//
// Measured at 110x30: one frame is ~5,000 bytes and 99% of it is text that did not change —
// the conversation, the banner, the rules — because one character was typed into the box.
// The fix writes only the rows that differ, so these tests are about BYTES, not about how
// many times the painter was called: a repaint that still writes 5 KB has fixed nothing.

// frameBytes draws one frame and reports how many bytes it wrote.
//
// It does NOT reset first: the caller decides what it is measuring, and resetting inside made
// an earlier version of this helper report the incremental frame as the baseline.
func frameBytes(t *testing.T, tu *TUI, out *bytes.Buffer) int {
	t.Helper()
	was := out.Len()
	tu.drawFrame()
	return out.Len() - was
}

func TestTypingOneCharacterDoesNotRewriteTheFrame(t *testing.T) {
	var out bytes.Buffer
	tu, _ := newKeyTUI("")
	tu.Out = &out
	tu.Width, tu.Height = 110, 30
	padBody(tu, 20)

	full := frameBytes(t, tu, &out) // the first frame: everything, unavoidable
	out.Reset()

	// One character typed: one more character in the box, everything else identical.
	tu.draft = "a"
	incremental := frameBytes(t, tu, &out)

	if incremental >= full {
		t.Fatalf("typing must not rewrite the whole frame: %d bytes vs %d", incremental, full)
	}
	// The budget is the changed input rows plus one cursor placement each: anything larger
	// means rows that did not change are being written again.
	if incremental > full/10 {
		t.Errorf("typing one character wrote %d bytes (a full frame is %d)", incremental, full)
	}
}

func TestAnUnchangedFrameWritesAlmostNothing(t *testing.T) {
	var out bytes.Buffer
	tu, _ := newKeyTUI("")
	tu.Out = &out
	tu.Width, tu.Height = 110, 30

	tu.drawFrame()
	out.Reset()
	tu.drawFrame()

	// Only the cursor placement: no row differs, so no row is written.
	if got := out.Len(); got > 40 {
		t.Errorf("an identical frame wrote %d bytes; only the cursor should move", got)
	}
}

func TestOnlyTheChangedRowsAreWritten(t *testing.T) {
	var out bytes.Buffer
	tu, _ := newKeyTUI("")
	tu.Out = &out
	tu.Width, tu.Height = 110, 30
	padBody(tu, 20)

	tu.drawFrame()
	out.Reset()

	// Change something far down the frame: exactly that row's position must be addressed.
	tu.draft = "x"
	tu.drawFrame()

	written := out.String()
	// Count how many distinct rows were addressed.
	rows := strings.Count(written, "\x1b[")
	if rows > 12 {
		t.Errorf("expected a handful of rows, wrote %d", rows)
	}
	// The frame is 30 rows tall, so nothing may address the banner row if only the input changed.
	if strings.Contains(written, "\x1b[1;1H") {
		t.Error("row 1 (the banner) did not change and must not be written")
	}
	// The input rows must be among the ones written: that is what changed.
	if !strings.Contains(written, ";1H") {
		t.Error("the changed rows must be addressed")
	}
}

// A stale row is the whole reason a full frame was written every time, so the cases where the
// diff cannot be trusted must fall back to writing everything.

func TestTheFirstFrameIsWrittenInFull(t *testing.T) {
	var out bytes.Buffer
	tu, _ := newKeyTUI("")
	tu.Out = &out
	tu.Width, tu.Height = 110, 30
	padBody(tu, 5)

	tu.drawFrame()

	// Every row is addressed on the first paint: nothing is known about the terminal yet.
	if got := strings.Count(out.String(), "\x1b["); got < 10 {
		t.Errorf("the first frame must write every row, only %d writes seen", got)
	}
}

func TestAResizeRepaintsEverything(t *testing.T) {
	var out bytes.Buffer
	tu, _ := newKeyTUI("")
	tu.Out = &out
	tu.Width, tu.Height = 110, 30
	padBody(tu, 20)

	tu.drawFrame()
	out.Reset()

	// A different geometry invalidates the comparison: the old rows describe a frame that no
	// longer exists.
	tu.Height = 20
	tu.drawFrame()

	if got := strings.Count(out.String(), "\x1b["); got < 10 {
		t.Errorf("a resize must repaint in full, only %d writes seen", got)
	}
}

func TestWipingTheScreenInvalidatesTheFrame(t *testing.T) {
	var out bytes.Buffer
	tu, _ := newKeyTUI("")
	tu.Out = &out
	tu.Width, tu.Height = 110, 30

	tu.drawFrame()
	tu.invalidateScreen()

	if tu.paintedScreen {
		t.Error("after a wipe the painter must not believe the rows are still on screen")
	}
	if tu.lastFrame != nil {
		t.Error("after a wipe there is nothing to diff against")
	}
}

// The cursor must be placed on every frame, even one where no row changed: it may have to move
// because the user pressed a key that shows nothing, or because a previous frame ended elsewhere.
func TestTheCursorIsPlacedEvenWhenNoRowChanged(t *testing.T) {
	var out bytes.Buffer
	tu, _ := newKeyTUI("")
	tu.Out = &out
	tu.Width, tu.Height = 110, 30

	tu.drawFrame()
	out.Reset()
	tu.drawFrame()

	if out.Len() == 0 {
		t.Error("the cursor must be placed even when nothing changed")
	}
	if !strings.Contains(out.String(), "\x1b[") {
		t.Errorf("the cursor needs a CSI move, got %q", out.String())
	}
}

// Rows are addressed absolutely, so a skipped row cannot shift the ones after it: the position
// written for a row must be its index, not a running offset.
func TestRowsAreAddressedByTheirOwnPosition(t *testing.T) {
	var out bytes.Buffer
	tu, _ := newKeyTUI("")
	tu.Out = &out
	tu.Width, tu.Height = 110, 30
	padBody(tu, 20)

	tu.drawFrame()
	out.Reset()
	tu.draft = "typed"
	tu.drawFrame()

	// Only "CSI number;number H" is a cursor move; everything else in the stream is a colour
	// or an erase, and reading those as row numbers is a mistake this test made first.
	moves := regexp.MustCompile(`\x1b\[(\d+);1H`).FindAllStringSubmatch(out.String(), -1)
	if len(moves) == 0 {
		t.Fatal("no rows were addressed at all")
	}
	for _, m := range moves {
		row, err := strconv.Atoi(m[1])
		if err != nil {
			t.Fatalf("unparseable row address %q", m[1])
		}
		if row < 1 || row > 30 {
			t.Errorf("row address %d is outside a 30-row frame", row)
		}
	}
}
