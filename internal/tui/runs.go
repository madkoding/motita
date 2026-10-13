package tui

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/madkoding/motita/internal/agent"
	"github.com/madkoding/motita/internal/config"
	"github.com/madkoding/motita/internal/onboard"
)

// Running what the user asked for: a task, a question in Plan mode, the list of models and the
// setup, with their progress drawn as they go.

// progressSender is the callback handed to the runner. It never blocks: when the
// buffer is full and the run has already been cancelled it drops the line, because
// the conversation it would have updated is gone. Blocking here would keep the
// runner's goroutine alive after the user asked it to stop.
//
// It is one named function rather than the same select inlined twice, so the
// behaviour is stated once and can be tested on its own.
//
// A live snapshot of the model's reasoning (agent.LivePrefix) is dropped here: this view writes
// every line as a frozen block, and a snapshot is meant to be REPLACED by the next one - kept,
// they would fill the conversation with the same sentence growing a word at a time. The
// phase's finished reasoning arrives as its own line and is kept.
func progressSender(ctx context.Context, ch chan string) func(format string, args ...any) {
	return func(format string, args ...any) {
		line := fmt.Sprintf(format, args...)
		if strings.HasPrefix(line, agent.LivePrefix) {
			return
		}
		select {
		case ch <- line:
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
func (t *TUI) awaitRun(ctx context.Context, progress <-chan string, done <-chan runOutcome,
	onProgress func(string)) runOutcome {
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
			// When the outcome and the last lines are ready together, select picks at random, and
			// the lines left in the channel were lost. The last one is often the run's final
			// snapshot of its agents: lost, the panel showed them running forever.
			drainProgress(progress, onProgress)
			return out
		case <-ctx.Done():
			// Waiting here is bounded: the runner watches this very context, and
			// it sends exactly once into a buffered channel.
			return <-done
		}
	}
}

func (t *TUI) runTask(ctx context.Context, task string) {
	t.runTaskAs(ctx, task, task)
}

// runTaskAs runs task and draws `shown` for it in the conversation.
func (t *TUI) runTaskAs(ctx context.Context, task, shown string) {
	if strings.TrimSpace(task) == "" {
		t.drawFrame()
		return
	}
	t.addMessage(AuthorUser, shown)

	// Cancel any previous run before starting a new one: a run followed from the gateway is
	// driven from its own goroutine, and a new task replaces it.
	if cancel := t.currentCancel(); cancel != nil {
		cancel()
	}

	progress := make(chan string, 16)
	runCtx, cancel := context.WithCancel(ctx)
	t.setCancel(t.stopper(cancel), runCtx)

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

	t.startTurn(runCtx, &turn{
		progress: progress,
		done:     done,
		leave:    cancel,
		// Task mode reports its phases through the same callback. A phase is a transient label
		// for the block that is running, so it is overwritten as the run advances and the last
		// visible one is replaced by the result. It is never frozen: unlike a plan tool call, a
		// phase is not an event worth keeping.
		onProgress: func(p string) {
			t.messages[pendingIdx].Text = p
			t.advance()
		},
		onDone: func(outcome runOutcome) {
			t.clearCancel()
			cancel()

			var messageText string
			switch {
			case outcome.err == context.Canceled:
				messageText = t.tr("cancelled.")
			case outcome.err != nil:
				messageText = t.errorText(outcome.err)
			case outcome.result != "":
				messageText = outcome.result
			default:
				messageText = t.tr("the task finished without reporting a result.")
			}
			// The block is settled here: the text goes in and the pending flag is cleared
			// together, so the frame the answer arrives on shows it whole.
			t.messages[pendingIdx].Text = messageText
			t.messages[pendingIdx].Pending = false
			t.endTurn()

			// The agent asked something. The window opens now, after the turn has ended, because
			// the question only exists once the run has returned — and opening it mid-run would
			// put the window over a turn that is still writing to the conversation.
			t.openAskIfPending()
		},
	})
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

	// actionCount and counterIdx back the compact view: one frozen line that counts the tools.
	actionCount int
	hasCounter  bool
	counterIdx  int
}

func (s *planStream) handle(line string) {
	// The drain at the end of a plan calls this directly, past the turn's wrapper.
	if s.tui.takeAgents(line) {
		return
	}
	if _, isPhase := phaseLabel(line); isPhase {
		// A phase is shown as movement (the spinner and the marker on the open
		// block), never as answer text: the block stays empty until real output
		// arrives, and an empty block is dropped when it closes.
		return
	}
	if label, isTool := toolLabel(line); isTool {
		if !s.tui.ShowActions {
			s.countAction()
			return
		}
		s.closePending()
		// The frozen tool line: it must never be written into again.
		s.tui.messages = append(s.tui.messages, Message{Author: AuthorAgent, Text: label, Frozen: true})
		s.openPending()
		return
	}
	s.text += line
	s.tui.messages[s.pendingIdx].Text = s.text
}

// actionLabel is the counter line shown instead of each command.
func actionLabel(n int) string {
	if n == 1 {
		return "1 action"
	}
	return fmt.Sprintf("%d actions", n)
}

// countAction records one tool call as a counter that is updated in place.
func (s *planStream) countAction() {
	s.actionCount++
	if s.hasCounter {
		s.tui.messages[s.counterIdx].Text = actionLabel(s.actionCount)
		return
	}
	s.closePending()
	s.tui.messages = append(s.tui.messages, Message{Author: AuthorAgent, Text: actionLabel(s.actionCount), Frozen: true})
	s.counterIdx = len(s.tui.messages) - 1
	s.hasCounter = true
	s.openPending()
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
		s.setText(s.tui.tr("cancelled."))
	case err != nil:
		s.setText(s.tui.errorText(err))
	case answer != "":
		s.setText(answer)
	case s.text != "":
		s.setText(s.text)
	default:
		s.setText(s.tui.tr("the model returned nothing to show."))
	}
}

func (s *planStream) setText(text string) {
	s.tui.messages[s.pendingIdx].Text = text
	s.tui.messages[s.pendingIdx].Pending = false
}

func (t *TUI) runPlan(ctx context.Context, prompt string) {
	t.runPlanAs(ctx, prompt, prompt)
}

// runPlanAs runs the planner on prompt and draws `shown` for it in the conversation.
func (t *TUI) runPlanAs(ctx context.Context, prompt, shown string) {
	if strings.TrimSpace(prompt) == "" {
		t.drawFrame()
		return
	}
	t.addMessage(AuthorUser, shown)

	if cancel := t.currentCancel(); cancel != nil {
		cancel()
	}

	progress := make(chan string, 64)
	runCtx, cancel := context.WithCancel(ctx)
	t.setCancel(t.stopper(cancel), runCtx)

	done := make(chan runOutcome, 1)
	go func() {
		answer, err := t.Runner.RunPlan(runCtx, prompt, progressSender(runCtx, progress))
		done <- runOutcome{result: answer, err: err}
	}()

	t.beginTurn()
	t.addMessage(AuthorAgent, "")
	stream := &planStream{tui: t, pendingIdx: len(t.messages) - 1}
	t.messages[stream.pendingIdx].Pending = true
	t.advance()

	t.startTurn(runCtx, &turn{
		progress: progress,
		done:     done,
		leave:    cancel,
		onProgress: func(p string) {
			stream.handle(p)
			t.advance()
		},
		onDone: func(result runOutcome) {
			// Whatever the runner queued before reporting is still worth showing: the
			// outcome can arrive while lines are in flight.
			drainProgress(progress, stream.handle)

			t.clearCancel()
			cancel()

			stream.closePending()
			if stream.pendingIdx >= len(t.messages) {
				// The last block was empty and got dropped: settle on a fresh one.
				stream.openPending()
			}
			stream.settle(result.err, result.result)
			t.endTurn()
		},
	})
}

// runModels lists the catalogue inside the panel: the report comes back as text
// and becomes part of the conversation, so it scrolls with everything else and is
// never written over the frame.
func (t *TUI) runModels(ctx context.Context) {
	t.beginTurn()
	t.addMessage(AuthorSystem, t.tr("asking the provider for its catalogue..."))
	pendingIdx := len(t.messages) - 1
	t.advance()

	report, err := t.Runner.RunModels(ctx)
	switch {
	case err != nil:
		t.messages[pendingIdx].Author = AuthorSystem
		t.messages[pendingIdx].Text = t.trf("the catalogue could not be read: %v", err)
	case strings.TrimSpace(report) == "":
		t.messages[pendingIdx].Text = t.tr("the provider published no models.")
	default:
		// A report is a block of labelled lines: it is shown as the model's answer, and drawn
		// as written - re-flowing it as prose collapsed the column its values are aligned on and
		// the indentation that marks the model in use. It ends with how to act on it.
		t.messages[pendingIdx].Author = AuthorAgent
		t.messages[pendingIdx].Preformatted = true
		t.messages[pendingIdx].Text = strings.TrimRight(report, "\n") + "\n\n" +
			t.tr("/models <id> switches to one of them for this session.")
	}
	t.messages[pendingIdx].Pending = false
	t.endTurn()
}

// runConfig runs the setup wizard and applies what it wrote. It suspends the chat while the wizard
// asks its questions, so the prompt and the chat frame do not get drawn on top of each other: the
// wizard is line-oriented (it needs the terminal back in cooked mode to read whole lines), and the
// chat is byte-oriented (it draws one frame at a time in cbreak mode), so running both at once
// leaves the two fighting for the screen.
//
// A setup that was written is APPLIED before this returns (see configReloader): the user ran the
// wizard to change what the next message runs with, and "configuration written" over a session
// still using the old provider was a sentence that was true about the file and false about motita.
func (t *TUI) runConfig(ctx context.Context) {
	t.beginTurn()
	t.addMessage(AuthorSystem, t.tr("opening the setup..."))
	pendingIdx := len(t.messages) - 1
	t.advance()

	err := t.suspendForWizard(ctx)
	switch {
	case errors.Is(err, onboard.ErrCancelled):
		t.messages[pendingIdx].Text = t.tr("setup cancelled: nothing was changed.")
	case err != nil:
		t.messages[pendingIdx].Text = t.trf("the setup failed: %v", err)
	default:
		t.messages[pendingIdx].Text = t.applySetup(ctx)
	}
	t.messages[pendingIdx].Pending = false
	t.endTurn()
}

// configReloader is a runner that can apply the configuration file as it is now.
type configReloader interface {
	ReloadConfig(ctx context.Context) error
}

// applySetup applies the setup the wizard just wrote and says what is in use now.
func (t *TUI) applySetup(ctx context.Context) string {
	rl, ok := t.Runner.(configReloader)
	if !ok {
		return t.tr("setup saved. Restart motita to use it.")
	}
	if err := rl.ReloadConfig(ctx); err != nil {
		return t.trf("setup saved, but it could not be applied now (%v). Restart motita to use it.", err)
	}
	llmCfg := t.Runner.Config().LLM
	text := t.trf("setup saved and applied: now using %s %s %s.", providerName(llmCfg.Provider), glyphMid, llmCfg.Model)
	if llmCfg.APIKey == "" && config.LLMNeedsKey(llmCfg) {
		text += " " + t.tr("There is no API key yet: run /config again to add one.")
	}
	return text
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
	run := t.Runner.RunConfig
	if t.Setup != nil {
		run = t.Setup
	}
	err := run(ctx)

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
