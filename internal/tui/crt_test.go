package tui

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/madkoding/starlight/internal/config"
)

func crtFor(t *testing.T, tweak func(*config.CRT)) *crt {
	t.Helper()
	c := config.Default().CRT
	if tweak != nil {
		tweak(&c)
	}
	got := newCRT(c)
	if got == nil {
		t.Fatal("the effect should be on by default")
	}
	return got
}

// --- switching it on and off ------------------------------------------------

// Off means nil, and a nil effect is what every caller checks: the interface then runs the plain
// path with no branches of its own and pays nothing for a feature it is not using.
func TestTheEffectIsOnByDefaultAndCanBeSwitchedOff(t *testing.T) {
	if newCRT(config.Default().CRT) == nil {
		t.Fatal("the effect must be on by default: it is the look the author intends")
	}
	off := config.Default().CRT
	off.Enabled = false
	if got := newCRT(off); got != nil {
		t.Fatalf("a disabled effect must produce nothing to do, got %+v", got)
	}
}

// A nil effect leaves the text alone, which is what makes "off" indistinguishable from the
// interface as it was before the feature existed.
func TestNilEffectLeavesTheTextAlone(t *testing.T) {
	var c *crt
	if got := c.paint("hola"); got != "hola" {
		t.Fatalf("a nil effect must not alter the text, got %q", got)
	}
}

// --- the phosphor -----------------------------------------------------------

// The text is drawn in the phosphor, and the interface's own colours are dropped: keeping them
// inside a green screen would look like a bug.
func TestTheTextIsDrawnInThePhosphor(t *testing.T) {
	c := crtFor(t, nil)
	got := c.paint("\x1b[36;47mhola\x1b[0m mundo")
	if !strings.Contains(got, "\x1b[38;2;") {
		t.Fatalf("the text should carry true-colour phosphor, got %q", got)
	}
	if strings.Contains(got, "36;47") {
		t.Fatalf("the interface's own colour must be dropped, got %q", got)
	}
	if !strings.Contains(got, "hola") || !strings.Contains(got, "mundo") {
		t.Fatalf("the text must survive, got %q", got)
	}
}

// The colour is the configured one, channel by channel, and it is emitted as ONE escape per run
// rather than per character: this is the hot path of every frame.
func TestThePhosphorIsTheConfiguredColour(t *testing.T) {
	c := crtFor(t, func(c *config.CRT) { c.Color = "#00ff00" })
	got := c.paint("abc")
	if !strings.HasPrefix(got, "\x1b[38;2;0;255;0m") {
		t.Fatalf("got %q, want the configured green", got)
	}
	if n := strings.Count(got, "\x1b[38;2;"); n != 1 {
		t.Fatalf("one run of text is one escape, got %d", n)
	}
}

// Empty in, empty out: a blank block stays blank rather than becoming an escape with nothing in
// it, which would add bytes to every frame for no visible change.
func TestEmptyTextStaysEmpty(t *testing.T) {
	c := crtFor(t, nil)
	if got := c.paint(""); got != "" {
		t.Fatalf("got %q, want empty", got)
	}
	if got := c.phosphor(""); got != "" {
		t.Fatalf("phosphor(\"\") = %q, want empty", got)
	}
}

// The escape scanner must skip the '[' that introduces a CSI sequence. Skipping only to the next
// "final byte" cut "\x1b[0;97m" after the bracket and left "0;97m" as text to be printed, which
// is how the banner came out carrying its own colours on the first real run. Found by looking at
// the captured screen, not by any unit test.
func TestTheEscapeScannerSkipsTheBracket(t *testing.T) {
	segs := splitSGR("\x1b[0;97mA\x1b[0;37mB")
	var texts []string
	for _, s := range segs {
		texts = append(texts, s.text)
	}
	if joined := strings.Join(texts, "|"); joined != "A|B" {
		t.Fatalf("segments = %q, want A and B with no escape debris", joined)
	}
	for _, s := range texts {
		if strings.Contains(s, "[") || strings.Contains(s, "m") {
			t.Fatalf("an escape leaked into the text: %q", s)
		}
	}
	// A line with no escapes is one segment, and an empty line is none.
	if got := splitSGR("solo texto"); len(got) != 1 || got[0].text != "solo texto" {
		t.Fatalf("got %+v", got)
	}
	if got := splitSGR(""); len(got) != 0 {
		t.Fatalf("an empty line has no segments, got %+v", got)
	}
}

// --- the typewriter ---------------------------------------------------------

// The reveal shows the text a character at a time rather than all at once.
//
// The target is much longer than one frame's worth of characters, because at a teletype speed a
// frame of a tenth of a second reveals about twenty of them: the point is that a long reply
// appears progressively, which is what a reader notices, rather than that any single frame is
// partial.
func TestTypewriterRevealsProgressively(t *testing.T) {
	c := crtFor(t, nil)
	long := strings.Repeat("x", 4000)
	c.startTyping(long)
	first := c.reveal(time.Second / 10)
	if n := len([]rune(first)); n >= 4000 || n < 2 {
		t.Fatalf("the first frame should show some but not all, got %d chars", n)
	}
	if !c.typingInProgress() {
		t.Fatal("a reveal catching up is in progress")
	}
	if last := c.reveal(time.Second * 60); last != long {
		t.Fatalf("the reveal must finish on the full text, got %d chars", len([]rune(last)))
	}
	if c.typingInProgress() {
		t.Fatal("a finished reveal is not in progress")
	}
}

// A growing target must not restart the reveal. Streamed text arrives in chunks, and a reveal
// that started over on each chunk would stutter back to the beginning.
func TestTypewriterRetargetsWithoutRestarting(t *testing.T) {
	c := crtFor(t, nil)
	c.startTyping("abcdef")
	c.reveal(time.Second)
	shown := len([]rune(c.reveal(0)))
	c.startTyping("abcdefghij")
	after := c.reveal(time.Second / 100)
	if len([]rune(after)) < shown {
		t.Fatalf("the reveal must not go backwards: %d chars then %d", shown, len([]rune(after)))
	}
}

// A target SHORTER than what has been shown means the text was replaced rather than extended, so
// the reveal starts over — otherwise it would show characters that are no longer there.
func TestTypewriterRestartsWhenTheTextIsReplaced(t *testing.T) {
	c := crtFor(t, nil)
	c.startTyping("una respuesta larga")
	c.reveal(time.Second * 5)
	if c.typedAt == 0 {
		t.Fatal("the reveal should have finished")
	}
	c.startTyping("corta")
	if c.typedAt != 0 {
		t.Fatalf("a shorter target must restart the reveal, typedAt = %d", c.typedAt)
	}
}

// Stopping clears the reveal, so the text is drawn whole from then on.
func TestStoppingTheTypewriterEndsTheReveal(t *testing.T) {
	c := crtFor(t, nil)
	c.startTyping("hola")
	c.reveal(time.Second / 1000)
	c.stopTyping()
	if c.typingInProgress() {
		t.Fatal("a stopped reveal is not in progress")
	}
	if got := c.reveal(time.Second); got != "" {
		t.Fatalf("after stopping, the target is cleared, got %q", got)
	}
}

// With the typewriter off the full text is returned immediately: the reveal is a presentation
// choice, not a buffer.
func TestTypewriterOffReturnsTheWholeText(t *testing.T) {
	c := crtFor(t, func(c *config.CRT) { c.Typewriter = false })
	c.startTyping("completo")
	if got := c.reveal(time.Nanosecond); got != "completo" {
		t.Fatalf("got %q, want the whole text", got)
	}
	if c.typingInProgress() {
		t.Fatal("with the typewriter off nothing is in progress")
	}
}

// A speed of zero falls back to the default instead of dividing by zero or freezing the reveal.
func TestTypewriterSpeedFallsBackToTheDefault(t *testing.T) {
	c := crtFor(t, func(c *config.CRT) { c.TypewriterCPS = 0 })
	if c.speed != config.Default().CRT.TypewriterCPS {
		t.Fatalf("speed = %v, want the default", c.speed)
	}
}

// A reveal SLOWER than one character per frame must still advance. Each frame's step rounds down
// to zero, so the fraction is carried between frames; without the carry the reveal would stall at
// zero for ever on a fast repainting terminal.
func TestASlowRevealStillAdvances(t *testing.T) {
	c := crtFor(t, func(c *config.CRT) { c.TypewriterCPS = 5 })
	c.startTyping(strings.Repeat("y", 100))
	for i := 0; i < 220; i++ {
		c.reveal(time.Millisecond)
	}
	if c.typedAt == 0 {
		t.Fatal("a slow reveal must still advance: the fraction is carried between frames")
	}
}

// The reveal is driven by the repaints the interface already does, so the first call has no
// previous frame and must still move one character along rather than stalling.
func TestTheFirstTickStillAdvances(t *testing.T) {
	c := crtFor(t, nil)
	if got := c.tick(); got <= 0 {
		t.Fatalf("the first tick must be positive, got %v", got)
	}
	if c.lastAt.IsZero() {
		t.Fatal("the first tick must record the time")
	}
	if got := c.tick(); got < 0 {
		t.Fatalf("a subsequent tick must not be negative, got %v", got)
	}
}

// A nil effect has no speed to measure, and the tick must still answer with a usable interval
// rather than zero: a zero interval would make the first reveal show nothing at all.
func TestTickWithoutASpeed(t *testing.T) {
	c := &crt{speed: 0}
	if got := c.tick(); got != time.Second {
		t.Fatalf("tick = %v, want one second", got)
	}
}

// --- inside the interface ---------------------------------------------------

// A settled message is drawn whole, and a pending one is revealed: revealing finished text again
// would make a completed answer flicker back and forth.
func TestCrtTextRevealsOnlyThePendingMessage(t *testing.T) {
	c := crtFor(t, nil)
	tui := &TUI{crt: c}
	settled := Message{Author: AuthorAgent, Text: "ya terminado", Pending: false}
	if got := tui.crtText(settled); !strings.Contains(got, "ya terminado") {
		t.Fatalf("a settled message must be shown whole, got %q", got)
	}
	pending := Message{Author: AuthorAgent, Text: strings.Repeat("z", 2000), Pending: true}
	if got := tui.crtText(pending); len([]rune(got)) >= 2000 {
		t.Fatalf("a pending message must be revealed, got %d chars", len([]rune(got)))
	}
}

// A settled message stops a reveal that was still running, so the typewriter cannot be left
// chasing a target that will never grow again.
func TestASettledMessageStopsARunningReveal(t *testing.T) {
	c := crtFor(t, nil)
	c.startTyping(strings.Repeat("z", 5000))
	c.reveal(time.Second / 100)
	if !c.typingInProgress() {
		t.Fatal("the reveal should be running")
	}
	tui := &TUI{crt: c}
	tui.crtText(Message{Text: "terminado", Pending: false})
	if c.typingInProgress() {
		t.Fatal("a settled message must stop the reveal")
	}
}

// The colour is applied to a settled message too: it is the screen colour, not a decoration of
// the reveal.
func TestASettledMessageIsStillColoured(t *testing.T) {
	c := crtFor(t, nil)
	tui := &TUI{crt: c}
	if got := tui.crtText(Message{Text: "listo", Pending: false}); !strings.Contains(got, "\x1b[38;2;") {
		t.Fatalf("a settled message must carry the phosphor, got %q", got)
	}
}

// With no effect there is nothing to reveal and nothing to colour: the text passes through.
func TestCrtTextWithoutAnEffect(t *testing.T) {
	tui := &TUI{}
	if got := tui.crtText(Message{Text: "texto", Pending: true}); got != "texto" {
		t.Fatalf("got %q", got)
	}
}

// The typewriter switched off still colours the text: they are two settings, and turning one off
// must not silently turn off the other.
func TestCrtTextWithTheTypewriterOffStillColours(t *testing.T) {
	c := crtFor(t, func(c *config.CRT) { c.Typewriter = false })
	tui := &TUI{crt: c}
	got := tui.crtText(Message{Text: "texto", Pending: true})
	if !strings.Contains(got, "texto") || !strings.Contains(got, "\x1b[38;2;") {
		t.Fatalf("got %q, want the text in the phosphor", got)
	}
}

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

// The reveal loop does nothing at all when there is no reveal to run, which is what makes the
// effect free when it is switched off.
func TestRevealPendingDoesNothingWithoutAnEffect(t *testing.T) {
	tui := &TUI{Out: &strings.Builder{}, Width: 80, Height: 24, Runner: &stubRunner{}}
	tui.messages = append(tui.messages, Message{Author: AuthorAgent, Text: "listo"})
	tui.revealPending(0) // no crt: must return immediately, not block

	// And with the effect on but the typewriter off.
	cfg := config.Default().CRT
	cfg.Typewriter = false
	tui.crt = newCRT(cfg)
	if tui.crt == nil {
		t.Fatal("the effect should be built")
	}
	tui.revealPending(0)
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

// The reveal loop runs the reveal to completion and then returns: it must not spin for ever, and
// it must not leave the block half-shown. A loop that only slept would never advance the reveal;
// the check is that it terminates with nothing left to show.
func TestRevealPendingFinishesTheReveal(t *testing.T) {
	tui := &TUI{Out: &strings.Builder{}, Width: 80, Height: 24, Runner: &stubRunner{}, revealInterval: time.Nanosecond}
	cfg := config.Default().CRT
	tui.crt = newCRT(cfg)
	tui.messages = append(tui.messages, Message{Author: AuthorAgent, Text: strings.Repeat("z", 400), Pending: true})
	tui.crt.startTyping(tui.messages[0].Text)
	tui.revealPending(0)
	if tui.crt.typingInProgress() {
		t.Fatal("the reveal must be finished when the loop returns")
	}
	if tui.crt.typedAt != 400 {
		t.Fatalf("the whole text must be revealed, got %d of 400", tui.crt.typedAt)
	}
}

// Inside the live reader, a mouse report is swallowed and never reaches the draft. This is the
// path that runs when the terminal is in cbreak mode, which is what a real terminal gives the
// interface: without the consumption, the report bytes were appended to the line being typed.
func TestTheLiveReaderSwallowsMouseReports(t *testing.T) {
	// A wheel report and then a letter and Enter: the letter must be the whole line.
	input := "\x1b[<64;31;10M" + "a" + "\n"
	tui := &TUI{
		In:             strings.NewReader(input),
		Out:            &strings.Builder{},
		Width:          80,
		Height:         24,
		Runner:         &stubRunner{},
		charMode:       true,
		revealInterval: time.Nanosecond,
	}
	if tui.crt == nil {
		tui.crt = nil // nothing: the reader path under test does not need the effect
	}
	line, ok := tui.readLineLive(context.Background())
	if !ok {
		t.Fatal("the reader should have produced a line")
	}
	if line != "a" {
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
	overlong := "[" + strings.Repeat("1;", 80)

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

// The reveal loop falls back to the default interval when none is configured, which is what keeps
// it from spinning with no delay and dumping the whole answer in one frame.
func TestTheRevealUsesTheDefaultInterval(t *testing.T) {
	tui := &TUI{Out: &strings.Builder{}, Width: 80, Height: 24, Runner: &stubRunner{}}
	tui.crt = newCRT(config.Default().CRT)
	tui.messages = append(tui.messages, Message{Author: AuthorAgent, Text: strings.Repeat("q", 300), Pending: true})
	tui.crt.startTyping(tui.messages[0].Text)
	start := time.Now()
	tui.revealPending(0)
	// With the default interval (30ms) and 300 characters, the reveal cannot finish instantly.
	if elapsed := time.Since(start); elapsed < 100*time.Millisecond {
		t.Fatalf("the reveal finished in %v: it is not pacing the frames", elapsed)
	}
	if tui.crt.typingInProgress() {
		t.Fatal("the reveal must finish")
	}
}
