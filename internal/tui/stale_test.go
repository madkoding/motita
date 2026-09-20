package tui

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/madkoding/starlight/internal/session"
)

// --- The screen, not the frame -------------------------------------------------
//
// These tests check what a terminal DISPLAYS after the frames are written, by feeding the output
// through the emulator in screen_test.go. Reading the frames' own text is not enough: a frame
// that writes "› " while the screen still shows "› hazlo" has completely correct text.
//
// The bug these cover: rows were written with no erase, so the terminal only overwrote the cells
// it was given and the tail of the previous, longer row stayed visible. The user sent a message,
// the draft was emptied, the frame was redrawn — and their text was still sitting in the input.

// TestTheSentTextLeavesTheScreen: after a message is sent the composer on the SCREEN is empty,
// not just empty in the frame's own text.
func TestTheSentTextLeavesTheScreen(t *testing.T) {
	scr := newScreen(100, 30)
	tu := New(&fakeRunner{cfg: configWithKey("k")})
	tu.In = strings.NewReader("")
	tu.Width, tu.Height = 100, 30

	// What the user sees while typing.
	tu.draft = "hola mundo"
	var typed bytes.Buffer
	tu.Out = &typed
	tu.drawFrame()
	scr.feed(typed.String())
	if !strings.Contains(scr.text(), "hola mundo") {
		t.Fatalf("the typed text must be on the screen:\n%s", scr.text())
	}

	// Now the message is sent and the composer is emptied, exactly as readLineLive does.
	tu.draft = ""
	var sent bytes.Buffer
	tu.Out = &sent
	tu.drawFrame()
	scr.feed(sent.String())

	if strings.Contains(scr.text(), "hola mundo") {
		t.Errorf("the sent text is still on the screen after sending:\n%s", scr.text())
	}
}

// TestAShorterRowReplacesALongerOne: the general form of the same fault, with no composer
// involved. When a row shrinks, the screen must show the short row and nothing of the long one.
func TestAShorterRowReplacesALongerOne(t *testing.T) {
	scr := newScreen(60, 24)

	long := &bytes.Buffer{}
	tu := New(&fakeRunner{cfg: configWithKey("k")})
	tu.Out = long
	tu.Width, tu.Height = 60, 24
	tu.messages = []Message{{Author: AuthorUser, Text: "una linea de conversacion bastante larga que ocupa todo"}}
	tu.drawFrame()
	scr.feed(long.String())
	if !strings.Contains(scr.text(), "bastante larga") {
		t.Fatalf("the long row must be on the screen:\n%s", scr.text())
	}

	// The same position now holds a much shorter row.
	short := &bytes.Buffer{}
	tu.Out = short
	tu.messages = []Message{{Author: AuthorUser, Text: "corta"}}
	tu.drawFrame()
	scr.feed(short.String())

	if strings.Contains(scr.text(), "bastante larga") {
		t.Errorf("the tail of the longer row survived the shorter one:\n%s", scr.text())
	}
	if !strings.Contains(scr.text(), "corta") {
		t.Errorf("the short row must be on the screen:\n%s", scr.text())
	}
}

// TestTheFrameLeavesNoStaleTailInTheStatus: the status row shrinks when the context report gets
// shorter (a percentage dropping from 100% to 9%, say). Its old tail must not survive either.
func TestTheFrameLeavesNoStaleTailInTheStatus(t *testing.T) {
	scr := newScreen(80, 24)
	tu := New(&fakeRunner{cfg: configWithKey("k")})
	tu.Out = &bytes.Buffer{}
	tu.Width, tu.Height = 80, 24

	r := &fakeRunner{cfg: configWithKey("k"), snapshot: session.Snapshot{Window: 65536, Tokens: 65536, Used: 1.0}}
	tu.Runner = r
	var first bytes.Buffer
	tu.Out = &first
	tu.drawFrame()
	scr.feed(first.String())
	if !strings.Contains(scr.text(), "65536/65536") {
		t.Fatalf("the long status must be on the screen:\n%s", scr.text())
	}

	r.snapshot = session.Snapshot{Window: 65536, Tokens: 2621, Used: 0.04}
	var second bytes.Buffer
	tu.Out = &second
	tu.drawFrame()
	scr.feed(second.String())

	if strings.Contains(scr.text(), "65536/65536") {
		t.Errorf("the old status tail survived:\n%s", scr.text())
	}
}

// TestEveryRowErasesItsTail: the property itself, stated directly. Whatever the frame writes, no
// row may leave cells to the right of it from a previous frame.
//
// It catches a row that forgets the erase as a class, rather than one case at a time.
func TestEveryRowErasesItsTail(t *testing.T) {
	scr := newScreen(70, 30)
	tu := New(&fakeRunner{cfg: configWithKey("k")})
	tu.Width, tu.Height = 70, 30

	// A first frame that fills every row with something long.
	wide := &bytes.Buffer{}
	tu.Out = wide
	tu.messages = nil
	for i := 0; i < 12; i++ {
		tu.messages = append(tu.messages, Message{Author: AuthorAgent, Text: strings.Repeat("X", 60)})
	}
	tu.drawFrame()
	scr.feed(wide.String())

	// A second frame with everything short.
	narrow := &bytes.Buffer{}
	tu.Out = narrow
	tu.messages = nil
	tu.draft = ""
	tu.drawFrame()
	scr.feed(narrow.String())

	if strings.Contains(scr.text(), "XXX") {
		t.Errorf("a row kept cells from the frame before it:\n%s", scr.text())
	}
}

// TestTheEraseIsPerRowNotJustBelow: the frame must erase each line, not only everything below the
// cursor. Clearing below (CSI J) starts AT the cursor, so by the time the loop reaches J every
// row has already been passed and its stale tail is untouched.
func TestTheEraseIsPerRowNotJustBelow(t *testing.T) {
	tu := New(&fakeRunner{cfg: configWithKey("k")})
	tu.Width, tu.Height = 50, 24
	tu.draft = "algo"
	var out bytes.Buffer
	tu.Out = &out
	tu.drawFrame()

	body := out.String()
	if !strings.Contains(body, "\x1b[K") {
		t.Error("each row must erase its own tail")
	}
	// And the erase must come after the row's text, so it does not wipe what was just written.
	composer := strings.LastIndex(body, "›")
	if composer < 0 {
		t.Fatal("the composer must be drawn")
	}
	after := body[composer:]
	if !strings.Contains(after, "\x1b[K") {
		t.Error("the composer row must erase its tail after writing its text")
	}
}

// TestClearingTheInputIsVisibleImmediately: the same check as the first one, driven through
// readLineLive the way the loop drives it — type a line, press Enter, and read the screen.
func TestClearingTheInputIsVisibleImmediately(t *testing.T) {
	scr := newScreen(100, 30)
	tu := New(&fakeRunner{cfg: configWithKey("k")})
	tu.In = strings.NewReader("manda esto\n")
	tu.Width, tu.Height = 100, 30
	tu.charMode = true

	var out bytes.Buffer
	tu.Out = &out

	line, ok := tu.readLineLive(context.Background())
	if !ok || line != "manda esto" {
		t.Fatalf("line=%q ok=%v", line, ok)
	}
	scr.feed(out.String())

	if strings.Contains(scr.text(), "manda esto") {
		t.Errorf("the sent text must not remain on the screen:\n%s", scr.text())
	}
}
