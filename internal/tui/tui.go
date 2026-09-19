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
	// The Tab is looked for in the raw input: readLine returns it as the single
	// byte "	", and trimming first would remove it, so the switch would never see
	// the key. That is what made Tab neither navigate nor reach the prompt.
	if strings.TrimSpace(line) == "	" || line == "	" {
		t.nextScreen()
		return true, false
	}

	trimmed := strings.TrimSpace(strings.ToLower(line))

	switch trimmed {
	case "q", "quit", "/quit", "/q":
		return true, true
	case "tab":
		t.nextScreen()
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

	// Drain every progress line still queued. awaitRun returns as soon as the
	// outcome is ready, so several lines can still be in flight, and a single
	// non-blocking read would drop them.
	for {
		select {
		case p := <-progress:
			stream.handle(p)
			continue
		default:
		}
		break
	}

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
func (t *TUI) beginTurn() {
	t.busy = true
	t.spin++
}

// endTurn clears the busy flag and repaints the finished conversation.
func (t *TUI) endTurn() {
	t.busy = false
	t.drawFrame()
}

// advance repaints while a turn is running, which animates the spinner. It is
// called from the progress loop, so a slow model still shows movement.
func (t *TUI) advance() {
	if t.busy {
		t.spin++
	}
	t.drawFrame()
}

// readLine reads one line from the input. It returns ok=false on EOF or when the
// context is cancelled.
//
// The first byte is read on its own, before a full line is asked for, because two
// keys have to be recognised without becoming part of the line:
//
//   - Tab switches view, so it must not be appended to a prompt (which is what
//     bufio would do, since it only stops at a newline).
//   - A newline on its own is an empty line, and it means "run the action of this
//     view". It has to be returned as such: reading a full line after the
//     newline was already consumed would silently return the *next* line and lose
//     the empty one.
func (t *TUI) readLine(ctx context.Context) (string, bool) {
	head, ok := t.readKey(ctx)
	if !ok {
		return "", false
	}
	if head == '	' {
		return "	", true
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

type lineResult struct {
	line string
	err  error
}

const helpText = `Starlight chat

Global shortcuts (type the letter/word and press Enter):
  t / task        switch to Task mode
  p / plan        switch to Plan mode
  m / models      list available models
  c / config      run the configuration wizard
  r / reasoning   cycle reasoning level (off/low/medium/high)
  h / help        show this help
  q / quit        leave

Task mode runs the 3-layer agent. Plan mode only explains what it would do.
Ctrl+C cancels the current run and returns to the prompt.
`
