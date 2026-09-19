package tui

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

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
