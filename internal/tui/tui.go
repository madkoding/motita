// Package tui implements the interactive text-based user interface.
//
// It is deliberately built with the Go standard library only: no termios, no
// curses, no raw mode. The screen is redrawn in full frames so the whole flow
// is testable with bytes.Buffer and portable to every target platform.
//
// The interface is a conversational chat: the user types tasks or prompts at
// the bottom and the agent answers above, showing what it is doing in plain
// English (or Spanish) instead of JSON log lines.
//
// Ctrl+C (SIGINT, SIGTERM) is handled through the context: the caller cancels
// the context and the TUI returns immediately.
package tui

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/madkoding/starlight/internal/agent"
)

// Exit codes returned by the TUI.
const (
	ExitSuccess     = 0
	ExitError       = 1
	ExitInterrupted = 2
)

// Author identifies who wrote a chat line.
type Author int

const (
	AuthorUser Author = iota
	AuthorAgent
	AuthorSystem
)

func (a Author) String() string {
	switch a {
	case AuthorUser:
		return "you"
	case AuthorAgent:
		return "starlight"
	case AuthorSystem:
		return "system"
	}
	return "?"
}

// Screen is one of the main views.
type Screen int

const (
	ScreenTask Screen = iota
	ScreenPlan
	ScreenModels
	ScreenConfig
)

func (s Screen) String() string {
	switch s {
	case ScreenTask:
		return "Task"
	case ScreenPlan:
		return "Plan"
	case ScreenModels:
		return "Models"
	case ScreenConfig:
		return "Config"
	}
	return "?"
}

var screenOrder = []Screen{ScreenTask, ScreenPlan, ScreenModels, ScreenConfig}

// Message is one line in the conversation.
type Message struct {
	Author  Author
	Text    string
	Pending bool // true while the agent is still producing this line
	// Frozen marks a line that must never be written into again: a tool
	// announcement is an event that has already happened, and the output that
	// follows it belongs to the next block.
	Frozen bool
	// Preformatted marks text whose layout is part of its meaning: the help screen
	// is a two-column reference, and re-flowing it destroys the alignment that makes
	// it readable at a glance. Preformatted text is drawn line by line, exactly as
	// written (still clipped to the panel, never rewrapped).
	Preformatted bool
}

// TUI is the conversational terminal user interface.
type TUI struct {
	In      io.Reader
	Out     io.Writer
	Err     io.Writer
	Runner  Runner
	NoColor bool

	// Width and Height override the drawing area. Zero means "ask the
	// environment": tests set them to make the layout deterministic, and an
	// embedder can pin them to a fixed size.
	Width, Height int

	// lastFrame is the rows as they were last written, and paintedScreen says whether anything
	// has been written yet. Together they let a repaint write only what changed instead of the
	// whole frame, which is what made typing feel slow.
	//
	// They are only meaningful while draw is held: a frame is built and diffed under the same
	// lock, so the comparison cannot see a half-updated copy.
	lastFrame     []string
	paintedScreen bool

	screen     Screen
	messages   []Message
	reader     *bufio.Reader
	cancelRun  context.CancelFunc
	runningCtx context.Context
	// busy is true while a turn is in flight, which turns the status dot into a
	// spinner; spin is the animation frame advanced on every repaint.
	busy bool
	spin int
	// draw serialises painting. A frame is drawn from the input loop and from the
	// resize watcher, and both read the state the other mutates, so the whole paint
	// is held — rendering outside the lock and writing inside it would still race on
	// Width, scroll and messages. -race caught exactly that.
	draw sync.Mutex
	// ask is the window the agent's questions are answered in, and nil when there is nothing
	// being asked. It is view state for a turn in progress: it opens when the agent cannot read
	// a request and closes when the answers are sent.
	ask *askState

	// confirm is the window a consequential command is approved in, and nil when nothing is
	// being confirmed. Unlike the questions window it is answered DURING a run: the agent is
	// blocked waiting for the answer while the run loop reads the keys.
	confirm *confirmState
	// approvals is how a blocked agent reaches this loop.
	//
	// It is created once, lazily, and the Once is not tidiness: this field is read from TWO
	// goroutines at once — the run loop is already receiving on the channel when the agent, in
	// its own goroutine, sends the first request. A bare `if nil` check is a data race between
	// them, and the damage is worse than a torn read: each caller can come away holding a
	// DIFFERENT channel, so the run loop waits on one nobody will ever send to while the agent
	// waits for an answer. The turn hangs, the confirmation window never opens, and the user
	// sees the agent stop responding. -race caught exactly that.
	//
	// Lazy rather than built in New because the tests construct a TUI as a struct literal — the
	// many small ones that never run a real turn never need a channel, and requiring one would
	// make every one of them wrong for no reason.
	approvalsOnce sync.Once
	approvals     chan *confirmState

	// query filters the conversation; searching is true while the user is typing it.
	//
	// Both are view state: they describe what is being looked at, not what the session
	// contains, so neither is persisted and neither survives a new turn's scroll reset.
	query     string
	searching bool
	// painted records that the first frame has been drawn, so the screen is wiped once at
	// launch and never again.
	painted bool
	// charMode is true while the terminal delivers one byte at a time, which is what makes
	// live editing possible. It is set from the mode the run obtained, so a terminal that
	// refused it keeps the whole-line path.
	charMode bool
	// completingIdx is the row the popup is highlighting. It moves up and down with the
	// arrow keys while the popup is open, so the user can pick a candidate without typing
	// the whole name. Zero is the first row, which is also the row Tab and the right
	// arrow used to fill — keeping that as the default is what makes the existing gesture
	// keep working for the user who never reaches for an arrow.
	completingIdx int
	// termMode is the handle the run obtained on the controlling terminal. The wizard
	// needs to step the terminal back to cooked so it can read whole lines, and then put
	// it back into the same mode when it returns — both moves go through this handle.
	termMode *terminalMode
	// draft is the line being typed. With the terminal in character mode the interface owns
	// the editing, which is what makes a live completion popup possible: the candidate list
	// has to know what has been typed so far.
	draft string
	// scroll is how many rows the conversation is lifted above its newest line.
	// Zero means "pinned to the bottom", which is where a chat belongs: new
	// output arrives at the end. Raising it walks back through history, which is
	// the only way to read a long answer on a fixed-height screen.
	//
	// It is transient view state, deliberately not persisted: it describes where
	// the window is, not what the session contains.
	scroll int
}

// New creates a TUI with sensible defaults for production use.
func New(runner Runner) *TUI {
	return &TUI{
		In:     os.Stdin,
		Out:    os.Stdout,
		Err:    os.Stderr,
		Runner: runner,
		screen: ScreenTask,
	}
}

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

// Run displays the chat and dispatches user input until the user quits or the
// context is cancelled.
func (t *TUI) Run(ctx context.Context) int {
	t.drawFrame()

	// The confirmation channel is installed before anything can run: a turn that proposes a
	// consequential command has to be able to reach the user, and an interface that installs
	// this after the first turn would refuse the commands of that turn instead of asking.
	t.installApprover()

	// Leaving wipes the screen.
	//
	// A full-screen interface that exits and leaves its frame behind hands the shell back a
	// window covered in text that is not the user's: their prompt is somewhere above it, and the
	// first thing they have to do is clear it. The interface took the screen over when it
	// started — it cleared on the way in — so it has to give it back on the way out.
	//
	// It is a defer, and it is the FIRST one registered, so it runs LAST: the mouse, the
	// terminal mode and the background painter are all restored before the screen is wiped, and
	// the wipe is therefore the final thing written.
	//
	// It runs on every exit path — quit, Ctrl+C, a cancelled context, a closed input, a panic
	// that unwinds through here — because a cleanup attached to one of them is a cleanup that
	// leaks on the others.
	defer t.clearOnExit()

	// A resize repaints at the new geometry. The read below cannot be interrupted by
	// a signal — the terminal is not in raw mode, so ReadByte blocks until a line
	// arrives — which is why this needs its own goroutine rather than a check in the
	// loop.
	//
	// The painter is waited for on the way out: stop closes the notification channel,
	// the loop ends, and only then does Run return. A paint still in flight would
	// otherwise write a frame over the shell prompt after the interface had exited.
	// The mouse is requested for the duration of the run and released on the way out. A
	// terminal left in reporting mode would send movement events to the shell after the
	// program exits, which is the same class of rudeness as leaving the cursor hidden.
	t.enableMouse()
	defer t.disableMouse()

	// Character-at-a-time input for the duration of the run, so a keystroke is seen as it is
	// typed instead of at the end of the line. The restore runs from a defer and is
	// idempotent; the panic path below reuses it.
	//
	// It degrades rather than failing: a terminal that refuses the mode leaves the interface
	// reading whole lines, which is the behaviour it had before, and the completion popup
	// simply does not appear.
	mode := enterRaw()
	t.charMode = mode.active
	t.termMode = mode
	defer mode.restore()
	defer recoverRaw(mode)()

	resized, stopWatch := watchResize()
	var painting sync.WaitGroup
	painting.Add(1)
	go func() {
		defer painting.Done()
		for range resized {
			t.drawFrame()
		}
	}()
	defer func() {
		stopWatch()
		painting.Wait()
	}()

	for {
		line, ok := t.readLine(ctx)
		if !ok {
			if ctx.Err() != nil {
				return ExitInterrupted
			}
			return ExitSuccess
		}

		// While the search is open the input belongs to it: a line typed there is a
		// query, not a task, and running it would be a surprise nobody asked for.
		if t.searching {
			t.applyQuery(line)
			continue
		}

		// While the agent's questions are open the input belongs to THEM, and it is checked
		// before the shortcuts for the same reason the search is: a digit or an arrow typed
		// here is an answer, not a task to run.
		if t.asking() && t.handleAskKey(ctx, line) {
			continue
		}

		// Global shortcuts are checked before interpreting the line as chat.
		if handled, quit := t.handleShortcut(ctx, line); handled {
			if quit {
				return ExitSuccess
			}
			continue
		}

		// In Model/Config screens an empty line triggers the runner action.
		switch t.screen {
		case ScreenTask:
			t.runTask(ctx, line)
		case ScreenPlan:
			t.runPlan(ctx, line)
		case ScreenModels:
			// The catalogue and the wizard are actions, not conversations: they
			// are triggered by Enter on an empty line. Anything else would run
			// them again by accident and write over the report the user is
			// reading, so other input is answered with a reminder instead.
			if strings.TrimSpace(line) == "" {
				t.runModels(ctx)
			} else {
				t.addMessage(AuthorSystem, "press Enter to refresh this view, or Tab to switch mode.")
			}
		case ScreenConfig:
			if strings.TrimSpace(line) == "" {
				t.runConfig(ctx)
			} else {
				t.addMessage(AuthorSystem, "press Enter to start the wizard, or Tab to switch mode.")
			}
		}
	}
}

// handleShortcut interprets command-like input and view-switching keys.
// It returns (handled, shouldQuit).
//
// It is two layers, because the interface really has two: keys the TERMINAL sends
// (escape sequences, control bytes, the mouse) which are matched against the raw
// line, and NAMES the user types, which are matched against a trimmed, lowercased
// copy. Mixing them in one switch is what made the raw cases fragile — trimming a
// control byte deletes it, so a normalising switch can never see Tab.
func (t *TUI) handleShortcut(ctx context.Context, line string) (bool, bool) {
	if handled, quit := t.handleRawKey(line); handled {
		return true, quit
	}
	return t.handleTypedCommand(ctx, line)
}

// handleRawKey answers the keys the terminal reports as bytes: control characters,
// escape sequences and mouse reports. None of them is something a user typed as
// text, so a key the interface does not use is CONSUMED rather than passed on —
// otherwise it reaches the chat and is sent to the model.
func (t *TUI) handleRawKey(line string) (bool, bool) {
	switch line {
	case "	", "tab":
		// Tab is special: TrimSpace deletes it, so it can only be seen here. The
		// "tab" spelling is matched in the typed switch below.
		t.nextScreen()
		return true, false
	case keyEsc:
		// Escape is the universal way out, and it leaves in the reverse order of how
		// the interface was entered: the search first, then a run, then the scroll.
		if t.searching || t.query != "" {
			t.closeSearch()
			return true, false
		}
		if t.cancelRun != nil {
			t.cancelRun()
			return true, false
		}
		t.scrollToBottom()
		return true, false
	case keyUp:
		t.scrollBy(+1)
		return true, false
	case keyDown:
		t.scrollBy(-1)
		return true, false
	case keyPgUp:
		t.scrollBy(t.chatRows())
		return true, false
	case keyPgDn:
		t.scrollBy(-t.chatRows())
		return true, false
	case keyHome:
		t.scrollToTop()
		return true, false
	case keyEnd:
		t.scrollToBottom()
		return true, false
	case keyHalfUp:
		t.scrollBy(t.chatRows() / 2)
		return true, false
	case keyHalfDown:
		t.scrollBy(-t.chatRows() / 2)
		return true, false
	case keyFind:
		t.openSearch()
		return true, false
	}

	// The remaining control bytes are not text and must not be sent. This is the belt to the
	// reader's braces: the reader refuses to build a line containing one, and this refuses to
	// dispatch one that arrived by some other path.
	//
	// That is exactly what happened to Ctrl+D, Ctrl+F and Ctrl+U: the reader captured them from
	// the input and returned them as tokens, the interface had no case for them, and they were
	// dispatched as a chat message — so the model received control characters and reported them.
	if isStrayControlByte(line) {
		return true, false
	}

	// A mouse report is a CSI sequence like any other, so it arrives here intact. The
	// wheel scrolls; anything else the terminal reports (a click, a drag, a release, a move) is
	// CONSUMED and ignored, because the keyboard is the interface and the mouse is an addition to
	// it.
	//
	// Consuming every report, not just the wheel, is what stopped the mouse from typing: a click
	// or a drag fell through this and every other handler, so it reached the chat as a line of
	// escape-sequence text. The terminal was asked to send these events; an unhandled one is not
	// something the user typed.
	if lines, ok := mouseScroll(line); ok {
		t.scrollBy(lines)
		return true, false
	}
	if isMouseReport(line) {
		return true, false
	}

	// Any OTHER escape sequence is a KEY, not a message.
	//
	// This is the catch-all that was missing. The cases above name the keys the interface
	// acts on, and everything else — the right and left arrows, Delete, F1, Shift+Tab, and every
	// other sequence a terminal can send — fell through to the dispatch and was sent as a chat
	// message. Pressing an arrow SENT A MESSAGE: the user saw their own input submitted as if
	// they had pressed Enter.
	//
	// A sequence introduced by ESC is never something a user typed: text does not contain
	// escape sequences, they are how the terminal reports a key. So the rule is stated once, by
	// shape, rather than by listing every sequence that exists — the list can only ever be
	// incomplete, and what is missing from it becomes a message sent by accident.
	if strings.HasPrefix(line, "\x1b") {
		return true, false
	}

	return false, false
}

// isStrayControlByte reports whether the line is a single control byte with no
// meaning of its own. The tokens that DO have a meaning — Ctrl+D, Ctrl+F, Ctrl+U —
// are matched before this and are deliberately not listed: an interface that both
// acts on a key and swallows it would be saying two things about the same byte.
func isStrayControlByte(line string) bool {
	if len(line) != 1 {
		return false
	}
	c := line[0]
	if c == 0x7f {
		return true
	}
	return c < 0x20 && c != '	' && c != '\r' && c != '\n'
}

// handleTypedCommand answers a NAME the user typed: a slash command, a single-letter binding,
// or a command followed by an argument.
//
// The names live in ONE table (see commands.go). With a hand-written switch the same name had
// to be kept in step with the completion popup and the help screen by hand, and a command that
// existed in two of the three was a promise the interface did not keep.
func (t *TUI) handleTypedCommand(ctx context.Context, line string) (bool, bool) {
	// The line is normalised to lowercase as a whole, so a typed "Tab", "Q" or "/PLAN" reaches
	// the same entry as its lowercase spelling. That is what the interface already did for
	// command names, and it is the only reason a spelled-out key name works at all.
	trimmed := strings.TrimSpace(strings.ToLower(line))

	// Shift+G is checked against the raw line, before the lowercasing above: the table is
	// keyed on a lowercased copy, so "G" could never reach its own entry and the binding would
	// be dead. The convention is worth the extra check — "g for the top, G for the bottom" is
	// what a vim user's fingers expect.
	if line == "G" {
		t.scrollToBottom()
		return true, false
	}

	if run, ok := singleKeyBindings[trimmed]; ok {
		run(t)
		return true, false
	}

	// The NAME is everything up to the first space; the rest is the argument. Splitting once,
	// here, is what lets a single table hold both "/plan" and "/bad <note>" — and it is why an
	// unknown name is refused instead of being matched by prefix against every entry.
	name, arg := trimmed, ""
	if i := strings.IndexAny(trimmed, " 	"); i >= 0 {
		name, arg = trimmed[:i], strings.TrimSpace(trimmed[i+1:])
	}

	// The argument is taken from the RAW line when the name matches, so it keeps the user's
	// own capitalisation and quotes: it is their words, and /bad quotes them back verbatim.
	if raw := strings.TrimSpace(line); arg != "" {
		if i := strings.IndexAny(raw, " 	"); i >= 0 {
			arg = strings.TrimSpace(raw[i+1:])
		}
	}

	for _, c := range commands {
		if c.Name != name && !c.hasAlias(name) {
			continue
		}
		run, ok := commandActions[c.Name]
		if !ok {
			// Unreachable while TestEveryCommandHasAnAction passes, but it is ANSWERED here
			// rather than left to fall through: falling through would dispatch the name to
			// the MODEL. The user typed a command the popup and the help screen advertised,
			// so it was never a question, and sending it on would spend a turn on a line
			// nobody asked. A menu entry that says it is broken is better than one that
			// becomes a task by accident.
			t.addMessage(AuthorSystem, "the command "+c.Name+" is listed but has no implementation.")
			return true, false
		}
		return true, run(t, ctx, arg)
	}

	// A line that matches no command and no binding is CONTENT, not an error: in Task and Plan
	// it is what the user wants sent to the model, and that is a documented contract
	// (TestUnknownCommandInAConversationViewIsATask). Being a slash-prefixed line does not
	// change that — the interface has no reserved word, and refusing "/unknown" here would
	// break the very case the contract exists for.
	return false, false
}

// applyFind filters the conversation in one step, without leaving the search box open.
// It is the typed form of the search: the filter is applied and the box is closed, so
// the next line typed is a task again.
func (t *TUI) applyFind(query string) {
	if query == "" {
		t.openSearch()
		return
	}
	t.query = query
	t.searching = false
	t.scroll = 0
	t.drawFrame()
}

// openSearch puts the interface into search mode. It is a mode rather than a prefix
// argument because the query is built a character at a time and the result is visible
// while it is typed.
func (t *TUI) openSearch() {
	t.searching = true
	t.scroll = 0 // a new filter is read from its start
	t.drawFrame()
}

// applyQuery updates the filter with a line of input.
//
// An empty line closes the search and keeps the filter: pressing Enter on an empty
// query means "leave it as it is", which is how a reader confirms a filter they are
// happy with. Esc is the way to clear it.
func (t *TUI) applyQuery(line string) {
	if strings.TrimSpace(line) == "" {
		t.searching = false
		t.drawFrame()
		return
	}
	t.query = strings.TrimSpace(line)
	t.scroll = 0
	t.drawFrame()
}

// closeSearch leaves search mode and clears the filter, restoring the whole
// conversation. Leaving a filter applied with no visible sign of it would hide the
// user's own history from them.
func (t *TUI) closeSearch() {
	t.searching = false
	t.query = ""
	t.scroll = 0
	t.drawFrame()
}

// matchingMessages is the conversation narrowed by the active query, or all of it when
// there is none. Matching is case-insensitive and covers the speaker as well as the
// text, so "you" finds the user's turns.
func (t *TUI) matchingMessages() []Message {
	if t.query == "" {
		return t.visibleMessages()
	}
	needle := strings.ToLower(t.query)
	var out []Message
	for _, m := range t.visibleMessages() {
		if strings.Contains(strings.ToLower(m.Text), needle) ||
			strings.Contains(strings.ToLower(m.Author.String()), needle) {
			out = append(out, m)
		}
	}
	return out
}

// chatRows is the distance a page key moves: the number of conversation rows the
// frame can currently show.
//
// It is computed from the same shedding rules the layout applies, but WITHOUT
// calling the layout: that would recurse, since the layout reads the scroll
// offset this paging changes. The arithmetic below mirrors the fixed parts of the
// frame and the droppable ones, so a page matches what is on screen.
func (t *TUI) chatRows() int {
	_, h := t.size()
	if h <= 0 {
		// The terminal did not report a height, so there is no page to speak of.
		return minChatLines
	}
	// No branch for a terminal below the size gate: the gate refuses to draw at all there, so
	// this helper is never reached with such a height. A branch for it would be unreachable, and
	// an unreachable branch reads as a safeguard while testing nothing.
	//
	// The fixed rows are counted in ONE place (permanentRows), so this cannot drift from what
	// the layout actually draws. It used to carry its own "8", written when the frame had a
	// different shape; a stale count here is a scrollbar that lies about how much is visible.
	room := h - permanentRows
	if room < minChatLines {
		return minChatLines
	}
	return room
}

// scrollBy moves the view. Scrolling up is clamped where the layout clamps it —
// at the oldest line that can still be shown — and scrolling down stops at the
// newest one, so the view can never be left floating in empty space.
func (t *TUI) scrollBy(delta int) {
	t.scroll += delta
	if t.scroll < 0 {
		t.scroll = 0
	}
	if max := t.maxScroll(); t.scroll > max {
		t.scroll = max
	}
	t.drawFrame()
}

func (t *TUI) scrollToTop() {
	t.scroll = t.maxScroll()
	t.drawFrame()
}

func (t *TUI) scrollToBottom() {
	t.scroll = 0
	t.drawFrame()
}

// maxScroll is the furthest back the view can go: as far as the oldest line that
// the window can actually show.
//
// It is the conversation length minus the rows on screen, NOT one less than the
// whole body. Offsetting by the whole body would lift the newest line off the
// bottom and, on a conversation short enough to fit, would hide the only message
// there is — the user would press "go to top" and watch their text disappear.
// When everything already fits there is nowhere to go, which is why this is zero.
func (t *TUI) maxScroll() int {
	total := len(t.bodyLines())
	if room := t.chatRows(); total > room {
		return total - room
	}
	return 0
}

// bodyLines is the conversation as it would be drawn with no trimming and no
// scroll. It is the coordinate space the scroll offset moves through.
func (t *TUI) bodyLines() []string {
	return t.chatLines(t.inner())
}

// nextScreen switches between Task and Plan.
//
// Exactly those two, and only those two. Tab used to walk the whole screen order, which also
// contains the model list and the configuration wizard: pressing it twice to get back where you
// started landed on a different screen instead, and the two screens a user actually toggles
// while working were buried in a four-stop cycle. The other two are still reachable by their
// commands (/models, /config), which is where they belong — they are things you go to on
// purpose, not things you rotate through by accident.
func (t *TUI) nextScreen() {
	// Anything that is not Plan goes to Task, so a stray screen never traps the toggle.
	if t.screen == ScreenPlan {
		t.setScreen(ScreenTask)
		return
	}
	t.setScreen(ScreenPlan)
}

func (t *TUI) setScreen(s Screen) {
	t.screen = s
	t.drawFrame()
}

func (t *TUI) cycleReasoning() {
	levels := []string{"off", "low", "medium", "high"}
	current := strings.ToLower(t.Runner.Config().LLM.Reasoning.Level)
	if current == "" {
		current = "medium"
	}
	nextIdx := 0
	for i, l := range levels {
		if l == current {
			nextIdx = (i + 1) % len(levels)
			break
		}
	}
	next := levels[nextIdx]
	t.Runner.SetReasoning(next)
	t.addMessage(AuthorSystem, fmt.Sprintf("reasoning set to %s", next))
	t.drawFrame()
}

// progressSender is the callback handed to the runner. It never blocks: when the
// buffer is full and the run has already been cancelled it drops the line, because
// the conversation it would have updated is gone. Blocking here would keep the
// runner's goroutine alive after the user asked it to stop.
//
// It is one named function rather than the same select inlined twice, so the
// behaviour is stated once and can be tested on its own.
func progressSender(ctx context.Context, ch chan string) func(format string, args ...any) {
	return func(format string, args ...any) {
		select {
		case ch <- fmt.Sprintf(format, args...):
		case <-ctx.Done():
		}
	}
}

// runOutcome is what a turn reports back: the text to show and, if it failed, why.
type runOutcome struct {
	result string
	err    error
}

// drainProgress applies every line still queued in ch and returns immediately when
// there is none.
//
// It is a named function rather than a loop inlined in the caller so the behaviour
// can be tested on its own: whether a real run leaves lines behind depends on how
// fast the runner reports, which is exactly the kind of condition that makes a test
// pass here and fail on a slower machine.
func drainProgress(ch <-chan string, handle func(string)) {
	for {
		select {
		case p := <-ch:
			handle(p)
			continue
		default:
		}
		return
	}
}

// awaitRun drives one turn to its end.
//
// The outcome always comes from the runner, and from nowhere else. The runner
// observes the same context this loop watches, so when the context is cancelled it
// still reports what happened, which means the answer the user sees does not depend
// on which channel a select happened to pick. An earlier version decided the
// outcome in two places — reading the runner's report in one case and building a
// cancellation error in the other — and when both channels were ready the choice
// was a coin toss. That is not a test-timing problem: it is the same event
// producing two different messages.
func (t *TUI) awaitRun(ctx context.Context, progress <-chan string, done <-chan runOutcome, onProgress func(string)) runOutcome {
	// The channel the agent reaches this loop through is created BEFORE the select, not inside a
	// case: a channel created in a case arm is created every time the select re-evaluates, which
	// works only because the second call happens to return the same one. Making it explicit is
	// what keeps that from being a thing a future reader has to verify.
	approvals := t.approvalChannel()
	for {
		select {
		case p := <-progress:
			onProgress(p)
		case c := <-approvals:
			// A consequential command proposed by the agent, waiting for the user. It is
			// answered HERE, in the loop that reads the keys, because the agent is blocked in
			// its own goroutine and cannot read anything itself.
			//
			// The answer is given inline rather than by spawning: while the window is open,
			// approving a command IS what the user is doing, and a second reader on the same
			// input would race with this one for the keystroke.
			t.answerConfirm(ctx, c)
		case out := <-done:
			return out
		case <-ctx.Done():
			// Waiting here is bounded: the runner watches this very context, and
			// it sends exactly once into a buffered channel.
			return <-done
		}
	}
}

func (t *TUI) runTask(ctx context.Context, task string) {
	if strings.TrimSpace(task) == "" {
		t.drawFrame()
		return
	}
	t.addMessage(AuthorUser, task)

	// Cancel any previous run before starting a new one.
	if t.cancelRun != nil {
		t.cancelRun()
	}

	progress := make(chan string, 16)
	runCtx, cancel := context.WithCancel(ctx)
	t.cancelRun = cancel
	t.runningCtx = runCtx

	done := make(chan runOutcome, 1)
	go func() {
		res, err := t.Runner.RunTask(runCtx, task, progressSender(runCtx, progress))
		done <- runOutcome{result: res, err: err}
	}()
	t.beginTurn()
	t.addMessage(AuthorAgent, "")
	pendingIdx := len(t.messages) - 1
	t.messages[pendingIdx].Pending = true
	t.advance()

	// Task mode reports its phases through the same callback. A phase is a
	// transient label for the block that is running, so it is overwritten as the
	// run advances and the last visible one is replaced by the result. It is never
	// frozen: unlike a plan tool call, a phase is not an event worth keeping.
	outcome := t.awaitRun(runCtx, progress, done, func(p string) {
		t.messages[pendingIdx].Text = p
		t.advance()
	})

	t.cancelRun = nil
	t.runningCtx = nil

	var messageText string
	switch {
	case outcome.err == context.Canceled:
		messageText = "cancelled."
	case outcome.err != nil:
		messageText = fmt.Sprintf("error: %v", outcome.err)
	case outcome.result != "":
		messageText = outcome.result
	default:
		messageText = "the task finished without reporting a result."
	}
	// The block is settled here: the text goes in and the pending flag is cleared together, so
	// the frame the answer arrives on shows it whole.
	t.messages[pendingIdx].Text = messageText
	t.messages[pendingIdx].Pending = false
	t.endTurn()

	// The agent asked something. The window opens now, after the turn has ended, because the
	// question only exists once the run has returned — and opening it mid-run would put the
	// window over a turn that is still writing to the conversation.
	t.openAskIfPending()
}

// askSource is implemented by a runner that can hand over the questions of the last turn.
//
// It is an OPTIONAL interface, checked by type assertion, so a runner that does not ask —
// including every test double — keeps working unchanged. Requiring it on Runner would break
// them all for a feature they do not exercise.
type askSource interface {
	TakePendingQuestions() ([]agent.AskItem, string)
}

// openAskIfPending opens the questions window when the last turn asked something.
func (t *TUI) openAskIfPending() {
	src, ok := t.Runner.(askSource)
	if !ok {
		return
	}
	items, origin := src.TakePendingQuestions()
	if len(items) == 0 {
		return
	}
	t.ask = newAsk(items, origin)
	// The cursor lands on the first question that needs an answer, which on a fresh window is
	// the first one.
	if i := t.ask.firstUnanswered(); i >= 0 {
		t.ask.cur = i
	}
	t.drawFrame()
}

// planStream accumulates the live output of one plan run into the chat.
//
// It owns one invariant: exactly one message is "pending" (the block the model is
// still writing into) and every tool announcement is frozen the moment it
// arrives. Without that, the text that follows a tool call is appended to the
// tool line and overwrites it, which is how the announcement used to disappear.
type planStream struct {
	tui        *TUI
	pendingIdx int
	text       string
}

func (s *planStream) handle(line string) {
	if _, isPhase := phaseLabel(line); isPhase {
		// A phase is shown as movement (the spinner and the marker on the open
		// block), never as answer text: the block stays empty until real output
		// arrives, and an empty block is dropped when it closes.
		return
	}
	if label, isTool := toolLabel(line); isTool {
		s.closePending()
		// The frozen tool line: it must never be written into again.
		s.tui.messages = append(s.tui.messages, Message{Author: AuthorAgent, Text: label, Frozen: true})
		s.openPending()
		return
	}
	s.text += line
	s.tui.messages[s.pendingIdx].Text = s.text
}

// closePending finishes the block being written, dropping it when it stayed empty
// so a tool call does not leave a blank turn behind it.
func (s *planStream) closePending() {
	if strings.TrimSpace(s.text) == "" && s.pendingIdx == len(s.tui.messages)-1 {
		s.tui.messages = s.tui.messages[:s.pendingIdx]
		return
	}
	s.tui.messages[s.pendingIdx].Text = s.text
	s.tui.messages[s.pendingIdx].Pending = false
}

// openPending starts the next block the model will write into.
func (s *planStream) openPending() {
	s.tui.messages = append(s.tui.messages, Message{Author: AuthorAgent, Pending: true})
	s.pendingIdx = len(s.tui.messages) - 1
	s.text = ""
}

// settle applies the outcome of the run to the block still open.
func (s *planStream) settle(err error, answer string) {
	switch {
	case err == context.Canceled:
		s.setText("cancelled.")
	case err != nil:
		s.setText(fmt.Sprintf("error: %v", err))
	case answer != "":
		s.setText(answer)
	case s.text != "":
		s.setText(s.text)
	default:
		s.setText("the model returned nothing to show.")
	}
}

func (s *planStream) setText(text string) {
	s.tui.messages[s.pendingIdx].Text = text
	s.tui.messages[s.pendingIdx].Pending = false
}

func (t *TUI) runPlan(ctx context.Context, prompt string) {
	if strings.TrimSpace(prompt) == "" {
		t.drawFrame()
		return
	}
	t.addMessage(AuthorUser, prompt)

	if t.cancelRun != nil {
		t.cancelRun()
	}

	progress := make(chan string, 64)
	runCtx, cancel := context.WithCancel(ctx)
	t.cancelRun = cancel
	t.runningCtx = runCtx

	done := make(chan runOutcome, 1)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		answer, err := t.Runner.RunPlan(runCtx, prompt, progressSender(runCtx, progress))
		done <- runOutcome{result: answer, err: err}
	}()

	t.beginTurn()
	t.addMessage(AuthorAgent, "")
	stream := &planStream{tui: t, pendingIdx: len(t.messages) - 1}
	t.messages[stream.pendingIdx].Pending = true
	t.advance()

	result := t.awaitRun(runCtx, progress, done, func(p string) {
		stream.handle(p)
		t.advance()
	})
	wg.Wait()
	close(done)

	// Whatever the runner queued before reporting is still worth showing: the
	// outcome can arrive while lines are in flight.
	drainProgress(progress, stream.handle)

	t.cancelRun = nil
	t.runningCtx = nil

	stream.closePending()
	if stream.pendingIdx >= len(t.messages) {
		// The last block was empty and got dropped: settle on a fresh one.
		stream.openPending()
	}
	stream.settle(result.err, result.result)
	t.endTurn()
}

// runModels lists the catalogue inside the panel: the report comes back as text
// and becomes part of the conversation, so it scrolls with everything else and is
// never written over the frame.
func (t *TUI) runModels(ctx context.Context) {
	t.beginTurn()
	t.addMessage(AuthorSystem, "asking the provider for its catalogue...")
	pendingIdx := len(t.messages) - 1
	t.advance()

	report, err := t.Runner.RunModels(ctx)
	switch {
	case err != nil:
		t.messages[pendingIdx].Author = AuthorSystem
		t.messages[pendingIdx].Text = fmt.Sprintf("the catalogue could not be read: %v", err)
	case strings.TrimSpace(report) == "":
		t.messages[pendingIdx].Text = "the provider published no models."
	default:
		// A report is a block of labelled lines: it is shown as the model's
		// answer so the panel draws it on the rail.
		t.messages[pendingIdx].Author = AuthorAgent
		t.messages[pendingIdx].Text = strings.TrimRight(report, "\n")
	}
	t.messages[pendingIdx].Pending = false
	t.endTurn()
}

// runConfig runs the first-run wizard. It suspends the chat while the wizard asks its
// questions, so the prompt and the chat frame do not get drawn on top of each other: the
// wizard is line-oriented (it needs the terminal back in cooked mode to read whole lines),
// and the chat is byte-oriented (it draws one frame at a time in cbreak mode), so running
// both at once leaves the two fighting for the screen.
//
// On the way in the terminal is restored to cooked, the cursor is put back where the shell
// expects it, and the screen is wiped. On the way out the terminal is put back into the same
// mode it was in, the chat is told to repaint from a clean state, and any spinner that was
// running is parked so the user does not see a stale animation when the wizard is gone.
func (t *TUI) runConfig(ctx context.Context) {
	t.beginTurn()
	t.addMessage(AuthorSystem, "starting the configuration wizard...")
	pendingIdx := len(t.messages) - 1
	t.advance()

	if err := t.suspendForWizard(ctx); err != nil {
		t.messages[pendingIdx].Text = fmt.Sprintf("the wizard failed: %v", err)
	} else {
		t.messages[pendingIdx].Text = "configuration written."
	}
	t.messages[pendingIdx].Pending = false
	t.endTurn()
}

// suspendForWizard hands the terminal to the wizard, runs it, and hands it back. The
// terminal must be in cooked mode for the wizard's line reader to see whole lines; the
// chat's redraw loop must be quiet so the wizard's output is not overpainted by a frame
// arriving a millisecond later. The two together are what was missing before the wizard
// interleaved with the chat.
//
// The wizard writes its config file itself; RunConfig returns an error when it failed,
// not the path it wrote. A successful run is reported by a nil error — the chat then
// shows "configuration written." which matches what the user actually sees.
func (t *TUI) suspendForWizard(ctx context.Context) error {
	// Step back to cooked: the wizard reads whole lines, and the chat's cbreak mode turns
	// every keystroke into a stray escape that the line reader would either swallow or
	// pass straight through. Restoring the original mode is what makes the wizard behave
	// the same way it does on the first run, before the chat was ever started.
	//
	// The deferred restore in Run will see the NEW handle's mode.active flag on its way
	// out, so swapping the pointer is enough — the new handle owns its own lifecycle.
	if t.termMode != nil {
		t.termMode.restore()
	}
	// Show the cursor and clear the screen. The chat hid the cursor while painting, and
	// its last frame is still there: leaving both in place is how the wizard ends up
	// typed on top of the chat.
	if t.Out != nil {
		fmt.Fprint(t.Out, "\x1b[?25h\x1b[2J\x1b[3J\x1b[H")
	}

	// Run the wizard through the runner so its config-file path and error mapping stay
	// in one place. Errors here are wizard failures (bad input, write error), not chat
	// failures, and the caller maps them onto the chat's own message format.
	err := t.Runner.RunConfig(ctx)

	// Hand the terminal back, regardless of whether the wizard succeeded. The new
	// handle replaces the one Run is holding in its defer, so the next keystroke is
	// delivered one byte at a time and the chat's redraw loop can move.
	mode := enterRaw()
	t.termMode = mode
	t.charMode = mode.active

	// Wipe whatever the wizard left on screen and redraw the chat from scratch. A
	// partial wipe would show the chat's previous frame under the wizard's last
	// question, which is what the user sees now.
	if t.Out != nil {
		fmt.Fprint(t.Out, "\x1b[?25h\x1b[2J\x1b[3J\x1b[H")
	}
	t.lastFrame = nil
	t.paintedScreen = false
	t.painted = false
	t.drawFrame()
	return err
}

// addMessage appends a chat line and repaints.
func (t *TUI) addMessage(author Author, text string) {
	t.messages = append(t.messages, Message{Author: author, Text: text})
	t.drawFrame()
}

// addPreformatted appends a message whose own layout carries meaning, so it is drawn
// as written instead of being word wrapped. The help screen is a key reference: the
// alignment between a key and its description is what makes it scannable, and
// wrapping collapses the runs of spaces that produce it.
func (t *TUI) addPreformatted(author Author, text string) {
	t.messages = append(t.messages, Message{Author: author, Text: text, Preformatted: true})
	t.drawFrame()
}

// beginTurn marks the interface as busy and repaints, so the status line shows a
// spinner for the whole duration of a turn instead of a static dot.
//
// A new turn also returns the view to the bottom: starting a task is a request to
// see its output, so the window follows the newest line again even if the user was
// reading history a moment ago.
func (t *TUI) beginTurn() {
	t.busy = true
	t.spin++
	t.scroll = 0
}

// endTurn clears the busy flag and repaints the finished conversation.
func (t *TUI) endTurn() {
	t.busy = false
	t.drawFrame()
}

// advance repaints while a turn is running, which animates the spinner. It is
// called from the progress loop, so a slow model still shows movement.
//
// New output never moves a view the user lifted: the offset counts rows above the
// newest line, so growing the conversation underneath leaves the window where it
// is — the reader keeps the line they were on while the answer grows below. Only
// an explicit jump (a new turn, g/G, End) returns to the bottom.
func (t *TUI) advance() {
	if t.busy {
		t.spin++
	}
	t.drawFrame()
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
		// so the message read "hola[C[D[A" — the sequence with its marker cut off, which no
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
// `hola\x1b[C\x1b[D\x1b[A\x1b[B\x1b[3~\x1bOP\x15/quit` — every arrow the user pressed, spelled
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
		// Skip the whole sequence. CSI (ESC [) runs to its final byte in 0x40..0x7e; SS3
		// (ESC O) is the three-byte form the function keys use; a bare ESC is just itself.
		j := i + 1
		switch {
		case j < len(runes) && runes[j] == '[':
			j++
			for j < len(runes) && !(runes[j] >= 0x40 && runes[j] <= 0x7e) {
				j++
			}
			if j < len(runes) {
				j++ // consume the final byte too
			}
		case j < len(runes) && runes[j] == 'O':
			j++
			if j < len(runes) {
				j++
			}
		default:
			j = i + 1 // a bare ESC: nothing follows it
		}
		i = j
	}
	return b.String()
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

// helpText is the help screen, generated from the command catalogue.
//
// It is built rather than written so the screen cannot drift from what the handler accepts:
// the previous version was a second copy of the same list, and it had already gone out of
// step — it advertised "/t task" while the handler and the popup knew "/task". A help screen
// that documents a spelling nothing accepts is worse than no help at all.
var helpText = buildHelp()

// buildHelp renders the reference: the navigation keys first, because they are what a
// newcomer needs, then every command with the description the catalogue carries.
func buildHelp() string {
	var b strings.Builder
	b.WriteString("Starlight chat\n\n")
	b.WriteString("Navigation — no Enter needed\n")
	for _, h := range [][2]string{
		{"Tab", "switch between Task and Plan"},
		{"→", "complete the command being typed"},
		{"j/k", "scroll one line"},
		{"PgUp/PgDn", "scroll one page"},
		{"Ctrl+U/Ctrl+D", "scroll half a page"},
		{"g/G", "oldest / newest"},
		{"Ctrl+F", "search the chat"},
		{"/find", "the same search, typed"},
		{"Esc", "search / cancel / bottom"},
		{"Ctrl+C", "cancel and leave"},
	} {
		b.WriteString("  " + pad(h[0], 16) + h[1] + "\n")
	}

	b.WriteString("\nCommands — type them and press Enter\n")
	for _, c := range commands {
		label := c.Name
		if len(c.Aliases) > 0 {
			label += " (" + strings.Join(c.Aliases, ", ") + ")"
		}
		if c.Arg != "" {
			label += " " + c.Arg
		}
		b.WriteString("  " + pad(label, 24) + c.Help + "\n")
	}
	return b.String()
}

// pad pads a plain string to a column, for the help's two-column layout. The width is in
// BYTES here because the labels are ASCII by construction: a command name is sanitised and the
// key names are literals. A rune-aware version would be the right call for anything else.
func pad(s string, width int) string {
	if len(s) >= width {
		return s + " "
	}
	return s + strings.Repeat(" ", width-len(s))
}

// minHeight is the number of rows below which the interface stops trying to draw
// a frame. The guide is explicit: below a workable size, show a message instead
// of a broken layout. Trying to fit anyway produces a frame whose every part has
// been dropped — the worst of both worlds, since the user cannot read it and
// cannot tell why.
// It is DERIVED from the parts the frame always draws, not chosen: two rules, the composer, the
// status line and the bar are permanent, and a conversation of fewer than minChatLines rows is
// not a conversation. A hand-picked 8 was two rows short of that, so the smallest terminal the
// gate accepted could not hold the frame it then drew — the frame came out taller than the
// window and scrolled.
const minHeight = permanentRows + minChatLines

// tooSmallLines is the whole screen when the terminal cannot hold the interface.
// It says the size it needs and the size it has, so the user can fix it instead
// of guessing.
func (t *TUI) tooSmallLines(w, h int) []string {
	msg := []string{
		"",
		"  " + t.color(colWarning, 0, "The window is too small to draw Starlight."),
		"",
		"  resize it to at least " + strconv.Itoa(minWidth) + " columns and " +
			strconv.Itoa(minHeight) + " rows,",
		"  or press q to quit.",
		"",
		"  now: " + strconv.Itoa(w) + " x " + strconv.Itoa(h),
	}
	return msg
}

// recoverRaw restores the terminal before letting a panic continue.
//
// A session that dies while the terminal is in cbreak with echo off hands the user back a
// shell that shows nothing they type and runs nothing they see. The restore therefore happens
// on the way out of a panic as well as on the normal path, and it is the FIRST thing done —
// before the panic is allowed to keep unwinding.
//
// It is a named function returning the deferred call rather than an inline closure so the
// behaviour can be tested: triggering a real panic inside a live terminal is not something a
// test should do.
func recoverRaw(mode *terminalMode) func() {
	return func() {
		if r := recover(); r != nil {
			mode.restore()
			panic(r)
		}
	}
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

// readByteOrCancel returns the next byte, or reports that the run is over.
//
// Reading happens on its own goroutine so a cancelled context can abandon the read: a
// blocking ReadByte on a terminal that is waiting for a keystroke never returns on its own,
// and without this the interface would ignore Ctrl+C until the user pressed something else.
func (t *TUI) readByteOrCancel(ctx context.Context) (byte, bool) {
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
	case v, ok := <-ch:
		return v, ok
	case <-ctx.Done():
		return 0, false
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
	if t.input().Buffered() == 0 {
		return keyEsc
	}
	peek, err := t.input().Peek(1)
	if err != nil || len(peek) == 0 || peek[0] != '[' {
		return keyEsc
	}
	seq := []byte{0x1b, '['}
	t.input().Discard(1)
	for {
		c, err := t.input().ReadByte()
		if err != nil {
			return keyEsc
		}
		seq = append(seq, c)
		if c >= 0x40 && c <= 0x7e {
			break
		}
		// The same bound as readEscape, and for the same reason: a mouse report is the longest
		// sequence the interface asks for, and cutting it short leaves its tail in the buffer to
		// be read as typed text.
		if len(seq) > 64 {
			return keyEsc
		}
	}
	return string(seq)
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
