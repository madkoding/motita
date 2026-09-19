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
func (t *TUI) handleShortcut(ctx context.Context, line string) (bool, bool) {
	// Special keys are matched against the RAW input, before any trimming: a
	// control byte such as Tab is deleted by TrimSpace, so a switch that
	// normalises first can never see it. This is why Tab used to do nothing.
	switch line {
	case "	":
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

	// A mouse report is a CSI sequence like any other, so it arrives here intact. The
	// wheel scrolls; anything else the terminal reports (a click, a drag, a release) is
	// accepted and ignored, because the keyboard is the interface and the mouse is an
	// addition to it.
	if lines, ok := mouseScroll(line); ok {
		t.scrollBy(lines)
		return true, false
	}

	// Shift+G is checked against the raw line, before the lowercasing below: the
	// switch runs on a lowercased copy, so "G" could never reach its own case and
	// the binding would be dead. The convention is worth the extra line — "g for
	// the top, G for the bottom" is what a vim user's fingers expect.
	if line == "G" {
		t.scrollToBottom()
		return true, false
	}

	trimmed := strings.TrimSpace(strings.ToLower(line))

	// "/find <text>" applies a filter in one line, which is what a user reaches for when
	// the Ctrl+F shortcut is swallowed by their terminal. Checked before the switch
	// because it carries an argument.
	if rest, ok := cutPrefix(trimmed, "/find "); ok {
		t.applyFind(strings.TrimSpace(rest))
		return true, false
	}

	switch trimmed {
	case "q", "quit", "/quit", "/q":
		return true, true
	case "tab":
		t.nextScreen()
		return true, false
	case "j":
		t.scrollBy(+1)
		return true, false
	case "k":
		t.scrollBy(-1)
		return true, false
	case "g":
		t.scrollToTop()
		return true, false
	case "/task", "/t":
		t.setScreen(ScreenTask)
		return true, false
	case "/plan", "/p":
		t.setScreen(ScreenPlan)
		return true, false
	case "/models", "/m":
		t.setScreen(ScreenModels)
		t.runModels(ctx)
		return true, false
	case "/config", "/c":
		t.setScreen(ScreenConfig)
		t.runConfig(ctx)
		return true, false
	case "/reasoning", "/r", "/think":
		t.cycleReasoning()
		return true, false
	case "/find", "/f":
		// A typed alternative to Ctrl+F, and the one that works everywhere: a terminal
		// in canonical mode consumes control bytes itself (Ctrl+U is the driver's
		// kill-line, Ctrl+D its EOF), so the shortcut cannot be relied on. The command
		// goes through the ordinary line reader, which is the same path a task takes.
		t.openSearch()
		return true, false
	case "/session", "/s":
		// The session report: the window, what is in use, and what a compaction carried.
		// A conversation the user cannot inspect is one they cannot trust.
		t.addPreformatted(AuthorSystem, t.Runner.ConversationReport())
		return true, false
	case "/new":
		t.Runner.ResetConversation()
		t.addMessage(AuthorSystem, "started a new session: the next question begins a fresh conversation.")
		return true, false
	case "/help", "/h", "h", "help", "?":
		t.addPreformatted(AuthorSystem, helpText)
		return true, false
	}
	return false, false
}

// cutPrefix is strings.CutPrefix spelled locally, so the behaviour does not depend on
// the toolchain's standard library version.
func cutPrefix(s, prefix string) (string, bool) {
	if len(s) >= len(prefix) && s[:len(prefix)] == prefix {
		return s[len(prefix):], true
	}
	return "", false
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
	if h < minHeight {
		return 1
	}
	// status (2) + blanks (2) + borders (2) + tabs (1) + prompt (1).
	room := h - 8
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
	for {
		select {
		case p := <-progress:
			onProgress(p)
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

	if outcome.err != nil {
		if outcome.err == context.Canceled {
			t.messages[pendingIdx].Text = "cancelled."
		} else {
			t.messages[pendingIdx].Text = fmt.Sprintf("error: %v", outcome.err)
		}
	} else if outcome.result != "" {
		t.messages[pendingIdx].Text = outcome.result
	} else {
		t.messages[pendingIdx].Text = "the task finished without reporting a result."
	}
	t.messages[pendingIdx].Pending = false
	t.endTurn()
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

// runConfig runs the first-run wizard.
func (t *TUI) runConfig(ctx context.Context) {
	t.beginTurn()
	t.addMessage(AuthorSystem, "starting the configuration wizard...")
	pendingIdx := len(t.messages) - 1
	t.advance()

	if err := t.Runner.RunConfig(ctx); err != nil {
		t.messages[pendingIdx].Text = fmt.Sprintf("the wizard failed: %v", err)
	} else {
		t.messages[pendingIdx].Text = "configuration written."
	}
	t.messages[pendingIdx].Pending = false
	t.endTurn()
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
	keyRight = "\x1b[C"
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
		return strings.TrimSpace(r.line), true
	case <-ctx.Done():
		go func() { <-ch }()
		return "", false
	}
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
		if len(seq) > 16 {
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
	defer func() {
		if t.draft != "" {
			t.draft = ""
			t.drawFrame()
		}
	}()

	for {
		ch := make(chan byte, 1)
		go func() {
			b, err := t.input().ReadByte()
			if err != nil {
				close(ch)
				return
			}
			ch <- b
		}()

		var b byte
		select {
		case v, ok := <-ch:
			if !ok {
				return "", false
			}
			b = v
		case <-ctx.Done():
			return "", false
		}

		switch b {
		case '\r', '\n':
			line := strings.TrimSpace(t.draft)
			t.draft = ""
			return line, true

		case 0x03: // Ctrl+C, which cbreak still delivers as a signal; this is the read path
			return "", false

		case 0x04, 0x06, 0x15: // Ctrl+D, Ctrl+F, Ctrl+U
			t.draft = ""
			t.drawFrame()
			return string(b), true

		case 0x7f, 0x08: // backspace
			if t.draft != "" {
				r := []rune(t.draft)
				t.draft = string(r[:len(r)-1])
				t.drawFrame()
			}

		case '\t':
			// Tab means ONE thing: switch between Task and Plan. It used to also accept the
			// completion when the popup was open, which meant the same key did two different
			// things depending on what had been typed — and a user reaching for the mode
			// switch in the middle of a line got a command inserted instead.
			//
			// Completion is still a keystroke away, on the right arrow: the gesture that means
			// "accept forward" everywhere else, and a key nothing here had claimed.
			t.draft = ""
			return "\t", true

		case 0x1b:
			// An escape sequence: read the rest without blocking on a lone Esc.
			seq := t.readEscapeLive()
			if seq == keyEsc {
				// Escape closes the popup first, and only leaves the line when there is
				// nothing to close.
				if t.completing() {
					t.draft = ""
					t.drawFrame()
					continue
				}
				t.draft = ""
				return keyEsc, true
			}
			// The right arrow accepts the completion the popup is showing: the popup exists
			// to save typing, and with Tab spoken for this is the key that does it. It only
			// consumes the key when there was something to accept, so an arrow press with no
			// popup open is still just an arrow press.
			if seq == keyRight && t.completeDraft() {
				continue
			}
			// The other arrows and the page keys are handled by the same switch as always;
			// the draft is cleared so the line does not survive the mode change.
			t.draft = ""
			return seq, true

		default:
			if b >= 0x20 && b != 0x7f {
				t.draft += string(b)
				t.drawFrame()
			}
		}
	}
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
		if len(seq) > 16 {
			return keyEsc
		}
	}
	return string(seq)
}

// completeDraft replaces what has been typed with the first candidate, so Tab accepts the
// suggestion the popup is showing.
//
// It reports whether it did anything: with no candidates Tab keeps its old meaning.
func (t *TUI) completeDraft() bool {
	cands := completions(t.draft)
	if len(cands) == 0 {
		return false
	}
	t.draft = cands[0].Name + " "
	t.drawFrame()
	return true
}
