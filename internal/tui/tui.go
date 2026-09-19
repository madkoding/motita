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

	for {
		line, ok := t.readLine(ctx)
		if !ok {
			if ctx.Err() != nil {
				return ExitInterrupted
			}
			return ExitSuccess
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
		// Escape is the universal way out. There are no overlays yet, so it
		// cancels a run in flight and otherwise returns the view to the bottom,
		// which is the state the user can always expect to get back to.
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
	case "/reasoning", "/r":
		t.cycleReasoning()
		return true, false
	case "/help", "/h", "h", "help", "?":
		t.addMessage(AuthorSystem, helpText)
		return true, false
	}
	return false, false
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

func (t *TUI) nextScreen() {
	idx := 0
	for i, s := range screenOrder {
		if s == t.screen {
			idx = i
			break
		}
	}
	t.setScreen(screenOrder[(idx+1)%len(screenOrder)])
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
	keyEsc  = "\x1b" // 0x1b on its own
	keyUp   = "\x1b[A"
	keyDown = "\x1b[B"
	keyPgUp = "\x1b[5~"
	keyPgDn = "\x1b[6~"
	keyHome = "\x1b[H"
	keyEnd  = "\x1b[F"
	// Control bytes arrive as themselves. The half-page pair is the vim convention
	// the interaction guide lists, and it is what a reader uses to skim a long
	// answer without losing their place the way a full page does.
	keyHalfUp   = "\x15" // Ctrl+U
	keyHalfDown = "\x04" // Ctrl+D
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
	// The half-page control bytes are keys, not text: returning them stops them
	// being typed into the prompt and then swallowed by the trim below.
	if head == 0x15 || head == 0x04 {
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

const helpText = `Starlight chat

Navigation (keys, no Enter needed):
  Tab             switch mode
  j / k           scroll the conversation down / up
  Up / Down       scroll one line
  PgUp / PgDn     scroll one page
  Ctrl+U / Ctrl+D scroll half a page
  g / G           jump to the oldest / newest line
  Esc             cancel the running turn, or return to the newest line
  Ctrl+C          cancel the turn and leave

Commands (type them and press Enter):
  /t  task        switch to Task mode
  /p  plan        switch to Plan mode
  /m  models      list the models the provider publishes
  /c  config      run the configuration wizard
  /r  reasoning   cycle reasoning level (off/low/medium/high)
  /h  help        show this help
  /q  quit        leave

Task mode runs the 3-layer agent in the sandbox and reports the result.
Plan mode reads only: it lists and reads files and runs read-only commands,
and explains what it would do before anything is executed.
`

// minHeight is the number of rows below which the interface stops trying to draw
// a frame. The guide is explicit: below a workable size, show a message instead
// of a broken layout. Trying to fit anyway produces a frame whose every part has
// been dropped — the worst of both worlds, since the user cannot read it and
// cannot tell why.
const minHeight = 8

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
