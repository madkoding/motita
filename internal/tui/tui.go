// Package tui implements the interactive text-based user interface.
//
// It deliberately avoids raw terminal mode: the program prints a menu, the user
// types a number/letter and presses Enter, and the chosen action runs. This makes
// the whole package testable with bytes.Buffer and portable across every target
// platform without termios or console API code.
//
// Ctrl+C (SIGINT) is handled through the context: the caller cancels the context and
// the TUI returns immediately instead of blocking on stdin.
package tui

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"strings"
)

// Exit codes returned by the TUI.
const (
	// ExitSuccess: the user exited normally (q, e, EOF).
	ExitSuccess = 0
	// ExitError: an action failed.
	ExitError = 1
	// ExitInterrupted: the context was cancelled (Ctrl+C, SIGTERM).
	ExitInterrupted = 2
)

// MenuOption identifies one of the main menu entries.
type MenuOption int

const (
	OptionPlan MenuOption = iota
	OptionTask
	OptionConfig
	OptionHelp
	OptionExit
)

func (m MenuOption) String() string {
	switch m {
	case OptionPlan:
		return "Plan mode"
	case OptionTask:
		return "Task mode"
	case OptionConfig:
		return "Configuration"
	case OptionHelp:
		return "Help"
	case OptionExit:
		return "Exit"
	}
	panic("unknown menu option")
}

// Key returns the one-letter shortcut for the option.
func (m MenuOption) Key() string {
	switch m {
	case OptionPlan:
		return "p"
	case OptionTask:
		return "t"
	case OptionConfig:
		return "c"
	case OptionHelp:
		return "h"
	case OptionExit:
		return "e"
	}
	panic("unknown menu option")
}

// menuOptions is the ordered list of main menu entries.
var menuOptions = []MenuOption{OptionPlan, OptionTask, OptionConfig, OptionHelp, OptionExit}

// TUI is the terminal user interface.
type TUI struct {
	In      io.Reader
	Out     io.Writer
	Err     io.Writer
	Runner  Runner
	NoColor bool
	reader  *bufio.Reader
}

// New creates a TUI with sensible defaults for production use.
func New(runner Runner) *TUI {
	return &TUI{
		In:     os.Stdin,
		Out:    os.Stdout,
		Err:    os.Stderr,
		Runner: runner,
	}
}

func (t *TUI) input() *bufio.Reader {
	if t.reader == nil {
		t.reader = bufio.NewReader(t.In)
	}
	return t.reader
}

// Run displays the main menu and dispatches the selected action.
// It returns ExitInterrupted if ctx is cancelled while waiting for input.
func (t *TUI) Run(ctx context.Context) int {
	t.clearScreen()
	for {
		t.drawMenu()
		choice, ok := t.readChoice(ctx)
		if !ok {
			// Distinguish EOF (clean exit) from a cancelled context (Ctrl+C).
			if ctx.Err() != nil {
				return ExitInterrupted
			}
			return ExitSuccess
		}
		act, _ := t.actionFor(choice)
		if act == nil {
			t.printLine(t.color(1, 0, "Unknown option. Press Enter to continue..."))
			t.waitEnter(ctx)
			t.clearScreen()
			continue
		}
		t.clearScreen()
		act(ctx, t)
		t.clearScreen()
	}
}

func (t *TUI) drawMenu() {
	t.printTitle("Starlight")
	for i, opt := range menuOptions {
		mark := "  "
		if i == 0 {
			mark = "> "
		}
		label := fmt.Sprintf("[%s] %s", opt.Key(), opt.String())
		fmt.Fprintf(t.Out, "%s%s\n", mark, label)
	}
	t.printFooter("Type a letter/number and press Enter, or q to quit")
}

func (t *TUI) readChoice(ctx context.Context) (string, bool) {
	t.printPrompt("choice")
	return t.readLine(ctx)
}

func (t *TUI) actionFor(choice string) (actionFunc, int) {
	choice = strings.ToLower(choice)
	if choice == "q" || choice == "quit" || choice == "exit" {
		return func(context.Context, *TUI) {}, -1
	}
	for i, opt := range menuOptions {
		if choice == opt.Key() || choice == fmt.Sprint(i+1) {
			return opt.action(), i
		}
	}
	return nil, -1
}

// actionFunc is the signature of a menu action.
type actionFunc func(context.Context, *TUI)

func (m MenuOption) action() actionFunc {
	switch m {
	case OptionPlan:
		return runPlan
	case OptionTask:
		return runTask
	case OptionConfig:
		return runConfig
	case OptionHelp:
		return runHelp
	case OptionExit:
		return func(context.Context, *TUI) {}
	}
	panic("unknown menu option")
}

func (t *TUI) printTitle(s string) {
	fmt.Fprintf(t.Out, "%s\n\n", t.color(1, 0, s))
}

func (t *TUI) printFooter(s string) {
	fmt.Fprintf(t.Out, "%s\n", t.color(2, 0, s))
}

func (t *TUI) printPrompt(label string) {
	fmt.Fprintf(t.Out, "%s: ", t.color(7, 0, label))
}

func (t *TUI) printLine(s string) {
	fmt.Fprintln(t.Out, s)
}

func (t *TUI) clearScreen() {
	fmt.Fprint(t.Out, "\x1b[2J\x1b[H")
}

// color returns a colored string using the 16-color ANSI palette.
func (t *TUI) color(fg, bg int, s string) string {
	if t.NoColor {
		return s
	}
	if bg == 0 {
		return fmt.Sprintf("\x1b[%dm%s\x1b[0m", 30+fg, s)
	}
	return fmt.Sprintf("\x1b[%d;%dm%s\x1b[0m", 30+fg, 40+bg, s)
}

func runPlan(ctx context.Context, t *TUI) {
	t.printPrompt("Prompt")
	prompt, ok := t.readLine(ctx)
	if !ok {
		return
	}
	if strings.TrimSpace(prompt) == "" {
		t.printLine("(empty prompt)")
		t.waitEnter(ctx)
		return
	}
	trace := func(format string, args ...any) {
		fmt.Fprintf(t.Err, format+"\n", args...)
	}
	if answer, err := t.Runner.RunPlan(ctx, prompt, trace); err != nil {
		fmt.Fprintf(t.Err, "\nerror: %v\n", err)
	} else if answer != "" {
		fmt.Fprintln(t.Out, answer)
	}
	t.waitEnter(ctx)
}

func runTask(ctx context.Context, t *TUI) {
	t.printPrompt("Task")
	task, ok := t.readLine(ctx)
	if !ok {
		return
	}
	if strings.TrimSpace(task) == "" {
		t.printLine("(empty task)")
		t.waitEnter(ctx)
		return
	}
	if err := t.Runner.RunTask(ctx, task); err != nil {
		fmt.Fprintf(t.Err, "\nerror: %v\n", err)
	}
	t.waitEnter(ctx)
}

func runConfig(ctx context.Context, t *TUI) {
	if err := t.Runner.RunConfig(ctx); err != nil {
		fmt.Fprintf(t.Err, "\nerror: %v\n", err)
	}
	t.waitEnter(ctx)
}

func runHelp(ctx context.Context, t *TUI) {
	fmt.Fprint(t.Out, "\n"+helpText)
	t.waitEnter(ctx)
}

// readLine reads one line from the input. It returns ok=false on EOF or when
// the context is cancelled, so Ctrl+C does not leave the TUI stuck.
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
		// The goroutine will finish when ReadString returns. On a real terminal
		// that may be after the user presses Enter, but the process is exiting
		// anyway because of the signal.
		go func() { <-ch }()
		return "", false
	}
}

type lineResult struct {
	line string
	err  error
}

func (t *TUI) waitEnter(ctx context.Context) {
	fmt.Fprintf(t.Out, "\n%s", t.color(2, 0, "Press Enter to return to the menu..."))
	buf := make([]byte, 1)
	for {
		ch := make(chan readResult, 1)
		go func() {
			n, err := t.In.Read(buf)
			ch <- readResult{n: n, err: err}
		}()
		select {
		case r := <-ch:
			if r.err != nil {
				return
			}
			if buf[0] == '\r' || buf[0] == '\n' {
				return
			}
		case <-ctx.Done():
			go func() { <-ch }()
			return
		}
	}
}

type readResult struct {
	n   int
	err error
}

const helpText = `Starlight interactive menu

How to use:
  p / 1    Plan mode      read-only exploration, then deliver a plan
  t / 2    Task mode      run a task through the 3-layer agent
  c / 3    Configuration  run the onboarding wizard
  h / 4    Help           show this help
  e / 5    Exit           leave the TUI
  q        Quit           leave the TUI

The TUI is shown by default when the binary is started with no arguments
and no configured task. Use -tui to force it, or -plan/-task to bypass it.
Ctrl+C (or any context cancellation) exits the TUI with code 2.
`
