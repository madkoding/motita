package tui

import (
	"strings"
	"testing"
)

// These tests cover the parts of the layout that only run at a size the other
// tests do not use: the clamps, the truncation paths and the branches that exist
// to handle a terminal nobody would choose but that still must not break the
// frame.

// TestInnerHasAFloor: a terminal narrower than the panel arithmetic can express
// must still produce a usable inner width instead of a negative one, which would
// make every padding computation overflow.
func TestInnerHasAFloor(t *testing.T) {
	for _, w := range []int{1, 2, 5, 12, 20, minWidth} {
		tui := &TUI{Width: w}
		if got := tui.inner(); got < 8 {
			t.Errorf("width %d: inner = %d, want at least 8", w, got)
		}
	}
}

// TestFrameColsLeavesTheLastColumn: the frame is deliberately one column short of
// the terminal, so a full row never wraps.
func TestFrameColsLeavesTheLastColumn(t *testing.T) {
	tui := &TUI{Width: 80}
	if got := tui.frameCols(); got != 79 {
		t.Errorf("frameCols = %d, want 79", got)
	}
	// A one-column terminal cannot be narrowed below the minimum the layout can
	// draw: size() clamps it first.
	tui = &TUI{Width: 1}
	if got := tui.frameCols(); got != minWidth-1 {
		t.Errorf("frameCols = %d, want %d", got, minWidth-1)
	}
}

// TestStatusLinesShedsItsTailWhenNarrow: the model is the most important item on
// the status line, so a narrow terminal must drop the reasoning level and the key
// state rather than clip the model.
func TestStatusLinesShedsItsTailWhenNarrow(t *testing.T) {
	tui := newFakeTUI("q\n", &fakeRunner{})
	tui.Width = 60

	wide := strings.Join(tui.statusLines(100), "\n")
	if !strings.Contains(stripANSI(wide), "reasoning") || !strings.Contains(stripANSI(wide), "key ") {
		t.Errorf("a wide status line must show everything:\n%s", stripANSI(wide))
	}

	narrow := strings.Join(tui.statusLines(20), "\n")
	plain := stripANSI(narrow)
	if strings.Contains(plain, "reasoning") {
		t.Errorf("a narrow status line must drop the reasoning level first:\n%s", plain)
	}
	if !strings.Contains(plain, "gpt-4o-mini") {
		t.Errorf("the model must survive the truncation:\n%s", plain)
	}
}

// TestStateGlyphReportsAKeylessSession: a session with no key is not "ready", and
// the status dot has to say so in red rather than green.
func TestStateGlyphReportsAKeylessSession(t *testing.T) {
	runner := &fakeRunner{cfg: configWithKey(""), cfgSet: true}
	tui := newFakeTUI("q\n", runner)
	tui.NoColor = false
	if got := tui.stateGlyph(); !strings.Contains(got, glyphMissing) {
		t.Errorf("a keyless session must show its own glyph, got %q", got)
	}
	if got := tui.stateGlyph(); !strings.Contains(got, "\x1b[31m") {
		t.Errorf("a keyless session must be red, got %q", got)
	}

	runner2 := &fakeRunner{cfg: configWithKey("k"), cfgSet: true}
	tui2 := newFakeTUI("q\n", runner2)
	tui2.NoColor = false
	if got := tui2.stateGlyph(); !strings.Contains(got, "\x1b[32m") {
		t.Errorf("a ready session must be green, got %q", got)
	}
}

// TestChatTopRowKeepsTheBorderStraight: the mode name is drawn inside the top
// border, so the rule has to shrink by exactly that much. A wrong count is only
// visible as a border that stops short of its corner.
func TestChatTopRowKeepsTheBorderStraight(t *testing.T) {
	tui := newFakeTUI("q\n", &fakeRunner{})
	tui.Width = 80
	for _, s := range screenOrder {
		tui.screen = s
		top := stripANSI(strings.Join(tui.chatTopRow(), "\n"))
		bot := stripANSI(tui.chatBottomRow())
		if visibleLen(top) != visibleLen(bot) {
			t.Errorf("screen %s: the top border is %d columns and the bottom is %d\n%q\n%q",
				s, visibleLen(top), visibleLen(bot), top, bot)
		}
	}
	// On a terminal where the arithmetic would go negative, the rule is clamped
	// instead of panicking in strings.Repeat.
	tui.Width = 1
	top := stripANSI(strings.Join(tui.chatTopRow(), "\n"))
	if !strings.Contains(top, glyphTopLeft) || !strings.Contains(top, glyphTopRight) {
		t.Errorf("even a degenerate width must produce a well-formed border: %q", top)
	}
}

// TestChatLinesMarksHiddenHistory: the scrollback keeps the newest lines and the
// marker says how many were left out.
func TestChatLinesMarksHiddenHistory(t *testing.T) {
	runner := &fakeRunner{}
	tui := newFakeTUI("q\n", runner)
	tui.Width = 80
	for i := 0; i < maxScrollback+25; i++ {
		tui.messages = append(tui.messages, Message{Author: AuthorSystem, Text: "line"})
	}
	out := stripANSI(strings.Join(tui.chatLines(tui.inner()), "\n"))
	if !strings.Contains(out, "earlier messages") {
		t.Errorf("the overflow of the scrollback must be announced:\n%s", out)
	}
}

// TestMessageLinesForEveryAuthor: each speaker has its own treatment, and a
// message with no body must not emit a stray rail.
func TestMessageLinesForEveryAuthor(t *testing.T) {
	tui := newFakeTUI("q\n", &fakeRunner{})
	tui.Width = 80
	inner := tui.inner()

	for _, m := range []Message{
		{Author: AuthorUser, Text: "hi"},
		{Author: AuthorAgent, Text: "there"},
		{Author: AuthorAgent, Text: "working", Pending: true},
		{Author: AuthorAgent, Text: "using execute_command", Frozen: true},
		{Author: AuthorAgent, Text: "[using tool: execute_command]"},
		{Author: AuthorSystem, Text: "a note"},
	} {
		lines := tui.messageLines(m, inner)
		if len(lines) == 0 {
			t.Errorf("message %+v produced no lines", m)
		}
		for _, l := range lines {
			if got := visibleLen(stripANSI(l)); got > inner+6 {
				t.Errorf("message %+v: line %q is %d columns, wider than the panel", m, l, got)
			}
		}
	}

	// A pending agent turn shows movement, not a static label.
	pending := strings.Join(tui.messageLines(Message{Author: AuthorAgent, Text: "x", Pending: true}, inner), "\n")
	if !strings.Contains(stripANSI(pending), "working") {
		t.Errorf("a pending turn must show the working marker:\n%s", stripANSI(pending))
	}
	// A frozen line is an event: it carries no rail.
	frozen := tui.messageLines(Message{Author: AuthorAgent, Text: "using ls", Frozen: true}, inner)
	if len(frozen) != 1 {
		t.Errorf("a frozen event must be a single row, got %d: %q", len(frozen), frozen)
	}
}

// TestToolLabelShapes: the announcement is shortened because the arguments can be
// arbitrarily long, and a malformed marker must not be mistaken for content.
func TestToolLabelShapes(t *testing.T) {
	cases := []struct {
		in      string
		want    string
		matched bool
	}{
		{"[using tool: execute_command]", "using execute_command", true},
		{"using tool: read_file", "using read_file", true},
		{"[using tool:]", "using a tool", true},
		{"[using tool: " + strings.Repeat("x", 80) + "]", "using " + strings.Repeat("x", 44) + "...", true},
		{"ordinary answer text", "", false},
	}
	for _, tc := range cases {
		got, ok := toolLabel(tc.in)
		if ok != tc.matched {
			t.Errorf("toolLabel(%q) matched = %v, want %v", tc.in, ok, tc.matched)
			continue
		}
		if ok && got != tc.want {
			t.Errorf("toolLabel(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestPhaseLabelRecognisesTheMarkers: the planner's phase lines are status, not
// answer text, and both spellings with and without brackets must be caught.
func TestPhaseLabelRecognisesTheMarkers(t *testing.T) {
	for _, in := range []string{"[thinking...]", "thinking...", " thinking... ", "[thinking]"} {
		if _, ok := phaseLabel(in); !ok {
			t.Errorf("phaseLabel(%q) must match", in)
		}
	}
	if _, ok := phaseLabel("thinking about the answer"); ok {
		t.Error("prose that merely starts with 'thinking' must not be treated as a marker")
	}
	if _, ok := phaseLabel("[using tool: x]"); ok {
		t.Error("a tool marker is not a phase marker")
	}
}

// TestHintLinesDropFromTheTail: the hints are the first thing a narrow terminal
// loses, and those that remain must always fit.
func TestHintLinesDropFromTheTail(t *testing.T) {
	for _, w := range []int{minWidth, 60, 80, 120} {
		tui := newFakeTUI("q\n", &fakeRunner{})
		tui.Width = w
		lines := tui.hintLines(w)
		for _, l := range lines {
			if got := visibleLen(stripANSI(l)); got > w {
				t.Errorf("width %d: hint line %q is %d columns", w, stripANSI(l), got)
			}
		}
		// The hints are dropped from the TAIL as the terminal narrows, so at the widest
		// supported size the first ones must be present and none may be a fragment. The
		// last one is the one that goes first, which is why this asserts the property
		// rather than a specific hint: adding a new hint above it is not a regression.
		if w == maxWidth {
			joined := stripANSI(strings.Join(lines, "\n"))
			if !strings.Contains(joined, "mode") || !strings.Contains(joined, "find") {
				t.Errorf("a terminal at the maximum width must show the leading hints:\n%s", joined)
			}
		}
	}
	// A terminal too narrow for even one hint shows none rather than a fragment.
	tui := newFakeTUI("q\n", &fakeRunner{})
	tui.Width = minWidth
	if lines := tui.hintLines(10); lines == nil {
		// nil is a valid answer here; what matters is that the width is respected.
		return
	} else if visibleLen(stripANSI(lines[0])) > 10 {
		t.Errorf("hint line %q overflows 10 columns", stripANSI(lines[0]))
	}
}

// TestEveryHintFitsAtTheMaximumWidth: the footer list has to be reachable in full at
// some supported width, or the hints at the end are truncated everywhere and might as
// well not be advertised. This is the invariant that caught a list which had grown past
// every possible terminal — the last two hints were unreachable no matter how wide the
// window was.
//
// It asserts the property rather than a hard-coded list, so adding a hint is allowed as
// long as the whole set still fits.
func TestEveryHintFitsAtTheMaximumWidth(t *testing.T) {
	tui := newFakeTUI("q\n", &fakeRunner{})
	lines := tui.hintLines(maxWidth)
	joined := stripANSI(strings.Join(lines, "\n"))

	// The advertised keys are read back from the source of truth (the rendered line)
	// rather than listed here, so the test cannot drift from what is shipped.
	advertised := strings.Count(joined, glyphMid) + 1
	if advertised < 4 {
		t.Errorf("the footer advertises only %d hints, which is too few to be useful:\n%s", advertised, joined)
	}
	// Every one of them has to be a real binding, checked elsewhere; here the question
	// is whether the whole line fits.
	if got := visibleLen(joined); got > maxWidth {
		t.Errorf("the hint line is %d columns, past the cap of %d:\n%s", got, maxWidth, joined)
	}
	// And the essential ones are present at the maximum width: navigation, search and
	// the two ways out. Losing any of these would make the interface undiscoverable.
	for _, want := range []string{"scroll", "find", "help", "quit"} {
		if !strings.Contains(joined, want) {
			t.Errorf("the footer must advertise %q at the maximum width:\n%s", want, joined)
		}
	}
}

// TestFitLineTruncatesAndResets: cutting a coloured line must close the escape,
// otherwise the colour bleeds into the rest of the screen.
func TestFitLineTruncatesAndResets(t *testing.T) {
	tui := &TUI{NoColor: false}
	long := tui.color(colAccent, 0, strings.Repeat("x", 100))
	got := tui.fitLine(long, 20, "  ")
	if !strings.HasSuffix(got, "\x1b[0m") {
		t.Errorf("a truncated line must end with a reset: %q", got)
	}
	if n := visibleLen(stripANSI(got)); n > 20 {
		t.Errorf("the truncated line is %d columns, want at most 20: %q", n, stripANSI(got))
	}
	// A line that already fits is returned untouched.
	short := tui.color(colAccent, 0, "ok")
	if got := tui.fitLine(short, 20, "  "); got != "  "+short {
		t.Errorf("a line that fits must not be rewritten: %q", got)
	}
}

// TestPadCenterCentresAndDoesNotShrink: an item wider than the frame is left
// alone (it will be dealt with by the caller) instead of being cut in half.
func TestPadCenterCentresAndDoesNotShrink(t *testing.T) {
	tui := &TUI{}
	got := tui.padCenter("abc", 9)
	if got != "   abc" {
		t.Errorf("padCenter = %q, want %q", got, "   abc")
	}
	if got := tui.padCenter("abcdefghij", 4); got != "abcdefghij" {
		t.Errorf("an oversized string must be returned unchanged, got %q", got)
	}
}

// TestVisibleMessagesKeepsTheNewest: the scrollback is bounded, and what is kept
// is the end of the conversation.
func TestVisibleMessagesKeepsTheNewest(t *testing.T) {
	tui := newFakeTUI("q\n", &fakeRunner{})
	for i := 0; i < maxScrollback+10; i++ {
		tui.messages = append(tui.messages, Message{Author: AuthorSystem, Text: "x"})
	}
	got := tui.visibleMessages()
	if len(got) != maxScrollback {
		t.Errorf("visibleMessages returned %d, want %d", len(got), maxScrollback)
	}
	if &got[0] != &tui.messages[len(tui.messages)-maxScrollback] {
		t.Error("the newest messages must be the ones kept")
	}
}

// TestWordWrapHandlesPathsAndBlankLines: an unwrappable token (a long path) is
// split, and an empty paragraph still yields one line so the layout stays stable.
func TestWordWrapHandlesPathsAndBlankLines(t *testing.T) {
	got := wordWrap("/a/very/long/path/that/cannot/possibly/fit/in/the/panel/width", 20)
	if len(got) < 2 {
		t.Fatalf("a long path must be split, got %q", got)
	}
	for _, l := range got {
		if visibleLen(l) > 20 {
			t.Errorf("line %q is %d columns, want at most 20", l, visibleLen(l))
		}
	}
	if got := wordWrap("", 20); len(got) != 1 || got[0] != "" {
		t.Errorf("an empty string must wrap to one empty line, got %q", got)
	}
	if got := wordWrap("a\n\nb", 20); len(got) != 2 {
		t.Errorf("blank paragraphs must be skipped, got %q", got)
	}
	if got := wordWrap("anything", 0); len(got) != 1 {
		t.Errorf("a zero width must return the text unchanged, got %q", got)
	}
}

// TestScanEscapesHandlesTruncatedSequences: a cut-off escape (a stream can end
// mid-sequence) must not be counted as text, and the plain runes around it must
// still be measured.
func TestScanEscapesHandlesTruncatedSequences(t *testing.T) {
	cases := []struct {
		in   string
		want int
	}{
		{"\x1b", 0},
		{"\x1b[", 0},
		{"\x1b[31", 0},
		{"a\x1b[31", 1},
		{"a\x1b[31mb\x1b[", 2},
		{"\x1b[?25hx", 1},
		{"\x1b]0;title\x07x", 1},
	}
	for _, tc := range cases {
		if got := visibleLen(tc.in); got != tc.want {
			t.Errorf("visibleLen(%q) = %d, want %d", tc.in, got, tc.want)
		}
	}
}
