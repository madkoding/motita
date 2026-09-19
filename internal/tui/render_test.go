package tui

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

// The layout has one invariant that is invisible in a unit test but obvious on
// screen: every row of the conversation panel must be exactly the same width, or
// the right border wobbles. These tests measure the frame the way a terminal
// would, after the escape sequences are removed.

// lastFrame returns the final repaint written to the TUI, which is the frame a
// user actually sees once the run is over.
func lastFrame(t *testing.T, tui *TUI) string {
	t.Helper()
	buf, ok := tui.Out.(*bytes.Buffer)
	if !ok {
		t.Fatal("the test TUI does not write to a buffer")
	}
	out := buf.String()
	idx := strings.LastIndex(out, "\x1b[H")
	if idx < 0 {
		t.Fatalf("no frame was painted: %q", out)
	}
	return out[idx:]
}

// panelRows returns every row of the last frame that carries a panel border.
func panelRows(frame string) []string {
	var rows []string
	for _, line := range strings.Split(stripANSI(frame), "\n") {
		if strings.ContainsRune(line, '\u2502') || strings.ContainsRune(line, '\u250c') ||
			strings.ContainsRune(line, '\u2514') {
			rows = append(rows, line)
		}
	}
	return rows
}

func TestPanelRowsHaveEqualWidth(t *testing.T) {
	for _, tc := range []struct {
		name  string
		input string
		width int
	}{
		{"task at 80", "una tarea\nq\n", 80},
		{"plan at 80", "/p\nun prompt\n\nq\n", 80},
		{"task at the minimum width", "t\nq\n", minWidth},
		{"task at the maximum width", "t\nq\n", maxWidth},
		{"plan at the minimum width", "/p\npregunta\n\nq\n", minWidth},
	} {
		t.Run(tc.name, func(t *testing.T) {
			runner := &fakeRunner{planAnswer: "listo"}
			tui := newFakeTUI(tc.input, runner)
			tui.Width, tui.Height = tc.width, 40
			tui.Run(context.Background())

			rows := panelRows(lastFrame(t, tui))
			if len(rows) < 2 {
				t.Fatalf("the panel was not drawn: %q", lastFrame(t, tui))
			}
			want := visibleLen(rows[0])
			for i, row := range rows {
				if got := visibleLen(row); got != want {
					t.Errorf("row %d is %d columns wide, want %d\\n%q\\n%q", i, got, want, rows[0], row)
				}
			}
		})
	}
}

// TestFrameNeverFillsTheLastColumn: a line that fills the row exactly makes some
// terminals wrap, which scrolls the whole interface up by one line on every
// repaint. The frame is deliberately one column short.
func TestFrameNeverFillsTheLastColumn(t *testing.T) {
	runner := &fakeRunner{planAnswer: "listo"}
	tui := newFakeTUI("/p\ncuenta las lineas de un archivo largo de verdad\n\nq\n", runner)
	tui.Width, tui.Height = 80, 40
	tui.Run(context.Background())

	for i, line := range strings.Split(stripANSI(lastFrame(t, tui)), "\n") {
		if got := visibleLen(line); got > 79 {
			t.Errorf("line %d is %d columns wide and would wrap (max 79): %q", i, got, line)
		}
	}
}

// TestCursorLandsAtThePrompt: the cursor is hidden during the repaint and the
// very last thing written is the prompt, so the terminal leaves it there.
func TestCursorLandsAtThePrompt(t *testing.T) {
	runner := &fakeRunner{planAnswer: "listo"}
	tui := newFakeTUI("/p\nprompt\n\nq\n", runner)
	tui.Run(context.Background())

	frame := lastFrame(t, tui)
	if !strings.HasSuffix(frame, "\x1b[?25h") {
		t.Fatalf("the frame must end by showing the cursor: %q", frame)
	}
	withoutCursor := strings.TrimSuffix(frame, "\x1b[?25h")
	tail := withoutCursor[strings.LastIndex(withoutCursor, "\n")+1:]
	if !strings.HasPrefix(stripANSI(tail), "  Plan > ") {
		t.Errorf("the cursor is not parked at the prompt: %q", stripANSI(tail))
	}
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
	for _, row := range bannerLines {
		plain := stripANSI(row)
		if !strings.Contains(frame, plain) {
			t.Errorf("the wordmark row %q is missing from the frame", plain)
		}
	}
}

// TestCompactMarkOnANarrowTerminal: the five-row wordmark is 73 columns wide and
// cannot be shown in a narrow window, so the single-line mark replaces it rather
// than being wrapped or clipped.
func TestCompactMarkOnANarrowTerminal(t *testing.T) {
	runner := &fakeRunner{}
	tui := newFakeTUI("q\n", runner)
	tui.Width, tui.Height = minWidth, 40
	tui.Run(context.Background())

	frame := stripANSI(lastFrame(t, tui))
	if !strings.Contains(frame, compactMark) {
		t.Errorf("a narrow terminal must fall back to %q: %q", compactMark, frame)
	}
	for _, row := range bannerLines {
		if strings.Contains(frame, stripANSI(row)) {
			t.Error("the wide wordmark must not be drawn when it does not fit")
		}
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
	for _, row := range bannerLines {
		if !strings.Contains(body, stripANSI(row)) {
			t.Errorf("the plain wordmark row %q is missing", stripANSI(row))
		}
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
	if !strings.Contains(frame, "starlight") || !strings.Contains(frame, "20") {
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
	tui = &TUI{Width: 500, Height: 0}
	if w, _ := tui.size(); w != maxWidth {
		t.Errorf("a huge terminal must be clamped to %d, got %d", maxWidth, w)
	}
}

// TestFrameFitsTheTerminalHeight: a frame that is one row too tall makes the
// shell scroll, which pushes the wordmark and the prompt off the screen on every
// repaint. The frame must be measured against the terminal before it is written,
// and it must shed content in a defined order when there is not enough room.
func TestFrameFitsTheTerminalHeight(t *testing.T) {
	runner := &fakeRunner{planAnswer: "una respuesta larga que ocupa bastante espacio en pantalla y obliga a recortar el marco"}
	for _, h := range []int{12, 16, 20, 24, 30, 40, 60} {
		tui := newFakeTUI("/p\nun prompt con bastante texto para llenar la conversacion\n\nq\n", runner)
		tui.Width, tui.Height = 80, h
		tui.Run(context.Background())

		lines, _ := tui.layout(80, h)
		if len(lines) > h {
			t.Errorf("height %d: the frame has %d rows and would scroll\n%s",
				h, len(lines), strings.Join(lines, "\n"))
		}
	}

	// The prompt is the one row that may never be dropped, whatever the height.
	tui := newFakeTUI("q\n", &fakeRunner{})
	tui.Width, tui.Height = 80, 8
	tui.Run(context.Background())
	_, prompt := tui.layout(80, 8)
	if !strings.Contains(stripANSI(prompt), "Task >") {
		t.Errorf("the prompt must survive any height, got %q", stripANSI(prompt))
	}
}

// TestSheddingOrder: when the terminal is short, the key hints go first, then the
// wordmark (five rows, then the one-line mark, then nothing) and only then the
// conversation. The order matters: the hints are the most redundant part of the
// screen, the wordmark is identity, and the conversation is the content.
func TestSheddingOrder(t *testing.T) {
	runner := &fakeRunner{}
	tui := newFakeTUI("q\n", runner)
	tui.Width = 80

	full, _ := tui.layout(80, 60)
	if strings.Contains(strings.Join(full, "\n"), compactMark) {
		t.Error("a tall terminal must show the full wordmark, not the compact one")
	}

	// Shorter: the hints go first, the banner stays.
	short, _ := tui.layout(80, 20)
	frame := strings.Join(short, "\n")
	if strings.Contains(frame, "switch mode") {
		t.Errorf("the hints must be dropped first:\n%s", frame)
	}
	if !strings.Contains(frame, stripANSI(bannerLines[0])) {
		t.Errorf("the wordmark must survive the loss of the hints:\n%s", frame)
	}

	// Shorter still: the wordmark goes in one step, leaving the conversation.
	tiny, _ := tui.layout(80, 15)
	frame = strings.Join(tiny, "\n")
	if strings.Contains(frame, compactMark) {
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
