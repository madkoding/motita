package tui

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

// The palette is the standard 16-colour one, so any terminal can render it.
// Colour carries information here: the brand mark, the active mode, the state of
// a run and who is speaking. Everything else is base or muted, which is what
// keeps the hierarchy readable instead of decorative.
const (
	colBase    = 7
	colMuted   = 8
	colAccent  = 6
	colSuccess = 2
	colWarning = 3
	colError   = 1
	colBrand   = 5
)

// The glyphs are chosen from the CP437 repertoire on purpose: the same interface
// has to look right on a physical Linux console with a VGA font (which covers
// box drawing, block elements and Latin-1, and nothing else) and on a modern
// terminal emulator. Anything outside that set — rounded corners, braille
// spinners, geometric shapes — is avoided, so nothing degrades into a blank box.
const (
	glyphRule     = "\u2500" // ─ horizontal rule
	glyphRail     = "\u2502" // │ vertical rail and panel side
	glyphTopLeft  = "\u250c" // ┌
	glyphTopRight = "\u2510" // ┐
	glyphBotLeft  = "\u2514" // └
	glyphBotRight = "\u2518" // ┘
	glyphDot      = "\u2022" // • separator and scrolled marker
	// glyphReady and glyphMissing are the readiness indicator. They differ in
	// SHAPE, not only in colour: a status that is only a colour is invisible with
	// colour off and ambiguous to a colour-blind reader.
	//
	// They are CP437 characters — • (0x07) and ° (0xF8) — so a physical console
	// with a VGA font renders them; anything outside that repertoire would come
	// out as a blank box on the target machine.
	glyphReady   = "\u2022" // • ready
	glyphMissing = "\u00b0" // ° nothing to talk to
	glyphUser    = "\u00bb" // » the user's turn
	glyphAgent   = "*"      // the agent's turn, and the star of the wordmark
	glyphMid     = "\u00b7" // · separator inside a line
	glyphPrompt  = "\u203a" // › the input prompt
)

// exitClear is what leaving the interface writes: show the cursor, wipe the screen and the
// scrollback, and put the cursor at the origin.
//
// It is a named constant so the tests can strip it by name: the wipe is written AFTER the last
// frame, so a helper that reads "the last thing drawn" has to remove it first or it finds a blank
// screen instead of the interface.
const exitClear = "\x1b[?25h\x1b[2J\x1b[3J\x1b[H"

// Layout metrics. They are named because the frame arithmetic depends on them: a
// single off-by-one puts the right border of the panel out of alignment.
const (
	defaultWidth = 80
	minWidth     = 44
	// There is deliberately NO maximum width.
	//
	// There used to be one (116), and it capped the interface in the middle of a wide window:
	// the rules, the status bar and the conversation all stopped at 116 columns and left the
	// rest of the screen blank, which reads as the frame failing to fill the terminal. The cap
	// was meant to keep lines readable, but line length is the user's choice — they sized the
	// window — and an interface that refuses to use the space it was given is worse than long
	// lines. The only column ever left unused is the last one, and that is to avoid a wrap.
	maxScrollback = 400
	// leftMargin is the breathing room between the interface and the terminal
	// edge. Nothing is ever drawn in the first column.
	leftMargin = 2
	// minChatLines is the smallest conversation area the layout keeps before it
	// starts dropping the oldest lines.
	minChatLines = 4
	// inputRows is the height of the input field. It is fixed so the frame never changes shape
	// while the user types.
	inputRows = 3
	// permanentRows is how many rows the frame always draws, whatever is on screen. Enumerated
	// because the frame arithmetic depends on the count being exact:
	//
	//	1  the status line (provider/model, reasoning)
	//	1  the rule above the conversation
	//	1  the empty row that separates the conversation from the input box
	//	3  the input field
	//	1  the rule under the input
	//	1  the status bar (mode, context, keys)
	//
	// Eight. The rule above the input box was removed so the conversation has a breathing row
	// before the input; an extra row would push the cursor off the field at the bottom of a
	// small terminal, so the empty row replaces the divider and the count stays where the rest
	// of the layout expects.
	permanentRows = 8
	)

// bannerLines is the Starlight wordmark: five shaded rows that carry their own
// ANSI colours, so they are stored raw and only placed by the layout. In
// no-colour mode the escape sequences are stripped before printing.
var bannerLines = []string{
	"\x1b[0;97m\u2580\u2580\u2580\u2580\x1b[0;37m\u2580\u2588\u2588\u2588 \u2580\u2580\u2588\u2588\u2588\u2580\u2580 \x1b[0;97m\u2584\x1b[0;97;47m\u2593\u2592\x1b[0;37m\u2580\u2580\u2588\u2588\u2584 \x1b[0;97m\u2580\u2580\u2580\x1b[0;37m\u2580\u2580\u2588\u2588\u2584 \x1b[0;97m\u2580\u2580\u2580\x1b[0;37m      \x1b[0;97m\u2588\x1b[0;97;47m\u2593\u2592\x1b[0;37m \x1b[0;97m\u2580\u2580\u2580\x1b[0;37m\u2580\u2580\u2588\x1b[0;90;47m\u2591\u2592\x1b[0;37m \x1b[0;97m\u2588\x1b[0;97;47m\u2593\u2592\x1b[0;37m  \u2588\u2588\u2588 \u2580\u2580\u2588\u2588\u2588\u2580\u2580\x1b[0m",
	"\x1b[0;97;47m\u2593\u2592\u2591\x1b[0;37m  \u2580\u2580\u2580 \x1b[0;90m\u2593\x1b[0;37m \u2588\u2588\u2588 \x1b[0;90m\u2593\x1b[0;37m \x1b[0;97m\u2580\u2580\u2580\x1b[0;37m  \u2588\u2588\u2588 \x1b[0;97;47m\u2593\u2592\u2591\x1b[0;37m  \u2588\u2588\u2588 \x1b[0;97;47m\u2593\u2592\u2591\x1b[0;90m\u2590\u2588\u2588\u2588\u2588\x1b[0;37m \x1b[0;97m\u2580\u2580\u2580\x1b[0;37m \x1b[0;97;47m\u2593\u2592\u2591\x1b[0;90m\u2590\u258c\x1b[0;37m\u2580\u2580\u2580 \x1b[0;97m\u2580\u2580\u2580\x1b[0;37m  \u2588\u2588\u2588 \x1b[0;90m\u2593\x1b[0;37m \u2588\u2588\u2588 \x1b[0;90m\u2593\x1b[0m",
	"\x1b[0;37m\u2580\u2580\u2580\u2580\u2588\u2588\u2584 \x1b[0;90m\u2588\x1b[0;37m \u2588\u2588\x1b[0;93;47m\u2591\x1b[0;37m \x1b[0;90m\u2588\x1b[0;37m \x1b[0;97;47m\u2592\u2591 \x1b[0;37m\u2580\u2580\u2588\u2588\u2588 \x1b[0;97;47m\u2592\u2591\x1b[0;37m\u2588\u2584\u2580\u2580\u2580  \x1b[0;97;47m\u2592\u2591\x1b[0;37m\u2588\x1b[0;90m\u2580\u2580\u2580\u2580\x1b[0;37m  \x1b[0;97;47m\u2592\u2591 \x1b[0;37m \x1b[0;97;47m\u2592\u2591\x1b[0;37m\u2588 \u2584\u2584\u2584\u2584 \x1b[0;97;47m\u2592\u2591 \x1b[0;37m\u2580\u2580\u2588\u2588\u2588 \x1b[0;90m\u2588\x1b[0;37m \u2588\u2588\x1b[0;93;47m\u2591\x1b[0;37m \x1b[0;90m\u2588\x1b[0m",
	"\x1b[0;97;47m\u2592\u2591\x1b[0;37m\u2588\x1b[0;90m\u2590\u258c\x1b[0;37m\u2588\u2588\x1b[0;93;47m\u2591\x1b[0;37m \x1b[0;90m\u2588\x1b[0;37m \u2588\x1b[0;93;47m\u2591\u2592\x1b[0;37m \x1b[0;90m\u2593\x1b[0;37m \x1b[0;97;47m\u2591 \x1b[0;37m\u2588\x1b[0;90m\u2590\u258c\x1b[0;37m\u2588\u2588\x1b[0;93;47m\u2591\x1b[0;37m \x1b[0;97;47m\u2591\x1b[0;37m\u2588\x1b[0;93;47m\u2591\x1b[0;37m  \u2588\u2588\u2584 \x1b[0;97;47m\u2591\x1b[0;37m\u2588\u2588\x1b[0;90m\u2590\u258c\x1b[0;97;47m\u2592\u2591 \x1b[0;37m \x1b[0;97;47m\u2591 \x1b[0;37m\u2588 \x1b[0;97;47m\u2591\x1b[0;37m\u2588\u2588  \x1b[0;97;47m\u2592\u2591 \x1b[0;37m \x1b[0;97;47m\u2591 \x1b[0;37m\u2588\x1b[0;90m\u2590\u258c\x1b[0;37m\u2588\u2588\x1b[0;93;47m\u2591\x1b[0;37m \x1b[0;90m\u2588\x1b[0;37m \u2588\x1b[0;93;47m\u2591\u2592\x1b[0;37m \x1b[0;90m\u2593\x1b[0m",
	"\x1b[0;97;47m\u2591\x1b[0;37m\u2588\u2588\u2584\u2584\u2588\x1b[0;93;47m\u2591\x1b[0;92m\u2580\x1b[0;37m \x1b[0;90m\u2593\x1b[0;37m \x1b[0;93;47m\u2591\u2592\u2593\x1b[0;37m \x1b[0;90m\u2592\x1b[0;37m \u2588\u2588\u2588  \u2588\x1b[0;93;47m\u2591\u2592\x1b[0;37m \u2588\x1b[0;93;47m\u2591\u2592\x1b[0;37m  \u2588\x1b[0;93;47m\u2591\u2592\x1b[0;37m \u2580\u2588\u2588\u2584\u2584\u2588\u2588\u2588 \u2588\u2588\u2588 \u2580\u2588\u2588\u2584\u2584\u2588\u2588\u2588 \u2588\u2588\u2588  \u2588\x1b[0;93;47m\u2591\u2592\x1b[0;37m \x1b[0;90m\u2593\x1b[0;37m \x1b[0;93;47m\u2591\u2592\u2593\x1b[0;37m \x1b[0;90m\u2592\x1b[0m",
}

// compactMark is the wordmark used when the terminal is too narrow for the
// five-row banner: the identity survives without wrapping the layout.
const compactMark = "* S T A R L I G H T"

// spinner is advanced on every repaint of a running turn. It is plain ASCII so
// it animates on every terminal, including a text console with a VGA font.
var spinner = [...]string{"|", "/", "-", "\\"}

// blank is the separator row used between sections.
const blank = ""

// layout builds the frame and, when the terminal is not tall enough, drops
// content in a defined order of importance instead of letting the top scroll off
// the screen.
//
// The order is the priority list, most expendable first:
//
//  1. the key hints (the mode line already teaches the interface),
//  2. the wordmark,
//  3. the oldest rows of the conversation.
//
// The compact one-line mark is not part of this list: it exists for a terminal
// that is too *narrow* for the five-row banner, not as a height fallback. On a
// short terminal three rows of conversation are worth more than three rows of
// branding, so the wordmark goes in one step.
//
// The prompt, the mode line and at least a few rows of conversation are never
// dropped: they are what the user came for. A layout that scrolls would push the
// prompt off the bottom, and that is the one row that must always be visible.
// layout is the whole interface, top to bottom, fitted to the terminal.
//
// The order is the one the user asked for:
//
//	wordmark                        (dropped first: it is branding)
//	provider/model  reasoning       (no key, no readiness)
//	────────────────────────────────
//	conversation (thinking, tools, answers)
//	────────────────────────────────
//	composer  (with the completion popup above it)
//	────────────────────────────────
//	mode · context used        keys
//
// The conversation is NOT boxed. A border around the only content there is adds a row of noise
// at each end and pushes the composer away from the bottom of the screen; the rules carry the
// structure instead, which is what separating with space rather than with a box means.
//
// FITTING. Every part is counted and the total is exactly h whenever h can hold the permanent
// rows, because a frame taller than the terminal scrolls — and a frame that scrolls moves the
// whole interface up on every repaint, which the user sees as the screen jumping when they
// press a key.
//
// The order of sacrifice is the wordmark, then the popup, then the conversation down to its
// floor. The composer, the rules and the status bar are never dropped: they are what the
// interface is. A height of zero means the terminal did not report one, and nothing can be
// trimmed — everything is drawn and the shell scrolls, as it must.
func (t *TUI) layout(w, h int) ([]string, string) {
	header := t.headerLines(w)
	status := t.statusLines(w)
	body := t.chatLines(t.conversationWidth())
	bar := t.bottomBar(w)
	popup := t.popupRows()

	// The composer is always two rows above the end of the frame — the rule and the status bar —
	// so the cursor is walked back up to it from wherever the frame leaves it.
	const belowComposer = rowsBelowComposer

	// A terminal too small to hold the interface gets an explanation instead of a broken frame.
	if h > 0 && h < minHeight {
		return t.tooSmallLines(w, h), ""
	}

	if h <= 0 {
		lines := append([]string{}, header...)
		if len(header) > 0 {
			lines = append(lines, blank)
		}
		lines = append(lines, status...)
		lines = append(lines, t.rule(w))
		lines = append(lines, body...)
		lines = append(lines, blank)
		lines = append(lines, t.composerLinesCapped(0)...)
		lines = append(lines, t.rule(w))
		lines = append(lines, bar)
		return lines, t.composerPrompt(belowComposer + inputRows - 1)
	}

	// 1. The wordmark is branding: it goes before anything the user came for.
	headerHeight := 0
	if len(header) > 0 {
		headerHeight = len(header) + 1 // plus the blank row beneath it
	}
	if permanentRows+headerHeight+popup+minChatLines > h {
		header = nil
		headerHeight = 0
	}

	// 2. The popup takes what is left after the permanent rows, the wordmark and a conversation
	// floor. It GROWS as the user types, so it is the part that can push the frame past the
	// bottom of the window if it is not capped here. It always keeps at least one row: a popup
	// that shows the command it is offering is worth a row, and the rest is a keystroke away.
	//
	// The cap is applied to the popup's OWN row count, which is what it will actually draw —
	// completionLinesCapped is given this number and returns exactly that many rows, trailing
	// hint included. Capping against a reservation instead of against the drawn rows is how the
	// frame came out one row too tall.
	if popup > 0 {
		if spare := h - permanentRows - headerHeight - minChatLines; popup > spare {
			popup = spare
			if popup < 1 {
				popup = 1
			}
		}
	}

	// 3. The conversation takes the remainder, and the total is exactly h — as long as the
	// terminal can hold the permanent rows and the popup it asked for.
	//
	// `room` is a RESIDUE, computed once and never re-decided: every earlier attempt that
	// clamped it separately (to a conversation floor) is what made the parts sum to more than
	// the window. When the terminal is genuinely too small the sum is allowed to exceed it, and
	// the size gate reports that case before this point is ever reached.
	// The residue is always positive here: the gate above refused anything shorter than
	// minHeight, and minHeight already includes the permanent rows and a conversation floor, so
	// subtracting the popup can only bring it down to that floor. A guard would be unreachable.
	room := h - permanentRows - headerHeight - popup

	// 4. Anchor the window to the newest line unless the user lifted it. The offset is applied
	// before trimming, so paging walks one row at a time instead of jumping by whatever the
	// current window happens to hold.
	if t.scroll > 0 {
		end := len(body) - t.scroll
		if end < 0 {
			end = 0
		}
		body = body[:end]
	}

	// 5. Trim to the room, then PAD back up to it. The padding is what puts the composer on the
	// last rows of the window instead of letting it float in the middle of a short conversation:
	// the input belongs at the foot of the screen.
	if len(body) > room {
		hidden := len(body) - room + 1
		body = append([]string{t.plainLine(t.muted(fmt.Sprintf("... %d earlier lines", hidden)))},
			body[len(body)-room+1:]...)
	}
	for len(body) < room {
		body = append(body, "")
	}

	lines := make([]string, 0, permanentRows+headerHeight+popup+len(body))
	lines = append(lines, header...)
	if len(header) > 0 {
		lines = append(lines, blank)
	}
	lines = append(lines, status...)
	lines = append(lines, t.rule(w))
	lines = append(lines, body...)
	// The rule above the conversation is now an empty row: the divider reads as the bottom of
	// the chat and lifts the whole input block visually against the cursor at its top, so the
	// row before the input box is left blank and the rule under the input remains as the only
	// horizontal line that frames the composer.
	lines = append(lines, blank)
	lines = append(lines, t.composerLinesCapped(popup)...)
	lines = append(lines, t.rule(w))
	lines = append(lines, bar)

	// The cursor is walked back up from the end of the frame to the row of the input field
	// that holds the draft. composerPrompt refines the row inside the box from the wrap, so the
	// caller only has to count the rows below the composer: the rule beneath it and the status
	// bar. The cursor targets the FIELD row of the input box — the first row of the box that
	// actually has the prompt character. With inputRows=3, the count from the bottom of the
	// frame is belowComposer + popup + inputRows - 1: rows under the box (rule + bar) plus the
	// two rows inside the box above the field (the divider and the blank). That keeps the
	// cursor on the field row whatever the wrap does to the prompt inside the box.
	rowsBelow := belowComposer + popup + inputRows - 1
	return lines, t.composerPrompt(rowsBelow)
}

// rowsBelowComposer is how many rows sit under the input box: the rule beneath it and the status
// bar. It is a package constant rather than a local of layout because the CRT effect needs the
// same number to know where the interface stops and the conversation begins, and two definitions
// of "how tall is the composer" would drift apart.
const rowsBelowComposer = 2

// crtText is the text to draw for a message: the phosphor colour, and the typewriter reveal while
// a reply is still being written.
//
// Only the block still being written is revealed a character at a time. Text that has already
// settled is drawn whole, always: revealing it again on every repaint would make a finished
// answer flicker back and forth — and the reveal exists to make incoming text feel printed, not
// to make old text unreadable.
//
// A message that is NOT pending has nothing left to reveal, so the reveal is ended here. That is
// what keeps it from being left running against a target that will never grow again.
func (t *TUI) crtText(m Message) string {
	if t.crt == nil {
		return m.Text
	}
	text := m.Text
	if t.crt.cfg.Typewriter {
		switch {
		case !m.Pending:
			if t.crt.typingInProgress() {
				t.crt.stopTyping()
			}
		default:
			// The reveal is retargeted to the growing text and advanced by the time since the
			// last frame. Reading the clock here rather than holding a ticker keeps the reveal
			// tied to the repaints the interface already does: text arriving IS a repaint, so
			// nothing extra is scheduled.
			t.crt.startTyping(text)
			text = t.crt.reveal(t.crt.tick())
		}
	}
	return t.crt.paint(text)
}

// drawFrame paints the whole interface.
//
// The order follows the reading order of the screen: the wordmark, the status
// line, then the conversation — the primary content, framed so it reads as a
// panel —, then the modes, the key hints and finally the input prompt.
//
// The prompt is the last thing written and carries no trailing newline, so the
// terminal leaves its cursor exactly where the user is about to type. The frame
// is measured against the terminal height before it is written, so it never
// scrolls and the prompt can never be pushed off the bottom.
// The painter is serialised: a frame is drawn from the input loop and from the resize
// watcher, and both read the state the other mutates. Holding the lock for the whole
// paint is what makes the two safe — measuring outside it would still race on Width,
// scroll and messages.
func (t *TUI) drawFrame() {
	t.draw.Lock()
	defer t.draw.Unlock()

	// The first frame of a run wipes the screen and the scrollback first. Launching the
	// program should give a clean interface rather than one appended under whatever the
	// shell was showing, and the erase must happen ONCE: doing it every frame is the blank
	// flash that home-and-paint exists to avoid.
	if !t.painted {
		t.painted = true
		// 2J clears the visible screen, 3J drops the scrollback, and home puts the cursor
		// at the origin. Some terminals do not implement 3J; they ignore it, which is fine.
		fmt.Fprint(t.Out, "\x1b[2J\x1b[3J\x1b[H")
		// The wipe invalidates anything remembered: the terminal is blank now, and a diff
		// against the previous frame would believe the old rows are still there.
		t.invalidateScreen()
	}

	w, h := t.size()
	lines, prompt := t.layout(w, h)

	var b strings.Builder
	// No home and no hide-first: every written row carries its own absolute position, and the
	// cursor is placed once at the end. Hiding it while painting would only matter if it could
	// be seen mid-frame, which absolute addressing already prevents.
	//
	// Only "\n" is never written between rows any more: a newline moves the cursor DOWN a row,
	// which is exactly the scroll that absolute addressing removes.
	// The frame is written with a newline BETWEEN its rows and never after the last one.
	//
	// A trailing newline moves the cursor down a row, and when the frame already fills the
	// window that row does not exist: the terminal scrolls, and every repaint pushes the whole
	// interface one line up. Writing the last row without its break keeps the cursor on the
	// bottom row, where the frame was measured to end.
	//
	// This only bites once the frame FILLS the height. When the frame was short the extra
	// newline landed in the empty space below it and went unnoticed — which is why it survived
	// until the body was padded to the bottom of the window.
	//
	// Only "\n" is written: the interface never switches the terminal to full raw mode, so the
	// driver's ONLCR translation is still on and "\n" already becomes CR+LF. Writing "\r\n"
	// would double the carriage return on a real terminal.
	// Every row is erased to the end of its line as it is written. Without this a SHORTER row
	// does not replace a longer one: the terminal only overwrites the columns it is given, so
	// the tail of the previous row survives.
	//
	// This is what made the input look like it never cleared. The draft WAS emptied in memory
	// and the frame WAS redrawn — but "› hazlo" became "› ", which writes five cells and leaves
	// the previous "hazlo" sitting there. Reading the frame's own text shows a clean row, which
	// is exactly why it went unnoticed: the content was right and the screen was not.
	//
	// The same fault showed up across the whole frame: a long conversation line shortened by
	// the next frame left its tail glued to the row that replaced it.
	//
	// Erasing to end of LINE is right where clearing BELOW (CSI J, after the loop) is not
	// enough on its own: J starts at the cursor, and by then the cursor has already passed the
	// stale cells.
	// Only the rows that CHANGED are written, and the cursor is moved to each one.
	//
	// Repainting the whole frame on every keystroke is what made typing feel slow: measured at
	// 110x30, one frame is 5,068 bytes and 99% of it is text that did not change — the
	// conversation, the banner, the rules — because a single character was typed into the box.
	// Writing ~5 KB per key is what the user feels, and on the target netbook (Atom N270) the
	// terminal's own parsing of that stream is the cost, not the formatting here.
	//
	// The frame is still built in full, because deciding what changed needs the whole thing;
	// what is skipped is the WRITE. That keeps layout, measurement and cursor placement in one
	// place, instead of a second incremental path that would have to agree with this one.
	//
	// Absolute cursor addressing (CSI row;col H) is used for each row rather than relative
	// moves, so a skipped row cannot accumulate an offset error: every write says where it is
	// going, and the sequence does not depend on how many writes preceded it.
	//
	// The first frame, and any frame after a resize, is written in full: nothing is known about
	// what the terminal holds, and a stale row left behind is exactly the class of bug the row
	// erases were added for.
	full := !t.paintedScreen || len(t.lastFrame) != len(lines)

	// The last row actually written, which is NOT the last row of the frame.
	//
	// This is what the cursor move has to start from. The prompt returned by layout() walks UP
	// from the end of the frame, which is only correct when the whole frame was written; an
	// incremental repaint stops at the last row that changed, and every walk-up computed from the
	// bottom of the frame then lands too high. The cursor was left floating outside the input
	// box for exactly that reason — the user sees it somewhere in the conversation while typing.
	//
	// A frame with nothing to write at all keeps the cursor where it is, and the walk-up is from
	// that same place, so `lastWritten` starts at -1 meaning "no row was written".
	lastWritten := -1
	for i, l := range lines {
		if !full && t.lastFrame[i] == l {
			continue
		}
		fmt.Fprintf(&b, "\x1b[%d;1H", i+1)
		b.WriteString(l)
		b.WriteString("\x1b[K")
		lastWritten = i
	}

	// The cursor is placed on its own AFTER the rows, and always: it may have to move even when
	// no row changed — the user typed nothing visible (a control key, a mode switch) or the
	// previous frame ended with the cursor elsewhere.
	//
	// The move is re-derived from `lastWritten`, so it is correct whichever rows were skipped.
	if prompt != "" {
		b.WriteString(t.cursorMove(prompt, lastWritten, len(lines)))
	}
	// The cursor is handed back explicitly, on every frame: nothing hides it any more, but the
	// terminal may have been left hidden by an earlier frame of this or another program, and
	// showing it is the only way to be certain where the user is typing.
	b.WriteString("\x1b[?25h")
	fmt.Fprint(t.Out, b.String())
	if len(t.lastFrame) != len(lines) {
		t.lastFrame = make([]string, len(lines))
	}
	copy(t.lastFrame, lines)
	t.paintedScreen = true
}

// invalidateScreen forgets what was last written, so the next frame is written in full.
//
// It is called when the geometry changes and when the screen is wiped underneath the
// painter: in both cases the terminal no longer holds what lastFrame claims it holds, and a
// diff computed against a stale copy would leave rows the user can see but the program
// thinks are gone.
func (t *TUI) invalidateScreen() {
	t.lastFrame = nil
	t.paintedScreen = false
}

// size returns the drawing area. An explicit Width/Height always wins (tests and
// embedders set them); otherwise the conventional COLUMNS/LINES variables are
// read — an interactive shell exports both and refreshes them on resize — and
// the defaults close the list.
//
// The terminal itself is not interrogated: that needs an ioctl through unsafe,
// which the standard library cannot express portably across linux, windows and
// darwin, and the two variables are what the shell already knows.
//
// A height of zero means "unknown": the conversation is then never trimmed,
// because nothing can be said about what fits on screen.
func (t *TUI) size() (int, int) {
	w, h := t.Width, t.Height

	// The question is asked in order of trustworthiness: an explicit override (tests
	// and embedders), then the terminal driver, then the environment.
	//
	// The driver comes before the environment because COLUMNS/LINES are copied at
	// exec and never updated, so they describe the size the program STARTED at. The
	// terminal knows its current size. Measured on the target machine: the parent
	// moved to 60 columns and its child still read 100 from the environment, while
	// `stty size` under the same conditions reported the truth.
	if w <= 0 || h <= 0 {
		if tw, th, ok := ttySize(); ok {
			if w <= 0 {
				w = tw
			}
			if h <= 0 {
				h = th
			}
		}
	}

	if w <= 0 {
		if w = envInt("COLUMNS"); w == 0 {
			w = defaultWidth
		}
	}
	if h <= 0 {
		h = envInt("LINES")
	}
	if w < minWidth {
		w = minWidth
	}
	return w, h
}

func envInt(name string) int {
	v, err := strconv.Atoi(strings.TrimSpace(os.Getenv(name)))
	if err != nil || v <= 0 {
		return 0
	}
	return v
}

// inner is the number of columns strictly between the two border glyphs of the
// conversation panel. Everything drawn inside it — the title rule, the message
// cells — is padded to exactly this width, which is what keeps the right border
// straight.
//
// The arithmetic works out as: left margin (2) + border (1) + padding (1) +
// inner + padding (1) + border (1), so inner is the frame width minus six. No
// clamp is needed: size() already floors the width at minWidth, which leaves
// minWidth-1-6 = 37 columns here. A clamp would be unreachable code pretending to
// be a safety net.
func (t *TUI) inner() int {
	return t.frameCols() - 6
}

// conversationWidth is what the conversation rows are padded to: the drawing area, minus the
// margins on both sides, minus the one column kept free so a terminal never wraps the last one.
//
// inner() above is the width the FRAMED layout used, and it is four columns narrower than
// this. The conversation is no longer boxed, so using it here left four blank columns at the
// right of every row and clipped long lines earlier than necessary.
func (t *TUI) conversationWidth() int {
	// No floor is needed, and writing one would be a lie: frameCols comes from size(), which
	// clamps the width to minWidth, and minWidth (44) is greater than twice leftMargin (4). The
	// subtraction is therefore always positive. A guard here would look like a safety net while
	// being unreachable — the kind of line a reader trusts and no test can ever fail.
	return t.frameCols() - 2*leftMargin
}

// frameCols is the total number of columns the interface may occupy.
//
// Exactly ONE column is given up, and only so that no row fills the terminal's last column: a
// row that does makes some terminals wrap, and a wrapped row scrolls the frame. Nothing else is
// subtracted here — the left margin is spent by whoever draws, not reserved twice.
func (t *TUI) frameCols() int {
	w, _ := t.size()
	if w > 1 {
		w-- // keep the last column free of ink
	}
	return w
}

// headerLines is the wordmark. It is never framed: the brand sits on its own,
// centred, so the eye lands on it first and the panel below reads as a separate
// surface rather than as part of the same box.
func (t *TUI) headerLines(w int) []string {
	if w-2*leftMargin < visibleLen(bannerLines[0]) {
		return []string{"", t.padCenter(t.brand(compactMark), w), ""}
	}
	lines := make([]string, 0, len(bannerLines)+2)
	lines = append(lines, "")
	for _, row := range bannerLines {
		if t.NoColor {
			row = stripANSI(row)
		}
		lines = append(lines, t.padCenter(row, w))
	}
	return append(lines, "")
}

// fits reports whether a decorated string is narrow enough for the given number
// of columns.
func (t *TUI) fits(s string, cols int) bool { return visibleLen(s) <= cols }

// stateGlyph is the readiness indicator.
//
// Each state gets its own SHAPE as well as its own colour, so the status is
// readable with colour switched off, on a monochrome terminal, and by somebody who
// cannot distinguish red from green. Colour alone is the accessibility failure the
// design guide names explicitly.
//
// The glyphs stay inside the CP437 repertoire for the same reason as the rest of
// the interface: a physical console with a VGA font has to render them.
func (t *TUI) stateGlyph() string {
	if t.busy {
		return t.color(colWarning, 0, spinner[t.spin%len(spinner)])
	}
	if t.Runner.Config().LLM.APIKey == "" {
		return t.color(colError, 0, glyphMissing)
	}
	return t.color(colSuccess, 0, glyphReady)
}

// scrollPercent is how far back the view sits, as a percentage of how far it CAN
// go: 0% is the newest line, 100% the oldest reachable one.
//
// The denominator is maxScroll and not the length of the conversation, because
// the last screenful of lines is not a position the view can occupy — the window
// always shows that many rows. Dividing by the total would make the "top" read as
// some arbitrary mid-percentage.
func (t *TUI) scrollPercent() int {
	max := t.maxScroll()
	if max <= 0 {
		return 100
	}
	pct := t.scroll * 100 / max
	if pct > 100 {
		pct = 100
	}
	return pct
}

// chatLines renders every visible message, oldest first.
func (t *TUI) chatLines(inner int) []string {
	if len(t.messages) == 0 {
		return t.emptyState(inner)
	}
	msgs := t.matchingMessages()

	// A filter that matches nothing is a dead end unless the interface says so and
	// says how to leave: the guide's rule for an empty state is an explanation plus an
	// action, and "no matches" with no way out would look like a hang.
	if len(msgs) == 0 {
		return t.noMatches(inner)
	}

	var lines []string
	if len(t.messages) > len(msgs) && t.query == "" {
		lines = append(lines, t.cell(t.muted(fmt.Sprintf("... %d earlier messages", len(t.messages)-len(msgs))), inner))
	}
	for i, m := range msgs {
		if i > 0 {
			lines = append(lines, t.cell("", inner)) // one blank row between turns
		}
		lines = append(lines, t.messageLines(m, inner)...)
	}
	return lines
}

// noMatches is the filtered-to-empty state: what was searched, and the two keys that
// get the user back. It is deliberately not the same screen as "nothing asked yet" —
// one means "start", the other means "widen your search".
func (t *TUI) noMatches(inner int) []string {
	lines := []string{t.cell("", inner)}
	for _, l := range []string{
		fmt.Sprintf("No line matches %q", t.query),
		"",
		"Ctrl+F  search again",
		"Esc     show the whole conversation",
	} {
		lines = append(lines, t.cell(t.muted(l), inner))
	}
	return append(lines, t.cell("", inner))
}

// searchBar is the prompt row while the search is open. It replaces the mode prompt so
// the user can see what they are typing into: a search that looks like a chat prompt
// invites a task to be typed into it.
func (t *TUI) searchBar() string {
	// The two facts are independent: a filter can be applied while the box is open for
	// editing it. Showing only one of them is what made the applied filter invisible.
	prompt := "  " + t.color(colAccent, 0, "find") + t.muted(" > ")
	if t.query == "" {
		return prompt
	}
	// A filter that is applied but not being edited still has to be visible, or the user
	// cannot tell why their history looks short.
	n := len(t.matchingMessages())
	label := strconv.Quote(t.query) + "  " + strconv.Itoa(n) + " lines  "
	if !t.searching {
		label += "[Ctrl+F edit  Esc clear]  "
	}
	return "  " + t.muted("filter ") + t.color(colAccent, 0, label) + prompt
}

// emptyState is a designed first screen, not a blank one: it says what the view
// is for and what to do next.
func (t *TUI) emptyState(inner int) []string {
	var msg string
	switch t.screen {
	case ScreenPlan:
		msg = "Ask a question and press Enter.\n" +
			"Plan mode only reads: it lists and reads files and runs read-only\n" +
			"commands, and it says what it would do before anything is executed."
	case ScreenModels:
		msg = "Press Enter to ask the provider which models it publishes, and to check\n" +
			"that the endpoint and the key in this configuration actually work."
	case ScreenConfig:
		msg = "Press Enter to walk through the first-run wizard: provider, model and\n" +
			"the check the anchor runs. It writes a working configuration file."
	default:
		msg = "Describe a task and press Enter.\n" +
			"The agent runs it in the sandbox, checks the result and reports back\n" +
			"what it actually did."
	}
	lines := []string{t.cell("", inner)}
	for _, l := range strings.Split(msg, "\n") {
		for _, wrapped := range wordWrap(l, inner-leftMargin) {
			lines = append(lines, t.cell(t.muted(wrapped), inner))
		}
	}
	return append(lines, t.cell("", inner))
}

// messageLines renders one turn: a header that identifies the speaker, then the
// body on a rail. The rail is what makes a long conversation easy to follow and
// it costs a single column.
func (t *TUI) messageLines(m Message, inner int) []string {
	switch m.Author {
	case AuthorUser:
		head := t.color(colAccent, 0, glyphUser+" ") + t.muted("you")
		return append([]string{t.cell(head, inner)}, t.railLines(m.Text, inner, colBase, colMuted)...)

	case AuthorAgent:
		// A frozen line holds a label that was already formatted (a tool
		// announcement), so it is shown as an event and never as answer text.
		if m.Frozen {
			return []string{t.cell(t.muted("  "+glyphAgent+" ")+t.color(colAccent, 0, m.Text), inner)}
		}
		if label, ok := toolLabel(m.Text); ok {
			return []string{t.cell(t.muted("  "+glyphAgent+" ")+t.color(colAccent, 0, label), inner)}
		}
		head := t.color(colSuccess, 0, glyphAgent+" starlight")
		body := colBase
		if m.Pending {
			head += "  " + t.color(colWarning, 0, spinner[t.spin%len(spinner)]+" working")
			body = colWarning
		}
		return append([]string{t.cell(head, inner)}, t.railLines(t.crtText(m), inner, body, colMuted)...)

	default:
		var lines []string
		// Preformatted text is placed line by line and clipped, never rewrapped:
		// the alignment between a key and its description is the whole point of the
		// help screen, and word wrapping collapses the runs of spaces that produce
		// it. Clipping loses a long description rather than mangling it.
		if m.Preformatted {
			for _, l := range strings.Split(strings.TrimRight(m.Text, "\n"), "\n") {
				lines = append(lines, t.cell(t.muted(clipLine(l, inner-leftMargin)), inner))
			}
			return lines
		}
		for _, l := range wordWrap(m.Text, inner-leftMargin) {
			lines = append(lines, t.cell(t.muted(l), inner))
		}
		return lines
	}
}

// clipLine truncates a PLAIN line (no escape sequences) to the given number of
// columns, marking the cut with an ellipsis. It is for text whose own layout must
// survive, which is why it clips instead of folding the line.
//
// No "does the rune count fit" guard is written below: it would be unreachable. For
// a string without escapes visibleLen is exactly the rune count, so once the first
// check has established that the measurement exceeds the width, the rune count does
// too. A guard there would only look like a safety net.
func clipLine(s string, width int) string {
	// No room at all means no text, not all of it. The earlier version returned the whole
	// string when the width was zero or negative, which is the opposite of clipping: on a
	// terminal too narrow for even the margins, every row would draw at full length and the
	// frame would break out of the window it was measured for.
	if width <= 0 {
		return ""
	}
	if visibleLen(s) <= width {
		return s
	}
	// One column is spent on the ellipsis, so the result still fits.
	runes := []rune(s)
	return strings.TrimRight(string(runes[:width-1]), " ") + "\u2026"
}

// toolLabel recognises the progress line that announces a tool call and turns it
// into a short label. It returns false for ordinary text, which is how the caller
// tells a tool announcement from model output.
func toolLabel(text string) (string, bool) {
	trimmed := strings.TrimSpace(text)
	trimmed = strings.TrimSuffix(strings.TrimPrefix(trimmed, "["), "]")
	if !strings.HasPrefix(trimmed, "using tool:") {
		return "", false
	}
	rest := strings.TrimSpace(strings.TrimPrefix(trimmed, "using tool:"))
	if rest == "" {
		return "using a tool", true
	}
	// Keep it short: the arguments can be long and this is only an event marker.
	if len(rest) > 44 {
		rest = rest[:44] + "..."
	}
	return "using " + rest, true
}

// phaseLabel recognises the planner's own phase announcements, which are status
// markers rather than answer text. The interface shows the phase as movement (the
// spinner and the "working" marker on the open block), so echoing the raw marker
// inside the answer would only add noise: a block that consists of nothing but a
// phase is not conversation.
func phaseLabel(text string) (string, bool) {
	trimmed := strings.TrimSpace(text)
	trimmed = strings.TrimSuffix(strings.TrimPrefix(trimmed, "["), "]")
	if trimmed == "thinking..." || trimmed == "thinking" {
		return "thinking", true
	}
	return "", false
}

// railLines wraps body text and puts it on the rail, padded to the panel.
//
// While a filter is active the match is highlighted in place. The guide asks for it by
// name — "highlight matches in the filtered content" — and it is what makes a filtered
// view readable: without it the user sees lines that match but not why.
func (t *TUI) railLines(text string, inner int, fg, railCol int) []string {
	if strings.TrimSpace(text) == "" {
		return nil
	}
	var lines []string
	for _, l := range wordWrap(text, inner-leftMargin) {
		body := t.color(fg, 0, l)
		if t.query != "" {
			body = t.highlight(l, fg)
		}
		// No rail: the conversation is not boxed, so the column the rail used to take is
		// given back to the text.
		lines = append(lines, t.cell(body, inner))
	}
	return lines
}

// highlight marks every occurrence of the query inside a line, keeping the rest in the
// surrounding colour. The search is case-insensitive, so the match is located on the
// lowercased copy and the ORIGINAL text is emitted — colouring a lowercased copy would
// silently rewrite what the user asked the agent.
func (t *TUI) highlight(line string, fg int) string {
	needle := strings.ToLower(t.query)
	lower := strings.ToLower(line)
	if needle == "" || !strings.Contains(lower, needle) {
		return t.color(fg, 0, line)
	}
	var b strings.Builder
	for {
		i := strings.Index(lower, needle)
		if i < 0 {
			b.WriteString(t.color(fg, 0, line))
			break
		}
		b.WriteString(t.color(fg, 0, line[:i]))
		b.WriteString(t.color(0, colAccent, line[i:i+len(needle)]))
		line = line[i+len(needle):]
		lower = lower[i+len(needle):]
	}
	return b.String()
}

// hintLines lists the keys that work right now, the key in the accent colour and
// its meaning muted. Hints that do not fit are dropped, never wrapped: a hint that
// is cut in half reads as a different key.
//
// Every hint here is a binding the handler accepts, and the test asserts exactly that:
// a footer advertising a key that does nothing is a lie the user finds in seconds.
//
// The list is deliberately SHORT and covers only the keys a user needs before they know
// the interface: the navigation keys and the two ways out. The mode shortcuts are in the
// help screen instead. That is not tidiness — a longer list was silently truncated at
// every supported width, so the hints at the end were unreachable no matter how wide the
// terminal was, which is worse than not advertising them at all. A test asserts the
// whole list fits within the width cap, so adding a hint that breaks that fails the
// build instead of quietly dropping the last one.
func (t *TUI) hintLines(w int) []string {
	hints := [][2]string{
		{"Tab", "mode"},
		{"j/k", "scroll"},
		{"^F", "find"},
		{"?", "help"},
		{"q", "quit"},
	}
	var out []string
	for _, h := range hints {
		candidate := append(append([]string{}, out...), t.color(colAccent, 0, h[0])+" "+t.muted(h[1]))
		joined := strings.Join(candidate, t.muted("  "+glyphMid+"  "))
		// The first hint is decided on its own: if it does not fit, the terminal
		// is too narrow for hints at all and none are shown. Adding it and then
		// letting fitLine trim it would print a truncated key, which promises a
		// binding that does not exist.
		if !t.fits("  "+joined, w-leftMargin) {
			break
		}
		out = candidate
	}
	if len(out) == 0 {
		return nil
	}
	return []string{"  " + strings.Join(out, t.muted("  "+glyphMid+"  "))}
}

// cell pads a decorated string to the panel width.
// cell is one conversation row: the left margin, the text, and blank space to the edge.
//
// There is no rail and no right border. The conversation is the primary content, and the
// guide's rule is to separate with spacing and the two rules rather than with a box around
// the only thing on screen — the box also pushed the composer away from the foot of the
// window, which is where the input belongs.
//
// The padding is what keeps the frame from filling the last column, so no row of this
// interface can trigger a terminal's auto-wrap.
func (t *TUI) cell(s string, available int) string {
	// available is the whole row width, margin included. The margin is spent first and the
	// text fills the rest, so every row is exactly as wide as the drawing area — which is
	// what keeps the two rules aligned with the content between them.
	//
	// The earlier version took the width INSIDE the frame and added the margin on top, so
	// every row came out two columns wider than the space it had. That was invisible while a
	// border sat at the right edge: the border was clipped, not the text.
	// The text is CLIPPED to the space it has. Without this a single long word — a path, a
	// URL, a model id — would push the row past the terminal's width, and a terminal wraps at
	// its width: the frame would gain a line, the layout would no longer fit the window, and
	// the interface would scroll under itself.
	room := available - leftMargin
	s = clipLine(s, room)
	pad := room - visibleLen(s)
	if pad < 0 {
		pad = 0
	}
	return strings.Repeat(" ", leftMargin) + s + strings.Repeat(" ", pad)
}

// fitLine truncates a decorated line to the given width, closing any open escape
// with a reset so a cut never bleeds colour into the next line.
func (t *TUI) fitLine(s string, w int, prefix string) string {
	if t.fits(prefix+s, w) {
		return prefix + s
	}
	var b strings.Builder
	visible, limit := 0, w-len(prefix)
	for _, r := range s {
		if r == 0x1b {
			b.WriteRune(r)
			continue
		}
		if visible >= limit-1 {
			break
		}
		b.WriteRune(r)
		visible++
	}
	return prefix + b.String() + "\x1b[0m"
}

// padCenter centres a decorated string in the given number of columns.
// padCenter centres a decorated string in the drawing area.
//
// It centres inside the SAME box everything else is drawn in — the frame minus the left margin
// and minus the column kept free at the end — not against the raw terminal width. Centring on
// the raw width put the banner half a column off from the panels beneath it, which is visible
// on an odd-width terminal and is what makes a centred mark look "not quite centred".
func (t *TUI) padCenter(s string, w int) string {
	// No floor on room: the size gate refuses anything narrower than minWidth, and minWidth is
	// wider than the margin plus the reserved column, so this is always positive.
	room := w - leftMargin - 1
	pad := room - visibleLen(s)
	if pad <= 0 {
		return strings.Repeat(" ", leftMargin) + s
	}
	// Both halves are rounded down, so the extra column stays on the right where it does not
	// shift the mark off the centre of the content.
	return strings.Repeat(" ", leftMargin+pad/2) + s
}

// centerPlain centres an undecorated string in the drawing area, for the status line under the
// banner. It pads BOTH sides so the result spans the full width, which keeps a row that is only
// sometimes wider from jumping around. See padCenter for why the box is the frame's, not the
// terminal's.
func (t *TUI) centerPlain(s string, w int) string {
	// Same reasoning as padCenter: the gate makes this positive.
	room := w - leftMargin - 1
	n := visibleLen(s)
	if n >= room {
		return strings.Repeat(" ", leftMargin) + s
	}
	pad := room - n
	left := pad / 2
	return strings.Repeat(" ", leftMargin+left) + s
}

// visibleMessages keeps the conversation bounded in memory.
func (t *TUI) visibleMessages() []Message {
	if len(t.messages) <= maxScrollback {
		return t.messages
	}
	return t.messages[len(t.messages)-maxScrollback:]
}

// muted renders secondary text: labels, hints, rails and rules.
func (t *TUI) muted(s string) string { return t.color(colMuted, 0, s) }

// brand renders the wordmark in the brand colour.
func (t *TUI) brand(s string) string { return t.color(colBrand, 0, s) }

// color returns an ANSI-coloured string. fg/bg use the 16-colour palette.
func (t *TUI) color(fg, bg int, s string) string {
	if t.NoColor {
		return s
	}
	if bg == 0 {
		return fmt.Sprintf("\x1b[%dm%s\x1b[0m", 30+fg, s)
	}
	return fmt.Sprintf("\x1b[%d;%dm%s\x1b[0m", 30+fg, 40+bg, s)
}

// wordWrap splits s into lines of at most width columns, breaking on spaces and
// only splitting a word when a single word is longer than the line.
func wordWrap(s string, width int) []string {
	if width <= 0 {
		return []string{s}
	}
	var lines []string
	for _, para := range strings.Split(s, "\n") {
		var cur strings.Builder
		for _, word := range strings.Fields(para) {
			if visibleLen(cur.String())+visibleLen(word)+1 > width {
				if cur.Len() == 0 {
					// A single word wider than the line: hard split it.
					runes := []rune(word)
					for len(runes) > width {
						lines = append(lines, string(runes[:width]))
						runes = runes[width:]
					}
					cur.WriteString(string(runes))
					continue
				}
				lines = append(lines, strings.TrimSpace(cur.String()))
				cur.Reset()
			}
			if cur.Len() > 0 {
				cur.WriteByte(' ')
			}
			cur.WriteString(word)
		}
		if cur.Len() > 0 {
			lines = append(lines, strings.TrimSpace(cur.String()))
		}
	}
	if len(lines) == 0 {
		lines = append(lines, "")
	}
	return lines
}

// Escapes are parsed with a state machine instead of "skip until a byte in
// 0x40..0x7E": in a CSI sequence the introducer itself, '[' (0x5b), already falls
// inside that range, so the naive rule stops one byte early and counts the whole
// colour sequence as text.
//
// Four shapes are recognised, which is what a terminal can be sent:
//
//	CSI  ESC [ params final            colours, cursor movement, erase
//	OSC  ESC ] … BEL or ESC \          window title, hyperlinks
//	ESC  ESC intermediate* final       charset selection: ESC ( B, ESC # 8
//	ESC  ESC <other single byte>       two-byte escapes such as ESC =
//
// The general ANSI rule is what the last two encode: after the ESC, bytes in
// 0x20..0x2F are intermediates and the first byte at or above 0x30 ends the
// sequence. Treating ESC ( B as "ESC then two visible characters" is what made
// the measured width of a decorated line wrong.
const (
	escNone = iota
	escSeen // the ESC itself was just read
	escCSI  // ESC [ … : parameters, then a final byte
	escOSC  // ESC ] … : a string, ended by BEL or by ESC \
	escOSCEsc
	escInter // ESC followed by intermediates, waiting for the final byte
)

// scanEscapes walks a string and reports, for every rune, whether it is part of
// an escape sequence. It is the single place that knows how escapes look, so the
// width measurement and the stripper can never disagree.
func scanEscapes(s string, visit func(r rune, isEscape bool)) {
	state := escNone
	for _, r := range s {
		switch state {
		case escSeen:
			visit(r, true)
			switch {
			case r == '[':
				state = escCSI
			case r == ']':
				state = escOSC
			case r >= 0x20 && r <= 0x2f:
				// An intermediate byte: the sequence continues.
				state = escInter
			default:
				state = escNone
			}
		case escInter:
			visit(r, true)
			switch {
			case r == 0x1b:
				// A new escape interrupts the one being read. Treating it as an
				// intermediate kept the parser in this state, and the following
				// "[31m" was then counted as four columns of text.
				state = escSeen
			case r >= 0x30:
				state = escNone
			}
		case escCSI:
			visit(r, true)
			if r >= 0x40 && r <= 0x7e {
				state = escNone
			}
		case escOSC:
			visit(r, true)
			switch r {
			case 0x07: // BEL ends it
				state = escNone
			case 0x1b: // ESC \ also ends it
				state = escOSCEsc
			}
		case escOSCEsc:
			visit(r, true)
			state = escNone
		default:
			if r == 0x1b {
				state = escSeen
				visit(r, true)
				continue
			}
			visit(r, false)
		}
	}
}

// visibleLen is the width a decorated string occupies on screen: escape
// sequences cost no columns and every other rune costs one.
func visibleLen(s string) int {
	n := 0
	scanEscapes(s, func(_ rune, isEscape bool) {
		if !isEscape {
			n++
		}
	})
	return n
}

// stripANSI removes every escape sequence, for no-colour mode.
func stripANSI(s string) string {
	var b strings.Builder
	scanEscapes(s, func(r rune, isEscape bool) {
		if !isEscape {
			b.WriteRune(r)
		}
	})
	return b.String()
}

// rule is a horizontal divider across the drawing area.
//
// It is what carries the structure now that the conversation is not boxed: one above the
// chat and one below it, so the middle of the screen is visibly the content and the bottom
// is visibly the controls.
func (t *TUI) rule(w int) string {
	// The rule is a solid run of ink from the left margin to the last usable column:
	//
	//	w - leftMargin (spent on the margin) - 1 (the column kept free so nothing wraps)
	//
	// The earlier version subtracted the margin from a width that had ALREADY given up a
	// column, so it came out one short — a strip of blank space down the right edge that was
	// most obvious on a wide terminal, where it stood out against an otherwise full-width frame.
	n := w - leftMargin - 1
	if n < 1 {
		n = 1
	}
	return strings.Repeat(" ", leftMargin) + t.muted(strings.Repeat(glyphRule, n))
}

// plainLine is a line with the left margin and nothing else: the conversation is not in a
// panel any more, so a body row is just text in the flow.
func (t *TUI) plainLine(s string) string {
	return strings.Repeat(" ", leftMargin) + s
}

// bodyWidth is the number of columns the conversation may use: the drawing area minus the
// margins on both sides.
func (t *TUI) bodyWidth() int {
	// Same reasoning as conversationWidth: size() clamps to minWidth, which exceeds twice the
	// margin, so this is always positive. The guard that used to sit here could not be reached —
	// which is not the same as being harmless: it read as protection while testing nothing.
	w, _ := t.size()
	return w - 2*leftMargin
}

// statusLines is the line under the wordmark: which model is answering, and how hard it is
// thinking. Nothing about keys or readiness — a user who reached a chat has a working key,
// and reporting it on every repaint is noise that the eye learns to skip past.
//
// The values are brighter than their labels, so the model stands out and the words around
// it recede. A narrow terminal drops from the tail: the model is never the thing dropped.
func (t *TUI) statusLines(w int) []string {
	cfg := t.Runner.Config()
	provider := cfg.LLM.Provider
	if provider == "" {
		provider = "openai"
	}
	model := cfg.LLM.Model
	if model == "" {
		model = "unknown"
	}
	reasoning := "off"
	if cfg.LLM.Reasoning.Enabled {
		reasoning = cfg.LLM.Reasoning.Level
	}
	state := t.stateGlyph()

	parts := []string{
		state + " " + t.color(colBase, 0, provider) + t.muted("/") + t.color(colBase, 0, model),
		t.muted("reasoning ") + t.color(colBase, 0, reasoning),
	}
	if t.query != "" || t.searching {
		parts = append(parts, t.color(colAccent, 0, "filter "+strconv.Quote(t.query)))
	}
	// Centred under the wordmark, in the same box: the identity line is part of the header, so it
	// belongs on the mark's axis rather than against the left edge.
	//
	// The shedding stays: on a narrow terminal the parts are dropped from the tail until what is
	// left fits. The provider and the model are never the thing dropped — they are what the line
	// is for.
	line := strings.Join(parts, t.muted("   "))
	for !t.fits(line, w-2*leftMargin-1) && len(parts) > 1 {
		parts = parts[:len(parts)-1]
		line = strings.Join(parts, t.muted("   "))
	}
	return []string{t.centerPlain(line, w)}
}

// bottomBar is the last line: where you are on the left, what you can do and how much
// context is gone on the right.
//
// The context figure is a percentage of the model's window with the token count beside it,
// because the percentage is what tells a user whether they are about to lose the earlier
// conversation and the count is what makes it trustworthy.
func (t *TUI) bottomBar(w int) string {
	left := t.color(colAccent, 0, t.screen.String())

	right := t.muted(t.contextLabel())
	if keys := t.keyHints(); keys != "" {
		right = keys + t.muted("   ") + right
	}
	if t.scroll > 0 {
		right = t.color(colWarning, 0, glyphDot+" "+strconv.Itoa(t.scroll)+" back") + t.muted("   ") + right
	}

	// The available columns are the frame minus the ONE margin plainLine will add: the right
	// edge is the last usable column, and nothing is reserved twice. Subtracting the margin here
	// as well as in plainLine is what left the bar one column short of the rules above it.
	room := w - leftMargin - 1

	gap := room - visibleLen(left) - visibleLen(right)
	if gap < 1 {
		// Too narrow for both ends: the mode and the context are what must survive, so the
		// key hints are what goes.
		only := t.muted(t.contextLabel())
		gap = room - visibleLen(left) - visibleLen(only)
		if gap < 1 {
			return t.plainLine(left)
		}
		return t.plainLine(left + strings.Repeat(" ", gap) + only)
	}
	return t.plainLine(left + strings.Repeat(" ", gap) + right)
}

// contextLabel describes how much of the model's window is gone.
//
// With no session yet it says nothing rather than inventing a figure: the window is known
// from the model id, but nothing has been sent, and a percentage of an empty conversation
// would be a decoration.
func (t *TUI) contextLabel() string {
	s := t.Runner.ConversationSummary()
	if s.Window <= 0 {
		return ""
	}
	return fmt.Sprintf("context %d%% (%d/%d)", int(s.Used*100+0.5), s.Tokens, s.Window)
}

// keyHints is the short list of keys for the current context, formatted for the status bar.
//
// While the search is open the hints change to the ones that work there, which is the
// guide's rule about showing only what is relevant right now.
func (t *TUI) keyHints() string {
	var hints [][2]string
	if t.searching {
		hints = [][2]string{{"Enter", "apply"}, {"Esc", "clear"}}
	} else {
		hints = [][2]string{{"^C", "stop"}, {"/", "commands"}, {"?", "help"}}
	}
	var parts []string
	for _, h := range hints {
		parts = append(parts, t.color(colAccent, 0, h[0])+" "+t.muted(h[1]))
	}
	return strings.Join(parts, t.muted("  "+glyphMid+"  "))
}

// composerLines is the input row.
//
// It is one line, always present, always the same height: a composer that changes size
// makes the whole screen jump as the user types. The prompt is written by the caller as the
// LAST thing on the frame, which is what parks the cursor at the end of it.
func (t *TUI) composerLines() []string {
	return t.composerLinesCapped(0)
}

// composerLinesCapped draws the popup and the input row, with the popup limited to `popupCap`
// rows (zero meaning no limit).
//
// The cap comes from the layout, which is the only place that knows how tall the terminal is.
// Passing it in rather than recomputing it here is what keeps the measured height and the drawn
// height the same: a popup that drew more rows than the layout reserved would overflow the
// window and scroll the interface on a keypress.
func (t *TUI) composerLinesCapped(popupCap int) []string {
	var lines []string
	// The questions window draws in the popup's place. The input box below it is NOT hidden:
	// the answer field and the free-answer row are the same thing, so the user can see exactly
	// what will be sent while picking an option or typing one.
	if t.asking() {
		lines = append(lines, t.askLines(popupCap)...)
	} else {
		lines = append(lines, t.completionLinesCapped(t.bodyWidth(), popupCap)...)
	}
	// The input area is a FIXED few rows: a box the user types into, with its own divider above
	// it. Three rows is the size the user asked for — enough to see a sentence or two of what
	// has been typed without the box dominating the window — and it is fixed, so the frame
	// never changes height while typing.
	//
	// The text is wrapped into the box rather than scrolled: at this size there is nothing to
	// scroll, and a box that never moves is easier to read than one that shifts.
	lines = append(lines, t.inputBoxLines()...)
	return lines
}

// inputBoxLines draws the input: a divider, then the field with what has been typed, then the
// blank row that keeps the field from touching the status bar.
//
// The label is drawn INSIDE the field, on the first row, so the cursor starts after it.
func (t *TUI) inputBoxLines() []string {
	width := t.bodyWidth()
	rows := inputRows

	// The field, wrapping the label plus the draft across the available rows. The width passed to
	// the wrap is the TEXT area, not the body: the left margin is spent by plainLine below, and
	// wrapping to the un-margined width would push every row two columns past the frame.
	text := t.composerLabel() + t.draft
	wrapped := wrapVisible(text, width)
	if len(wrapped) > rows {
		// Keep the END: the user is typing there, and the tail is what matters.
		wrapped = wrapped[len(wrapped)-rows:]
	}

	// EVERY row of the box goes through plainLine, filled and wrapped alike.
	//
	// The wrapped rows used to be emitted raw while the padding rows went through plainLine, so
	// the first rows of the field began at column 0 and the empty ones at column 2. The cursor
	// then had no column that was correct for both: it was computed with the margin, so it sat two
	// columns past the text on every row that carried any.
	out := make([]string, 0, rows)
	for _, l := range wrapped {
		out = append(out, t.plainLine(l))
	}
	for len(out) < rows {
		out = append(out, t.plainLine(""))
	}
	return out
}

// wrapVisible wraps a decorated string to a column width, carrying escape sequences along with
// the text they style.
//
// It breaks mid-word: the input can receive a long path or a URL with no space in it, and
// refusing to break would push the text out of its box. Escape sequences cost no columns, so
// only visible runes are counted.
func wrapVisible(s string, width int) []string {
	if width < 1 {
		width = 1
	}
	if visibleLen(s) <= width {
		return []string{s}
	}

	var out []string
	var cur strings.Builder
	curVis := 0
	for _, r := range s {
		// An escape sequence is copied whole and costs no columns. It is never split: a
		// truncated escape would leak colour into the rest of the frame.
		if r == 0x1b {
			cur.WriteRune(r)
			continue
		}
		if curVis >= width {
			out = append(out, cur.String())
			cur.Reset()
			curVis = 0
		}
		cur.WriteRune(r)
		curVis++
	}
	// The final line always exists, even when it is empty: the loop above writes every rune it
	// is given, so the only way to reach here with nothing is an empty input — which is a row of
	// no width, not an absent row. A guard for "no output" would be unreachable.
	if cur.Len() > 0 {
		out = append(out, cur.String())
	}
	return out
}

// composerLabel is the visible part of the input row: an indicator that says what the line
// will be interpreted as. While a slash command is being typed it becomes the command
// name, and while the search is open it says so, because a search that looks like a chat
// prompt invites a task to be typed into it.
func (t *TUI) composerLabel() string {
	// The prompt carries NO mode name. The status bar already reports it, and printing it here
	// as well put the same word on two rows of every single frame — the input said "Task >"
	// directly above a bar that said "Task". One fact, one place.
	//
	// The search is the exception: while the box is open the line being typed is a query, not a
	// task, and that is something the user has to be able to see at the point of typing.
	if t.searching {
		return t.color(colAccent, 0, "find") + t.muted(" > ")
	}
	if t.query != "" {
		return t.muted("filter "+strconv.Quote(t.query)) + t.muted("  ") +
			t.color(colAccent, 0, "find") + t.muted(" > ")
	}
	return t.color(colAccent, 0, glyphPrompt) + t.muted(" ")
}

// composerPrompt is what moves the cursor back to the point of typing.
//
// It is not written after the frame: the composer sits a couple of rows ABOVE the bottom (the
// rule and the status bar are under it), so appending the prompt to the end of the frame would
// park the cursor on the last row — outside the frame it belongs to. What is returned here is
// the escape that walks the cursor up to the composer's row and along to the end of the draft,
// which is what the caller needs to leave the terminal's own cursor where the user types.
//
// The number of rows to walk up is how many rows follow the composer in the frame: in the
// padded layout that is the rule and the status bar, so two. It is passed in rather than
// assumed, because the un-trimmed path (a terminal that did not report its height) draws the
// frame without the two rules and must not walk off the top.
func (t *TUI) composerPrompt(rowsBelow int) string {
	// Where the cursor goes: the row and column of the LAST character of the input.
	//
	// The input is a box of inputRows rows and the text wraps inside it, so the position of the
	// cursor is not "the first row, at the total length". Once the text passes the width its tail
	// is on a later row, and computing the column from the whole string put the cursor past the
	// right edge — where the terminal moves it to the next row on its own, or refuses to move it
	// at all. The user sees a cursor that is not where they are typing.
	//
	// It is derived from the same wrap the box draws with, so the two cannot disagree.
	width := t.bodyWidth()
	wrapped := wrapVisible(t.composerLabel()+t.draft, width)
	row := len(wrapped) - 1
	col := leftMargin + visibleLen(wrapped[row])
	if row >= inputRows {
		// The box shows only the last inputRows rows, so the cursor is on the last visible one.
		row = inputRows - 1
	}
	// rowsBelow counts the rows AFTER the cursor's row: everything the caller listed below the
	// input, plus the box's own rows that come after it.
	// rowsBelow is never negative: the caller counts at least the rule and the status bar below
	// the input, and `row` only ever subtracts rows that are inside the box. A floor here would be
	// unreachable.
	rowsBelow -= row

	// The cursor is moved with CSI sequences ONLY — never with a bare carriage return.
	//
	// A "\r" looks harmless and is not: the interface runs the terminal in cbreak with echo
	// off, and in that mode the driver's output translation is not what one assumes. Measured
	// on the target machine, the "\r" came out of the pty as "\n", which moves the cursor DOWN
	// a row — off the bottom of a frame that fills the window — and the terminal scrolls. That
	// is the extra line the user saw being pushed upward on every Tab.
	//
	// CSI G (cursor to column) and CSI A (cursor up) move without depending on any translation.
	var b strings.Builder
	if rowsBelow > 0 {
		fmt.Fprintf(&b, "\x1b[%dA", rowsBelow)
	}
	fmt.Fprintf(&b, "\x1b[%dG", col+1) // columns are 1-based
	return b.String()
}

// cursorMove turns the layout's cursor move into one that is correct for the rows actually
// written.
//
// layout() returns a move that walks up from the END of the frame, which is right only when the
// whole frame was painted. An incremental repaint stops at the last row that changed, so that
// walk-up overshoots by (total rows - last written - 1) and the cursor lands somewhere in the
// conversation instead of in the input box.
//
// Rather than teach the layout about incremental painting — it composes the frame and should not
// have to know how it is written — the move is adjusted here, where the written rows are known.
// The adjustment is the distance between the end of the frame and the last written row, which is
// exactly the overshoot.
func (t *TUI) cursorMove(prompt string, lastWritten, total int) string {
	if lastWritten < 0 {
		// Nothing was written: the terminal's cursor has not moved. Returning the layout's
		// walk-up here would still execute it, and a mouse event that arrives in an idle
		// chat would then jump the cursor up to wherever the layout decided, which is
		// exactly what shows up as the input moving while the user only moved the pointer.
		// The only thing that has to change is the visibility flag, drawn separately.
		return ""
	}
	overshoot := total - 1 - lastWritten
	if overshoot <= 0 {
		return prompt
	}
	// The prompt is "\x1b[<up>A\x1b[<col>G" or just the column move when there is nothing to
	// walk up. Rewriting the count is safer than composing a new sequence: the column is the part
	// the layout computed from the wrap, and re-deriving it here would be a second implementation
	// of the same calculation.
	up, col, ok := parseCursorMove(prompt)
	if !ok {
		return prompt
	}
	up -= overshoot
	if up < 0 {
		// The input is ABOVE the last written row, which happens when a repaint below the input
		// (the status bar, a rule) is all that changed. Moving up would leave the cursor in the
		// wrong place entirely, so the row is addressed absolutely instead: from the last written
		// row, the input's row is a known distance away.
		return fmt.Sprintf("\x1b[%dA\x1b[%dG", total-1-lastWritten+rowsBelowComposer+inputRows-1, col)
	}
	return fmt.Sprintf("\x1b[%dA\x1b[%dG", up, col)
}

// parseCursorMove reads the row/column out of a cursor move built by composerPrompt.
//
// It accepts both shapes the layout produces: the walk-up plus a column, and a bare column when
// nothing had to be walked up.
func parseCursorMove(prompt string) (up, col int, ok bool) {
	body := prompt
	if strings.HasPrefix(body, "\x1b[") {
		body = body[2:]
	} else {
		return 0, 0, false
	}
	if i := strings.IndexByte(body, 'A'); i >= 0 {
		n, err := strconv.Atoi(body[:i])
		if err != nil {
			return 0, 0, false
		}
		up = n
		body = body[i+1:]
		if !strings.HasPrefix(body, "\x1b[") {
			return 0, 0, false
		}
		body = body[2:]
	}
	i := strings.IndexByte(body, 'G')
	if i < 0 {
		return 0, 0, false
	}
	n, err := strconv.Atoi(body[:i])
	if err != nil {
		return 0, 0, false
	}
	return up, n, true
}

// completionRowsNeeded is how many extra rows the completion popup asks for right now: zero when
// there is nothing to suggest, and one row per candidate plus the hint otherwise.
//
// It is a method rather than a field so the two places that need it — the size gate and the
// budgeting — cannot disagree about how big the popup is.
func (t *TUI) popupRows() int {
	// The questions window takes the reservation the completion popup would use. The two cannot
	// be open together: the window CAPTURES the keys, so nothing is being typed into the input
	// for a completion to react to — and if both drew, the frame would be taller than the layout
	// measured and the interface would scroll on a keypress.
	if t.asking() {
		return t.askRows()
	}
	if !t.completing() {
		return 0
	}
	return len(completions(t.draft)) + 1
}

// clearOnExit wipes the screen and parks the cursor at the origin.
//
// The sequence is the same one the launch uses, and it wipes the SCROLLBACK as well: the frames
// the user scrolled through are part of what the interface put on the screen, and leaving them
// above the prompt means the terminal still looks like the interface after the interface has
// gone.
//
// It is deliberately unconditional — no check for a terminal, no check for a tty. Writing these
// escapes to something that is not a terminal is harmless (a pipe, a file, a test's buffer),
// while missing them on a real terminal is the bug being fixed. Deciding "am I a terminal?" here
// would need the very ioctl the rest of the interface avoids, and would add a case where the
// screen is left dirty.
//
// The cursor is put back at the top-left, which is where a shell draws its prompt from, and the
// cursor is made visible: the frame hides it while painting, and an exit that leaves it hidden
// gives the user a terminal with no cursor.
func (t *TUI) clearOnExit() {
	if t.Out == nil {
		return
	}
	fmt.Fprint(t.Out, exitClear)
}
