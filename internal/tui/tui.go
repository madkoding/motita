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
}

// TUI is the conversational terminal user interface.
type TUI struct {
	In      io.Reader
	Out     io.Writer
	Err     io.Writer
	Runner  Runner
	NoColor bool

	screen     Screen
	messages   []Message
	reader     *bufio.Reader
	cancelRun  context.CancelFunc
	runningCtx context.Context
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

		switch t.screen {
		case ScreenTask:
			t.runTask(ctx, line)
		case ScreenPlan:
			t.runPlan(ctx, line)
		case ScreenModels:
			t.runModels(ctx)
		case ScreenConfig:
			t.runConfig(ctx)
		}
	}
}

// handleShortcut interprets command-like input and view-switching keys.
// It returns (handled, shouldQuit).
func (t *TUI) handleShortcut(ctx context.Context, line string) (bool, bool) {
	trimmed := strings.TrimSpace(strings.ToLower(line))

	switch trimmed {
	case "q", "quit", "/quit", "/q":
		return true, true
	case "tab", "	":
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
		return true, false
	case "/config", "/c":
		t.setScreen(ScreenConfig)
		return true, false
	case "/reasoning", "/r":
		t.cycleReasoning()
		return true, false
	case "/help", "/h", "h", "help":
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

	type runOutcome struct {
		result string
		err    error
	}
	done := make(chan runOutcome, 1)
	go func() {
		res, err := t.Runner.RunTask(runCtx, task, func(format string, args ...any) {
			select {
			case progress <- fmt.Sprintf(format, args...):
			case <-runCtx.Done():
			}
		})
		done <- runOutcome{result: res, err: err}
	}()

	t.addMessage(AuthorAgent, "thinking...")
	pendingIdx := len(t.messages) - 1

	var outcome runOutcome
loop:
	for {
		select {
		case p := <-progress:
			t.messages[pendingIdx].Text = p
			t.drawFrame()
		case outcome = <-done:
			break loop
		case <-runCtx.Done():
			outcome = runOutcome{err: runCtx.Err()}
			break loop
		}
	}

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
		t.messages[pendingIdx].Text = "done."
	}
	t.messages[pendingIdx].Pending = false
	t.drawFrame()
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

	progress := make(chan string, 16)
	runCtx, cancel := context.WithCancel(ctx)
	t.cancelRun = cancel
	t.runningCtx = runCtx

	done := make(chan struct {
		answer string
		err    error
	}, 1)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		answer, err := t.Runner.RunPlan(runCtx, prompt, func(format string, args ...any) {
			select {
			case progress <- fmt.Sprintf(format, args...):
			case <-runCtx.Done():
			}
		})
		done <- struct {
			answer string
			err    error
		}{answer, err}
	}()

	t.addMessage(AuthorAgent, "thinking...")
	pendingIdx := len(t.messages) - 1

	var result struct {
		answer string
		err    error
	}
loop:
	for {
		select {
		case p, ok := <-progress:
			if !ok {
				break loop
			}
			t.messages[pendingIdx].Text = p
			t.drawFrame()
		case r, ok := <-done:
			if !ok {
				break loop
			}
			result = r
			break loop
		case <-runCtx.Done():
			result.err = runCtx.Err()
			break loop
		}
	}
	wg.Wait()
	close(done)

	// Drain any trailing progress after completion/cancellation without blocking.
	select {
	case p := <-progress:
		t.messages[pendingIdx].Text = p
	default:
	}

	t.cancelRun = nil
	t.runningCtx = nil

	if result.err != nil {
		if result.err == context.Canceled {
			t.messages[pendingIdx].Text = "cancelled."
		} else {
			t.messages[pendingIdx].Text = fmt.Sprintf("error: %v", result.err)
		}
	} else if result.answer != "" {
		t.messages[pendingIdx].Text = result.answer
	} else {
		t.messages[pendingIdx].Text = "done."
	}
	t.messages[pendingIdx].Pending = false
	t.drawFrame()
}

func (t *TUI) runModels(ctx context.Context) {
	t.addMessage(AuthorSystem, "fetching models...")
	pendingIdx := len(t.messages) - 1
	t.drawFrame()

	if err := t.Runner.RunModels(ctx); err != nil {
		t.messages[pendingIdx].Text = fmt.Sprintf("models error: %v", err)
	} else {
		t.messages[pendingIdx].Text = "models listed above."
	}
	t.messages[pendingIdx].Pending = false
	t.drawFrame()
}

func (t *TUI) runConfig(ctx context.Context) {
	t.addMessage(AuthorSystem, "running configuration wizard...")
	pendingIdx := len(t.messages) - 1
	t.drawFrame()

	if err := t.Runner.RunConfig(ctx); err != nil {
		t.messages[pendingIdx].Text = fmt.Sprintf("config error: %v", err)
	} else {
		t.messages[pendingIdx].Text = "configuration saved."
	}
	t.messages[pendingIdx].Pending = false
	t.drawFrame()
}

func (t *TUI) addMessage(author Author, text string) {
	t.messages = append(t.messages, Message{Author: author, Text: text, Pending: author == AuthorAgent && text == "thinking..."})
	t.drawFrame()
}

// readLine reads one line from the input. It returns ok=false on EOF or when
// the context is cancelled.
func (t *TUI) readLine(ctx context.Context) (string, bool) {
	ch := make(chan lineResult, 1)
	go func() {
		l, err := t.input().ReadString('\n')
		ch <- lineResult{line: l, err: err}
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
