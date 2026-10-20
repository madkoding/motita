package tui

import (
	"bufio"
	"bytes"
	"context"
	"strings"
	"time"
	"unicode/utf8"
)

// Reading the keyboard: whole lines or one key at a time, and the escape sequences the terminal
// sends for the keys that are not text.

func (t *TUI) input() *bufio.Reader {
	if t.reader == nil {
		t.reader = bufio.NewReader(t.In)
	}
	return t.reader
}

// readKey returns the next byte from input without consuming a full line.
// It is used to intercept Tab before it reaches the input buffer.
func (t *TUI) readKey(ctx context.Context) (byte, bool) {
	ch := make(chan byte, 1)
	go func() {
		b, err := t.input().ReadByte()
		if err != nil {
			close(ch)
			return
		}
		ch <- b
	}()
	select {
	case b, ok := <-ch:
		return b, ok
	case <-ctx.Done():
		return 0, false
	}
}

// Special keys are returned by readLine as raw tokens. The user cannot type
// them, so a caller that receives one knows the keyboard produced it: this is
// the same contract the Tab already used, extended to the keys the interaction
// guide calls universal.
//
// The bytes come from the terminal, not from the guide: a bare ESC is the
// cancel key, and the CSI sequences are what an xterm-compatible terminal sends
// for the arrows and the page keys. Reading them costs nothing and is what makes
// the interface navigable without a mouse.
const (
	keyEsc   = "\x1b" // 0x1b on its own
	keyUp    = "\x1b[A"
	keyDown  = "\x1b[B"
	keyPgUp  = "\x1b[5~"
	keyPgDn  = "\x1b[6~"
	keyLeft  = "\x1b[D"
	keyRight = "\x1b[C"
	// Enter is not a byte the reader returns: pressing it ends the line, so it arrives as an
	// EMPTY line rather than as a token. This constant exists so the window can name the key it
	// means instead of comparing against "" at each call site — a bare "" reads like a bug.
	keyEnter = ""
	keyHome  = "\x1b[H"
	keyEnd   = "\x1b[F"
	// Control bytes arrive as themselves. The half-page pair is the vim convention
	// the interaction guide lists, and it is what a reader uses to skim a long
	// answer without losing their place the way a full page does.
	keyHalfUp   = "\x15" // Ctrl+U
	keyHalfDown = "\x04" // Ctrl+D
	// Search follows the vim convention the interaction guide lists: `/` opens it, Esc
	// leaves it. `/` is taken by the mode shortcuts, so Ctrl+F opens it instead — the
	// guide's own alternative binding for the same action.
	keyFind = "\x06" // Ctrl+F
)

// readLine reads one line from the input. It returns ok=false on EOF or when the
// context is cancelled.
//
// The first byte is read on its own, before a full line is asked for, because
// several keys have to be recognised without becoming part of the line:
//
//   - Tab switches view, so it must not be appended to a prompt (which is what
//     bufio would do, since it only stops at a newline).
//   - A newline on its own is an empty line, and it means "run the action of this
//     view". It has to be returned as such: reading a full line after the
//     newline was already consumed would silently return the *next* line and lose
//     the empty one.
//   - ESC opens a key that is not text: either a bare cancel, or one of the
//     arrow and page sequences the terminal sends.
func (t *TUI) readLine(ctx context.Context) (string, bool) {
	// In character mode the line is edited here, which is what lets the completion popup see
	// what has been typed as it is typed. It returns the same tokens the whole-line reader
	// does — the Tab, the escape sequences, the control bytes — so everything downstream is
	// unchanged and the two paths stay interchangeable.
	if t.charMode {
		return t.readLineLive(ctx)
	}
	head, ok := t.readKey(ctx)
	if !ok {
		return "", false
	}
	if head == '	' {
		return "	", true
	}
	if head == 0x1b {
		// The sequence is returned and the dispatcher acts on it. Whatever followed it on the
		// same read is NOT consumed here: the next call reads it, which is the whole reason a
		// command typed after an arrow survives.
		return t.readEscape(), true
	}
	// The control bytes are keys, not text: returning them stops them being typed into
	// the prompt and then swallowed by the trim below.
	if head == 0x15 || head == 0x04 || head == 0x06 {
		return string(head), true
	}
	if head == '\n' || head == '\r' {
		return "", true
	}

	// Not a special key: read the rest of the line and put the first byte back.
	ch := make(chan lineResult, 1)
	go func() {
		rest, err := t.input().ReadString('\n')
		ch <- lineResult{line: string(head) + rest, err: err}
	}()
	select {
	case r := <-ch:
		if r.err != nil {
			return "", false
		}
		// ORDER MATTERS: the sequences are stripped FIRST.
		//
		// sanitiseLine removes every byte below 0x20, and the escape introducer is one of them.
		// Running it first deleted the ESC and left the parameter bytes behind as ordinary text,
		// so the message read "hello[C[D[A" — the sequence with its marker cut off, which no
		// longer looks like a sequence to anything.
		//
		// The introducer is the only thing that says where a sequence starts, so it has to be
		// read before it is erased.
		return sanitiseLine(stripKeySequences(strings.TrimSpace(r.line))), true
	case <-ctx.Done():
		go func() { <-ch }()
		return "", false
	}
}

// sanitiseLine removes every control character from a line.
//
// Both readers go through this. The live reader builds its line one character at a time and
// refuses to add anything below U+0020, but the whole-line reader takes a run of bytes from the
// terminal and only trims the ENDS — so a control byte in the MIDDLE of a line survived and was
// dispatched as part of the message. That is how the agent came to answer "caracteres de control
// intercalados: \x01, \x0b, \x17": the terminal's own control keys were reaching the model as
// text.
//
// Trimming the ends is not the same as removing what is inside, and a key is a key wherever it
// sits in the line.
func sanitiseLine(s string) string {
	if !strings.ContainsFunc(s, func(r rune) bool { return r < 0x20 || r == 0x7f }) {
		return s // the common case, and no allocation
	}
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if r < 0x20 || r == 0x7f {
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// stripKeySequences removes every escape sequence that sits INSIDE a line.
//
// The whole-line reader handles an ESC only when it is the FIRST byte of the line. An arrow
// pressed after some text has already been typed does not put the ESC at the start: it arrives
// in the middle of the run that ReadString hands back, and the bytes were being kept as text.
// Measured on the target machine, the message that reached the model was
// `hello\x1b[C\x1b[D\x1b[A\x1b[B\x1b[3~\x1bOP\x15/quit` — every arrow the user pressed, spelled
// out as literal backslash-escapes.
//
// A sequence is removed whole: from the introducer to its final byte. Removing only the ESC
// would leave the parameter bytes behind as text — "[C" and "[3~" would appear in the message.
func stripKeySequences(s string) string {
	if !strings.ContainsRune(s, 0x1b) {
		return s // the common case: nothing to strip
	}
	var b strings.Builder
	b.Grow(len(s))
	runes := []rune(s)
	for i := 0; i < len(runes); {
		if runes[i] != 0x1b {
			b.WriteRune(runes[i])
			i++
			continue
		}
		i = sequenceEnd(runes, i)
	}
	return b.String()
}

// csiEnd returns the index after the final byte of the CSI sequence whose parameters start at j.
func csiEnd(runes []rune, j int) int {
	for j < len(runes) && !(runes[j] >= 0x40 && runes[j] <= 0x7e) {
		j++
	}
	if j < len(runes) {
		j++ // consume the final byte too
	}
	return j
}

// sequenceEnd returns the index after the key sequence whose ESC is at i. CSI (ESC [) runs to its
// final byte in 0x40..0x7e; SS3 (ESC O) is the three-byte form the function keys use; a bare ESC
// is just itself.
func sequenceEnd(runes []rune, i int) int {
	j := i + 1
	switch {
	case j < len(runes) && runes[j] == '[':
		j = csiEnd(runes, j+1)
	case j < len(runes) && runes[j] == 'O':
		j++
		if j < len(runes) {
			j++
		}
	default:
		j = i + 1 // a bare ESC: nothing follows it
	}
	return j
}

// readEscape completes a key that starts with ESC.
//
// A bare ESC is the cancel key. When more bytes are already buffered the
// terminal sent a sequence, and what follows the introducer says which key it
// was. The test for "more bytes are buffered" is what tells the two apart
// without waiting: a human pressing Esc sends one byte, while a terminal sends
// the whole sequence in a single write.
//
// An unrecognised sequence is reported as the cancel key rather than as text:
// the alternative is typing the raw bytes into the prompt, which would be worse
// than doing the safe thing.
func (t *TUI) readEscape() string {
	if t.input().Buffered() == 0 {
		return keyEsc
	}
	peek, err := t.input().Peek(1)
	if err != nil || len(peek) == 0 || peek[0] != '[' {
		return keyEsc
	}
	seq := []byte{0x1b, '['}
	t.input().Discard(1) // the introducer, now that it is known to be one
	for {
		c, err := t.input().ReadByte()
		if err != nil {
			return keyEsc
		}
		seq = append(seq, c)
		// A CSI sequence ends at its final byte, and the parameter bytes that
		// precede it are never in 0x40..0x7E.
		if c >= 0x40 && c <= 0x7e {
			break
		}
		// The length is bounded so a terminal that never sends a final byte cannot make this loop
		// read for ever. The bound has to clear the LONGEST sequence the interface asks for, and
		// that is a mouse report: "ESC [ < b ; x ; y M" is around fifteen bytes, and a drag or a
		// move is no shorter. At 16 the limit was reached mid-report, the loop gave up, and the
		// REST OF THE REPORT stayed in the buffer to be read as typed text — which is why moving
		// the trackpad typed escape-sequence gibberish into the input box.
		//
		// 64 leaves room for the widest plausible report (a large terminal, several digits per
		// coordinate, a modifier) without letting the loop run away.
		if len(seq) > 64 {
			return keyEsc
		}
	}
	return string(seq)
}

type lineResult struct {
	line string
	err  error
}

// readLineLive edits one line as it is typed, redrawing the composer and the completion
// popup on every keystroke.
//
// This is the difference between a command interface and a chat one: with the terminal in
// character mode the program owns the editing, so it knows what has been typed while it is
// being typed — which is the only way a completion popup can offer anything.
//
// The returned tokens are exactly the ones the whole-line reader produces, so the rest of the
// interface does not know which path delivered them.
func (t *TUI) readLineLive(ctx context.Context) (string, bool) {
	t.draft = ""
	// The popup highlight resets on the first keystroke of the new line, not here: a test
	// that pre-seeds the highlight so the reader's first move can accept it would see that
	// seed overwritten, and the test would fail for the wrong reason. The reset on every
	// keystroke covers it — the first character that opens the popup zeroes the highlight
	// along with the draft.
	//
	// The composer is repainted on the way out, UNCONDITIONALLY.
	//
	// It used to repaint only when text was left over, and that guard is why the input looked
	// like it never cleared: pressing Enter empties the draft inside the loop (so it is already
	// "" by the time this runs), the guard sees nothing to do, and the last frame on the screen
	// is still the one drawn while the text was being typed. The line was consumed and the
	// screen still showed it.
	//
	// Repainting always costs one frame per line read and removes the whole class: whatever
	// emptied the draft, the screen matches it before the next thing happens.
	defer func() {
		t.draft = ""
		t.drawFrame()
	}()

	for {
		b, ok := t.readByteOrCancel(ctx)
		if !ok {
			return "", false
		}

		// Each key is answered by its own function. The body of this loop used to be one
		// switch of nearly two hundred lines, which is where a reader loses the shape of the
		// interface: every rule below is a sentence in the interface's contract with the
		// terminal, and they read as one only when each is written on its own.
		// An escape sequence is never text: it is how the terminal reports a key, so it is
		// answered here and never reaches the branch below that appends a character.
		if b == 0x1b {
			line, dispatch := t.handleEscapeLive()
			if !dispatch {
				continue
			}
			return line, true
		}

		if line, dispatch, handled := t.handleLiveKey(b); handled {
			if !dispatch {
				continue
			}
			return line, true
		}
	}
}

// byteRead is what the one outstanding read produced.
type byteRead struct {
	b   byte
	err error
}

// readByteOrCancel returns the next byte, or reports that the input is over.
//
// The byte is read on its own goroutine so a cancelled context can abandon the WAIT: a blocking
// ReadByte on a terminal that is waiting for a keystroke never returns on its own, and without this
// the interface would ignore Ctrl+C until the user pressed something else. The READ is not
// abandoned: it stays outstanding, and the byte it eventually produces is the one the next call
// returns. Starting a fresh read on every call - which is what this used to do - left the old one
// running beside it, and the keystroke went to whichever won.
//
// While waiting, this is also where a turn in flight is driven: its progress, its requests for
// approval, its outcome and the spinner's tick are applied here, between keystrokes, on the
// goroutine that owns the view (see turns.go).
func (t *TUI) readByteOrCancel(ctx context.Context) (byte, bool) {
	if t.inbox == nil {
		t.inbox = make(chan byteRead, 1)
	}
	if !t.reading {
		t.reading = true
		go func() {
			b, err := t.input().ReadByte()
			t.inbox <- byteRead{b: b, err: err}
		}()
	}
	approvals := t.approvalChannel()
	for {
		var (
			progress <-chan string
			done     <-chan runOutcome
			tick     <-chan time.Time
		)
		if tr := t.current; tr != nil {
			progress, done, tick = tr.progress, tr.done, tr.tick.C
		}
		select {
		case r := <-t.inbox:
			t.reading = false
			if r.err != nil {
				return 0, false
			}
			return r.b, true
		case <-ctx.Done():
			return 0, false
		case p := <-progress:
			t.current.onProgress(p)
		case c := <-approvals:
			t.openConfirm(c)
		case out := <-done:
			t.endCurrent(ctx, out)
		case <-tick:
			t.advance()
		}
	}
}

// handleLiveKey answers the keys that are not an escape sequence. It reports whether the key
// was used, and whether the line is finished (in which case the caller returns it).
//
// Three outcomes, and the difference matters: `handled=false` means the byte is TEXT; a
// handled key with `dispatch=false` changed the screen and the loop continues; a handled key
// with `dispatch=true` produced the line and the reader is done.
func (t *TUI) handleLiveKey(b byte) (line string, dispatch, handled bool) {
	switch b {
	case '\r', '\n':
		return t.acceptLine(), true, true

	case 0x03: // Ctrl+C, which cbreak still delivers as a signal; this is the read path
		return "", false, true

	case 0x04, 0x06, 0x15: // Ctrl+D, Ctrl+F, Ctrl+U
		// These three are TOKENS the interface acts on: half a page down, search, clear.
		// They are returned, never typed — the draft is dropped so the line does not
		// survive a key that is not part of it.
		t.draft = ""
		t.drawFrame()
		return string(b), true, true

	case 0x7f, 0x08: // backspace
		t.backspace()
		return "", false, true

	case keyAgents: // Ctrl+G
		t.toggleAgents()
		return "", false, true

	case '\t':
		// Tab means ONE thing: switch between Task and Plan. It used to also accept the
		// completion when the popup was open, which meant the same key did two different
		// things depending on what had been typed — and a user reaching for the mode
		// switch in the middle of a line got a command inserted instead.
		//
		// Completion is still a keystroke away, on the right arrow: the gesture that means
		// "accept forward" everywhere else, and a key nothing here had claimed.
		t.draft = ""
		return "\t", true, true
	default:
		// Any other byte is text, or a control key refused below.
	}

	// A CONTROL byte is captured, never typed.
	//
	// The earlier guard only checked `b >= 0x20`, which is a test on a BYTE, not on a
	// character: 0x80 and above passed it and were appended one byte at a time, so a UTF-8
	// letter arrived as mojibake and any control byte the cases above did not name (Ctrl+A,
	// Ctrl+B, Ctrl+K, Ctrl+W, Ctrl+Z...) was injected into the line and drawn on screen. Both
	// are the same defect: the input accepted things that are not text.
	//
	// The rule is stated once: text starts at U+0020 and DEL is not text. Anything below is a
	// key, and the interface owns the keys.
	if b < 0x20 || b == 0x7f {
		return "", false, false
	}

	ch, ok := t.readRuneFrom(b)
	if !ok {
		// A broken multi-byte sequence is dropped, and the loop continues: the character was
		// never text, so there is nothing to add to the line.
		return "", false, true
	}
	t.appendDraft(ch)
	return "", false, true
}

// acceptLine sanitises the draft and finishes the line.
//
// When the popup is open, Enter accepts the highlighted candidate AND dispatches the filled
// line in one motion. Two presses for what is visually a single "pick and run" would be a
// guess the user has to make about which Enter does what — and a half-typed "/co" submitted
// because the popup was ignored is a worse failure than asking for Enter again, because it
// actually reached the model.
//
// The accept path fills the draft through the same routine as the right arrow, and delivers
// it through the same sanitiser as a plain submit: one source of truth for what the line
// becomes, regardless of how it was completed.
func (t *TUI) acceptLine() string {
	if t.completing() {
		t.completeDraft()
	}
	// Sanitised for the same reason as the whole-line reader: the reader never ADDS a control
	// character, but the line is the interface's contract with the model, and one place that
	// enforces it is better than two that must agree.
	line := sanitiseLine(stripKeySequences(t.draft))
	t.draft = ""
	return line
}

// backspace removes the last character, and resets the popup highlight.
//
// The reset is the same as for a typed character: the prefix that fed the popup has shrunk,
// the row the user highlighted no longer matches a candidate, and the next repaint opens the
// popup on the first row.
//
// The deletion is per RUNE, not per byte: a byte-wise version would cut a multi-byte character
// in half and leave invalid UTF-8 in the line.
func (t *TUI) backspace() {
	if t.draft == "" {
		return
	}
	r := []rune(t.draft)
	t.draft = string(r[:len(r)-1])
	t.completingIdx = 0
	t.drawFrame()
}

// appendDraft adds one typed character and resets the highlight.
//
// A new character is also a new prefix: the previous highlight no longer refers to a row that
// matches what is on screen, and any row the user picked a moment ago is now stale. Resetting
// to the first row is the natural choice — the popup reopens with the top candidate selected,
// the way every menu behaves when the filter changes.
//
// The reset runs on EVERY new keystroke, including the first one that opens the popup: the
// previous turn may have left the highlight on a row, and the new line is a fresh start.
// Skipping the first keystroke would let a stale highlight survive into the new popup —
// exactly the bug a test that pre-seeds the highlight expects to fix.
func (t *TUI) appendDraft(ch string) {
	t.completingIdx = 0
	t.draft += ch
	t.drawFrame()
}

// handleEscapeLive answers everything that arrives as an escape sequence. It always uses the
// sequence — an escape sequence is never text — and reports whether the line is finished.
//
// dispatch=false means the sequence changed the screen and the loop continues. dispatch=true
// returns it as the line, which is how the keys the SHORTCUT dispatcher owns (the page keys,
// Home, End) reach it: the draft is cleared first so the line does not survive a mode change.
func (t *TUI) handleEscapeLive() (seq string, dispatch bool) {
	s := t.readEscapeLive()
	if s == pasteStart {
		t.readPaste()
		return "", false
	}
	if s == keyEsc {
		// Escape closes the popup first, and only leaves the line when there is nothing to
		// close.
		if t.completing() {
			t.draft = ""
			t.completingIdx = 0
			t.drawFrame()
			return "", false
		}
		t.draft = ""
		return keyEsc, true
	}

	// The arrow keys navigate the popup when one is open: up and down move the highlight
	// between candidates, right accepts. The same arrows scroll the conversation when no popup
	// is open — that is the original shortcut's job and it stays where the popup does not claim
	// it. Picking here, BEFORE the scroll handler in handleShortcut, is what makes the popup and
	// the chat not fight for the same key.
	//
	// Wrapping on the candidate list is the same shape every menu uses, and it is what lets the
	// user land on a row without knowing how many candidates there are.
	if t.completing() {
		cands := completions(t.draft)
		switch {
		case s == keyUp && len(cands) > 0:
			t.completingIdx = (t.completingIdx - 1 + len(cands)) % len(cands)
			t.drawFrame()
			return "", false
		case s == keyDown && len(cands) > 0:
			t.completingIdx = (t.completingIdx + 1) % len(cands)
			t.drawFrame()
			return "", false
		default:
			// Not a key that moves the highlight.
		}
	}

	// The right arrow accepts the completion the popup is showing: the popup exists to save
	// typing, and with Tab spoken for this is the key that does it. It only consumes the key
	// when there was something to accept, so an arrow press with no popup open is still just an
	// arrow press.
	if s == keyRight && t.completeDraft() {
		return "", false
	}

	// A MOUSE REPORT is consumed here and never reaches the draft.
	//
	// The terminal is asked to report the mouse, so these sequences arrive while the user is
	// typing. They used to be returned to the caller like any other CSI key, and a report that
	// no handler acted on — a click, a drag, a move — fell through to the chat and was drawn in
	// the input box as escape-sequence text. Moving the trackpad typed into the input.
	//
	// The wheel is the one gesture the interface acts on, and it is handled by the shortcut
	// dispatcher through the returned token; every OTHER report is swallowed here, because it
	// is not something the user typed and there is nothing to do with it.
	if isMouseReport(s) {
		if lines, ok := mouseScroll(s); ok {
			t.scrollBy(lines)
		}
		t.drawFrame()
		return "", false
	}

	// The other arrows and the page keys are handled by the shortcut dispatcher; the draft is
	// cleared so the line does not survive the mode change.
	t.draft = ""
	return s, true
}

// Bracketed paste: the terminal wraps a paste in pasteStart and pasteEnd, so the live reader can
// tell pasted text from typed keys. Without it, every pasted newline ended the line, and a
// multi-line paste was sent as several messages, of which the queue kept only the last.
const (
	pasteOn    = "\x1b[?2004h"
	pasteOff   = "\x1b[?2004l"
	pasteStart = "\x1b[200~"
	pasteEnd   = "\x1b[201~"
)

// setPaste asks the terminal to mark pastes, or to stop. Only the live reader understands the
// marks, so the caller turns it on only while the terminal is in character mode.
func (t *TUI) setPaste(on bool) {
	if t.NoColor || !isTerminalOut(t.Out) {
		return
	}
	s := pasteOff
	if on {
		s = pasteOn
	}
	_, _ = t.Out.Write([]byte(s))
}

// readPaste adds a bracketed paste to the draft as text. Its newlines become spaces, so the paste
// is one message that Enter sends, and no pasted line can run as a command on its own.
func (t *TUI) readPaste() {
	var buf []byte
	for !bytes.HasSuffix(buf, []byte(pasteEnd)) {
		c, err := t.input().ReadByte()
		if err != nil {
			break
		}
		buf = append(buf, c)
	}
	text := strings.TrimRight(strings.TrimSuffix(string(buf), pasteEnd), "\r\n")
	text = strings.NewReplacer("\r\n", " ", "\r", " ", "\n", " ", "\t", " ").Replace(text)
	t.appendDraft(sanitiseLine(text))
}

// readRuneFrom assembles one character from the first byte plus, when it is a multi-byte
// sequence, the continuation bytes that follow.
//
// A byte-at-a-time reader is the right shape for a terminal — that is what makes every keystroke
// visible as it is typed — but it means UTF-8 has to be reassembled here. Without this an
// accented letter or an emoji arrives as several separate bytes and is drawn as mojibake.
//
// The continuation bytes are read without a deadline: the terminal wrote them together with the
// leading byte, so they are already in the buffer. A truncated sequence (a paste cut short, a
// terminal that died mid-character) reports failure and the caller drops it.
func (t *TUI) readRuneFrom(first byte) (string, bool) {
	if first < 0x80 {
		return string(rune(first)), true
	}
	// RFC 3629: the number of continuation bytes is implied by the leading byte.
	var need int
	switch {
	case first&0xe0 == 0xc0:
		need = 1
	case first&0xf0 == 0xe0:
		need = 2
	case first&0xf8 == 0xf0:
		need = 3
	default:
		return "", false // a stray continuation byte: not a character
	}
	buf := make([]byte, 1, need+1)
	buf[0] = first
	for i := 0; i < need; i++ {
		c, err := t.input().ReadByte()
		if err != nil {
			return "", false
		}
		if c&0xc0 != 0x80 {
			// Not a continuation byte: the sequence is broken. The byte is NOT part of the
			// broken character — it is the start of whatever the user typed next, so it is
			// pushed back and will be read as its own character. Consuming it silently is how
			// a broken sequence used to swallow the letter that followed it.
			t.input().UnreadByte()
			return "", false
		}
		buf = append(buf, c)
	}
	if !utf8.Valid(buf) {
		return "", false
	}
	return string(buf), true
}

// readEscapeLive completes a sequence that followed an ESC, without blocking on a bare Esc.
//
// Same rule as the whole-line reader: a human pressing Escape sends one byte, a terminal
// sends the whole CSI sequence in one write, and the bytes already buffered are what tells
// them apart.
func (t *TUI) readEscapeLive() string {
	// The very same reading as readEscape, with the same bound on the length: a mouse report is
	// the longest sequence the interface asks for, and cutting it short leaves its tail in the
	// buffer to be read as typed text.
	return t.readEscape()
}

// completeDraft replaces what has been typed with the candidate the popup is highlighting.
// The right arrow and Enter both call this when a popup is open: the first one is the
// "accept forward" gesture that exists in every editor, the second is the universal submit,
// and reusing the same routine keeps the two paths from drifting.
//
// It reports whether it did anything: with no candidates, both gestures keep their old
// meaning (right arrow is just an arrow, Enter ends the line). The user who reaches for the
// arrows is the same one who would notice if a candidate did NOT show up — that is why the
// "did anything" report exists rather than silently doing nothing.
func (t *TUI) completeDraft() bool {
	cands := completions(t.draft)
	if len(cands) == 0 {
		return false
	}
	idx := t.completingIdx
	if idx < 0 || idx >= len(cands) {
		// The popup was reset to row 0 by a draft change, but a stray key from before
		// the reset could still leave the index out of range. Falling back to the first
		// row is what the gesture used to do unconditionally, and is the safe pick when
		// the bookkeeping is stale.
		idx = 0
	}
	t.draft = cands[idx].Name + " "
	t.completingIdx = 0
	t.drawFrame()
	return true
}
