package tui

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/madkoding/motita/internal/config"
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
	//	1  the top bar (the name, the readiness, provider and model)
	//	1  the rule under it
	//	1  the top border of the input box, which carries the mode
	//	3  the input field
	//	1  the bottom border of the input box
	//	1  the footer (the keys that work right now, and the context used)
	//
	// Eight. The box's borders are what separate the conversation from the input, so no blank
	// row or extra rule is spent on it.
	permanentRows = 8
)

// spinner is advanced on every repaint of a running turn. It is plain ASCII so
// it animates on every terminal, including a text console with a VGA font.
var spinner = [...]string{"|", "/", "-", "\\"}

// layout is the whole interface, top to bottom, fitted to the terminal:
//
//	motita  v1.2                 • openai · gpt-5-mini · reasoning medium
//	────────────────────────────────────────────────────────────────────
//	conversation (or the welcome screen)
//	(completion popup / questions / confirmation)
//	┌─ Task · makes changes, then proves them with your check ────────┐
//	│ › what the user is typing                                       │
//	│                                                                 │
//	│                                                                 │
//	└─────────────────────────────────────────────────────────────────┘
//	Enter send · Tab Plan mode · / commands · ? help        context 12%
//
// One row of identity at the top, the conversation in the middle, and everything the user acts
// with at the bottom: the box says what Enter will do (the mode is its title), and the footer says
// which keys work right now.
//
// FITTING. Every part is counted and the total is exactly h whenever h can hold the permanent
// rows, because a frame taller than the terminal scrolls — and a frame that scrolls moves the
// whole interface up on every repaint, which the user sees as the screen jumping when they
// press a key.
//
// The order of sacrifice is the popup, then the conversation down to its floor. The top bar, the
// box and the footer are never dropped: they are what the interface is. A height of zero means
// the terminal did not report one, and nothing can be trimmed — everything is drawn and the shell
// scrolls, as it must.
func (t *TUI) layout(w, h int) ([]string, string) {
	top := t.statusLines(w)
	body := t.chatLines(t.conversationWidth())
	bar := t.bottomBar(w)
	popup := t.popupRows()

	// The composer is always two rows above the end of the frame — the box's bottom border and
	// the footer — so the cursor is walked back up to it from wherever the frame leaves it.
	const belowComposer = rowsBelowComposer

	// A terminal too small to hold the interface gets an explanation instead of a broken frame.
	if h > 0 && h < minHeight {
		return t.tooSmallLines(w, h), ""
	}

	if h <= 0 {
		lines := append([]string{}, top...)
		lines = append(lines, t.rule(w))
		lines = append(lines, body...)
		lines = append(lines, t.composerLinesCapped(0)...)
		lines = append(lines, bar)
		return lines, t.composerPrompt(belowComposer + inputRows - 1)
	}

	// 1. The popup takes what is left after the permanent rows and a conversation floor. It
	// GROWS as the user types, so it is the part that can push the frame past the bottom of the
	// window if it is not capped here. It always keeps at least one row: a popup that shows the
	// command it is offering is worth a row, and the rest is a keystroke away.
	//
	// The cap is applied to the popup's OWN row count, which is what it will actually draw —
	// completionLinesCapped is given this number and returns exactly that many rows, trailing
	// hint included. Capping against a reservation instead of against the drawn rows is how the
	// frame came out one row too tall.
	if popup > 0 {
		if spare := h - permanentRows - minChatLines; popup > spare {
			popup = spare
			if popup < 1 {
				popup = 1
			}
		}
	}

	// 2. The conversation takes the remainder, and the total is exactly h. `room` is a RESIDUE,
	// computed once and never re-decided: the size gate above refused anything shorter than
	// minHeight, which already includes the permanent rows and a conversation floor, so
	// subtracting the capped popup can only bring it down to that floor.
	room := h - permanentRows - popup

	// 3. Anchor the window to the newest line unless the user lifted it. The offset is applied
	// before trimming, so paging walks one row at a time instead of jumping by whatever the
	// current window happens to hold.
	if t.scroll > 0 {
		end := len(body) - t.scroll
		if end < 0 {
			end = 0
		}
		body = body[:end]
	}

	// 4. Trim to the room, then PAD back up to it. The padding is what puts the composer on the
	// last rows of the window instead of letting it float in the middle of a short conversation:
	// the input belongs at the foot of the screen.
	//
	// A conversation loses its OLDEST rows, because the newest are what the user is following.
	// The welcome screen is the opposite: it is read from the top, and losing its greeting to
	// keep its last tip would be cutting the wrong end.
	if len(body) > room {
		if len(t.messages) == 0 {
			body = body[:room]
		} else {
			hidden := len(body) - room + 1
			body = append([]string{t.plainLine(t.muted(t.trf("... %d earlier lines", hidden)))},
				body[len(body)-room+1:]...)
		}
	}
	for len(body) < room {
		body = append(body, "")
	}

	lines := make([]string, 0, permanentRows+popup+len(body))
	lines = append(lines, top...)
	lines = append(lines, t.rule(w))
	lines = append(lines, body...)
	lines = append(lines, t.composerLinesCapped(popup)...)
	lines = append(lines, bar)

	// The cursor is walked back up from the end of the frame to the row of the input field
	// that holds the draft. composerPrompt refines the row inside the box from the wrap, so the
	// caller only has to count the rows BELOW the cursor's row: everything drawn under the input
	// and the box's own rows that come after it.
	//
	// The popup is NOT one of them. It is drawn ABOVE the input box (composerLinesCapped draws
	// it first), so counting its rows would put the cursor that many rows too high — on a line of
	// the popup instead of in the box the user is typing into.
	rowsBelow := belowComposer + inputRows - 1
	return lines, t.composerPrompt(rowsBelow)
}

// rowsBelowComposer is how many rows sit under the input field: the box's bottom border and the
// footer. It is a package constant rather than a local of layout because the cursor arithmetic needs
// the same number, and two definitions of "how tall is the composer" would drift apart.
const rowsBelowComposer = 2

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
//
// It shows READINESS only. A running turn is shown where the work is - on the answer being
// written and on the input box - and a third spinner in the corner would be the same fact drawn
// three times.
func (t *TUI) stateGlyph() string {
	if llmCfg := t.Runner.Config().LLM; llmCfg.APIKey == "" && config.LLMNeedsKey(llmCfg) {
		return t.color(colError, 0, glyphMissing)
	}
	return t.color(colSuccess, 0, glyphReady)
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
		lines = append(lines, t.cell(t.muted(t.trf("... %d earlier messages", len(t.messages)-len(msgs))), inner))
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
		t.trf("No line matches %q", t.query),
		"",
		t.tr("Ctrl+F  search again"),
		t.tr("Esc     show the whole conversation"),
	} {
		lines = append(lines, t.cell(t.muted(l), inner))
	}
	return append(lines, t.cell("", inner))
}

// emptyState is a designed first screen, not a blank one: what the view is for, what to type,
// how the two modes differ, the handful of commands a newcomer needs, and - when the setup is not
// usable yet - exactly how to fix it.
//
// Each block is short and wrapped to the width, so the screen reads the same on a netbook console
// as on a wide terminal. On a short terminal the layout keeps its TOP, which is why the most
// useful lines come first.
func (t *TUI) emptyState(inner int) []string {
	width := inner - leftMargin
	lines := []string{t.cell("", inner)}
	// The static texts below are translated here, once; a line built from parts is translated
	// where it is built, as one format, so the catalog never holds a fragment.
	text := func(s string, col int) {
		for _, l := range wordWrap(t.tr(s), width) {
			lines = append(lines, t.cell(t.color(col, 0, l), inner))
		}
	}
	gap := func() { lines = append(lines, t.cell("", inner)) }
	// pairs draws a short key/description table, with the keys in the accent colour and their
	// descriptions aligned. A row that does not fit drops its description rather than wrapping
	// in the middle of the table.
	pairs := func(rows [][2]string) {
		kw := 0
		for _, r := range rows {
			if n := visibleLen(r[0]); n > kw {
				kw = n
			}
		}
		for _, r := range rows {
			row := "  " + t.color(colAccent, 0, r[0]) + strings.Repeat(" ", kw-visibleLen(r[0])) + "  " + t.muted(t.tr(r[1]))
			if !t.fits(row, width) {
				row = "  " + t.color(colAccent, 0, r[0])
			}
			lines = append(lines, t.cell(row, inner))
		}
	}

	// examples draws what the user could type, after the mark their messages carry once sent.
	examples := func(ex ...string) {
		for _, e := range ex {
			for _, l := range wordWrap(t.tr(e), width-4) {
				lines = append(lines, t.cell("  "+t.color(colAccent, 0, glyphUser)+" "+l, inner))
			}
		}
	}

	if t.Notice != "" {
		text(t.Notice, colSuccess)
		gap()
	}

	switch t.screen {
	case ScreenPlan:
		text("Plan mode: ask anything about your project.", colBase)
		text("motita reads files and runs read-only commands to answer. Nothing is changed.", colMuted)
		gap()
		text("Try something like", colMuted)
		examples("how is the configuration loaded?", "what would it take to add a --json flag?")
		gap()
		pairs([][2]string{{"Tab", "back to Task mode, to make changes"}})
	case ScreenModels:
		text("Press Enter to ask the provider which models it offers.", colBase)
		text("It also checks that the endpoint and the key in your setup really work.", colMuted)
		gap()
		pairs([][2]string{{"/models <id>", "switch to that model for this session"}})
	case ScreenConfig:
		text("Press Enter to run the setup again: provider, sign-in, model and check.", colBase)
		text("Your current settings are kept as a backup, and the new ones apply right away.", colMuted)
	default:
		text("What should motita do? Describe the task and press Enter.", colBase)
		text("It makes the change, then runs your check to prove it worked before calling it done.", colMuted)
		gap()
		text("Try something like", colMuted)
		examples("add a --verbose flag and a test for it", "fix the failing test in the parser")
		gap()
		pairs([][2]string{
			{"Tab", "Plan mode: ask questions, nothing is changed"},
			{"/models", "choose another model"},
			{"/config", "change provider, key or check"},
			{"?", "every command and key"},
		})
	}

	// A setup that cannot answer is said HERE, on the screen the user is looking at, with the
	// fix. A red dot in a corner is not an explanation.
	if llmCfg := t.Runner.Config().LLM; llmCfg.APIKey == "" && config.LLMNeedsKey(llmCfg) {
		gap()
		text(t.trf("%s No API key for %s yet: type /config to add one,", glyphMissing, providerName(llmCfg.Provider)), colWarning)
		text(t.trf("  or export %s before starting motita.", config.ProviderKeyVariable(llmCfg.Provider)), colWarning)
	}
	return append(lines, t.cell("", inner))
}

// providerName is the provider as the top bar shows it, with the default spelled out.
func providerName(p string) string {
	if p == "" {
		return "openai"
	}
	return p
}

// messageLines renders one turn: a header that identifies the speaker, then the
// body on a rail. The rail is what makes a long conversation easy to follow and
// it costs a single column.
func (t *TUI) messageLines(m Message, inner int) []string {
	switch m.Author {
	case AuthorUser:
		head := t.color(colAccent, 0, glyphUser+" ") + t.muted(t.tr("you"))
		return append([]string{t.cell(head, inner)}, t.railLines(m.Text, inner, colBase, colMuted)...)

	case AuthorAgent:
		// A frozen line holds a label that was already formatted (a tool
		// announcement), so it is shown as an event and never as answer text.
		if m.Frozen {
			return []string{t.cell(t.muted("  "+glyphAgent+" ")+t.color(colAccent, 0, t.eventLabel(m.Text)), inner)}
		}
		if label, ok := toolLabel(m.Text); ok {
			return []string{t.cell(t.muted("  "+glyphAgent+" ")+t.color(colAccent, 0, t.eventLabel(label)), inner)}
		}
		head := t.color(colSuccess, 0, glyphAgent+" motita")
		body := colBase
		if m.Pending {
			head += "  " + t.color(colWarning, 0, spinner[t.spin%len(spinner)]+" "+t.tr("working"))
			body = colWarning
		}
		if m.Preformatted {
			lines := []string{t.cell(head, inner)}
			for _, l := range strings.Split(m.Text, "\n") {
				lines = append(lines, t.cell(t.color(body, 0, clipLine(l, inner-leftMargin)), inner))
			}
			return lines
		}
		return append([]string{t.cell(head, inner)}, t.railLines(m.Text, inner, body, colMuted)...)

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

// cell is one conversation row: the left margin, the text, and blank space to the edge.
//
// There is no rail and no right border. The conversation is the primary content, and the
// guide's rule is to separate with spacing and the two rules rather than with a box around
// the only thing on screen.
//
// The padding is what keeps the frame from filling the last column, so no row of this
// interface can trigger a terminal's auto-wrap.
func (t *TUI) cell(s string, available int) string {
	// available is the whole row width, margin included. The margin is spent first and the
	// text fills the rest, so every row is exactly as wide as the drawing area — which is
	// what keeps the two rules aligned with the content between them.
	//
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
//
// Colours 8 to 15 are the BRIGHT half of the palette, and they have codes of their own (90-97 and
// 100-107). Adding 8 to the base code, which is what this used to do for the muted colour, wrote
// "ESC[38m" - not a colour at all but the introducer of an extended one, missing its parameters.
// A terminal is free to ignore it or to read the next sequence as its arguments, which is how the
// hints, the rules and every secondary line came out in the default colour, or not at all.
func (t *TUI) color(fg, bg int, s string) string {
	if t.NoColor {
		return s
	}
	if bg == 0 {
		return fmt.Sprintf("\x1b[%dm%s\x1b[0m", paletteCode(30, fg), s)
	}
	return fmt.Sprintf("\x1b[%d;%dm%s\x1b[0m", paletteCode(30, fg), paletteCode(40, bg), s)
}

// paletteCode is the SGR code of a palette colour: base+c for the first eight, and base+60+(c-8) for the
// bright ones (30 -> 90 for a foreground, 40 -> 100 for a background).
func paletteCode(base, c int) int {
	if c >= 8 {
		return base + 60 + c - 8
	}
	return base + c
}

// wordWrap breaks text into lines of at most `width` COLUMNS.
//
// The width is measured with visibleLen, so an escape sequence costs nothing and a multi-byte
// rune costs one column — which is what keeps an accented or emoji line from being counted as
// several columns too wide.
//
// Two things are done in one pass here, and both matter on the hot path (this runs for every
// conversation block on every keystroke):
//
//   - the running width is kept as a NUMBER instead of being recovered with
//     visibleLen(cur.String()) once per word. The old form re-scanned everything accumulated so
//     far for every word added, which is O(n^2) in the length of a paragraph.
//   - a word that does not fit is hard split, whether or not the line is still empty. Hard
//     splitting only when `cur` was empty meant a long word AFTER a short one was emitted whole
//     and broke the promise this function makes: `wordWrap("one accents", 4)` returned a
//     7-column line for a 4-column width.
//
// The caller then hands that line to cell(), which clips it to the panel and marks the cut with
// an ellipsis. So the visible damage is SILENT TRUNCATION: a path or a URL in a conversation
// block loses its tail and the reader has no way to know something is missing — they see a
// plausible-looking shorter path. The width invariant is therefore asserted directly in
// TestWordWrapWidthIsMeasuredInVisibleColumns, which is the regression test for this bug.
func wordWrap(s string, width int) []string {
	if width <= 0 {
		return []string{s}
	}
	var lines []string
	for _, para := range strings.Split(s, "\n") {
		// A blank line between paragraphs is kept, once: it is what separates the steps of an
		// answer, and dropping it ran a list and the sentence after it together. Runs of blank
		// lines are collapsed, and the ones at either end are trimmed below.
		if strings.TrimSpace(para) == "" {
			if len(lines) > 0 && lines[len(lines)-1] != "" {
				lines = append(lines, "")
			}
			continue
		}
		var cur strings.Builder
		curWidth := 0

		flush := func() {
			if cur.Len() > 0 {
				lines = append(lines, strings.TrimSpace(cur.String()))
				cur.Reset()
				curWidth = 0
			}
		}

		for _, word := range strings.Fields(para) {
			w := visibleLen(word)
			// The separator costs a column when something is already on the line.
			need := w
			if curWidth > 0 {
				need++
			}
			if curWidth+need <= width {
				if curWidth > 0 {
					cur.WriteByte(' ')
					curWidth++
				}
				cur.WriteString(word)
				curWidth += w
				continue
			}
			// The word does not fit on the current line.
			flush()
			if w <= width {
				cur.WriteString(word)
				curWidth = w
				continue
			}
			// Wider than a whole line: hard split it into width-sized pieces, keeping the
			// remainder on the current line so the next word can share it.
			for w > width {
				cut, piece := splitAtWidth(word, width)
				lines = append(lines, piece)
				word = word[cut:]
				w = visibleLen(word)
			}
			if w > 0 {
				cur.WriteString(word)
				curWidth = w
			}
		}
		flush()
	}
	if n := len(lines); n > 0 && lines[n-1] == "" {
		lines = lines[:n-1]
	}
	if len(lines) == 0 {
		lines = append(lines, "")
	}
	return lines
}

// splitAtWidth cuts s after the first `width` COLUMNS and returns the byte offset of the cut
// together with the piece before it.
//
// It walks runes rather than taking s[:width]: a rune that is not ASCII occupies one column but
// several bytes, so cutting by byte offset would slice an encoding in half, and the halves of a
// broken UTF-8 sequence are not text — the terminal draws them as replacement characters.
//
// The offset returned is always > 0 for a non-empty s — a rune is at least one byte, and an
// invalid byte is decoded as a one-byte rune — which is what makes the caller's split loop
// terminate instead of spinning on a zero-length piece.
//
// Callers pass uncoloured text: wordWrap runs BEFORE t.color, so there are no escape sequences
// here to step over. If that ever changes, this needs the same state machine scanEscapes uses.
func splitAtWidth(s string, width int) (int, string) {
	cols := 0
	for i := 0; i < len(s); {
		if cols == width {
			return i, s[:i]
		}
		_, size := utf8.DecodeRuneInString(s[i:])
		i += size
		cols++
	}
	return len(s), s
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

// visibleLen returns how many COLUMNS a line occupies, escapes excluded.
//
// No ESC byte means every rune is text, so the count is taken directly and the closure-per-rune
// state machine is skipped. That is the common case — this function is called per word while
// wrapping and per line while fitting the frame — and the machine was 36% of the per-keystroke
// profile even when there was nothing to unescape.
func visibleLen(s string) int {
	if strings.IndexByte(s, 0x1b) < 0 {
		return utf8.RuneCountInString(s)
	}
	n := 0
	scanEscapes(s, func(_ rune, isEscape bool) {
		if !isEscape {
			n++
		}
	})
	return n
}

// stripANSI removes every escape sequence, for no-colour mode.
//
// A line with no ESC byte is already stripped, so it is returned as-is instead of being copied
// rune by rune through the state machine.
func stripANSI(s string) string {
	if strings.IndexByte(s, 0x1b) < 0 {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
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

// statusLines is the top bar: the name of the program on the left, and on the right whether the
// setup can answer, which provider and model it answers with, and how hard the model thinks.
//
// The right side is shed from its tail on a narrow terminal - the reasoning level, then the
// filter - and the model is never the thing dropped: it is what the bar is for. When the setup has
// no key, the bar says so in words, because a coloured dot alone is not an instruction.
func (t *TUI) statusLines(w int) []string {
	cfg := t.Runner.Config()
	model := cfg.LLM.Model
	if model == "" {
		model = t.tr("unknown")
	}
	reasoning := "off"
	if cfg.LLM.Reasoning.Enabled {
		reasoning = cfg.LLM.Reasoning.Level
	}

	left := t.brand(glyphAgent + " motita")
	if v := strings.TrimSpace(t.Version); v != "" {
		left += "  " + t.muted(v)
	}

	parts := []string{t.stateGlyph() + " " + t.color(colBase, 0, providerName(cfg.LLM.Provider)) + t.muted(" "+glyphMid+" ") + t.color(colBase, 0, model)}
	// A missing key is said in words, beside the model it is missing for, and it is kept longer
	// than the reasoning level when the line is shed: it is the reason nothing will work.
	if cfg.LLM.APIKey == "" && config.LLMNeedsKey(cfg.LLM) {
		parts = append(parts, t.color(colError, 0, t.tr("no API key"))+t.muted(" (/config)"))
	}
	parts = append(parts, t.muted(t.tr("reasoning")+" ")+t.color(colBase, 0, reasoning))
	if t.query != "" || t.searching {
		parts = append(parts, t.color(colAccent, 0, t.trf("filter %s", strconv.Quote(t.query))))
	}

	room := w - leftMargin - 1
	right := strings.Join(parts, t.muted("  "+glyphMid+"  "))
	for visibleLen(left)+2+visibleLen(right) > room && len(parts) > 1 {
		parts = parts[:len(parts)-1]
		right = strings.Join(parts, t.muted("  "+glyphMid+"  "))
	}
	// Too narrow for the name AND the model: the model wins, it is the line's whole purpose.
	if visibleLen(left)+2+visibleLen(right) > room {
		return []string{t.plainLine(right)}
	}
	gap := room - visibleLen(left) - visibleLen(right)
	return []string{t.plainLine(left + strings.Repeat(" ", gap) + right)}
}

// bottomBar is the footer: the keys that work RIGHT NOW on the left, and how much of the model's
// context is in use on the right.
//
// The hints follow what the user is doing - typing a command, reading back through history, waiting
// for a run, searching - because a list of every key is a manual, and a list of the three that work
// at this moment is an instruction.
func (t *TUI) bottomBar(w int) string {
	left := t.keyHints()
	// The agents segment goes before the context gauge: it is what changes while a run works.
	right := t.muted(strings.Join(nonEmpty(t.agentsLabel(), t.contextLabel()), "  "+glyphMid+"  "))
	if t.scroll > 0 {
		left = t.color(colWarning, 0, t.trf("%s %d lines up", glyphDot, t.scroll)) + t.muted("  "+glyphMid+"  ") + left
	}

	// The available columns are the frame minus the ONE margin plainLine will add: the right
	// edge is the last usable column, and nothing is reserved twice.
	room := w - leftMargin - 1

	// Hints are dropped from the END until the line fits: the first ones are the most important.
	hints := t.hintList()
	for visibleLen(left)+1+visibleLen(right) > room && len(hints) > 1 {
		hints = hints[:len(hints)-1]
		left = t.formatHints(hints)
		if t.scroll > 0 {
			left = t.color(colWarning, 0, t.trf("%s %d lines up", glyphDot, t.scroll)) + t.muted("  "+glyphMid+"  ") + left
		}
	}
	gap := room - visibleLen(left) - visibleLen(right)
	if gap < 1 {
		// Too narrow for both: the keys are what the user acts with, the context is a gauge.
		return t.plainLine(left)
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
	return t.trf("context %d%% (%d/%d)", int(s.Used*100+0.5), s.Tokens, s.Window)
}

// hintList is the keys that work right now, most important first.
func (t *TUI) hintList() [][2]string {
	switch {
	case t.answeringConfirm():
		return [][2]string{{"y", "run it"}, {"n", "don't"}, {"Enter", "don't"}}
	case t.asking():
		return [][2]string{{"1-9", "pick"}, {"←→", "question"}, {"Enter", "send answers"}, {"Esc", "close"}}
	case t.searching:
		return [][2]string{{"Enter", "apply"}, {"Esc", "clear"}}
	case t.completing():
		return [][2]string{{"↑↓", "choose"}, {"Enter", "run"}, {"→", "complete"}, {"Esc", "close"}}
	case t.busy:
		return [][2]string{{"Esc", "stop"}, {"PgUp/PgDn", "scroll"}, {"^C", "quit"}}
	case t.scroll > 0:
		return [][2]string{{"PgUp/PgDn", "scroll"}, {"End", "newest"}}
	case t.query != "":
		return [][2]string{{"Esc", "show everything"}, {"^F", "search again"}}
	}
	other := "Plan mode"
	if t.screen == ScreenPlan {
		other = "Task mode"
	}
	return [][2]string{{"Enter", "send"}, {"Tab", other}, {"/", "commands"}, {"?", "help"}, {"^C", "quit"}}
}

// keyHints is the hint list formatted for the footer.
func (t *TUI) keyHints() string { return t.formatHints(t.hintList()) }

func (t *TUI) formatHints(hints [][2]string) string {
	parts := make([]string, 0, len(hints))
	for _, h := range hints {
		parts = append(parts, t.color(colAccent, 0, h[0])+" "+t.muted(t.tr(h[1])))
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
	// The confirmation window draws in the popup's place, and it takes precedence over the
	// completion popup: it is answered during a run, when nothing is being typed, and it asks
	// about the one thing that must not be missed.
	//
	// The questions window draws there too. The input box below is NOT hidden for either of
	// them: the answer field and the free-answer row are the same thing, so the user can see
	// exactly what will be sent while picking an option or typing one.
	if t.answeringConfirm() {
		lines = append(lines, t.confirmLines(popupCap)...)
	} else if t.asking() {
		lines = append(lines, t.askLines(popupCap)...)
	} else if t.showingAgents() {
		lines = append(lines, t.agentsLinesCapped(popupCap)...)
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

// inputBoxLines draws the input box: a top border that names the mode (what Enter will do), the
// field with what has been typed, and a bottom border.
//
// The box is what makes the input findable at a glance: a prompt on a bare line looks like one
// more line of the conversation. It is a FIXED height, so the frame never changes shape while the
// user types, and the text is wrapped inside it rather than scrolled.
func (t *TUI) inputBoxLines() []string {
	w, _ := t.size()
	outer := w - leftMargin - 1 // the same span as the rule above the conversation
	inner := t.inputWidth()

	text := t.composerLabel() + t.shownDraft()
	wrapped := wrapVisible(text, inner)
	if len(wrapped) > inputRows {
		// Keep the END: the user is typing there, and the tail is what matters.
		wrapped = wrapped[len(wrapped)-inputRows:]
	}

	side := t.muted(glyphRail)
	out := make([]string, 0, inputRows+2)
	out = append(out, t.plainLine(t.boxTop(outer)))
	for i := 0; i < inputRows; i++ {
		row := ""
		if i < len(wrapped) {
			row = wrapped[i]
		}
		out = append(out, t.plainLine(side+" "+row+strings.Repeat(" ", inner-visibleLen(row))+" "+side))
	}
	out = append(out, t.plainLine(t.muted(glyphBotLeft+strings.Repeat(glyphRule, outer-2)+glyphBotRight)))
	return out
}

// inputWidth is the number of columns inside the input box: the box spans the drawing area, and
// its two sides and the space beside each take four.
func (t *TUI) inputWidth() int {
	w, _ := t.size()
	return w - leftMargin - 1 - 4
}

// boxTop is the input box's top border, with the mode as its title: what the line will be used
// for, and - while a turn runs - that it is running and how to stop it.
//
// The title is shortened from its tail when the terminal is narrow. The mode's NAME is never the
// part dropped: it is the one word that says what Enter does.
func (t *TUI) boxTop(outer int) string {
	name, about := t.modeTitle()
	name, about = t.tr(name), t.tr(about)
	head := t.color(colAccent, 0, name)
	desc := t.muted(" " + glyphMid + " " + about)
	working := ""
	if t.busy {
		working = "  " + t.color(colWarning, 0, spinner[t.spin%len(spinner)]+" "+t.tr("working")) + t.muted(t.tr(", Esc stops"))
	}
	// The longest title that fits wins: everything; the name and the running notice; the name and
	// what the mode does; the name and a bare "working"; the name alone.
	candidates := []string{head + desc + working, head + working, head + desc}
	if t.busy {
		candidates = append(candidates, head+" "+t.color(colWarning, 0, spinner[t.spin%len(spinner)]+" "+t.tr("working")))
	}
	// ┌─ title ─...─┐ : the corners, the first rule and the two spaces cost five columns.
	room := outer - 5
	title := head
	for _, c := range candidates {
		if visibleLen(c) <= room {
			title = c
			break
		}
	}
	// No floor on the fill: the narrowest frame the size gate accepts leaves room for the longest
	// mode name, so the last candidate - the name alone - always fits.
	fill := room - visibleLen(title)
	return t.muted(glyphTopLeft+glyphRule+" ") + title + t.muted(" "+strings.Repeat(glyphRule, fill)+glyphTopRight)
}

// modeTitle is what the input box is for right now, and a few words on what that means.
func (t *TUI) modeTitle() (string, string) {
	switch {
	case t.answeringConfirm():
		return "Approve", "the agent is waiting for your answer"
	case t.asking():
		return "Answer", "type an answer, or pick an option above"
	case t.searching:
		return "Search", "filter the conversation"
	}
	switch t.screen {
	case ScreenPlan:
		return "Plan", "read-only: explores and explains"
	case ScreenModels:
		return "Models", "Enter lists them, /models <id> switches"
	case ScreenConfig:
		return "Setup", "Enter runs the setup again"
	}
	return "Task", "makes changes, then proves them with your check"
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
	find := t.color(colAccent, 0, t.tr("find")) + t.muted(" > ")
	if t.searching {
		return find
	}
	if t.query != "" {
		// An applied filter has to be visible at all times, count included: without it the
		// user is looking at a short history and has no way to tell why, and the count is
		// what makes the number of rows on screen agree with what they are being shown.
		label := t.muted(t.trf("filter %s", strconv.Quote(t.query))+"  ") +
			t.color(colAccent, 0, t.trf("%d lines", len(t.matchingMessages())))
		return label + t.muted("  ") + find
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
	wrapped := wrapVisible(t.composerLabel()+t.shownDraft(), t.inputWidth())
	row := len(wrapped) - 1
	// The text starts after the margin, the box's side and the space beside it.
	col := leftMargin + 2 + visibleLen(wrapped[row])
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
	return cursorMoveSeq(rowsBelow, col+1) // columns are 1-based
}

// cursorMoveSeq is the sequence that takes the cursor from the last written row to a given row
// above it and a given column.
//
// A count of ZERO is NOT "move zero rows": in a CSI sequence an omitted OR zero parameter takes
// its DEFAULT, which for CSI A is one row. So "\x1b[0A" walks the cursor up exactly like
// "\x1b[1A" — and that is what the user saw as "the cursor moves up while I type": the cursor
// was already on the input's first row, the layout computed a walk-up of zero from the wrap, and
// the zero was emitted as a sequence the terminal executed as a move of one. The cursor left the
// text and sat on the blank row above the box.
//
// Zero rows up is therefore expressed by NOT writing the CSI A at all, which leaves the column
// move as the whole sequence. The column is always >= 1 (it carries the left margin plus the
// prompt glyph), so it is never the parameter that needs this treatment.
func cursorMoveSeq(up, col int) string {
	if up <= 0 {
		return fmt.Sprintf("\x1b[%dG", col)
	}
	return fmt.Sprintf("\x1b[%dA\x1b[%dG", up, col)
}

// cursorMove turns the layout's cursor move into one that is correct for the rows actually
// written.
//
// layout() returns a move that walks up from the END of the frame, and that count is the distance
// from the frame's last row to the CARET's row — the row the wrap put the caret on, not simply the
// input box's first row. An incremental repaint stops at the LAST row it wrote, so from there the
// caret's row is a known distance in one of two directions, and only the direction that reaches it
// may be used.
//
// Passing the layout's walk-up through unchanged had two faults, and both left the cursor somewhere
// other than the text:
//
//   - a correction that came out at ZERO was still written as a sequence, and "\x1b[0A" is not
//     "move zero rows": a CSI parameter of zero takes its default, so it walks the cursor one row
//     UP. Measured on a real terminal: once a character had been typed the cursor left the input
//     and sat on the blank row above the box, on every keystroke.
//   - when the last written row was ABOVE the caret's row, the count was applied as a walk-up
//     again: from row 20, "up 25" is not "down 5", and the cursor landed on row 0 — the top of the
//     screen — instead of in the input.
//
// Both shapes are now one expression with no sign to get wrong, and a walk-up of zero is expressed
// by not writing the row move at all.
func (t *TUI) cursorMove(prompt string, lastWritten, total int) string {
	if lastWritten < 0 {
		// Nothing was written: the terminal's cursor has not moved. Returning the layout's
		// walk-up here would still execute it, and a mouse event that arrives in an idle
		// chat would then jump the cursor up to wherever the layout decided, which is
		// exactly what shows up as the input moving while the user only moved the pointer.
		// The only thing that has to change is the visibility flag, drawn separately.
		return ""
	}
	// The walk-up is the distance from the end of the frame to the caret's row; the column is the
	// part the layout computed from the wrap, and re-deriving either here would be a second
	// implementation of the same calculation. A prompt that does not parse is passed through
	// unchanged rather than dropped.
	up, col, ok := parseCursorMove(prompt)
	if !ok {
		return prompt
	}

	// Where the caret's row is, counted from the last row written: positive DOWN, negative UP.
	target := total - 1 - up
	delta := lastWritten - target

	switch {
	case delta > 0:
		return cursorMoveSeq(delta, col)
	case delta < 0:
		// The caret's row is BELOW the last one written, which happens when a repaint above the
		// input was all that changed: the user pressed a key whose only visible effect was
		// earlier in the conversation. CSI B goes down without a count measured from the wrong
		// end.
		return cursorDownSeq(-delta, col)
	default:
		// The last written row IS the caret's row, which is the ordinary case while typing.
		return cursorMoveSeq(0, col)
	}
}

// cursorDownSeq is the descending counterpart of cursorMoveSeq, with the same rule about zero:
// "\x1b[0B" is one row DOWN, not none, so a count of zero is expressed by the column move alone.
func cursorDownSeq(down, col int) string {
	if down <= 0 {
		return fmt.Sprintf("\x1b[%dG", col)
	}
	return fmt.Sprintf("\x1b[%dB\x1b[%dG", down, col)
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
	// The confirmation window takes the reservation first. It is open during a run, when the
	// completion popup cannot be (nothing is being typed), so the two never compete.
	if t.answeringConfirm() {
		return t.confirmRows()
	}
	// The questions window takes the reservation the completion popup would use. The two cannot
	// be open together: the window CAPTURES the keys, so nothing is being typed into the input
	// for a completion to react to — and if both drew, the frame would be taller than the layout
	// measured and the interface would scroll on a keypress.
	if t.asking() {
		return t.askRows()
	}
	if t.showingAgents() {
		return t.agentsRows()
	}
	if !t.completing() {
		return 0
	}
	return len(completions(t.draft))
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

// eventLabel is a frozen event line (a tool announcement, the actions counter) in the
// interface's language. The line is stored in English, as it was produced, and translated each
// time it is drawn, so switching the language redraws the history too.
func (t *TUI) eventLabel(text string) string {
	if text == "using a tool" {
		return t.tr(text)
	}
	if rest, ok := strings.CutPrefix(text, "using "); ok {
		return t.trf("using %s", rest)
	}
	if n, ok := strings.CutSuffix(text, " actions"); ok {
		if count, err := strconv.Atoi(n); err == nil {
			return t.trf("%d actions", count)
		}
	}
	return t.tr(text)
}
