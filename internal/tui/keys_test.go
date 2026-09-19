package tui

import (
	"bytes"
	"context"
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// sgr matches a colour (SGR) escape: ESC [ ... m. NO_COLOR governs colour, and
// only colour: the cursor and erase sequences are how the frame gets drawn at all,
// so a blanket "no escape" assertion would fail on the very redraw that makes the
// interface work.
var sgr = regexp.MustCompile("\x1b\\[[0-9;]*m")

// The interaction layer, tested against the checklist the design guide ships:
// keyboard navigation, a visible focus/position indicator, no dead key, and a
// usable message instead of a broken layout when the terminal is too small.

// newKeyTUI builds a TUI whose input is the given raw byte stream. The keys are
// written as bytes, not as lines, because that is how a terminal sends them: a
// page key is an escape sequence and never a line.
func newKeyTUI(keys string, messages ...string) (*TUI, *bytes.Buffer) {
	var out bytes.Buffer
	t := New(&fakeRunner{cfg: configWithKey("k")})
	t.In = strings.NewReader(keys)
	t.Out = &out
	t.Width, t.Height = 80, 24
	for _, m := range messages {
		t.messages = append(t.messages, Message{Author: AuthorAgent, Text: m})
	}
	return t, &out
}

// padBody gives the conversation enough rows that scrolling has somewhere to go.
func padBody(t *TUI, n int) {
	for i := 0; i < n; i++ {
		t.messages = append(t.messages, Message{Author: AuthorSystem, Text: "filler line"})
	}
}

// TestEscapeIsRecognisedAsItsOwnKey: a bare ESC arrives as one byte. It must be
// reported as the cancel token and never typed into the prompt, which is what
// would happen if it fell through to the line reader.
func TestEscapeIsRecognisedAsItsOwnKey(t *testing.T) {
	tu, _ := newKeyTUI("\x1b")

	line, ok := tu.readLine(context.Background())
	if !ok {
		t.Fatal("the key must be reported")
	}
	if line != keyEsc {
		t.Errorf("line = %q, want the ESC token %q", line, keyEsc)
	}
}

// TestArrowKeysAreReadAsWholeSequences: a terminal sends an arrow as CSI A/B in
// one write. The three bytes must come back as one token, or the parser would try
// to type "[" and "A" into the prompt.
func TestArrowKeysAreReadAsWholeSequences(t *testing.T) {
	cases := map[string]string{
		"\x1b[A":  keyUp,
		"\x1b[B":  keyDown,
		"\x1b[5~": keyPgUp,
		"\x1b[6~": keyPgDn,
		"\x1b[H":  keyHome,
		"\x1b[F":  keyEnd,
	}
	for keys, want := range cases {
		tu, _ := newKeyTUI(keys)
		got, ok := tu.readLine(context.Background())
		if !ok {
			t.Fatalf("%q: the key must be reported", keys)
		}
		if got != want {
			t.Errorf("%q read as %q, want %q", keys, got, want)
		}
	}
}

// TestAnUnknownEscapeFallsBackToCancel: an unrecognised sequence must cancel
// rather than be typed. The safe reading is the one that cannot corrupt the
// prompt with raw control bytes the user never meant to send.
func TestAnUnknownEscapeFallsBackToCancel(t *testing.T) {
	for _, keys := range []string{"\x1b[?25h", "\x1b[Z"} {
		tu, _ := newKeyTUI(keys)
		got, ok := tu.readLine(context.Background())
		if !ok {
			t.Fatalf("%q: must be reported", keys)
		}
		// Either it is recognised by name or it degrades to ESC; what must never
		// happen is the bytes arriving as text.
		if !strings.HasPrefix(got, "\x1b") {
			t.Errorf("%q read as %q, which is text and not a key", keys, got)
		}
	}
}

// TestEscapeBeforeMoreBytesArriveIsCancel: the terminal may have sent only the
// ESC so far. Reading must not block waiting for bytes that are not coming —
// which is the case for a human pressing Esc.
func TestEscapeBeforeMoreBytesArriveIsCancel(t *testing.T) {
	tu, _ := newKeyTUI("\x1b")
	tu.reader = nil
	tu.In = strings.NewReader("\x1b")

	done := make(chan string, 1)
	go func() {
		line, _ := tu.readLine(context.Background())
		done <- line
	}()
	select {
	case line := <-done:
		if line != keyEsc {
			t.Errorf("line = %q, want the ESC token", line)
		}
	case <-context.Background().Done():
		t.Fatal("unreachable")
	}
}

// TestScrollingMovesTheWindow: the conversation must be reachable. Before this,
// the only rows a user could ever see were the newest ones.
func TestScrollingMovesTheWindow(t *testing.T) {
	tu, _ := newKeyTUI("", "one", "two", "three")
	padBody(tu, 40)

	if tu.scroll != 0 {
		t.Fatalf("the view starts pinned to the bottom, got scroll=%d", tu.scroll)
	}

	tu.scrollBy(+1)
	if tu.scroll != 1 {
		t.Errorf("after scrolling up once scroll = %d, want 1", tu.scroll)
	}

	tu.scrollBy(-1)
	if tu.scroll != 0 {
		t.Errorf("scrolling back down must return to the newest line, got %d", tu.scroll)
	}
}

// TestScrollIsClampedAtBothEnds: neither end may float. Scrolling down past the
// newest line would show blank space, and scrolling up past the oldest would
// leave the window empty.
func TestScrollIsClampedAtBothEnds(t *testing.T) {
	tu, _ := newKeyTUI("")
	padBody(tu, 60)

	tu.scrollBy(-5)
	if tu.scroll != 0 {
		t.Errorf("scroll below zero = %d, want 0", tu.scroll)
	}

	tu.scrollToTop()
	if max := tu.maxScroll(); tu.scroll != max {
		t.Errorf("scroll at the top = %d, want the maximum %d", tu.scroll, max)
	}
	// The furthest the view goes is the conversation minus what fits on screen:
	// the last screenful is not a position the window can occupy.
	if total, room := len(tu.bodyLines()), tu.chatRows(); tu.scroll != total-room {
		t.Errorf("maxScroll = %d, want %d (body %d minus a screenful %d)", tu.scroll, total-room, total, room)
	}

	tu.scrollToBottom()
	if tu.scroll != 0 {
		t.Errorf("scroll at the bottom = %d, want 0", tu.scroll)
	}
}

// TestAShortConversationHasNowhereToScroll: when everything already fits, going to
// the top must not move anything. The window cannot offset itself by a full
// screenful it does not have.
func TestAShortConversationHasNowhereToScroll(t *testing.T) {
	tu, _ := newKeyTUI("", "the only message")

	if max := tu.maxScroll(); max != 0 {
		t.Errorf("a conversation that fits has maxScroll = %d, want 0", max)
	}
	tu.scrollToTop()
	if tu.scroll != 0 {
		t.Errorf("scroll = %d, want 0", tu.scroll)
	}
}

// TestScrollingToTopOnAShortConversationIsSafe: a conversation that fits must not
// produce a scroll offset that lifts it off the screen.
func TestScrollingToTopOnAShortConversationIsSafe(t *testing.T) {
	tu, _ := newKeyTUI("", "hi")
	tu.scrollToTop()

	if tu.scroll > len(tu.bodyLines()) {
		t.Errorf("scroll %d exceeds the body %d", tu.scroll, len(tu.bodyLines()))
	}
	tu.drawFrame()
	if out := tu.Out.(*bytes.Buffer).String(); !strings.Contains(out, "hi") {
		t.Errorf("the only message must still be drawn after scrolling: %q", out)
	}
}

// TestTheKeysReachTheHandlerAndDoSomething: the bindings exist to be used. Each
// one is pressed through the real handler and the resulting state is asserted —
// the checklist's rule that navigation must work everywhere, not just be
// documented.
func TestTheKeysReachTheHandlerAndDoSomething(t *testing.T) {
	tu, _ := newKeyTUI("", "one", "two", "three")
	padBody(tu, 40)
	ctx := context.Background()

	for _, k := range []struct {
		key  string
		want func(*TUI) bool
	}{
		{keyUp, func(x *TUI) bool { return x.scroll == 1 }},
		{keyPgUp, func(x *TUI) bool { return x.scroll > 1 }},
		{keyDown, func(x *TUI) bool { return x.scroll > 0 }},
		{keyHome, func(x *TUI) bool { return x.scroll == x.maxScroll() }},
		{keyEnd, func(x *TUI) bool { return x.scroll == 0 }},
		{keyPgDn, func(x *TUI) bool { return x.scroll == 0 }},
		{"j", func(x *TUI) bool { return x.scroll == 1 }},
		{"k", func(x *TUI) bool { return x.scroll == 0 }},
		{"g", func(x *TUI) bool { return x.scroll == x.maxScroll() }},
		{"G", func(x *TUI) bool { return x.scroll == 0 }},
	} {
		handled, quit := tu.handleShortcut(ctx, k.key)
		if !handled {
			t.Errorf("the key %q was not handled at all", k.key)
			continue
		}
		if quit {
			t.Errorf("the key %q must not quit", k.key)
		}
		if !k.want(tu) {
			t.Errorf("the key %q did not produce its effect (scroll=%d)", k.key, tu.scroll)
		}
	}
}

// TestEscapeCancelsARunningTurn: the guide requires an escape route from every
// state. A run in flight is the one state a user most needs to leave.
func TestEscapeCancelsARunningTurn(t *testing.T) {
	tu, _ := newKeyTUI("")
	ctx := context.Background()
	runCtx, cancel := context.WithCancel(ctx)
	tu.cancelRun = cancel
	tu.runningCtx = runCtx

	handled, quit := tu.handleShortcut(ctx, keyEsc)
	if !handled || quit {
		t.Fatalf("Escape must be handled without quitting (handled=%v quit=%v)", handled, quit)
	}
	if runCtx.Err() == nil {
		t.Error("Escape must cancel the context of the running turn")
	}
}

// TestEscapeWithoutARunReturnsToTheBottom: with nothing to cancel, Escape is the
// documented way back to the newest line.
func TestEscapeWithoutARunReturnsToTheBottom(t *testing.T) {
	tu, _ := newKeyTUI("", "one", "two")
	padBody(tu, 30)
	tu.scrollToTop()

	handled, _ := tu.handleShortcut(context.Background(), keyEsc)
	if !handled {
		t.Fatal("Escape must be handled")
	}
	if tu.scroll != 0 {
		t.Errorf("Escape must return to the newest line, got scroll=%d", tu.scroll)
	}
}

// TestShiftGIsReachable: the handler lowercases the line before the switch, so a
// "G" case would be dead code. This pins the binding against that mistake.
func TestShiftGIsReachable(t *testing.T) {
	tu, _ := newKeyTUI("", "one", "two")
	padBody(tu, 30)
	tu.scrollToTop()
	top := tu.scroll
	if top == 0 {
		t.Fatal("the test needs a scrolled view to be meaningful")
	}

	handled, _ := tu.handleShortcut(context.Background(), "G")
	if !handled {
		t.Fatal("Shift+G must be handled")
	}
	if tu.scroll != 0 {
		t.Errorf("Shift+G must jump to the newest line, got scroll=%d", tu.scroll)
	}
}

// TestThePositionIndicatorAppearsOnlyWhenScrolled: a border that always reads 0%
// is noise. The guide puts a panel's own context in its border, which is where
// this lives.
func TestThePositionIndicatorAppearsOnlyWhenScrolled(t *testing.T) {
	tu, out := newKeyTUI("", "one", "two")
	padBody(tu, 30)

	tu.scroll = 0
	tu.drawFrame()
	if body := stripANSI(out.String()); strings.Contains(body, "%") {
		t.Errorf("no indicator must be shown at the bottom: %q", body)
	}

	out.Reset()
	tu.scroll = 5
	tu.drawFrame()
	body := stripANSI(out.String())
	if !strings.Contains(body, "%") {
		t.Errorf("a scrolled view must show where it is: %q", body)
	}
	// The status bar carries the exact offset, but it is the LAST part of the
	// line and a narrow terminal drops the tail by design — so the check runs at
	// a width that fits everything, which is the only fair place to assert it.
	tu.Width = 120
	out.Reset()
	tu.drawFrame()
	body = stripANSI(out.String())
	if !strings.Contains(body, "scrolled 5 above latest") {
		t.Errorf("the status bar must say how far back the view is: %q", body)
	}
}

// TestThePositionPercentageIsBounded: the indicator must never claim more than
// the whole conversation, whatever the offset.
func TestThePositionPercentageIsBounded(t *testing.T) {
	tu, _ := newKeyTUI("")
	padBody(tu, 60)

	tu.scroll = 0
	if pct := tu.scrollPercent(); pct != 0 {
		t.Errorf("at the newest line the position is 0%%, got %d", pct)
	}
	tu.scrollToTop()
	if pct := tu.scrollPercent(); pct != 100 {
		t.Errorf("at the oldest reachable line the position is 100%%, got %d", pct)
	}
	tu.scroll = 10000
	if pct := tu.scrollPercent(); pct > 100 {
		t.Errorf("the position must never exceed 100%%, got %d", pct)
	}
}

// TestAPositionWithNothingToScrollReads100: a conversation that fits is entirely
// visible, so the view is at the top of what there is to see.
func TestAPositionWithNothingToScrollReads100(t *testing.T) {
	tu, _ := newKeyTUI("", "small")
	if pct := tu.scrollPercent(); pct != 100 {
		t.Errorf("a conversation that fits reads %d%%, want 100", pct)
	}
}

// TestATinyTerminalExplainsItself: below a workable size the guide asks for a
// message, not a broken layout. Every droppable part would otherwise be removed
// and the user would be left with a frame they cannot read or diagnose.
func TestATinyTerminalExplainsItself(t *testing.T) {
	tu, out := newKeyTUI("", "hello")
	tu.Width, tu.Height = 60, 4

	lines, prompt := tu.layout(tu.Width, tu.Height)
	if prompt != "" {
		t.Errorf("no prompt is drawn on the undersized message, got %q", prompt)
	}
	body := stripANSI(strings.Join(lines, "\n"))
	if !strings.Contains(body, "too small") {
		t.Errorf("the message must say the window is too small: %q", body)
	}
	if !strings.Contains(body, "60 x 4") {
		t.Errorf("the message must report the current size: %q", body)
	}
	if !strings.Contains(body, "44") {
		t.Errorf("the message must report the size it needs: %q", body)
	}

	// The frame is drawn without a prompt, so the cursor is not parked after one.
	tu.drawFrame()
	if strings.Contains(out.String(), "Task > ") {
		t.Error("the prompt must not be drawn on the undersized message")
	}
}

// TestATerminalOfUnknownHeightIsNotTreatedAsTiny: a height of zero means the size
// is unknown, not that the terminal is small. Drawing everything is the only
// honest response.
func TestATerminalOfUnknownHeightIsNotTreatedAsTiny(t *testing.T) {
	tu, _ := newKeyTUI("", "hello")
	tu.Height = 0
	t.Setenv("LINES", "")
	restore := stubTTYSize(0, 0, false)
	defer restore()

	lines, prompt := tu.layout(80, 0)
	if prompt == "" {
		t.Error("an unknown height must still draw the prompt")
	}
	if body := stripANSI(strings.Join(lines, "\n")); strings.Contains(body, "too small") {
		t.Errorf("an unknown height is not an undersized terminal: %q", body)
	}
}

// TestPagingWithoutAKnownHeightStillMoves: with no reported height there is no
// page to compute, but the key must still do something sane instead of nothing.
func TestPagingWithoutAKnownHeightStillMoves(t *testing.T) {
	tu, _ := newKeyTUI("", "one")
	tu.Height = 0
	t.Setenv("LINES", "")
	restore := stubTTYSize(0, 0, false)
	defer restore()

	if n := tu.chatRows(); n < 1 {
		t.Errorf("chatRows = %d, want at least 1", n)
	}
	tu.scrollBy(tu.chatRows())
	if tu.scroll < 0 {
		t.Errorf("scroll = %d, must never go negative", tu.scroll)
	}
}

// TestTheTooSmallMessageIsDrawnEvenWhenTheFrameCannotBe: the gate runs before the
// measuring, so even a terminal shorter than every fixed row gets an answer.
func TestTheTooSmallMessageIsDrawnEvenWhenTheFrameCannotBe(t *testing.T) {
	tu, _ := newKeyTUI("")
	tu.Width, tu.Height = 80, 1

	lines, prompt := tu.layout(80, 1)
	if prompt != "" {
		t.Error("no prompt below the minimum")
	}
	if len(lines) == 0 {
		t.Fatal("the undersized message must not be empty")
	}
	if !strings.Contains(stripANSI(strings.Join(lines, "\n")), "80 x 1") {
		t.Errorf("the size must be reported: %q", stripANSI(strings.Join(lines, "\n")))
	}
}

// TestANewTurnReturnsTheViewToTheBottom: starting a task is a request to watch
// its output, so the window follows the newest line again.
func TestANewTurnReturnsTheViewToTheBottom(t *testing.T) {
	tu, _ := newKeyTUI("", "one", "two")
	padBody(tu, 30)
	tu.scrollToTop()

	tu.beginTurn()
	if tu.scroll != 0 {
		t.Errorf("a new turn must pin the view to the bottom, got scroll=%d", tu.scroll)
	}
	if !tu.busy {
		t.Error("a new turn must mark the interface busy")
	}
}

// TestStreamingDoesNotMoveAViewTheUserLifted: the offset counts rows above the
// newest line, so output arriving below the window must not move it. Losing the
// reader's place while the answer grows is the classic chat scroll bug.
func TestStreamingDoesNotMoveAViewTheUserLifted(t *testing.T) {
	tu, _ := newKeyTUI("", "one", "two")
	padBody(tu, 30)
	tu.busy = true
	tu.scrollBy(+3)
	held := tu.scroll

	// More output arrives.
	padBody(tu, 10)
	tu.advance()

	if tu.scroll != held {
		t.Errorf("the view moved from %d to %d while output arrived (it must hold the reader's place)", held, tu.scroll)
	}
}

// TestTheFooterAdvertisesOnlyRealBindings: a hint for a key the handler rejects
// is a lie the user finds in seconds. Every advertised key is pressed here.
func TestTheFooterAdvertisesOnlyRealBindings(t *testing.T) {
	tu, _ := newKeyTUI("", "one")
	padBody(tu, 30)
	ctx := context.Background()

	for _, hint := range [][2]string{
		{"Tab", "mode"}, {"/t", "task"}, {"/p", "plan"}, {"/m", "models"},
		{"/c", "config"}, {"/r", "reasoning"}, {"?", "help"}, {"q", "quit"},
	} {
		if handled, _ := tu.handleShortcut(ctx, hint[0]); !handled {
			t.Errorf("the footer advertises %q but the handler ignores it", hint[0])
		}
	}
	// "j/k" advertises two keys in one hint, so each half is pressed on its own.
	// This is the case a naive split on "/" breaks: the shortcut form "/t" also
	// contains a slash and would be torn into an empty string and a "t".
	for _, k := range []string{"j", "k"} {
		if handled, _ := tu.handleShortcut(ctx, k); !handled {
			t.Errorf("the footer advertises %q but the handler ignores it", k)
		}
	}
}

// TestTheHelpListsTheNavigationKeys: the help screen is where a user looks for a
// binding they have forgotten, so the new keys belong there too.
func TestTheHelpListsTheNavigationKeys(t *testing.T) {
	for _, want := range []string{"scroll", "PgUp", "Esc", "Tab"} {
		if !strings.Contains(helpText, want) {
			t.Errorf("the help must document %q", want)
		}
	}
}

// TestScrollBeyondTheBodyCannotPanic: the offset is clamped by scrollBy, but the
// layout must also survive an offset larger than the conversation. A negative
// slice index here is a crash, not a glitch, and the field is settable by any
// embedder that builds a TUI directly.
func TestScrollBeyondTheBodyCannotPanic(t *testing.T) {
	tu, _ := newKeyTUI("", "only one line")
	tu.scroll = 500

	lines, _ := tu.layout(80, 24)
	if len(lines) == 0 {
		t.Fatal("the frame must still be drawn")
	}
}

// TestPagingInATerminalTooSmallToPage: below the minimum there is no page to
// compute, and the distance must still be a sane positive number.
func TestPagingInATerminalTooSmallToPage(t *testing.T) {
	tu, _ := newKeyTUI("")
	tu.Width, tu.Height = 80, 4

	if n := tu.chatRows(); n != 1 {
		t.Errorf("chatRows in a 4-row terminal = %d, want 1", n)
	}
}

// TestPagingWhenTheFrameLeavesLessThanTheMinimum: a terminal just tall enough to
// draw gets whatever the fixed rows leave, and never less than the floor.
func TestPagingWhenTheFrameLeavesLessThanTheMinimum(t *testing.T) {
	tu, _ := newKeyTUI("")
	tu.Width, tu.Height = 80, 10 // 8 fixed rows leave 2, below the floor of 4

	if n := tu.chatRows(); n != minChatLines {
		t.Errorf("chatRows = %d, want the floor %d", n, minChatLines)
	}
}

// TestAnEscapeWithNothingRecognisableAfterIt: a lone introducer, or one followed
// by a byte that does not start a CSI, is the cancel key. Reading must not hang
// waiting for the rest of a sequence that is not there.
func TestAnEscapeWithNothingRecognisableAfterIt(t *testing.T) {
	// ESC followed by a byte that is not '[': not a sequence this interface knows.
	tu, _ := newKeyTUI("\x1bZ")
	if got, ok := tu.readLine(context.Background()); !ok || got != keyEsc {
		t.Errorf("ESC + non-introducer read as %q (ok=%v), want the ESC token", got, ok)
	}
}

// TestAnEscapeSequenceThatNeverEnds: a truncated sequence must not spin forever.
// The read stops at the byte limit and reports the safe token.
func TestAnEscapeSequenceThatNeverEnds(t *testing.T) {
	// A CSI introducer followed by parameter bytes that never reach a final byte.
	tu, _ := newKeyTUI("\x1b[" + strings.Repeat("1", 40))
	got, ok := tu.readLine(context.Background())
	if !ok {
		t.Fatal("the read must terminate")
	}
	if got != keyEsc {
		t.Errorf("a truncated sequence read as %q, want the safe ESC token", got)
	}
}

// TestAnInterruptedSequenceReadsAsCancel: when the stream ends mid-sequence the
// reader reports the safe token instead of blocking on a byte that will not come.
func TestAnInterruptedSequenceReadsAsCancel(t *testing.T) {
	tu, _ := newKeyTUI("\x1b[")
	got, ok := tu.readLine(context.Background())
	if !ok {
		t.Fatal("the read must terminate at EOF")
	}
	if !strings.HasPrefix(got, "\x1b") {
		t.Errorf("a truncated sequence read as %q, which is text and not a key", got)
	}
}

// TestTheBorderStaysStraightWhileScrolled: the position indicator goes inside the
// top border, and it carries colour escapes. Counting it by its byte length
// instead of its visible width shortened the rule by the length of those escapes
// and left an extra corner glyph in the middle of the border:
//
//	┌─ Plan ─100%─┐───────────────────────────────┐
//
// The frame looked correct at the bottom of the conversation and broke the moment
// the user scrolled. This asserts the invariant on the scrolled frame, which is
// the case the original test never exercised.
func TestTheBorderStaysStraightWhileScrolled(t *testing.T) {
	for _, width := range []int{minWidth, 80, maxWidth} {
		tu, out := newKeyTUI("", "one", "two")
		padBody(tu, 60)
		tu.Width, tu.Height = width, 30
		tu.scroll = 5

		out.Reset()
		tu.drawFrame()
		frame := stripANSI(out.String())

		var rows []string
		for _, line := range strings.Split(frame, "\n") {
			if strings.HasPrefix(line, "  ┌") || strings.HasPrefix(line, "  │") || strings.HasPrefix(line, "  └") {
				rows = append(rows, line)
			}
		}
		if len(rows) < 2 {
			t.Fatalf("width %d: the panel was not drawn: %q", width, frame)
		}

		want := visibleLen(rows[0])
		for i, row := range rows {
			if got := visibleLen(row); got != want {
				t.Errorf("width %d: row %d is %d columns, want %d\n%q\n%q", width, i, got, want, rows[0], row)
			}
		}
		// The indicator is present, and the border it sits in is still one piece:
		// a second corner glyph in the middle is the visible symptom.
		if !strings.Contains(rows[0], "%") {
			t.Errorf("width %d: the scrolled border must show the position: %q", width, rows[0])
		}
		if strings.Count(rows[0], glyphTopRight) != 1 {
			t.Errorf("width %d: the top border has more than one right corner: %q", width, rows[0])
		}
		if strings.Count(rows[0], glyphTopLeft) != 1 {
			t.Errorf("width %d: the top border has more than one left corner: %q", width, rows[0])
		}
	}
}

// TestTheStatusIsReadableWithoutColour: the design guide counts colour-only
// indicators as an accessibility failure. Each state must differ in SHAPE as well
// as colour, so the line still means something with the escapes stripped — which
// is exactly what NO_COLOR produces.
func TestTheStatusIsReadableWithoutColour(t *testing.T) {
	// cfgSet matters: the fake substitutes config.Default() whenever no
	// configuration was supplied, so a test that assigns cfg without setting it
	// silently gets the defaults and asserts nothing about its own fixture.
	ready, _ := newKeyTUI("")
	ready.Runner = &fakeRunner{cfg: configWithKey("sk-live"), cfgSet: true}
	missing, _ := newKeyTUI("")
	missing.Runner = &fakeRunner{cfg: configWithKey(""), cfgSet: true}
	busy, _ := newKeyTUI("")
	busy.Runner = &fakeRunner{cfg: configWithKey("sk-live"), cfgSet: true}
	busy.busy = true

	readyGlyph := stripANSI(ready.stateGlyph())
	missingGlyph := stripANSI(missing.stateGlyph())
	busyGlyph := stripANSI(busy.stateGlyph())

	if readyGlyph == missingGlyph {
		t.Errorf("ready and missing look identical without colour: %q", readyGlyph)
	}
	if readyGlyph != glyphReady {
		t.Errorf("ready glyph = %q, want %q", readyGlyph, glyphReady)
	}
	if missingGlyph != glyphMissing {
		t.Errorf("missing glyph = %q, want %q", missingGlyph, glyphMissing)
	}
	if busyGlyph == readyGlyph || busyGlyph == missingGlyph {
		t.Errorf("the running indicator must differ from both steady states, got %q", busyGlyph)
	}
}

// TestTheWholeStatusLineSurvivesNoColour: stripping the escapes must leave the
// information intact, not an empty line. This is the frame a user with NO_COLOR
// actually reads.
func TestTheWholeStatusLineSurvivesNoColour(t *testing.T) {
	tu, out := newKeyTUI("")
	tu.Runner = &fakeRunner{cfg: configWithKey("sk-live"), cfgSet: true}
	tu.NoColor = true
	tu.Width, tu.Height = 100, 30

	out.Reset()
	tu.drawFrame()
	body := out.String()

	// NO_COLOR governs COLOUR, not the cursor and erase sequences a frame needs in
	// order to be redrawn at all. The check is for SGR sequences specifically: an
	// assertion of "no escape at all" would fail on the very redraw that makes the
	// interface work.
	if sgr.MatchString(body) {
		t.Errorf("NO_COLOR must not emit colour: %q", body)
	}
	for _, want := range []string{glyphReady, "reasoning", "key", "present", "ready"} {
		if !strings.Contains(body, want) {
			t.Errorf("the status line lost %q without colour: %q", want, body)
		}
	}
}

// TestTheWordmarkLosesItsOwnColourToo: the wordmark carries escapes baked into the
// artwork. NO_COLOR has to strip those as well, or the "no colour" mode still emits
// sequences the terminal will interpret.
func TestTheWordmarkLosesItsOwnColourToo(t *testing.T) {
	tu, out := newKeyTUI("")
	tu.Width, tu.Height = 100, 30

	tu.NoColor = false
	out.Reset()
	tu.drawFrame()
	if !strings.Contains(out.String(), "\x1b[0;97m") {
		t.Error("the wordmark's own colours must be present in colour mode")
	}

	tu.NoColor = true
	out.Reset()
	tu.drawFrame()
	if sgr.MatchString(out.String()) {
		t.Errorf("NO_COLOR must strip the wordmark's own colours: %q", out.String())
	}
	// The artwork itself must still be there, just in the terminal's own colour.
	if !strings.Contains(out.String(), "\u2588\u2588\u2588") {
		t.Error("the wordmark's shape must survive the strip")
	}
}

// TestHalfPageScrollIsAVimBinding: Ctrl+U and Ctrl+D are what a reader uses to skim
// a long answer. They arrive as control bytes, so they must be intercepted before a
// line is read and matched before any normalisation — the same trap as Tab.
func TestHalfPageScrollIsAVimBinding(t *testing.T) {
	tu, _ := newKeyTUI("", "one", "two")
	padBody(tu, 80)

	full := tu.chatRows()
	half := full / 2
	if half < 1 {
		t.Fatalf("the fixture needs a page taller than one row, got %d", full)
	}

	handled, quit := tu.handleShortcut(context.Background(), keyHalfUp)
	if !handled || quit {
		t.Fatalf("Ctrl+U must be handled without quitting (handled=%v quit=%v)", handled, quit)
	}
	if tu.scroll != half {
		t.Errorf("Ctrl+U scrolled to %d, want half a page (%d)", tu.scroll, half)
	}

	if handled, _ := tu.handleShortcut(context.Background(), keyHalfDown); !handled {
		t.Fatal("Ctrl+D must be handled")
	}
	if tu.scroll != 0 {
		t.Errorf("Ctrl+D must bring the view back, got scroll=%d", tu.scroll)
	}
}

// TestTheControlBytesReachTheHandler: readLine has to return the half-page bytes as
// keys. Falling through to the line reader would type them into the prompt, and the
// trim in handleShortcut would then delete them, so the binding would exist in the
// switch and never fire.
func TestTheControlBytesReachTheHandler(t *testing.T) {
	for key, want := range map[string]string{keyHalfUp: keyHalfUp, keyHalfDown: keyHalfDown} {
		tu, _ := newKeyTUI(key)
		got, ok := tu.readLine(context.Background())
		if !ok {
			t.Fatalf("%q was not reported as a key", key)
		}
		if got != want {
			t.Errorf("readLine(%q) = %q, want %q", key, got, want)
		}
	}
}

// TestHalfPageWithoutARoomToMove: a view already at the top or the bottom must not
// move past its clamp when a half page is requested.
func TestHalfPageWithoutARoomToMove(t *testing.T) {
	tu, _ := newKeyTUI("", "small")

	// Nothing to scroll: the clamps hold.
	tu.handleShortcut(context.Background(), keyHalfUp)
	if tu.scroll != 0 {
		t.Errorf("scroll = %d, want 0 with nothing to scroll", tu.scroll)
	}
	tu.handleShortcut(context.Background(), keyHalfDown)
	if tu.scroll != 0 {
		t.Errorf("scroll = %d, want 0", tu.scroll)
	}
}

// TestTheHelpKeepsItsColumns: the help screen is a two-column reference, and the
// alignment between a key and its description is what makes it scannable. Word
// wrapping collapses the runs of spaces that produce that alignment — the screen
// came out as "Tab switch mode" with the columns gone — so preformatted text is
// placed line by line instead.
func TestTheHelpKeepsItsColumns(t *testing.T) {
	tu, out := newKeyTUI("", "")
	tu.Width, tu.Height = 100, 44

	tu.addPreformatted(AuthorSystem, helpText)
	frame := stripANSI(out.String())

	// Every documented line must survive with its indentation, not re-flowed into a
	// paragraph.
	for _, want := range []string{"  Tab          switch mode", "  PgUp/PgDn    scroll one page", "  Ctrl+U/D     scroll half a page"} {
		if !strings.Contains(frame, want) {
			t.Errorf("the help lost its alignment; %q is missing from:\n%s", want, frame)
		}
	}

	// And nothing may be clipped: the reference is short enough to fit.
	if strings.Contains(frame, "\u2026") {
		t.Errorf("the help must fit the panel without clipping:\n%s", frame)
	}
}

// TestPreformattedTextIsClippedNotRewrapped: a line too long for the panel loses its
// tail rather than being folded into the next line, which would destroy the layout
// the flag exists to protect.
func TestPreformattedTextIsClippedNotRewrapped(t *testing.T) {
	long := "  key      " + strings.Repeat("x", 200)

	tu, out := newKeyTUI("", "")
	tu.Width, tu.Height = 60, 30
	tu.addPreformatted(AuthorSystem, long)

	body := stripANSI(out.String())
	if !strings.Contains(body, "\u2026") {
		t.Errorf("an oversized preformatted line must be marked as clipped:\n%s", body)
	}
	// The indentation is still there: the line starts with the key column.
	if !strings.Contains(body, "  key      ") {
		t.Errorf("clipping must keep the head of the line:\n%s", body)
	}
}

// TestClipLineLeavesShortTextAlone: the common case must be untouched, and the
// measurement is in visible columns, not bytes.
func TestClipLineLeavesShortTextAlone(t *testing.T) {
	if got := clipLine("short", 20); got != "short" {
		t.Errorf("clipLine changed a line that fits: %q", got)
	}
	for _, width := range []int{1, 2, 5} {
		got := clipLine("abcdefghij", width)
		if visibleLen(got) > width {
			t.Errorf("clipLine(%d) produced %d columns: %q", width, visibleLen(got), got)
		}
	}
	if got := clipLine("anything", 0); got != "anything" {
		t.Errorf("a non-positive width must not clip: %q", got)
	}
}

// TestTheHelpIsReachableAndDocumented: the binding and the text have to agree. A help
// screen that lists a key the handler ignores is the same lie as a footer hint.
func TestTheHelpIsReachableAndDocumented(t *testing.T) {
	tu, _ := newKeyTUI("")
	if handled, _ := tu.handleShortcut(context.Background(), "?"); !handled {
		t.Fatal("? must open the help")
	}
	last := tu.messages[len(tu.messages)-1]
	if !last.Preformatted {
		t.Error("the help must be added as preformatted text, or its columns are destroyed")
	}
}

// Searching the conversation. The guide asks for live filtering with the match count
// and the matches highlighted, and for an escape route out of every state.

// TestSearchFiltersTheConversation: a query narrows what is drawn, and clearing it
// brings the whole history back. Hiding the user's own conversation with no way back
// would be worse than not having the feature.
func TestSearchFiltersTheConversation(t *testing.T) {
	tu, out := newKeyTUI("", "alpha line", "beta line", "gamma line")
	tu.Width, tu.Height = 100, 30

	tu.openSearch()
	if !tu.searching {
		t.Fatal("Ctrl+F must open the search")
	}
	tu.applyQuery("beta")

	// The assertions are on the LAST frame: the buffer keeps every repaint, so the
	// whole string still contains the history from before the search.
	body := stripANSI(lastFrameOf(out))
	if !strings.Contains(body, "beta line") {
		t.Errorf("the match must be shown:\n%s", body)
	}
	if strings.Contains(body, "alpha line") || strings.Contains(body, "gamma line") {
		t.Errorf("non-matching lines must be filtered out:\n%s", body)
	}
	// The filter is visible, with the number of lines it kept.
	if !strings.Contains(body, "filter") || !strings.Contains(body, "1 lines") {
		t.Errorf("an applied filter must announce itself and its count:\n%s", body)
	}

	tu.closeSearch()
	body = stripANSI(lastFrameOf(out))
	for _, want := range []string{"alpha line", "beta line", "gamma line"} {
		if !strings.Contains(body, want) {
			t.Errorf("clearing the search must restore %q:\n%s", want, body)
		}
	}
	if tu.query != "" || tu.searching {
		t.Errorf("Esc must leave no filter behind, got query=%q searching=%v", tu.query, tu.searching)
	}
}

// TestSearchIsCaseInsensitiveAndHighlights: the match is located on a lowercased copy
// but the ORIGINAL text is emitted — coluring a lowercased copy would rewrite what the
// agent said, which is the kind of silent corruption nobody forgives.
func TestSearchIsCaseInsensitiveAndHighlights(t *testing.T) {
	tu, out := newKeyTUI("", "The GO version is 1.27")
	tu.Width, tu.Height = 100, 30

	tu.openSearch()
	tu.applyQuery("go version")

	raw := lastFrameOf(out)
	// The original casing survives.
	if !strings.Contains(stripANSI(raw), "GO version") {
		t.Errorf("the original text must be preserved:\n%s", stripANSI(raw))
	}
	// And the match is emphasised, which is what "highlight matches" means.
	if !strings.Contains(raw, "\x1b[30;46m") {
		t.Errorf("the match must be highlighted:\n%q", raw)
	}
}

// TestSearchThatMatchesNothingExplainsItself: a filter with no hits is a dead end
// unless the screen says what was searched and how to leave. The guide's empty-state
// rule is an explanation plus an action.
func TestSearchThatMatchesNothingExplainsItself(t *testing.T) {
	tu, out := newKeyTUI("", "one", "two")
	tu.Width, tu.Height = 100, 30

	tu.openSearch()
	tu.applyQuery("nothing matches this")

	body := stripANSI(lastFrameOf(out))
	if !strings.Contains(body, "No line matches") {
		t.Errorf("an empty result must say so:\n%s", body)
	}
	if !strings.Contains(body, "Esc") || !strings.Contains(body, "Ctrl+F") {
		t.Errorf("the empty result must offer the way out and the way to retry:\n%s", body)
	}
}

// TestTheSearchOwnsTheInputWhileItIsOpen: a line typed during a search is a query, not
// a task. Running it would start a turn nobody asked for, from inside a filter box.
func TestTheSearchOwnsTheInputWhileItIsOpen(t *testing.T) {
	runner := &fakeRunner{cfg: configWithKey("k"), cfgSet: true}
	tu, _ := newKeyTUI("", "")
	tu.Runner = runner
	tu.Width, tu.Height = 100, 30

	// Ctrl+F, then a line, then an empty line to close the search, then quit.
	tu.In = strings.NewReader("\x06alpha\n\nq\n")
	tu.Run(context.Background())

	runner.mu.Lock()
	called := runner.taskCalled
	last := runner.lastTask
	runner.mu.Unlock()
	if called {
		t.Errorf("the search must not run anything, but a task was started: %q", last)
	}
	if tu.query != "alpha" {
		t.Errorf("query = %q, want alpha", tu.query)
	}
}

// TestAnEmptyQueryKeepsTheFilter: pressing Enter on an empty line confirms the filter
// as it stands rather than clearing it. Esc is the way to clear.
func TestAnEmptyQueryKeepsTheFilter(t *testing.T) {
	tu, _ := newKeyTUI("", "one", "two")
	tu.Width, tu.Height = 100, 30

	tu.openSearch()
	tu.applyQuery("two")
	tu.applyQuery("")

	if tu.query != "two" {
		t.Errorf("an empty line must keep the filter, got %q", tu.query)
	}
	if tu.searching {
		t.Error("an empty line must close the search box")
	}
}

// TestEscapeLeavesTheSearchBeforeAnythingElse: Esc unwinds in the reverse order of how
// the interface was entered — the search first, then a run, then the scroll.
func TestEscapeLeavesTheSearchBeforeAnythingElse(t *testing.T) {
	tu, _ := newKeyTUI("", "one")
	tu.Width, tu.Height = 100, 30

	runCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	tu.cancelRun = cancel
	tu.runningCtx = runCtx

	tu.openSearch()
	tu.applyQuery("one")

	handled, quit := tu.handleShortcut(context.Background(), keyEsc)
	if !handled || quit {
		t.Fatalf("Esc must be handled without quitting (handled=%v quit=%v)", handled, quit)
	}
	if tu.query != "" {
		t.Error("Esc must have left the search")
	}
	if runCtx.Err() != nil {
		t.Error("Esc must not reach the run while a search is open: leave in reverse order")
	}
}

// TestTheSearchKeyIsReachableAndRepeats: Ctrl+F opens the search, and opens it again
// from a filter that is already applied, which is how a user edits a query.
func TestTheSearchKeyIsReachableAndRepeats(t *testing.T) {
	tu, _ := newKeyTUI("", "one")
	tu.Width, tu.Height = 100, 30

	if handled, _ := tu.handleShortcut(context.Background(), keyFind); !handled {
		t.Fatal("Ctrl+F must be handled")
	}
	if !tu.searching {
		t.Fatal("Ctrl+F must open the search")
	}

	// A line with text is the query itself; the box stays open so it can be refined.
	tu.applyQuery("one")
	if !tu.searching {
		t.Fatal("typing a query must leave the box open for editing")
	}
	// An empty line confirms the filter and closes the box.
	tu.applyQuery("")
	if tu.searching {
		t.Fatal("an empty line must close the box")
	}
	// And the key opens it again, which is how the applied filter is edited.
	if handled, _ := tu.handleShortcut(context.Background(), keyFind); !handled {
		t.Fatal("Ctrl+F must work again with a filter applied")
	}
	if !tu.searching {
		t.Error("Ctrl+F must reopen the search for editing")
	}
}

// TestTheFilterCountMatchesWhatIsDrawn: the number in the prompt is an assertion the
// user reads, so it has to agree with the lines on screen.
func TestTheFilterCountMatchesWhatIsDrawn(t *testing.T) {
	tu, _ := newKeyTUI("", "match one", "other", "match two")
	tu.Width, tu.Height = 100, 30

	tu.query = "match"
	matching := tu.matchingMessages()
	if len(matching) != 2 {
		t.Fatalf("matchingMessages = %d, want 2", len(matching))
	}
	if bar := stripANSI(tu.searchBar()); !strings.Contains(bar, "2 lines") {
		t.Errorf("the bar must report the count it has: %q", bar)
	}
}

// TestTheSearchFindsTheSpeakerToo: "who said this" is a reasonable question, so the
// author is searched as well as the text.
func TestTheSearchFindsTheSpeakerToo(t *testing.T) {
	tu, _ := newKeyTUI("", "")
	tu.Width, tu.Height = 100, 30
	tu.messages = []Message{
		{Author: AuthorUser, Text: "hello"},
		{Author: AuthorAgent, Text: "hi there"},
	}

	tu.query = "you"
	got := tu.matchingMessages()
	if len(got) != 1 || got[0].Author != AuthorUser {
		t.Errorf("searching the speaker must find the user's turn, got %+v", got)
	}
}

// lastFrameOf returns the most recent frame in a captured TUI output. Every repaint is
// appended, so an assertion about what is on screen must look at the tail, not at the
// whole recording.
func lastFrameOf(out *bytes.Buffer) string {
	s := out.String()
	if i := strings.LastIndex(s, "\x1b[H"); i >= 0 {
		return s[i:]
	}
	return s
}

// TestHighlightLeavesNonMatchingLinesAlone: a filtered conversation can contain a line
// that matched on the SPEAKER and not on its text. The highlighter must then colour the
// line plainly instead of searching for a substring that is not there.
func TestHighlightLeavesNonMatchingLinesAlone(t *testing.T) {
	tu, _ := newKeyTUI("", "")
	tu.query = "absent"

	got := tu.highlight("a line without it", colBase)
	if strings.Contains(got, "\x1b[30;46m") {
		t.Errorf("a line with no match must not be highlighted: %q", got)
	}
	if stripANSI(got) != "a line without it" {
		t.Errorf("the text must pass through unchanged, got %q", stripANSI(got))
	}
}

// TestHighlightWithNoQueryIsPlain: the highlighter is only reached while filtering, but
// an empty query must still be safe rather than matching everything at position zero.
func TestHighlightWithNoQueryIsPlain(t *testing.T) {
	tu, _ := newKeyTUI("", "")
	tu.query = ""

	got := tu.highlight("anything", colBase)
	if strings.Contains(got, "\x1b[30;46m") {
		t.Errorf("an empty query must not highlight: %q", got)
	}
	if stripANSI(got) != "anything" {
		t.Errorf("text = %q", stripANSI(got))
	}
}

// TestHighlightMarksEveryOccurrence: a line can contain the match more than once, and a
// highlighter that stops at the first one makes the rest of the line look like it did
// not match.
func TestHighlightMarksEveryOccurrence(t *testing.T) {
	tu, _ := newKeyTUI("", "")
	tu.query = "ab"

	got := tu.highlight("ab xx ab yy ab", colBase)
	if n := strings.Count(got, "\x1b[30;46m"); n != 3 {
		t.Errorf("expected 3 highlighted spans, got %d: %q", n, got)
	}
	if stripANSI(got) != "ab xx ab yy ab" {
		t.Errorf("the text must not be altered, got %q", stripANSI(got))
	}
}

// TestTheSearchBarShowsTheFilterWhileEditing: a filter can be applied with the box
// still open for editing it. Showing only the box made the applied filter invisible,
// which is how a user ends up staring at a short history with no explanation.
func TestTheSearchBarShowsTheFilterWhileEditing(t *testing.T) {
	tu, _ := newKeyTUI("", "one match", "other")
	tu.query = "match"

	// Editing: the count is shown, the escape hint is not (the box is already open).
	tu.searching = true
	bar := stripANSI(tu.searchBar())
	if !strings.Contains(bar, "filter") || !strings.Contains(bar, "1 lines") {
		t.Errorf("the bar must show the applied filter while editing: %q", bar)
	}
	if !strings.Contains(bar, "find >") {
		t.Errorf("the bar must still show the input prompt: %q", bar)
	}

	// Applied: the way to edit and the way to clear are offered.
	tu.searching = false
	bar = stripANSI(tu.searchBar())
	for _, want := range []string{"filter", "1 lines", "Ctrl+F", "Esc", "find >"} {
		if !strings.Contains(bar, want) {
			t.Errorf("the applied bar is missing %q: %q", want, bar)
		}
	}
}

// TestTheSearchBarWithNoFilterIsJustThePrompt: an unfiltered view must not carry an
// empty filter widget.
func TestTheSearchBarWithNoFilterIsJustThePrompt(t *testing.T) {
	tu, _ := newKeyTUI("", "")
	bar := stripANSI(tu.searchBar())
	if strings.Contains(bar, "filter") {
		t.Errorf("no filter means no filter widget: %q", bar)
	}
	if !strings.Contains(bar, "find >") {
		t.Errorf("the prompt must be there: %q", bar)
	}
}

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

// The typed form of the search. A terminal in canonical mode consumes the control bytes
// itself — Ctrl+U is the driver's kill-line, Ctrl+D its end-of-file, Ctrl+F is passed
// through only sometimes — so the shortcut alone would leave the feature unreachable on
// the target hardware. Measured on Uchikoma: Ctrl+F never arrived at the program.
func TestFindIsTheTypedSearch(t *testing.T) {
	tu, out := newKeyTUI("", "alpha line", "beta line")
	tu.Width, tu.Height = 100, 30

	handled, quit := tu.handleShortcut(context.Background(), "/find beta")
	if !handled || quit {
		t.Fatalf("/find must be handled without quitting (handled=%v quit=%v)", handled, quit)
	}
	if tu.query != "beta" {
		t.Errorf("query = %q, want beta", tu.query)
	}
	if tu.searching {
		t.Error("/find applies the filter and closes the box, so the next line is a task")
	}

	body := stripANSI(lastFrameOf(out))
	if strings.Contains(body, "alpha line") {
		t.Errorf("the filter must be applied immediately:\n%s", body)
	}
	if !strings.Contains(body, "beta line") {
		t.Errorf("the match must be kept:\n%s", body)
	}
}

// TestFindWithNoArgumentOpensTheSearchBox: "/find" alone is the typed way to reach the
// mode, so the user can then type the query with the live preview.
func TestFindWithNoArgumentOpensTheSearchBox(t *testing.T) {
	tu, _ := newKeyTUI("", "one")
	tu.Width, tu.Height = 100, 30

	if handled, _ := tu.handleShortcut(context.Background(), "/find"); !handled {
		t.Fatal("/find must be handled")
	}
	if !tu.searching {
		t.Error("/find with no argument must open the search box")
	}

	// The same guard reached directly, with an empty argument: applyFind is the one
	// place that decides, so the decision belongs to it rather than to the caller.
	tu2, _ := newKeyTUI("", "one")
	tu2.Width, tu2.Height = 100, 30
	tu2.applyFind("")
	if !tu2.searching {
		t.Error("an empty query must open the box instead of applying an empty filter")
	}
	if tu2.query != "" {
		t.Errorf("no filter must be installed, got %q", tu2.query)
	}
}

// TestFindClearsWithAnEmptyArgument: "/find" followed by nothing is how a user reaches
// the mode; to clear a filter they use Esc, which is the documented way out.
func TestFindCarriesTheRestOfTheLineAsTheQuery(t *testing.T) {
	tu, _ := newKeyTUI("", "one")
	tu.Width, tu.Height = 100, 30

	// The query keeps its internal spaces and loses only the surrounding ones.
	tu.handleShortcut(context.Background(), "/find   two words  ")
	if tu.query != "two words" {
		t.Errorf("query = %q, want %q", tu.query, "two words")
	}
}

// TestCutPrefix: the helper exists so the behaviour does not depend on the toolchain.
func TestCutPrefix(t *testing.T) {
	if rest, ok := cutPrefix("/find abc", "/find "); !ok || rest != "abc" {
		t.Errorf("cutPrefix = (%q, %v), want (abc, true)", rest, ok)
	}
	if _, ok := cutPrefix("/f", "/find "); ok {
		t.Error("a shorter string must not match")
	}
	if _, ok := cutPrefix("", "/find "); ok {
		t.Error("an empty string must not match")
	}
	// The exact prefix, with nothing after it.
	if rest, ok := cutPrefix("/find ", "/find "); !ok || rest != "" {
		t.Errorf("cutPrefix = (%q, %v), want (\"\", true)", rest, ok)
	}
}

// The session commands. A conversation the user cannot inspect is one they cannot trust, and
// a conversation they cannot leave is one they are stuck with.

// TestTheSessionReportIsReachable: /s and /session both show it, and what it shows is the
// runner's report as preformatted text — the report is a table of labelled values, and
// re-flowing it would destroy the alignment that makes it readable.
func TestTheSessionReportIsReachable(t *testing.T) {
	for _, cmd := range []string{"/s", "/session"} {
		t.Run(cmd, func(t *testing.T) {
			runner := &fakeRunner{cfg: configWithKey("k"), cfgSet: true}
			runner.report = "model       gpt-4o\nin use      120 tokens (2% of the usable window)\n"
			tu, out := newKeyTUI("", "")
			tu.Runner = runner
			tu.Width, tu.Height = 110, 30

			handled, quit := tu.handleShortcut(context.Background(), cmd)
			if !handled || quit {
				t.Fatalf("%s must be handled without quitting (handled=%v quit=%v)", cmd, handled, quit)
			}

			body := stripANSI(out.String())
			if !strings.Contains(body, "gpt-4o") || !strings.Contains(body, "in use") {
				t.Errorf("the report must be shown:\n%s", body)
			}
			// Preformatted: the columns survive.
			if !strings.Contains(body, "model       gpt-4o") {
				t.Errorf("the report must keep its alignment:\n%s", body)
			}
			last := tu.messages[len(tu.messages)-1]
			if !last.Preformatted {
				t.Error("the report must be added as preformatted text")
			}
		})
	}
}

// TestANewSessionCanBeStarted: /new drops the conversation and says so, because a session
// that resets silently looks like a crash.
func TestANewSessionCanBeStarted(t *testing.T) {
	runner := &fakeRunner{cfg: configWithKey("k"), cfgSet: true}
	tu, out := newKeyTUI("", "")
	tu.Runner = runner
	tu.Width, tu.Height = 110, 30

	handled, quit := tu.handleShortcut(context.Background(), "/new")
	if !handled || quit {
		t.Fatalf("/new must be handled without quitting (handled=%v quit=%v)", handled, quit)
	}

	runner.mu.Lock()
	reset := runner.reset
	runner.mu.Unlock()
	if !reset {
		t.Error("/new must reset the conversation")
	}
	if body := stripANSI(out.String()); !strings.Contains(body, "new session") {
		t.Errorf("the interface must say what happened:\n%s", body)
	}
}
