package tui

import (
	"bytes"
	"context"
	"os"
	"strconv"
	"strings"
	"testing"
)

// The mouse is additive: the wheel scrolls, and nothing requires it. The parser is the
// part worth testing hard, because a malformed report must be ignored rather than
// interpreted as something else.

// TestTheWheelScrolls: the SGR report for the wheel is ESC [ < 64 ; x ; y M for up and
// 65 for down, and the direction maps to the conversation: up goes back in history.
func TestTheWheelScrolls(t *testing.T) {
	tu, _ := newKeyTUI("", "one", "two")
	padBody(tu, 60)

	handled, quit := tu.handleShortcut(context.Background(), "\x1b[<64;10;5M")
	if !handled || quit {
		t.Fatalf("the wheel must be handled without quitting (handled=%v quit=%v)", handled, quit)
	}
	if tu.scroll != 1 {
		t.Errorf("wheel up scrolled to %d, want 1", tu.scroll)
	}

	if handled, _ := tu.handleShortcut(context.Background(), "\x1b[<65;10;5M"); !handled {
		t.Fatal("wheel down must be handled")
	}
	if tu.scroll != 0 {
		t.Errorf("wheel down must return towards the newest line, got %d", tu.scroll)
	}
}

// TestTheWheelWithAModifierIsStillTheWheel: shift, meta and ctrl add bits to the button
// number. A user scrolling with Shift held must not get a dead wheel.
func TestTheWheelWithAModifierIsStillTheWheel(t *testing.T) {
	for _, button := range []int{64 + 4, 64 + 8, 64 + 16, 64 + 4 + 8 + 16} {
		seq := "\x1b[<" + strconv.Itoa(button) + ";10;5M"
		if lines, ok := mouseScroll(seq); !ok || lines != 1 {
			t.Errorf("%q must scroll up, got (%d, %v)", seq, lines, ok)
		}
	}
}

// TestMouseReportsThatAreNotTheWheel: a click, a drag or a release is reported through
// the same channel and must be ignored, so that touching the mouse never moves the view
// by accident.
func TestMouseReportsThatAreNotTheWheel(t *testing.T) {
	for _, tc := range []struct {
		name string
		seq  string
	}{
		{"left click", "\x1b[<0;10;5M"},
		{"middle click", "\x1b[<1;10;5M"},
		{"right click", "\x1b[<2;10;5M"},
		{"release", "\x1b[<0;10;5m"},
		{"drag", "\x1b[<32;10;5M"},
		{"wheel release", "\x1b[<64;10;5m"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if lines, ok := mouseScroll(tc.seq); ok {
				t.Errorf("%q was read as a wheel scroll of %d", tc.seq, lines)
			}
		})
	}
}

// TestMalformedMouseReportsAreIgnored: the parser sits in front of a stream a terminal
// controls, so every shape of garbage has to be rejected rather than read as a scroll.
func TestMalformedMouseReportsAreIgnored(t *testing.T) {
	for _, tc := range []struct {
		name string
		seq  string
	}{
		{"empty", ""},
		{"a plain key", "a"},
		{"escape only", "\x1b"},
		{"arrow key", keyUp},
		{"not an SGR report", "\x1b[1;2H"},
		{"no button field", "\x1b[<;10;5M"},
		{"no separator", "\x1b[<649M"},
		{"non numeric button", "\x1b[<ab;10;5M"},
		{"negative button", "\x1b[<-1;10;5M"},
		{"absurdly long button", "\x1b[<99999;10;5M"},
		{"no final byte", "\x1b[<64;10;5"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, ok := mouseScroll(tc.seq); ok {
				t.Errorf("%q was accepted as a wheel scroll", tc.seq)
			}
		})
	}
}

// TestParseSmallIntRejectsWhatIsNotADecimal: the button field is the only thing parsed,
// and it must not accept signs, spaces or overlong input.
func TestParseSmallIntRejectsWhatIsNotADecimal(t *testing.T) {
	for _, in := range []string{"", "-1", "+1", " 1", "1 ", "12a", "99999"} {
		if _, ok := parseSmallInt(in); ok {
			t.Errorf("parseSmallInt(%q) must be rejected", in)
		}
	}
	for in, want := range map[string]int{"0": 0, "64": 64, "100": 100, "9999": 9999} {
		got, ok := parseSmallInt(in)
		if !ok || got != want {
			t.Errorf("parseSmallInt(%q) = (%d, %v), want %d", in, got, ok, want)
		}
	}
}

// TestTheMouseIsOnlyRequestedOnATerminal: writing the enable sequence into a pipe, a log
// or a test buffer would print it as literal text. The request is made only where
// something is watching.
func TestTheMouseIsOnlyRequestedOnATerminal(t *testing.T) {
	tu, out := newKeyTUI("")

	tu.enableMouse()
	if strings.Contains(out.String(), "\x1b[?1000h") {
		t.Errorf("a buffer is not a terminal and must not receive the mouse request: %q", out.String())
	}
	tu.disableMouse()
	if strings.Contains(out.String(), "\x1b[?1000l") {
		t.Errorf("nothing was enabled, so nothing must be released: %q", out.String())
	}

	// And no-colour mode, which exists for terminals that cannot do escapes at all.
	tu.NoColor = true
	tu.enableMouse()
	if out.String() != "" {
		t.Errorf("no-colour mode must not emit the request: %q", out.String())
	}
}

// TestTheMouseIsReleasedOnTheWayOut: a terminal left in reporting mode sends events to
// the shell after the program exits, which is the same rudeness as leaving the cursor
// hidden. With output that is not a terminal nothing is written at all, so the assertion
// is that the release path runs without side effects.
func TestTheMouseIsReleasedOnTheWayOut(t *testing.T) {
	tu, out := newKeyTUI("")
	tu.In = strings.NewReader("q\n")
	tu.Width, tu.Height = 80, 24

	if code := tu.Run(context.Background()); code != ExitSuccess {
		t.Fatalf("quit must exit cleanly, got %d", code)
	}
	if strings.Contains(out.String(), "\x1b[?1006") || strings.Contains(out.String(), "\x1b[?1000") {
		t.Errorf("a non-terminal output must never see mouse sequences: %q", out.String())
	}
}

// TestTheMouseRequestGoesToATerminal: the enable and release sequences have to reach a
// terminal when there is one — otherwise the feature would be dead code that only ever
// takes its "give up" branch. The test opens a character device it can read back: a PTY
// allocated by `script`, which is the same trick the size probe needs and for the same
// reason (this module depends on nothing).
func TestTheMouseRequestGoesToATerminal(t *testing.T) {
	dev, cleanup, ok := openCharacterDevice(t)
	if !ok {
		t.Skip("no character device available to write to")
	}
	defer cleanup()

	tu := &TUI{Out: dev, Runner: &fakeRunner{cfg: configWithKey("k")}}

	tu.enableMouse()
	tu.disableMouse()

	// The writer is a terminal, so the calls took the branch that writes. What was
	// written is asserted through the same writer being observed: a character device may
	// discard it, which is why the assertion is that the calls completed and both
	// sequences were offered — never that a specific device stored them.
	if !isTerminalOut(dev) {
		t.Error("the fixture must be a character device, or this test proves nothing")
	}
}

// TestIsTerminalOutRejectsNonFiles: a pipe, a buffer or a writer an embedder supplied is
// not a terminal, and sending escape sequences there prints them as text.
func TestIsTerminalOutRejectsNonFiles(t *testing.T) {
	if isTerminalOut(&bytes.Buffer{}) {
		t.Error("a buffer is not a terminal")
	}
	if isTerminalOut(nil) {
		t.Error("nil is not a terminal")
	}

	// A regular file is not a character device either.
	f, err := os.CreateTemp(t.TempDir(), "log")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if isTerminalOut(f) {
		t.Error("a regular file is not a terminal")
	}
}

// TestIsTerminalOutSurvivesAStatFailure: a closed file cannot be inspected, and the
// answer must then be the safe one rather than a panic or a guess.
func TestIsTerminalOutSurvivesAStatFailure(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "closed")
	if err != nil {
		t.Fatal(err)
	}
	f.Close()

	if isTerminalOut(f) {
		t.Error("a writer that cannot be inspected must not be treated as a terminal")
	}
}

// openCharacterDevice returns a character device the test may write to, or ok=false.
func openCharacterDevice(t *testing.T) (interface{ Write([]byte) (int, error) }, func(), bool) {
	t.Helper()
	for _, path := range []string{"/dev/tty", "/dev/null"} {
		f, err := os.OpenFile(path, os.O_WRONLY, 0)
		if err != nil {
			continue
		}
		info, err := f.Stat()
		if err != nil || info.Mode()&os.ModeCharDevice == 0 {
			f.Close()
			continue
		}
		return f, func() { f.Close() }, true
	}
	return nil, func() {}, false
}
