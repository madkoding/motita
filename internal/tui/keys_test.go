package tui

import (
	"bytes"
	"context"
	"regexp"
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
	out.Reset()
	tu.drawFrame()
	if body := stripANSI(lastFrameOf(out)); strings.Contains(body, "lines up") {
		t.Errorf("no indicator must be shown at the bottom: %q", body)
	}

	out.Reset()
	tu.scroll = 5
	tu.drawFrame()
	body := stripANSI(lastFrameOf(out))
	if !strings.Contains(body, "5 lines up") {
		t.Errorf("a scrolled view must show where it is: %q", body)
	}
	// The offset is reported on the status bar at the foot of the screen. The check is on
	// the LAST frame: the buffer keeps every repaint.
	body = stripANSI(lastFrameOf(out))
	if !strings.Contains(body, "5 lines up") {
		t.Errorf("the status bar must say how far back the view is: %q", body)
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

// TestPagingInATerminalTooSmallToPage: below the size gate there is no page to compute, and the
// distance must still be a sane positive number rather than zero or negative — a page of zero
// rows would make every paging key a no-op, and a negative one would scroll the wrong way.
//
// The gate refuses to draw at this height, so this is the contract of the helper standing on its
// own: whatever the terminal, the caller gets something usable.
func TestPagingInATerminalTooSmallToPage(t *testing.T) {
	tu, _ := newKeyTUI("")
	tu.Width, tu.Height = 80, 4

	if n := tu.chatRows(); n < 1 {
		t.Errorf("chatRows in a 4-row terminal = %d, want a positive page", n)
	}
}

// TestPagingWhenTheFrameLeavesLessThanTheMinimum: a terminal just tall enough to
// draw gets whatever the fixed rows leave, and never less than the floor.
func TestPagingWhenTheFrameLeavesLessThanTheMinimum(t *testing.T) {
	tu, _ := newKeyTUI("")
	tu.Width, tu.Height = 80, minHeight // the fixed rows leave less than the floor

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

// TestTheFrameHoldsItsShapeWhileScrolled: the conversation is no longer boxed, so the
// invariants are the ones the new layout has — both rules the same width, every row inside
// the drawing area, and the composer and the status bar present and in that order.
//
// The scrolled case is the one that used to break: the position indicator moved from the
// border to the status bar, and the earlier version of this test could not see the layout
// change at all.
func TestTheFrameHoldsItsShapeWhileScrolled(t *testing.T) {
	for _, width := range []int{minWidth, 80, 116, 240} {
		tu, out := newKeyTUI("", "one", "two")
		padBody(tu, 60)
		tu.Width, tu.Height = width, 30
		tu.scroll = 5

		out.Reset()
		tu.drawFrame()
		lines, _ := tu.layout(width, 30)

		// Every row fits the drawing area, which is the width minus the one column left
		// free so a terminal never wraps the last one.
		for i, l := range lines {
			if got := visibleLen(l); got > width {
				t.Errorf("width %d: row %d is %d columns: %q", width, i, got, stripANSI(l))
			}
		}
		// The structure: status, rule, conversation, rule, status bar.
		body := stripANSI(strings.Join(lines, "\n"))
		if n := strings.Count(body, strings.Repeat(glyphRule, 10)); n < 2 {
			t.Errorf("width %d: the two rules must bracket the conversation, found %d:\n%s", width, n, body)
		}
		// The offset is on the footer; the mode is the title of the input box.
		if width >= 80 && !strings.Contains(body, "5 lines up") {
			t.Errorf("width %d: the scrolled offset must be on the status bar:\n%s", width, body)
		}
		if !strings.Contains(body, tu.screen.String()) {
			t.Errorf("width %d: the active mode must survive on the status bar:\n%s", width, body)
		}
		// The input box is above the footer, and the footer is last and says the view is lifted:
		// the input sits at the foot of the window, which is the whole point.
		if !strings.Contains(stripANSI(lines[len(lines)-1]), "lines up") {
			t.Errorf("width %d: the last row must be the footer:\n%s", width, stripANSI(lines[len(lines)-1]))
		}
	}
}

// TestDeleteAndTheOtherStrayControlBytesAreSwallowed: a control byte the interface has
// no meaning for must be CONSUMED, not passed on.
//
// This is the case that matters most: if the byte falls through, it is built into a line
// and SENT TO THE MODEL, which is exactly what happened to Ctrl+D, Ctrl+F and Ctrl+U before
// they had cases of their own. DEL (0x7f) is the one a terminal sends for the Delete key,
// and it is checked here because it is the only byte the range test treats specially.
func TestDeleteAndTheOtherStrayControlBytesAreSwallowed(t *testing.T) {
	for _, tc := range []struct {
		name string
		key  string
	}{
		{"DEL, the Delete key", "\x7f"},
		{"Ctrl+A, the readline home", "\x01"},
		{"Ctrl+B", "\x02"},
		{"Ctrl+Z, which the terminal may forward", "\x1a"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tu, _ := newKeyTUI("", "one")
			handled, quit := tu.handleShortcut(context.Background(), tc.key)
			if !handled {
				t.Fatalf("%q must be consumed, not passed to the chat as text", tc.key)
			}
			if quit {
				t.Error("a stray control byte must not quit")
			}
		})
	}

	// The bytes that DO have a meaning keep it: swallowing them here would be saying two
	// things about the same byte. Ctrl+U scrolls a half page, which was checked above, so
	// this only asserts they are not treated as stray.
	for _, key := range []string{"\t", "\r", "\n"} {
		if isStrayControlByte(key) {
			t.Errorf("%q is text-ish and must not be classified as a stray control byte", key)
		}
	}
	// A multi-byte line is never a stray control byte, however it starts.
	for _, line := range []string{"\x1b[A", "\x7f\x7f", "a\x7f"} {
		if isStrayControlByte(line) {
			t.Errorf("%q is not a single control byte and must not be swallowed as one", line)
		}
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
	for _, want := range []string{"  Tab             switch between Task and Plan", "  g / G           oldest / newest",
		"  Ctrl+F          search the conversation (or /find)"} {
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
	// Zero room means no text, not all of it. The old expectation here was that a
	// non-positive width left the line alone — which is the opposite of clipping, and on a
	// terminal too narrow for its own margins it drew every row at full length.
	if got := clipLine("anything", 0); got != "" {
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
	if body := stripANSI(out.String()); !strings.Contains(body, "Started a new conversation") {
		t.Errorf("the interface must say what happened:\n%s", body)
	}
	// And the screen is clean: the old conversation is gone from the model's memory, and leaving
	// it on screen would suggest the next answer can still see it.
	if len(tu.messages) != 0 {
		t.Errorf("the old conversation must be cleared from the view, got %d messages", len(tu.messages))
	}
}
