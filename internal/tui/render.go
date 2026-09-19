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
	glyphDot      = "\u2022" // • status dot
	glyphUser     = "\u00bb" // » the user's turn
	glyphAgent    = "*"      // the agent's turn, and the star of the wordmark
	glyphMid      = "\u00b7" // · separator inside a line
)

// Layout metrics. They are named because the frame arithmetic depends on them: a
// single off-by-one puts the right border of the panel out of alignment.
const (
	defaultWidth  = 80
	minWidth      = 44
	maxWidth      = 116
	maxScrollback = 400
	// leftMargin is the breathing room between the interface and the terminal
	// edge. Nothing is ever drawn in the first column.
	leftMargin = 2
	// minChatLines is the smallest conversation area the layout keeps before it
	// starts dropping the oldest lines.
	minChatLines = 4
)

// bannerLines is the Starlight wordmark: five shaded rows that carry their own
// ANSI colours, so they are stored raw and only placed by the layout. In
// no-colour mode the escape sequences are stripped before printing.
var bannerLines = []string{
	"\x1b[0;97m\u2580\u2580\u2580\u2580\x1b[0;37m\u2580\u2588\u2588\u2588 \u2580\u2580\u2588\u2588\u2588\u2580\u2580 \x1b[0;97m\u2584\x1b[0;97;47m\u2593\u2592\x1b[0;37m\u2580\u2580\u2588\u2588\u2584 \x1b[0;97m\u2580\u2580\u2580\x1b[0;37m\u2580\u2580\u2588\u2588\u2584 \x1b[0;97m\u2580\u2580\u2580\x1b[0;37m      \x1b[0;97m\u2588\x1b[0;97;47m\u2593\u2592\x1b[0;37m \x1b[0;97m\u2580\u2580\u2580\x1b[0;37m\u2580\u2580\u2588\x1b[0;90;47m\u2591\u2592\x1b[0;37m \x1b[0;97m\u2588\x1b[0;97;47m\u2593\u2592\x1b[0;37m  \u2588\u2588\u2588 \u2580\u2580\u2588\u2588\u2588\u2580\u2580\x1b[0m",
	"\x1b[0;97;47m\u2593\u2592\u2591\x1b[0;37m  \u2580\u2580\u2580 \x1b[0;90m\u2593\x1b[0;37m \u2588\u2588\u2588 \x1b[0;90m\u2593\x1b[0;37m \x1b[0;97m\u2580\u2580\u2580\x1b[0;37m  \u2588\u2588\u2588 \x1b[0;97;47m\u2593\u2592\u2591\x1b[0;37m  \u2588\u2588\u2588 \x1b[0;97;47m\u2593\u2592\u2591\x1b[0;90m\u2590\u2588\u2588\u2588\u2588\x1b[0;37m \x1b[0;97m\u2580\u2580\u2580\x1b[0;37m \x1b[0;97;47m\u2593\u2592\u2591\x1b[0;90m\u2590\u258c\x1b[0;37m\u2580\u2580\u2580 \x1b[0;97m\u2580\u2580\u2580\x1b[0;37m  \u2588\u2588\u2588 \x1b[0;90m\u2593\x1b[0;37m \u2588\u2588\u2588 \x1b[0;90m\u2593\x1b[0m",
	"\x1b[0;37m \u2580\u2580\u2580\u2580\u2588\u2588\u2584 \x1b[0;90m\u2588\x1b[0;37m \u2588\u2588\x1b[0;93;47m\u2591\x1b[0;37m \x1b[0;90m\u2588\x1b[0;37m \x1b[0;97;47m\u2592\u2591 \x1b[0;37m\u2580\u2580\u2588\u2588\u2588 \x1b[0;97;47m\u2592\u2591\x1b[0;37m\u2588\u2584\u2580\u2580\u2580  \x1b[0;97;47m\u2592\u2591\x1b[0;37m\u2588\x1b[0;90m\u2580\u2580\u2580\u2580\x1b[0;37m \x1b[0;97;47m\u2592\u2591 \x1b[0;37m \x1b[0;97;47m\u2592\u2591\x1b[0;37m\u2588 \u2584\u2584\u2584\u2584 \x1b[0;97;47m\u2592\u2591 \x1b[0;37m\u2580\u2580\u2588\u2588\u2588 \x1b[0;90m\u2588\x1b[0;37m \u2588\u2588\x1b[0;93;47m\u2591\x1b[0;37m \x1b[0;90m\u2588\x1b[0m",
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
func (t *TUI) layout(w, h int) ([]string, string) {
	prompt := t.promptLine()

	header := t.headerLines(w)
	status := t.statusLines(w)
	tabsRow := t.tabsLine(w)
	hints := t.hintLines(w)
	body := t.chatLines(t.inner())

	// A height of zero means the terminal did not say how tall it is. Nothing can
	// be trimmed then, so everything is drawn and the shell scrolls as usual.
	if h <= 0 {
		lines := append([]string{}, header...)
		lines = append(lines, status...)
		lines = append(lines, blank)
		lines = append(lines, t.chatTopRow()...)
		lines = append(lines, body...)
		lines = append(lines, t.chatBottomRow())
		lines = append(lines, blank)
		lines = append(lines, tabsRow)
		lines = append(lines, hints...)
		return lines, prompt
	}

	// rows is the total height of the frame for a given set of decisions. It
	// counts the rows the layout always spends — the status block, the two blank
	// separators, the two panel borders, the tab row and the prompt — plus the
	// parts that can be dropped.
	const fixed = 2 /* status */ + 2 /* blanks */ + 2 /* panel borders */ + 1 /* tabs */ + 1 /* prompt */
	rows := func(header, hints []string, bodyRows int) int {
		n := fixed + len(header) + bodyRows + len(hints)
		if len(header) > 0 {
			n++ // the blank row that separates the wordmark from the status line
		}
		return n
	}

	// 1. Shed the droppable parts first, in the documented order: the hints, then
	// the wordmark. Deciding this before the conversation is trimmed is what
	// leaves the maximum number of rows for the content: trimming first would
	// reserve space for branding that is about to be dropped anyway.
	for len(hints) > 0 && rows(header, hints, minChatLines) > h {
		hints = nil
	}
	for len(header) > 0 && rows(header, hints, minChatLines) > h {
		header = nil
	}

	// 2. Give the conversation everything that is left.
	room := h - (rows(header, hints, 0))
	if room < minChatLines {
		// Nothing left to drop and the terminal is still too small. The frame
		// overflows and the shell will scroll, which is still better than showing
		// a blank screen; the prompt is written last, so it stays at the bottom
		// of the last page.
		room = minChatLines
	}
	if len(body) > room {
		hidden := len(body) - room + 1
		body = append([]string{t.cell(t.muted(fmt.Sprintf("... %d earlier lines", hidden)), t.inner())},
			body[len(body)-room+1:]...)
	}

	lines := make([]string, 0, rows(header, hints, len(body)))
	lines = append(lines, header...)
	if len(header) > 0 {
		lines = append(lines, blank)
	}
	lines = append(lines, status...)
	lines = append(lines, blank)
	lines = append(lines, t.chatTopRow()...)
	lines = append(lines, body...)
	lines = append(lines, t.chatBottomRow())
	lines = append(lines, blank)
	lines = append(lines, tabsRow)
	lines = append(lines, hints...)
	return lines, prompt
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
func (t *TUI) drawFrame() {
	w, h := t.size()
	lines, prompt := t.layout(w, h)

	var b strings.Builder
	// Home, repaint, then clear whatever is left below: this avoids the blank
	// flash of a full clear and never scrolls the screen.
	//
	// Only "\n" is written: the interface never switches the terminal to raw
	// mode, so the driver's ONLCR translation is still on and "\n" already
	// becomes CR+LF. Writing "\r\n" would double the carriage return on a real
	// terminal.
	b.WriteString("\x1b[H")
	b.WriteString("\x1b[?25l") // keep the cursor out of the way while painting
	b.WriteString(strings.Join(lines, "\n"))
	b.WriteString("\n")
	b.WriteString("\x1b[J")
	b.WriteString(prompt)
	b.WriteString("\x1b[?25h") // and hand it back, sitting at the prompt
	fmt.Fprint(t.Out, b.String())
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
	if w > maxWidth {
		w = maxWidth
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

// frameCols is the total number of columns the interface may occupy.
func (t *TUI) frameCols() int {
	w, _ := t.size()
	if w > 1 {
		w--
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

// statusLines is the one line that answers "what am I talking to, and is it
// ready?". Labels are muted and values are base, so the values are what stands
// out. The API key is only ever reported as present or missing, never printed.
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
	keyText, keyCol := "missing", colError
	if cfg.LLM.APIKey != "" {
		keyText, keyCol = "present", colSuccess
	}
	reasoning := "off"
	if cfg.LLM.Reasoning.Enabled {
		reasoning = cfg.LLM.Reasoning.Level
	}
	state := t.stateGlyph()

	// Most important first: a narrow terminal drops the tail, never the model.
	parts := []string{
		state + " " + t.color(colBase, 0, provider) + t.muted("/") + t.color(colBase, 0, model),
		t.muted("reasoning ") + t.color(colBase, 0, reasoning),
		t.muted("key ") + t.color(keyCol, 0, keyText),
	}
	line := "  " + strings.Join(parts, t.muted("   "))
	for !t.fits(line, w-leftMargin) && len(parts) > 1 {
		parts = parts[:len(parts)-1]
		line = "  " + strings.Join(parts, t.muted("   "))
	}
	return []string{line, t.muted(strings.Repeat(glyphRule, w-leftMargin*2))}
}

// stateGlyph is the running indicator: a spinner while a turn is in flight, a
// green dot when the session is idle and ready, a red dot when a key is missing.
func (t *TUI) stateGlyph() string {
	if t.busy {
		return t.color(colWarning, 0, spinner[t.spin%len(spinner)])
	}
	if t.Runner.Config().LLM.APIKey == "" {
		return t.color(colError, 0, glyphDot)
	}
	return t.color(colSuccess, 0, glyphDot)
}

// chatTopRow is the top border of the conversation panel, with the current mode
// as its title.
func (t *TUI) chatTopRow() []string {
	title := t.screen.String()
	// The top rule is the bottom rule minus the width of the title block, which
	// occupies the corner, a rule and a space before the title, and a space after
	// it. Getting this wrong is only visible as a top border that stops short of
	// the right corner.
	//
	// No lower clamp is needed: size() floors the width at minWidth, which leaves
	// 30 columns of rule for the longest title ("Models"). A clamp would be
	// unreachable code that only looks like a safety net.
	rule := t.inner() - 1 - len(title)
	return []string{"  " + t.muted(glyphTopLeft+glyphRule+" ") + t.color(colAccent, 0, title) +
		" " + t.muted(strings.Repeat(glyphRule, rule)+glyphTopRight)}
}

// chatBottomRow is the bottom border of the conversation panel.
func (t *TUI) chatBottomRow() string {
	return "  " + t.muted(glyphBotLeft+strings.Repeat(glyphRule, t.inner()+2)+glyphBotRight)
}

// chatLines renders every visible message, oldest first.
func (t *TUI) chatLines(inner int) []string {
	if len(t.messages) == 0 {
		return t.emptyState(inner)
	}
	msgs := t.visibleMessages()
	var lines []string
	if len(t.messages) > len(msgs) {
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
		lines = append(lines, t.cell(t.muted(l), inner))
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
		return append([]string{t.cell(head, inner)}, t.railLines(m.Text, inner, body, colMuted)...)

	default:
		var lines []string
		for _, l := range wordWrap(m.Text, inner-2) {
			lines = append(lines, t.cell(t.muted(l), inner))
		}
		return lines
	}
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
func (t *TUI) railLines(text string, inner int, fg, railCol int) []string {
	if strings.TrimSpace(text) == "" {
		return nil
	}
	var lines []string
	for _, l := range wordWrap(text, inner-4) {
		lines = append(lines, t.cell(t.color(railCol, 0, glyphRail+" ")+t.color(fg, 0, l), inner))
	}
	return lines
}

// tabsLine shows the four modes. The active one is a filled block: the strongest
// focus signal a terminal has, and it never depends on colour alone.
func (t *TUI) tabsLine(w int) string {
	var parts []string
	for _, s := range screenOrder {
		label := s.String()
		if t.screen == s {
			parts = append(parts, t.color(0, colAccent, " "+label+" "))
		} else {
			parts = append(parts, t.muted(" "+label+" "))
		}
	}
	return t.fitLine(strings.Join(parts, " "), w, "  ")
}

// hintLines lists the keys that work right now, the key in the accent colour and
// its meaning muted. Hints that do not fit are dropped, never wrapped: a hint that
// is cut in half reads as a different key.
func (t *TUI) hintLines(w int) []string {
	hints := [][2]string{
		{"Tab", "switch mode"},
		{"/t", "task"},
		{"/p", "plan"},
		{"/m", "models"},
		{"/c", "config"},
		{"/r", "reasoning"},
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

// promptLine is the input prompt: the last line of the frame, with the cursor
// left right after it.
func (t *TUI) promptLine() string {
	return "  " + t.color(colBrand, 0, t.screen.String()) + t.muted(" > ")
}

// cell pads a decorated string to the panel width.
func (t *TUI) cell(s string, inner int) string {
	pad := inner - visibleLen(s)
	if pad < 0 {
		pad = 0
	}
	return "  " + t.muted(glyphRail+" ") + s + strings.Repeat(" ", pad) + t.muted(" "+glyphRail)
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
func (t *TUI) padCenter(s string, w int) string {
	pad := w - visibleLen(s)
	if pad <= 0 {
		return s
	}
	return strings.Repeat(" ", pad/2) + s
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
