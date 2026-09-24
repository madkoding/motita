package tui

import (
	"bytes"
	"context"
	"fmt"
	"regexp"
	"strings"
	"testing"
)

// The layout has one invariant that is invisible in a unit test but obvious on
// screen: every row of the conversation panel must be exactly the same width, or
// the right border wobbles. These tests measure the frame the way a terminal
// would, after the escape sequences are removed.

// lastFrame returns what the terminal would be SHOWING, as text with one row per line.
//
// It used to return the raw bytes between the last cursor-home and the end, which only worked
// while a frame was written as a run of rows separated by newlines. Now that a repaint writes
// only the rows that changed — each one preceded by its own absolute position — the bytes are
// not a picture of the screen any more: joining them concatenates rows that were never adjacent.
//
// So the frame is replayed into the same minimal emulator the screen tests use, and the rows
// come out of its cells. That is strictly stronger than before: these tests now check what is
// displayed rather than what was written, which is the distinction that let a stale input row
// survive four rounds of green tests.
//
// The exit wipe is applied first, because leaving the interface clears the screen: it is
// written after the last frame and would otherwise blank what this returns.
func lastFrame(t *testing.T, tui *TUI) string {
	t.Helper()
	buf, ok := tui.Out.(*bytes.Buffer)
	if !ok {
		t.Fatal("the test TUI does not write to a buffer")
	}
	out := strings.TrimSuffix(buf.String(), exitClear)
	if !strings.Contains(out, "\x1b[") {
		t.Fatalf("no frame was painted: %q", out)
	}
	w, h := tui.size()
	if w <= 0 || h <= 0 {
		w, h = 80, 24
	}
	sc := newScreen(w, h)
	sc.feed(out)
	return sc.text()
}

// TestEveryRowFitsTheDrawingArea: the frame's width invariant. The conversation is no longer
// boxed, so what must hold is that no row is wider than the area it is drawn into — the
// layout leaves one column free so a terminal never wraps the last one and scrolls the whole
// interface up by a line on every repaint.
func TestEveryRowFitsTheDrawingArea(t *testing.T) {
	for _, tc := range []struct {
		name  string
		input string
		width int
	}{
		{"task at 80", "a task\nq\n", 80},
		{"plan at 80", "/p\nun prompt\n\nq\n", 80},
		{"task at the minimum width", "t\nq\n", minWidth},
		{"task at a wide terminal", "t\nq\n", 240},
		{"plan at the minimum width", "/p\nquestion\n\nq\n", minWidth},
	} {
		t.Run(tc.name, func(t *testing.T) {
			runner := &fakeRunner{planAnswer: "ready"}
			tui := newFakeTUI(tc.input, runner)
			tui.Width, tui.Height = tc.width, 40
			tui.Run(context.Background())

			frame := lastFrame(t, tui)
			for i, line := range strings.Split(stripANSI(frame), "\n") {
				if got := visibleLen(line); got > tc.width {
					t.Errorf("row %d is %d columns, past the %d-column terminal:\n%q", i, got, tc.width, line)
				}
			}
			// And the structure is there: two rules bracketing the conversation.
			plain := stripANSI(frame)
			if n := strings.Count(plain, strings.Repeat(glyphRule, 10)); n < 2 {
				t.Errorf("the two rules must bracket the conversation, found %d:\n%s", n, plain)
			}
		})
	}
}

// TestFrameNeverFillsTheLastColumn: a line that fills the row exactly makes some
// terminals wrap, which scrolls the whole interface up by one line on every
// repaint. The frame is deliberately one column short.
func TestFrameNeverFillsTheLastColumn(t *testing.T) {
	runner := &fakeRunner{planAnswer: "ready"}
	tui := newFakeTUI("/p\ncount the lines of a genuinely long file\n\nq\n", runner)
	tui.Width, tui.Height = 80, 40
	tui.Run(context.Background())

	for i, line := range strings.Split(stripANSI(lastFrame(t, tui)), "\n") {
		if got := visibleLen(line); got > 79 {
			t.Errorf("line %d is %d columns wide and would wrap (max 79): %q", i, got, line)
		}
	}
}

// TestCursorLandsAtThePrompt: the cursor is placed at the prompt, and the terminal is left
// SHOWING it.
//
// This one reads the written stream rather than the screen, and it has to: showing the cursor
// is not a cell, it is a mode, so an emulator of cells cannot see it. The screen is still
// checked separately (lastFrame) for where the rows ended up.
func TestCursorLandsAtThePrompt(t *testing.T) {
	runner := &fakeRunner{planAnswer: "ready"}
	tui := newFakeTUI("/p\nprompt\n\nq\n", runner)
	tui.Run(context.Background())

	buf, ok := tui.Out.(*bytes.Buffer)
	if !ok {
		t.Fatal("the test TUI does not write to a buffer")
	}
	stream := strings.TrimSuffix(buf.String(), exitClear)
	if !strings.HasSuffix(stream, "\x1b[?25h") {
		t.Fatalf("the stream must end by showing the cursor: %q", tail(stream, 60))
	}
	// The cursor is moved UP to the input field rather than left at the end of the frame. Below
	// the input's first row sit the rest of the field, the rule and the status bar, so a cursor
	// at the end of the frame would be outside the box it is meant to be in.
	want := fmt.Sprintf("\x1b[%dA", 2+inputRows-1)
	if !strings.Contains(stream, want) {
		t.Errorf("the cursor must be walked up to the input field, expected %q in:\n%q",
			want, tail(stream, 200))
	}
	// And it lands at a COLUMN, which is what "after the prompt glyph" means. The sequence is
	// "CSI <n> G" with the column number, so matching the bare "CSI G" finds nothing.
	if !regexp.MustCompile(`\x1b\[\d+G`).MatchString(stream) {
		t.Errorf("the cursor must be moved to a column, not just a row:\n%q", tail(stream, 200))
	}
	// The composer row itself is drawn, with the prompt on it. It carries no mode name: the
	// status bar already reports that.
	body := lastFrame(t, tui)
	if !strings.Contains(body, "›") {
		t.Errorf("the composer must be drawn:\n%s", body)
	}
	if strings.Count(body, "Task") > 1 || strings.Count(body, "Plan") > 1 {
		t.Errorf("the mode must be named once:\n%s", body)
	}
}

// tail returns the last n bytes of s, for readable failure messages.
func tail(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return "..." + s[len(s)-n:]
}

// TestWordmarkIsNotFramed: the banner was inside a box and the user asked for the
// box to go. The only borders left belong to the conversation panel.
func TestWordmarkIsNotFramed(t *testing.T) {
	runner := &fakeRunner{}
	tui := newFakeTUI("q\n", runner)
	tui.Width, tui.Height = 80, 40
	tui.Run(context.Background())

	frame := stripANSI(lastFrame(t, tui))
	if strings.Contains(frame, "\u2554") || strings.Contains(frame, "\u2551") || strings.Contains(frame, "\u255d") {
		t.Errorf("the wordmark must not be framed any more: %q", frame)
	}
	if !strings.Contains(frame, "motita") {
		t.Errorf("the wordmark text must be present in the frame: %q", frame)
	}
}

// TestTheVersionIsDrawnUnderTheWordmark: the answer to "which build am I running" has to be
// readable from the interface itself. Nothing else on screen names the build, so after installing
// a new binary over an old one there is no way to tell which one is actually answering.
func TestTheVersionIsDrawnUnderTheWordmark(t *testing.T) {
	runner := &fakeRunner{}
	tui := newFakeTUI("q\n", runner)
	tui.Version = "v1.2.3-abc"
	tui.Run(context.Background())

	frame := stripANSI(lastFrame(t, tui))
	if !strings.Contains(frame, "v1.2.3-abc") {
		t.Fatalf("the version is not on the frame: %q", frame)
	}
}

// A build with no version says nothing rather than drawing an empty box: an embedder that never
// sets Version must see the frame it saw before this feature existed.
func TestAnInterfaceWithNoVersionDrawsNothingExtra(t *testing.T) {
	named := newFakeTUI("q\n", &fakeRunner{})
	named.Version = "v1.2.3-abc"
	named.Run(context.Background())

	anon := newFakeTUI("q\n", &fakeRunner{})
	anon.Run(context.Background())

	if strings.Contains(stripANSI(lastFrame(t, anon)), "v1.2.3-abc") {
		t.Fatalf("a version appeared with none set: %q", stripANSI(lastFrame(t, anon)))
	}
	if n, a := strings.Count(stripANSI(lastFrame(t, named)), "\n"), strings.Count(stripANSI(lastFrame(t, anon)), "\n"); n != a {
		t.Fatalf("setting a version changed the frame height: %d rows named, %d anonymous", n, a)
	}
}

// TestAVersionTooNarrowToReadIsNotDrawn: the version row REPLACES the blank row under the
// wordmark instead of adding one. If it added a row, the vertical budget would grow by one and
// the bottom of the interface would be pushed off a small terminal - which is the bug this test
// exists to catch, not a cosmetic preference.
func TestAVersionTooNarrowToReadIsNotDrawn(t *testing.T) {
	wide := newFakeTUI("q\n", &fakeRunner{})
	wide.Version = "v1.2.3-abc"
	wide.Run(context.Background())

	narrow := newFakeTUI("q\n", &fakeRunner{})
	narrow.Version = strings.Repeat("9", 200) // longer than any terminal
	narrow.Run(context.Background())

	wideLines := strings.Count(stripANSI(lastFrame(t, wide)), "\n")
	narrowLines := strings.Count(stripANSI(lastFrame(t, narrow)), "\n")
	if wideLines != narrowLines {
		t.Fatalf("the version row changed the frame height: %d rows with it, %d without", wideLines, narrowLines)
	}
	if strings.Contains(stripANSI(lastFrame(t, narrow)), "9999") {
		t.Fatal("a version that cannot be read in full was drawn anyway, cut to fit")
	}
}

// TestNoColourStripsTheWordmark: NO_COLOR mode must not emit escape sequences,
// which includes the ones baked into the wordmark.
func TestNoColourStripsTheWordmark(t *testing.T) {
	runner := &fakeRunner{}
	tui := newFakeTUI("q\n", runner)
	tui.NoColor = true
	tui.Width, tui.Height = 80, 40
	tui.Run(context.Background())

	frame := lastFrame(t, tui)
	body := strings.TrimSuffix(frame, "\x1b[?25h")
	if strings.Contains(body, "\x1b[0;97m") {
		t.Errorf("NO_COLOR must strip the wordmark's own colours: %q", body)
	}
	if !strings.Contains(body, "motita") {
		t.Errorf("the plain wordmark text must be present")
	}
}

// TestEmptyStatePerScreen: an empty view is designed, not blank, and each mode
// explains what it is for.
func TestEmptyStatePerScreen(t *testing.T) {
	for _, tc := range []struct {
		screen Screen
		want   string
	}{
		{ScreenTask, "Describe a task and press Enter."},
		{ScreenPlan, "Ask a question and press Enter."},
		{ScreenModels, "Press Enter to ask the provider"},
		{ScreenConfig, "Press Enter to walk through the first-run wizard"},
	} {
		t.Run(tc.screen.String(), func(t *testing.T) {
			tui := newFakeTUI("q\n", &fakeRunner{})
			tui.screen = tc.screen
			tui.Width, tui.Height = 80, 40
			tui.Run(context.Background())
			frame := stripANSI(lastFrame(t, tui))
			if !strings.Contains(frame, tc.want) {
				t.Errorf("the empty %s view must explain itself: %q", tc.screen, frame)
			}
		})
	}
}

// TestTypingInAModelsViewDoesNotRerunIt: the catalogue is an action. Any key other
// than Enter must not run it again and write over the report being read.
func TestTypingInAModelsViewDoesNotRerunIt(t *testing.T) {
	runner := &fakeRunner{}
	// The input has exactly two empty lines: the one that runs the view and the
	// one implied by the end of the typed word. A third Enter would legitimately
	// run it again.
	tui := newFakeTUI("/m\n\nsomething\nq\n", runner)
	tui.Run(context.Background())

	if runner.modelsCalls != 2 {
		t.Errorf("RunModels ran %d times, want 2 (the /m and the empty line only)", runner.modelsCalls)
	}
	if !strings.Contains(stripANSI(outputOf(tui)), "press Enter to refresh this view") {
		t.Errorf("typed text in the models view must be answered with a hint: %q", stripANSI(outputOf(tui)))
	}
}

// TestToolCallBecomesItsOwnLine: a tool announcement is an event, not prose, and
// it is rendered as a labelled line rather than merged into the answer.
func TestToolCallBecomesItsOwnLine(t *testing.T) {
	runner := &fakeRunner{planProgress: []string{"[using tool: execute_command]", "20"}}
	tui := newFakeTUI("/p\ncuenta\n\nq\n", runner)
	tui.Width, tui.Height = 80, 40
	tui.Run(context.Background())

	frame := stripANSI(lastFrame(t, tui))
	if !strings.Contains(frame, "using execute_command") {
		t.Errorf("the tool call must be shown as its own line: %q", frame)
	}
	if !strings.Contains(frame, "motita") || !strings.Contains(frame, "20") {
		t.Errorf("the answer must still be shown after the tool event: %q", frame)
	}
}

// TestSpinnerAppearsWhileRunning: a turn that takes seconds needs movement on
// screen, otherwise the interface looks hung.
func TestSpinnerAppearsWhileRunning(t *testing.T) {
	runner := &fakeRunner{}
	tui := newFakeTUI("q\n", runner)
	tui.busy = true
	tui.spin = 1
	if got := tui.stateGlyph(); !strings.Contains(got, spinner[1]) {
		t.Errorf("a busy session must show a spinner frame, got %q", got)
	}
	tui.busy = false
	// This TUI carries no key, so its steady state is the "nothing to talk to"
	// glyph rather than the ready one.
	if got := tui.stateGlyph(); !strings.Contains(got, glyphMissing) {
		t.Errorf("an idle keyless session must show its own glyph, got %q", got)
	}
}

// TestVisibleLenCountsColumns: the whole layout depends on this measurement, and
// a naive "stop at the first byte in 0x40..0x7e" rule is wrong because the '[' of
// a CSI sequence is itself in that range.
func TestVisibleLenCountsColumns(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want int
	}{
		{"abc", 3},
		{"", 0},
		{"\x1b[31mred\x1b[0m", 3},
		{"\x1b[0;97;47m\u2593\u2592\x1b[0m", 2},
		{"\x1b[0m", 0},
		{"a\x1b[1mb\x1b[0mc", 3},
	} {
		if got := visibleLen(tc.in); got != tc.want {
			t.Errorf("visibleLen(%q) = %d, want %d", tc.in, got, tc.want)
		}
	}
}

// TestStripANSIRemovesEverything: no escape may survive into no-colour output.
func TestStripANSIRemovesEverything(t *testing.T) {
	if got := stripANSI("\x1b[0;97m\u2580\u2580\x1b[0;37m\x1b[0m"); got != "\u2580\u2580" {
		t.Errorf("stripANSI left %q", got)
	}
}

// TestWordWrapBreaksLongWords: a single word wider than the panel must be split,
// which is the difference between a wrapped line and a broken border.
func TestWordWrapBreaksLongWords(t *testing.T) {
	got := wordWrap("aaaaaaaaaaaaaaaaaaaa", 6)
	if len(got) != 4 {
		t.Fatalf("wordWrap produced %d lines, want 4: %q", len(got), got)
	}
	for _, line := range got {
		if visibleLen(line) > 6 {
			t.Errorf("line %q is %d columns, want at most 6", line, visibleLen(line))
		}
	}
}

// TestSizeIsClamped: a tiny terminal still gets a drawable area instead of a
// panic or a zero-width panel.
func TestSizeIsClamped(t *testing.T) {
	tui := &TUI{Width: 10, Height: 5}
	w, h := tui.size()
	if w != minWidth || h != 5 {
		t.Errorf("size = (%d, %d), want (%d, 5)", w, h, minWidth)
	}
	// A wide terminal is NOT clamped. There used to be a maximum width that stopped the
	// interface in the middle of a wide window and left the rest blank, which the user saw as
	// the frame failing to fill the screen. This test used to assert that cap as correct, which
	// is how it survived: the expectation was written against the behaviour instead of the goal.
	tui = &TUI{Width: 500, Height: 0}
	if w, _ := tui.size(); w != 500 {
		t.Errorf("a wide terminal must be used in full, got %d", w)
	}
}

// TestFrameFitsTheTerminalHeight: a frame that is one row too tall makes the
// shell scroll, which pushes the wordmark and the prompt off the screen on every
// repaint. The frame must be measured against the terminal before it is written,
// and it must shed content in a defined order when there is not enough room.
func TestFrameFitsTheTerminalHeight(t *testing.T) {
	runner := &fakeRunner{planAnswer: "a long reply that takes up a lot of screen space and forces the frame to be clipped"}
	for _, h := range []int{12, 16, 20, 24, 30, 40, 60} {
		tui := newFakeTUI("/p\na prompt with enough text to fill the conversation\n\nq\n", runner)
		tui.Width, tui.Height = 80, h
		tui.Run(context.Background())

		lines, _ := tui.layout(80, h)
		if len(lines) > h {
			t.Errorf("height %d: the frame has %d rows and would scroll\n%s",
				h, len(lines), strings.Join(lines, "\n"))
		}
	}

	// The composer is the one row that may never be dropped. It is found by looking in the frame
	// rather than in the returned prompt: the prompt is now a cursor movement, and the row it
	// points at is what has to be there.
	tui := newFakeTUI("q\n", &fakeRunner{})
	tui.Width, tui.Height = 80, minHeight
	tui.Run(context.Background())

	// At the smallest workable height the composer is drawn...
	lines, prompt := tui.layout(80, minHeight)
	if prompt == "" {
		t.Error("the layout must place the cursor at the smallest workable height")
	}
	found := false
	for _, l := range lines {
		if strings.Contains(stripANSI(l), "›") {
			found = true
		}
	}
	if !found {
		t.Errorf("the composer must survive the smallest workable height:\n%s", stripANSI(strings.Join(lines, "\n")))
	}
	if len(lines) > minHeight {
		t.Errorf("the frame must fit the smallest workable height: %d rows in %d", len(lines), minHeight)
	}

	// ...and below it the gate explains instead of drawing a frame nobody can use. Both sides of
	// the threshold are asserted, because a frame that overflows and a frame that refuses are
	// different failures and only one of them is acceptable.
	lines, prompt = tui.layout(80, minHeight-1)
	if prompt != "" {
		t.Error("below the smallest height there is no composer to place a cursor on")
	}
	if len(lines) == 0 {
		t.Error("below the smallest height the interface must explain itself, not draw nothing")
	}
	body := stripANSI(strings.Join(lines, "\n"))
	if !strings.Contains(body, fmt.Sprint(minHeight)) {
		t.Errorf("the explanation must name the height it needs (%d):\n%s", minHeight, body)
	}
}

// TestSheddingOrder: when the terminal is short, the key hints go first, then the
// wordmark and only then the conversation. The order matters: the hints are the
// most redundant part of the screen, the wordmark is identity, and the
// conversation is the content.
func TestSheddingOrder(t *testing.T) {
	runner := &fakeRunner{}
	tui := newFakeTUI("q\n", runner)
	tui.Width = 80

	full, _ := tui.layout(80, 60)
	if !strings.Contains(stripANSI(strings.Join(full, "\n")), "motita") {
		t.Error("a tall terminal must show the wordmark")
	}

	// Shorter: the hints go first, the wordmark stays.
	short, _ := tui.layout(80, 20)
	frame := strings.Join(short, "\n")
	if strings.Contains(frame, "switch mode") {
		t.Errorf("the hints must be dropped first:\n%s", frame)
	}
	if !strings.Contains(stripANSI(frame), "motita") {
		t.Errorf("the wordmark must survive the loss of the hints:\n%s", frame)
	}

	// Shorter still: the wordmark goes, leaving the conversation.
	tiny, _ := tui.layout(80, 15)
	frame = strings.Join(tiny, "\n")
	if strings.Contains(stripANSI(frame), "motita") {
		t.Errorf("the wordmark must be dropped on a very short terminal:\n%s", frame)
	}
	if !strings.Contains(frame, "Task") {
		t.Errorf("the mode line and the conversation must remain:\n%s", frame)
	}
}

// TestSizeFallsBackToTheColourVariables: with no explicit size, an interactive
// shell's COLUMNS/LINES are what describes the window.
func TestSizeFallsBackToTheColourVariables(t *testing.T) {
	// The probe is stubbed to "unknown" so the environment is what answers. Without
	// this the test would pass or fail depending on whether the machine it runs on has
	// a terminal — and since the probe is now consulted BEFORE the environment, a real
	// terminal would override these values and the assertion would be about nothing.
	restore := stubTTYSize(0, 0, false)
	defer restore()

	t.Setenv("COLUMNS", "100")
	t.Setenv("LINES", "30")
	tui := &TUI{}
	w, h := tui.size()
	if w != 100 || h != 30 {
		t.Errorf("size = (%d, %d), want (100, 30)", w, h)
	}
	t.Setenv("COLUMNS", "not-a-number")
	if w, _ := tui.size(); w != defaultWidth {
		t.Errorf("an unparsable COLUMNS must fall back to %d, got %d", defaultWidth, w)
	}
	t.Setenv("COLUMNS", "0")
	if w, _ := tui.size(); w != defaultWidth {
		t.Errorf("a zero COLUMNS must fall back to %d, got %d", defaultWidth, w)
	}
	t.Setenv("COLUMNS", "100")
	t.Setenv("LINES", "not-a-number")
	if _, h := tui.size(); h != 0 {
		t.Errorf("an unparsable LINES means the height is unknown, got %d", h)
	}
}
