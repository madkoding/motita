package tui

import (
	"fmt"
	"strconv"
	"strings"
)

// The input box at the foot of the frame, its title, and the cursor walked back into it.

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
		working = "  " + t.color(colWarning, 0, spinner[t.spin%len(spinner)]+" "+t.tr("working")) +
			t.muted(t.tr(", Esc stops"))
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
	default:
		// Nothing is being answered or searched: the screen decides below.
	}
	switch t.screen {
	case ScreenPlan:
		return "Plan", "read-only: explores and explains"
	case ScreenModels:
		return "Models", "Enter lists them, /models <id> switches"
	case ScreenConfig:
		return "Setup", "Enter runs the setup again"
	case ScreenAuto:
		return "Auto", "picks plan or task from what you write"
	default:
		return "Task", "makes changes, then proves them with your check"
	}
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
	state := escNone
	for _, r := range s {
		// An escape sequence is copied whole and costs no columns. It is never split: a
		// truncated escape would leak colour into the rest of the frame. Only its first byte
		// used to be recognised, so the rest of "ESC[38;5;75m" was counted as text and the
		// box wrapped earlier than the room it had.
		var isEscape bool
		state, isEscape = escStep(state, r)
		if isEscape {
			cur.WriteRune(r)
			continue
		}
		rw := runeWidth(r)
		if curVis+rw > width && curVis > 0 {
			out = append(out, cur.String())
			cur.Reset()
			curVis = 0
		}
		cur.WriteRune(r)
		curVis += rw
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
