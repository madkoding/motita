package tui

import (
	"bytes"
	"context"
	"os"
	"strings"
	"testing"
)

// The live composer: the terminal delivers one byte at a time, the interface owns the editing,
// and the completion popup is drawn from what has been typed. These tests drive the reader
// directly, with the keys written as the terminal would send them.

// liveTUI builds a TUI reading the given raw bytes in character mode.
func liveTUI(keys string, messages ...string) (*TUI, *bytes.Buffer) {
	t, out := newKeyTUI(keys, messages...)
	t.charMode = true
	t.Width, t.Height = 100, 30
	return t, out
}

// TestLiveInputReturnsTheTypedLine: the ordinary path — characters, then Enter.
func TestLiveInputReturnsTheTypedLine(t *testing.T) {
	tu, _ := liveTUI("count the files\n")

	line, ok := tu.readLine(context.Background())
	if !ok {
		t.Fatal("Enter must end the line")
	}
	if line != "count the files" {
		t.Errorf("line = %q", line)
	}
	if tu.draft != "" {
		t.Errorf("the draft must be cleared, got %q", tu.draft)
	}
}

// TestLiveInputRedrawsAsItIsTyped: the whole reason for character mode is that the frame can
// show what has been typed before Enter. Without the redraw the popup could never appear.
func TestLiveInputRedrawsAsItIsTyped(t *testing.T) {
	tu, out := liveTUI("/pl\n")

	if _, ok := tu.readLine(context.Background()); !ok {
		t.Fatal("Enter must end the line")
	}
	frame := stripANSI(lastFrameOf(out))
	if !strings.Contains(frame, "/pl") {
		t.Errorf("the typed text must be drawn:\n%s", frame)
	}
	if !strings.Contains(frame, "/plan") {
		t.Errorf("the popup must be drawn while typing:\n%s", frame)
	}
}

// TestLiveBackspaceRemovesTheLastRune: backspace edits the draft, and it removes a RUNE, not a
// byte: a byte-wise delete would leave half a character, which the terminal renders as garbage.
func TestLiveBackspaceRemovesTheLastRune(t *testing.T) {
	tu, _ := liveTUI("hola\x7fx\n")

	line, ok := tu.readLine(context.Background())
	if !ok {
		t.Fatal("Enter must end the line")
	}
	if line != "holx" {
		t.Errorf("line = %q, want the last rune removed", line)
	}
}

// TestLiveBackspaceOnAnEmptyDraft: pressing backspace at the start of a line must do nothing
// rather than panic on an empty slice.
func TestLiveBackspaceOnAnEmptyDraft(t *testing.T) {
	tu, _ := liveTUI("\x7f\x7f\x08ok\n")

	line, ok := tu.readLine(context.Background())
	if !ok {
		t.Fatal("Enter must end the line")
	}
	if line != "ok" {
		t.Errorf("line = %q", line)
	}
}

// TestLiveTabAlwaysSwitchesMode: Tab means ONE thing — switch between Task and Plan.
//
// It used to also accept the completion when the popup was open, so the same key did two
// different things depending on what had been typed: a user reaching for the mode switch in the
// middle of a line got a command inserted instead. That is worse than losing the shortcut, which
// is why completion moved to the right arrow.
func TestLiveTabAlwaysSwitchesMode(t *testing.T) {
	tu, _ := liveTUI("/pl\t\n")

	line, ok := tu.readLine(context.Background())
	if !ok {
		t.Fatal("the token must be returned")
	}
	if line != "\t" {
		t.Errorf("line = %q, want the Tab token so the mode switch happens", line)
	}
	if tu.draft != "" {
		t.Errorf("the draft must be cleared on a mode switch, got %q", tu.draft)
	}
}

// TestTheRightArrowAcceptsTheCompletion: completion still exists, on the key that means "accept
// forward" everywhere else and that nothing here had claimed.
func TestTheRightArrowAcceptsTheCompletion(t *testing.T) {
	tu, _ := liveTUI("/pl" + keyRight + "\n")

	line, ok := tu.readLine(context.Background())
	if !ok {
		t.Fatal("Enter must end the line")
	}
	if strings.TrimSpace(line) != "/plan" {
		t.Errorf("line = %q, want the right arrow to have completed the command", line)
	}
}

// TestTheRightArrowIsStillAnArrowWithNoPopup: accepting only consumes the key when there was
// something to accept. An arrow press on an ordinary line must reach the navigation switch.
func TestTheRightArrowIsStillAnArrowWithNoPopup(t *testing.T) {
	tu, _ := liveTUI("hello" + keyRight)

	line, ok := tu.readLine(context.Background())
	if !ok {
		t.Fatal("the sequence must be returned")
	}
	if line != keyRight {
		t.Errorf("line = %q, want the arrow to pass through", line)
	}
}

// TestLiveTabSwitchesModeWhenThereIsNothingToComplete: Tab has always meant "next mode" here,
// and the completion popup must not take that away. This is the regression that would break
// every existing user of the interface.
func TestLiveTabSwitchesModeWhenThereIsNothingToComplete(t *testing.T) {
	tu, _ := liveTUI("\thello\n")

	line, ok := tu.readLine(context.Background())
	if !ok {
		t.Fatal("the token must be returned")
	}
	if line != "\t" {
		t.Errorf("line = %q, want the Tab token", line)
	}
}

// TestLiveEscapeClosesThePopupFirst: Escape dismisses the suggestion list, and only clears the
// line when there is nothing left to dismiss. One key, one job at a time.
func TestLiveEscapeClosesThePopupFirst(t *testing.T) {
	tu, out := liveTUI("/pl\x1bok\n")

	line, ok := tu.readLine(context.Background())
	if !ok {
		t.Fatal("Enter must end the line")
	}
	if line != "ok" {
		t.Errorf("line = %q, want the popup dismissed and the line cleared", line)
	}
	if strings.Contains(stripANSI(lastFrameOf(out)), "/plan") {
		t.Errorf("the popup must be closed after Escape:\n%s", stripANSI(lastFrameOf(out)))
	}
}

// TestLiveEscapeWithoutAPopupIsTheCancelToken: with nothing to dismiss, Escape is the cancel
// token the rest of the interface already understands.
func TestLiveEscapeWithoutAPopupIsTheCancelToken(t *testing.T) {
	tu, _ := liveTUI("hello\x1b")

	line, ok := tu.readLine(context.Background())
	if !ok {
		t.Fatal("the token must be returned")
	}
	if line != keyEsc {
		t.Errorf("line = %q, want the Escape token", line)
	}
}

// TestLiveControlBytesAreTokens: the control keys the interface handles stay keys and are never
// typed into the prompt.
func TestLiveControlBytesAreTokens(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   string
		want string
	}{
		{"Ctrl+F", "ab\x06", "\x06"},
		{"Ctrl+D", "ab\x04", "\x04"},
		{"Ctrl+U", "ab\x15", "\x15"},
	} {
		tu, _ := liveTUI(tc.in)
		line, ok := tu.readLine(context.Background())
		if !ok {
			t.Fatalf("%s: the token must be returned", tc.name)
		}
		if line != tc.want {
			t.Errorf("%s: line = %q, want %q", tc.name, line, tc.want)
		}
	}
}

// TestLiveArrowKeysAreReturnedAsTheirSequence: the navigation keys arrive as escape sequences
// and reach the same switch as always, so scrolling works while the terminal is in character
// mode — which is a new situation, since the whole-line reader never delivered them live.
func TestLiveArrowKeysAreReturnedAsTheirSequence(t *testing.T) {
	tu, _ := liveTUI("\x1b[B")

	line, ok := tu.readLine(context.Background())
	if !ok {
		t.Fatal("the sequence must be returned")
	}
	if line != keyDown {
		t.Errorf("line = %q, want %q", line, keyDown)
	}
}

// TestLiveOtherEscapeSequencesPassThrough: any CSI sequence is read whole and handed on, so a
// key the completion code has no opinion about is never mistaken for a bare Escape.
func TestLiveOtherEscapeSequencesPassThrough(t *testing.T) {
	tu, _ := liveTUI("\x1b[5~")

	line, ok := tu.readLine(context.Background())
	if !ok {
		t.Fatal("the sequence must be returned")
	}
	if line != keyPgUp {
		t.Errorf("line = %q, want %q", line, keyPgUp)
	}
}

// TestLiveInputStopsAtEndOfInput: a closed input ends the read rather than spinning.
func TestLiveInputStopsAtEndOfInput(t *testing.T) {
	tu, _ := liveTUI("")

	if _, ok := tu.readLine(context.Background()); ok {
		t.Error("a closed input must report that the read is over")
	}
}

// TestLiveInputStopsWhenTheContextIsCancelled: the reader must not hold the interface open when
// the run is being abandoned, which is what Ctrl+C does.
func TestLiveInputStopsWhenTheContextIsCancelled(t *testing.T) {
	tu, _ := liveTUI("")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, ok := tu.readLine(ctx); ok {
		t.Error("a cancelled context must end the read")
	}
}

// TestLiveUnprintableBytesAreIgnored: a control byte with no meaning here must not be inserted
// into the line. Typing garbage into a prompt is worse than ignoring the key.
func TestLiveUnprintableBytesAreIgnored(t *testing.T) {
	tu, _ := liveTUI("a\x01b\n")

	line, ok := tu.readLine(context.Background())
	if !ok {
		t.Fatal("Enter must end the line")
	}
	if line != "ab" {
		t.Errorf("line = %q, want the control byte dropped", line)
	}
}

// TestLiveEscapeWithoutASequenceIsBareEscape: a lone ESC sends one byte and nothing follows, so
// a read that insisted on completing a sequence would hang the interface on every Escape.
func TestLiveEscapeWithoutASequenceIsBareEscape(t *testing.T) {
	tu, _ := liveTUI("\x1b")
	tu.charMode = true

	line, ok := tu.readLine(context.Background())
	if !ok {
		t.Fatal("Escape must be returned")
	}
	if line != keyEsc {
		t.Errorf("line = %q, want %q", line, keyEsc)
	}
}

// TestLiveEscapeFollowedByANonCSISequence: ESC plus something that is not "[" is a bare Escape,
// because that is the shape a human's keypress has.
func TestLiveEscapeFollowedByANonCSISequence(t *testing.T) {
	tu, _ := liveTUI("\x1bx")

	if tu.readEscapeLive() != keyEsc {
		t.Error("ESC followed by a plain byte must read as a bare Escape")
	}
}

// TestLiveEscapeSequenceTooLongIsAnEscape: a sequence that never terminates must not make the
// reader consume the rest of the input.
func TestLiveEscapeSequenceTooLongIsAnEscape(t *testing.T) {
	tu, _ := liveTUI("\x1b[1234567890123456789012")

	// ESC first, then the completion: without this the reader sees ESC as the byte after it
	// and answers from the peek branch instead of ever entering the loop it is meant to bound.
	if _, err := tu.input().ReadByte(); err != nil {
		t.Fatal(err)
	}
	if tu.readEscapeLive() != keyEsc {
		t.Error("an over-long sequence must fall back to a bare Escape")
	}
}

// TestLiveReaderIsNotStuckWhenTheSequenceIsTruncated: an input that ends mid-sequence must
// report an Escape rather than block.
func TestLiveReaderIsNotStuckWhenTheSequenceIsTruncated(t *testing.T) {
	tu, _ := liveTUI("\x1b[")

	if _, err := tu.input().ReadByte(); err != nil {
		t.Fatal(err)
	}
	if tu.readEscapeLive() != keyEsc {
		t.Error("a truncated sequence must fall back to a bare Escape")
	}
}

// TestTheDraftIsClearedWhenTheReadEnds: a draft that survived an abandoned read would reappear
// in the next prompt, which looks like the interface typing by itself.
func TestTheDraftIsClearedWhenTheReadEnds(t *testing.T) {
	tu, _ := liveTUI("gone")

	if _, ok := tu.readLine(context.Background()); ok {
		t.Fatal("the read must end")
	}
	if tu.draft != "" {
		t.Errorf("the draft must be cleared, got %q", tu.draft)
	}
}

// TestTheDraftIsRedrawnWhenTheReadEndsMidLine: the frame must be repainted so the abandoned
// text does not stay on screen.
func TestTheDraftIsRedrawnWhenTheReadEndsMidLine(t *testing.T) {
	tu, out := liveTUI("typing")
	out.Reset()

	if _, ok := tu.readLine(context.Background()); ok {
		t.Fatal("the read must end")
	}
	if out.Len() == 0 {
		t.Error("the frame must be redrawn when an abandoned draft is cleared")
	}
}

// The terminal mode itself.

// TestRestoringIsIdempotent: restore runs from the deferred call AND from the panic path, and
// the second one must not try to close an already-closed handle.
func TestRestoringIsIdempotent(t *testing.T) {
	var calls int
	old := runSttyMode
	runSttyMode = func(f *os.File, args ...string) error { calls++; return nil }
	defer func() { runSttyMode = old }()

	f, err := os.CreateTemp(t.TempDir(), "tty")
	if err != nil {
		t.Fatal(err)
	}
	m := &terminalMode{tty: f, active: true}

	m.restore()
	m.restore()
	m.restore()

	if calls != 1 {
		t.Errorf("restore ran %d times, want exactly 1", calls)
	}
	if m.active {
		t.Error("the mode must be marked inactive after restoring")
	}
}

// TestRestoringANilOrInactiveModeIsSafe: the caller should not have to branch on whether the
// terminal was ever changed.
func TestRestoringANilOrInactiveModeIsSafe(t *testing.T) {
	var nilMode *terminalMode
	nilMode.restore()
	(&terminalMode{}).restore()
}

// TestEnteringTheModeAsksTheDriverForCBreakWithoutEcho: cbreak is what makes a keystroke arrive
// as it is typed, and -echo is what stops the driver drawing the line a second time underneath
// the one the interface draws.
func TestEnteringTheModeAsksTheDriverForCBreakWithoutEcho(t *testing.T) {
	var got []string
	oldRun, oldOpen, oldStat := runSttyMode, openTTYMode, statTTY
	defer func() { runSttyMode, openTTYMode, statTTY = oldRun, oldOpen, oldStat }()

	runSttyMode = func(f *os.File, args ...string) error { got = args; return nil }
	openTTYMode = func(name string, flag int, perm os.FileMode) (*os.File, error) {
		return os.CreateTemp(t.TempDir(), "tty")
	}
	statTTY = func(*os.File) (os.FileMode, error) { return os.ModeCharDevice, nil }

	m := enterRaw()
	defer m.restore()

	if !m.active {
		t.Fatal("the mode must be active when the driver accepts it")
	}
	if len(got) != 2 || got[0] != "cbreak" || got[1] != "-echo" {
		t.Errorf("stty args = %v, want cbreak -echo", got)
	}
}

// TestNoTerminalDegradesToWholeLines: piped input has no controlling terminal, and the
// interface must keep working there — it just cannot offer live completion.
func TestNoTerminalDegradesToWholeLines(t *testing.T) {
	oldOpen := openTTYMode
	defer func() { openTTYMode = oldOpen }()
	openTTYMode = func(name string, flag int, perm os.FileMode) (*os.File, error) {
		return nil, os.ErrNotExist
	}

	m := enterRaw()
	defer m.restore()
	if m.active {
		t.Error("without a terminal the mode must stay inactive")
	}
}

// TestANonTerminalFileDegrades: a redirection to a file looks like a terminal to an open call
// but is not one, and asking it for cbreak would fail noisily.
func TestANonTerminalFileDegrades(t *testing.T) {
	oldOpen, oldStat := openTTYMode, statTTY
	defer func() { openTTYMode, statTTY = oldOpen, oldStat }()
	openTTYMode = func(name string, flag int, perm os.FileMode) (*os.File, error) {
		return os.CreateTemp(t.TempDir(), "file")
	}
	statTTY = func(*os.File) (os.FileMode, error) { return 0, nil }

	m := enterRaw()
	defer m.restore()
	if m.active {
		t.Error("a regular file must not be put into character mode")
	}
}

// TestADriverThatRefusesTheModeDegrades: a terminal that will not take the mode leaves the
// behaviour we had — whole lines, no popup, working interface. Degrading is right; failing
// would make the program unusable on such a terminal.
func TestADriverThatRefusesTheModeDegrades(t *testing.T) {
	oldRun, oldOpen, oldStat := runSttyMode, openTTYMode, statTTY
	defer func() { runSttyMode, openTTYMode, statTTY = oldRun, oldOpen, oldStat }()

	openTTYMode = func(name string, flag int, perm os.FileMode) (*os.File, error) {
		return os.CreateTemp(t.TempDir(), "tty")
	}
	statTTY = func(*os.File) (os.FileMode, error) { return os.ModeCharDevice, nil }
	runSttyMode = func(f *os.File, args ...string) error { return os.ErrInvalid }

	m := enterRaw()
	defer m.restore()
	if m.active {
		t.Error("a refused mode must leave the terminal alone")
	}
}

// TestTheTerminalIsRestoredBeforeAPanicContinues: a session that dies while the terminal is in
// cbreak with echo off gives the user back a shell that shows nothing they type. The restore
// must happen on the way out of a panic, before it keeps unwinding.
func TestTheTerminalIsRestoredBeforeAPanicContinues(t *testing.T) {
	oldRun, oldOpen, oldStat := runSttyMode, openTTYMode, statTTY
	defer func() { runSttyMode, openTTYMode, statTTY = oldRun, oldOpen, oldStat }()

	var restored bool
	openTTYMode = func(name string, flag int, perm os.FileMode) (*os.File, error) {
		return os.CreateTemp(t.TempDir(), "tty")
	}
	statTTY = func(*os.File) (os.FileMode, error) { return os.ModeCharDevice, nil }
	runSttyMode = func(f *os.File, args ...string) error {
		if len(args) == 1 && args[0] == "sane" {
			restored = true
		}
		return nil
	}

	m := enterRaw()
	func() {
		// The panic is CAUGHT here, after recoverRaw has had its turn: recoverRaw re-panics by
		// design, so the outer recover is what lets the test observe both facts — that the
		// terminal was put back, and that the panic was not swallowed.
		defer func() { _ = recover() }()
		defer recoverRaw(m)()
		panic("something went wrong deep in the paint")
	}()

	if !restored {
		t.Error("the terminal must be put back before the panic continues")
	}
}

// TestThePanicIsNotSwallowed: recoverRaw restores the terminal and then lets the panic keep
// going. A version that swallowed it would turn a crash into a silent, wrong result.
func TestThePanicIsNotSwallowed(t *testing.T) {
	var seen any
	func() {
		defer func() { seen = recover() }()
		defer recoverRaw(&terminalMode{})()
		panic("boom")
	}()

	if seen != "boom" {
		t.Errorf("the panic must continue, got %v", seen)
	}
}

// TestASuccessfulExitDoesNotPanic: recoverRaw must re-panic ONLY when there was a panic. A
// version that panicked unconditionally would tear down every normal exit.
func TestASuccessfulExitDoesNotPanic(t *testing.T) {
	func() {
		defer recoverRaw(&terminalMode{})()
	}()
}

// TestNoControlKeyReachesTheInput: every control byte a terminal can send must be CAPTURED by
// the interface. Typing is what a printable character does; a control byte is a key, and a key
// that gets appended to the line is drawn on screen as garbage and sent to the model as part of
// the message.
//
// This is a sweep rather than a list of examples: the defect it prevents is a control byte
// nobody thought about falling through a switch, which is exactly how Ctrl+A, Ctrl+B, Ctrl+K,
// Ctrl+W and Ctrl+Z used to be inserted.
func TestNoControlKeyReachesTheInput(t *testing.T) {
	// The tokens the interface returns on purpose. They are not text, and the caller acts on
	// them instead of sending them.
	tokens := map[byte]bool{0x03: true, 0x04: true, 0x06: true, 0x15: true, 0x1b: true, '\t': true}

	for b := byte(0x01); b < 0x20; b++ {
		if tokens[b] {
			continue
		}
		tu, _ := newKeyTUI(string([]byte{b}) + "\n")
		tu.charMode = true
		tu.Width, tu.Height = 100, 24

		line, ok := tu.readLine(context.Background())
		if !ok {
			continue // a key that ends the read is not inserted either
		}
		for _, r := range line {
			if r == rune(b) {
				t.Errorf("Ctrl+%c (0x%02x) was inserted into the input: %q", 'A'+b-1, b, line)
				break
			}
		}
	}
}

// TestHighBytesDoNotBecomeMojibake: a byte above 0x7f is the START of a UTF-8 sequence, not a
// character. The old guard tested the byte against 0x20, which every high byte passes, so an
// accented letter was appended one byte at a time and drawn as mojibake.
func TestHighBytesDoNotBecomeMojibake(t *testing.T) {
	for _, text := range []string{"café", "señal", "año 2026", "→ ok", "日本"} {
		tu, _ := newKeyTUI(text + "\n")
		tu.charMode = true
		tu.Width, tu.Height = 100, 24

		line, ok := tu.readLine(context.Background())
		if !ok {
			t.Fatalf("%q: the read ended early", text)
		}
		if line != text {
			t.Errorf("input %q came back as %q", text, line)
		}
	}
}

// TestATruncatedUTF8SequenceIsDropped: a paste cut short, or a terminal that died mid-character,
// leaves a leading byte with no continuation. It must be dropped rather than inserted as a
// partial character.
func TestATruncatedUTF8SequenceIsDropped(t *testing.T) {
	// 0xc3 starts a two-byte sequence, and the byte that follows is a printable one rather than
	// a continuation. The broken pair is dropped; what comes after it is the user's text and has
	// to survive.
	tu, _ := newKeyTUI(string([]byte{0xc3, 'a'}) + "\n")
	tu.charMode = true
	tu.Width, tu.Height = 100, 24

	line, ok := tu.readLine(context.Background())
	if !ok {
		t.Fatal("the read must end at the newline")
	}
	if line != "a" {
		t.Errorf("the broken sequence must be dropped and the rest kept, got %q", line)
	}
	// And nothing that is not text may appear in the line.
	for _, r := range line {
		if r < 0x20 {
			t.Errorf("a control character survived: %q", line)
		}
	}
}

// TestFourByteCharactersArriveWhole: an emoji is four bytes, and the reader has to assemble all
// of them. A version that stopped at two would render it as two mojibake characters, which is
// the same defect as a broken accent but harder to notice in a test that only checks Latin-1.
func TestFourByteCharactersArriveWhole(t *testing.T) {
	for _, text := range []string{"listo 🚀", "ok ✅✅", "un 🙂 emoji"} {
		tu, _ := newKeyTUI(text + "\n")
		tu.charMode = true
		tu.Width, tu.Height = 100, 24

		line, ok := tu.readLine(context.Background())
		if !ok {
			t.Fatalf("%q: the read ended early", text)
		}
		if line != text {
			t.Errorf("input %q came back as %q", text, line)
		}
	}
}

// TestAStrayContinuationByteIsNotACharacter: a continuation byte with no leading byte before it
// is not text, and inserting it would draw a lone replacement character.
func TestAStrayContinuationByteIsNotACharacter(t *testing.T) {
	// 0x80 is a continuation byte on its own.
	tu, _ := newKeyTUI(string([]byte{0x80, 'a'}) + "\n")
	tu.charMode = true
	tu.Width, tu.Height = 100, 24

	line, ok := tu.readLine(context.Background())
	if !ok {
		t.Fatal("the read must end")
	}
	if line != "a" {
		t.Errorf("the stray byte must be dropped and the rest kept, got %q", line)
	}
	if _, bad := tu.readRuneFrom(0x80); bad {
		t.Error("a stray continuation byte must not be reported as a character")
	}
}

// TestAnUnreadableInputDuringAUTF8SequenceEndsTheRead: the continuation bytes are read without a
// deadline because the terminal wrote them with the leading byte. If the input is exhausted
// instead — a file that ends, a pipe that closes — the read must END rather than block or
// invent a character.
func TestAnUnreadableInputDuringAUTF8SequenceEndsTheRead(t *testing.T) {
	// A lone leading byte with nothing after it.
	tu, _ := newKeyTUI(string([]byte{0xc3}))
	tu.charMode = true
	tu.Width, tu.Height = 100, 24

	if _, ok := tu.readLine(context.Background()); ok {
		t.Error("an exhausted input must end the read, not report a line")
	}
}

// TestAnOverlongButWellFormedSequenceIsRejected: a four-byte leading byte whose continuation
// bytes are all present can still encode a value RFC 3629 does not allow — an overlong form.
// utf8.Valid is what rejects it, and without that check the interface would accept a byte
// sequence that is not a character.
func TestAnOverlongButWellFormedSequenceIsRejected(t *testing.T) {
	// 0xf0 0x80 0x80 0x80 is a four-byte sequence encoding U+0000 the long way: the shape is
	// right and the value is illegal.
	tu, _ := newKeyTUI(string([]byte{0xf0, 0x80, 0x80, 0x80, 'a'}) + "\n")
	tu.charMode = true
	tu.Width, tu.Height = 100, 24

	line, ok := tu.readLine(context.Background())
	if !ok {
		t.Fatal("the read must end at the newline")
	}
	if line != "a" {
		t.Errorf("the illegal sequence must be dropped and the rest kept, got %q", line)
	}
}

// TestNoControlByteEverReachesTheChat: the round trip that matters. The reader captures control
// keys, and the dispatcher must consume the tokens it returns — otherwise they fall through to
// the chat and are SENT TO THE MODEL as control characters in the message.
//
// This was a real defect: Ctrl+D, Ctrl+F and Ctrl+U were captured from the input, returned as
// tokens, had no case in the dispatcher, and reached the provider. The agent's own answer named
// them back ("caracteres de control intercalados: \x01, \x0b, \x17, \x15").
func TestNoControlByteEverReachesTheChat(t *testing.T) {
	// The control bytes a terminal sends that are NOT tokens the interface acts on.
	for b := byte(0x01); b < 0x20; b++ {
		if b == '\t' || b == '\n' || b == '\r' {
			continue
		}
		tu, _ := newKeyTUI("")
		tu.Width, tu.Height = 100, 24

		// The dispatcher is what decides: a handled line never reaches the chat.
		handled, _ := tu.handleShortcut(context.Background(), string([]byte{b}))
		if !handled {
			t.Errorf("Ctrl+%c (0x%02x) is not consumed by the dispatcher, so it would be sent as a message",
				'A'+b-1, b)
		}
	}
	// And the tokens with a meaning of their own ARE handled, which is different from swallowed:
	// acting on them is what stops them being text.
	for b, what := range map[byte]string{0x04: "Ctrl+D", 0x06: "Ctrl+F", 0x15: "Ctrl+U"} {
		tu, _ := newKeyTUI("")
		tu.Width, tu.Height = 100, 24
		if handled, _ := tu.handleShortcut(context.Background(), string([]byte{b})); !handled {
			t.Errorf("%s (0x%02x) must be handled", what, b)
		}
	}
}

// TestTheInputIsClearedAfterAMessageIsSent: once a message is dispatched it belongs to the
// thread, and leaving it in the field invites sending it twice — or editing the copy while
// believing the sent one is being changed.
func TestTheInputIsClearedAfterAMessageIsSent(t *testing.T) {
	tu, _ := newKeyTUI("una pregunta\n\nq\n")
	tu.Width, tu.Height = 100, 24

	if _, ok := tu.readLine(context.Background()); !ok {
		t.Fatal("the line must be read")
	}
	if tu.draft != "" {
		t.Errorf("the draft must be empty after sending, got %q", tu.draft)
	}
	// And the drawn field must be empty too: the text moved to the thread, it is not shown twice.
	field := stripANSI(strings.Join(tu.composerLines(), "\n"))
	if strings.Contains(field, "una pregunta") {
		t.Errorf("the sent text is still drawn in the input:\n%s", field)
	}
}

// TestTheWholeLineReaderRemovesControlCharactersToo: both readers must agree about what text is.
//
// The live reader refuses to add a control character; the whole-line reader takes a run of bytes
// and only trimmed the ENDS, so a control byte in the middle of a line survived and was sent to
// the model. The terminal's own control keys ended up in the message, and the agent reported
// them back. Trimming the ends is not the same as removing what is inside.
func TestTheWholeLineReaderRemovesControlCharactersToo(t *testing.T) {
	// The whole-line path: not in character mode, which is what happens when the terminal will
	// not accept cbreak — piped input, a cron job, a script.
	tu, _ := newKeyTUI("hola\x01mundo\x0b\x17\n")
	tu.charMode = false
	tu.Width, tu.Height = 100, 24

	line, ok := tu.readLine(context.Background())
	if !ok {
		t.Fatal("the read must return the line")
	}
	if line != "holamundo" {
		t.Errorf("line = %q, want the control characters gone", line)
	}
	for _, r := range line {
		if r < 0x20 || r == 0x7f {
			t.Errorf("a control character survived: %q", line)
		}
	}
}

// TestSanitiseLineKeepsWhatIsText: the sanitiser must be surgical. It removes keys and DEL, and
// every printable character stays — including the accented ones and the emoji, which are above
// U+0020 and must not be touched.
func TestSanitiseLineKeepsWhatIsText(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"hola", "hola"},
		{"hola mundo", "hola mundo"},
		{"hola\x01mundo", "holamundo"},
		{"\x0b\x17a\x1fb", "ab"},
		{"café ☕ 🚀", "café ☕ 🚀"},
		{"  spaces are kept  ", "  spaces are kept  "},
		{"tab\tinside", "tabinside"},
		{"del\x7fete", "delete"},
		{"", ""},
		{"\x01\x02\x03", ""},
	} {
		if got := sanitiseLine(tc.in); got != tc.want {
			t.Errorf("sanitiseLine(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestNoKeySequenceIsEverSentAsAMessage: pressing an ARROW used to send a message.
//
// The dispatcher named the keys it acts on and fell through to the chat for everything else, so
// a sequence it did not name — the right and left arrows, Delete, F1, Shift+Tab, and any other a
// terminal can send — was dispatched as a chat message. The user saw their own input submitted as
// if they had pressed Enter.
//
// The rule is asserted by SHAPE, not by a list: an escape sequence is how a terminal reports a
// key, and text never contains one. A list can only ever be incomplete, and whatever is missing
// from it becomes a message sent by accident — which is how this bug existed at all.
func TestNoKeySequenceIsEverSentAsAMessage(t *testing.T) {
	sequences := []string{
		// The arrows, in their plain and modified forms.
		"\x1b[A", "\x1b[B", "\x1b[C", "\x1b[D",
		"\x1b[1;5C", "\x1b[1;5D", "\x1b[1;2A", "\x1b[1;3B",
		// Navigation and editing.
		"\x1b[5~", "\x1b[6~", "\x1b[H", "\x1b[F", "\x1b[1~", "\x1b[4~",
		"\x1b[2~", "\x1b[3~",
		// Function keys, in both common encodings.
		"\x1bOP", "\x1b[11~", "\x1b[Z",
		// And the ones the interface DOES act on: handled is the same answer, because acting on a
		// key and refusing to send it are not in conflict.
		keyEsc, keyUp, keyDown, keyPgUp, keyPgDn, keyHome, keyEnd, keyRight,
	}
	for _, seq := range sequences {
		tu, out := newKeyTUI("")
		tu.Width, tu.Height = 100, 24
		out.Reset()

		handled, quit := tu.handleShortcut(context.Background(), seq)
		if !handled {
			t.Errorf("%q is not consumed, so it would be sent as a message", seq)
		}
		if quit {
			t.Errorf("%q must not be treated as a quit command", seq)
		}
	}
}

// TestAKeySequenceIsNotEchoedIntoTheChat: consuming the key is not enough — it must leave no
// trace. A key that is handled AND written to the conversation would still show up as a message
// in the thread, empty or not.
func TestAKeySequenceIsNotEchoedIntoTheChat(t *testing.T) {
	tu, _ := newKeyTUI("")
	tu.Width, tu.Height = 100, 24
	before := len(tu.messages)

	for _, seq := range []string{"\x1b[C", "\x1b[D", "\x1b[3~", "\x1bOP"} {
		tu.handleShortcut(context.Background(), seq)
	}
	if len(tu.messages) != before {
		t.Errorf("a key added %d message(s) to the conversation", len(tu.messages)-before)
	}
}

// TestTypedTextIsStillAMessage: the catch-all must be exact. Ordinary text — including text that
// looks like a command — still reaches the chat, or the interface would silently swallow what
// the user typed.
func TestTypedTextIsStillAMessage(t *testing.T) {
	// "/find algo" is deliberately NOT in this list: it is a command the interface acts on, and
	// consuming it is correct. What must reach the chat is ordinary text.
	for _, text := range []string{"hola", "una pregunta larga", "x", "1234", "cuenta los ficheros"} {
		tu, _ := newKeyTUI("")
		tu.Width, tu.Height = 100, 24

		if handled, _ := tu.handleShortcut(context.Background(), text); handled {
			t.Errorf("%q must reach the chat, not be swallowed", text)
		}
	}
}

// TestArrowsTypedAfterTextNeverReachTheMessage: pressing an arrow with text already on the line
// must not put the sequence in the message.
//
// The whole-line reader handled an ESC only as the FIRST byte of a line. An arrow pressed after
// some text does not put it there — it arrives in the middle of the run, and the bytes were kept
// as literal text. Measured on the target machine, the message that reached the model was
// `hola\x1b[C\x1b[D\x1b[A\x1b[B\x1b[3~\x1bOP\x15/quit`.
func TestArrowsTypedAfterTextNeverReachTheMessage(t *testing.T) {
	tu, _ := newKeyTUI("hola\x1b[C\x1b[D\x1b[A\x1b[B\x1b[3~\x1bOP\n")
	tu.charMode = false // the whole-line path, which is where this leaked
	tu.Width, tu.Height = 100, 24

	line, ok := tu.readLine(context.Background())
	if !ok {
		t.Fatal("the read must return the line")
	}
	if line != "hola" {
		t.Errorf("line = %q, want just the typed text", line)
	}
	if strings.ContainsRune(line, 0x1b) {
		t.Errorf("an escape byte survived into the message: %q", line)
	}
}

// TestStripKeySequencesRemovesTheWholeSequence: taking out only the ESC would leave the parameter
// bytes behind as text, so "[C" and "[3~" would appear in the message.
func TestStripKeySequencesRemovesTheWholeSequence(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"hola", "hola"},
		{"hola\x1b[C", "hola"},
		{"\x1b[C\x1b[D\x1b[A\x1b[B", ""},
		{"a\x1b[3~b", "ab"},
		{"a\x1bOPb", "ab"},
		{"a\x1bb", "ab"},             // a bare ESC
		{"a\x1b[1;5Cb", "ab"},        // a modified arrow
		{"café \x1b[A 🚀", "café  🚀"}, // text around it survives
		{"sin secuencias", "sin secuencias"},
		{"\x1b[", ""},      // truncated at the end
		{"\x1b", ""},       // nothing but the introducer
		{"a\x1b[Zb", "ab"}, // Shift+Tab
	} {
		if got := stripKeySequences(tc.in); got != tc.want {
			t.Errorf("stripKeySequences(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestASlashCommandStillWorksAfterAKey: the stripping must not damage the text around it, or a
// command typed after an arrow would stop being recognised.
func TestASlashCommandStillWorksAfterAKey(t *testing.T) {
	tu, _ := newKeyTUI("\x1b[C/quit\n")
	tu.charMode = false
	tu.Width, tu.Height = 100, 24

	// The key comes back FIRST — the dispatcher has to see it to act on it — and the text that
	// shared its read follows on the next one instead of being thrown away. Asserting "both in
	// one call" would have been asserting something the design never promised; what matters is
	// that neither is lost.
	first, ok := tu.readLine(context.Background())
	if !ok {
		t.Fatal("the read must return the key")
	}
	if first != keyRight {
		t.Fatalf("the first read = %q, want the key sequence", first)
	}
	if handled, _ := tu.handleShortcut(context.Background(), first); !handled {
		t.Error("the key must be handled, not sent")
	}

	second, ok := tu.readLine(context.Background())
	if !ok {
		t.Fatal("the text that followed the key must still be readable")
	}
	if second != "/quit" {
		t.Errorf("the text after the key = %q, want the command intact", second)
	}
	handled, quit := tu.handleShortcut(context.Background(), second)
	if !handled || !quit {
		t.Errorf("the command after a key must still be recognised (handled=%v quit=%v)", handled, quit)
	}
}
