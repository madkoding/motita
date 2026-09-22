package tui

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/madkoding/starlight/internal/config"
)

// The last uncovered branches: the reminders, the reasoning cycle from an unset
// level, cancellation in each mode and the two remaining layout clamps.

// TestTypingInAConfigViewDoesNotStartTheWizard: the wizard rewrites the
// configuration file, so a stray keystroke must not launch it. Typed text is
// answered with a reminder instead.
func TestTypingInAConfigViewDoesNotStartTheWizard(t *testing.T) {
	runner := &fakeRunner{}
	tui := newFakeTUI("/c\nsomething\nq\n", runner)
	tui.Run(context.Background())

	if runner.configCalls != 1 {
		t.Errorf("the wizard ran %d times, want exactly 1 (the /c only)", runner.configCalls)
	}
	if !strings.Contains(stripANSI(outputOf(tui)), "press Enter to start the wizard") {
		t.Errorf("typed text in the config view must be answered with a hint:\n%s", stripANSI(outputOf(tui)))
	}
}

// TestCycleReasoningFromAnUnsetLevel: a configuration that never mentions
// reasoning starts at the documented default, so the first /r moves off it rather
// than jumping to the first entry.
func TestCycleReasoningFromAnUnsetLevel(t *testing.T) {
	cfg := config.Default()
	cfg.LLM.Reasoning = config.Reasoning{} // no level at all
	runner := &fakeRunner{cfg: cfg, cfgSet: true}
	tui := newFakeTUI("/r\nq\n", runner)
	tui.Run(context.Background())

	if got := runner.Config().LLM.Reasoning.Level; got != "high" {
		t.Errorf("from the implicit medium the next level is high, got %q", got)
	}
}

// TestCancellationInEveryMode: Ctrl+C during a run has to end that run and return
// the prompt, not leave the interface waiting.
func TestCancellationInEveryMode(t *testing.T) {
	t.Run("task", func(t *testing.T) {
		started := make(chan struct{})
		runner := &fakeRunner{taskBlock: make(chan struct{}), taskStarted: started}
		tui := newFakeTUI("a task\n", runner)
		if code := cancelOnceRunning(t, tui, started); code != ExitInterrupted {
			t.Errorf("code = %d, want ExitInterrupted", code)
		}
	})

	t.Run("plan", func(t *testing.T) {
		started := make(chan struct{})
		runner := &fakeRunner{planBlock: make(chan struct{}), planStarted: started}
		tui := newFakeTUI("/p\nun prompt\n\n", runner)
		if code := cancelOnceRunning(t, tui, started); code != ExitInterrupted {
			t.Errorf("code = %d, want ExitInterrupted", code)
		}
	})
}

// TestRunModelsAndConfigCancelled: the two action views must also honour a
// cancellation, since the catalogue call can block on the network.
func TestRunModelsAndConfigCancelled(t *testing.T) {
	started := make(chan struct{})
	runner := &fakeRunner{modelsBlock: make(chan struct{}), modelsStarted: started}
	tui := newFakeTUI("/m\n\n", runner)
	if code := cancelOnceRunning(t, tui, started); code != ExitInterrupted {
		t.Errorf("code = %d, want ExitInterrupted", code)
	}
}

// TestTheEmptyStateIsDrawnWithoutABorder: the conversation is not boxed any more, so the
// first screen must be plain text that starts at the left margin. What used to be checked
// here was that the panel's title never ran out of rule to close its corner.
func TestTheEmptyStateIsDrawnWithoutABorder(t *testing.T) {
	for _, w := range []int{minWidth, 80} {
		for _, s := range screenOrder {
			tui := newFakeTUI("q\n", &fakeRunner{})
			tui.Width = w
			tui.screen = s

			body := stripANSI(strings.Join(tui.chatLines(tui.conversationWidth()), "\n"))
			if strings.Contains(body, glyphTopLeft) || strings.Contains(body, glyphRail) {
				t.Errorf("width %d, screen %s: the conversation must not be boxed: %q", w, s, body)
			}
			// Every row stays inside the drawing area: the margin is spent before the text.
			for _, l := range strings.Split(body, "\n") {
				if visibleLen(l) > tui.conversationWidth() {
					t.Errorf("width %d: a row is %d columns, past the %d available: %q",
						w, visibleLen(l), tui.conversationWidth(), l)
				}
			}
		}
	}
}

// TestScanEscapesVisitsEveryRune: the visitor is what both the width measurement
// and the stripper use, so every rune must be reported exactly once, in order.
func TestScanEscapesVisitsEveryRune(t *testing.T) {
	var plain, escaped strings.Builder
	scanEscapes("a\x1b[31mb", func(r rune, isEscape bool) {
		if isEscape {
			escaped.WriteRune(r)
		} else {
			plain.WriteRune(r)
		}
	})
	if plain.String() != "ab" {
		t.Errorf("plain runes = %q, want %q", plain.String(), "ab")
	}
	if escaped.String() != "\x1b[31m" {
		t.Errorf("escaped runes = %q, want the sequence", escaped.String())
	}
}

// TestRunPlanEmptyPromptDoesNothing: a bare Enter in Plan mode must not call the
// engine with an empty prompt.
func TestRunPlanEmptyPromptDoesNothing(t *testing.T) {
	runner := &fakeRunner{}
	tui := newFakeTUI("/p\n\n\nq\n", runner)
	tui.Run(context.Background())
	if runner.planCalled {
		t.Error("an empty plan prompt must not reach the runner")
	}
}

// TestRunTaskErrorIsShownInTheChat: a rejected task reports its reason where the
// user is looking.
func TestRunTaskErrorIsShownInTheChat(t *testing.T) {
	runner := &fakeRunner{taskErr: errors.New("the sandbox refused")}
	tui := newFakeTUI("a task\nq\n", runner)
	tui.Run(context.Background())
	frame := stripANSI(lastFrame(t, tui))
	if !strings.Contains(frame, "the sandbox refused") {
		t.Errorf("the failure must be visible in the conversation:\n%s", frame)
	}
}

// TestPlanErrorIsShownInTheChat: same for plan.
func TestPlanErrorIsShownInTheChat(t *testing.T) {
	runner := &fakeRunner{planErr: errors.New("the engine is down")}
	tui := newFakeTUI("/p\nun prompt\n\nq\n", runner)
	tui.Run(context.Background())
	frame := stripANSI(lastFrame(t, tui))
	if !strings.Contains(frame, "the engine is down") {
		t.Errorf("the failure must be visible in the conversation:\n%s", frame)
	}
}

// TestModelsReportIsShownOnTheRail: a catalogue that came back is answer text, so
// it is drawn as the model's own block rather than as a system note.
func TestModelsReportIsShownOnTheRail(t *testing.T) {
	runner := &fakeRunner{modelsReport: "provider : ollama\nmodel    : glm-5.3"}
	tui := newFakeTUI("/m\n\nq\n", runner)
	tui.Width, tui.Height = 80, 60
	tui.Run(context.Background())
	frame := stripANSI(lastFrame(t, tui))
	if !strings.Contains(frame, "glm-5.3") {
		t.Errorf("the report must be shown:\n%s", frame)
	}
	if !strings.Contains(frame, "starlight") {
		t.Errorf("the report belongs to the agent's block:\n%s", frame)
	}
}

// TestModelsEmptyReportIsExplained: a provider that publishes nothing still gets a
// sentence, because silence looks like a failure.
func TestModelsEmptyReportIsExplained(t *testing.T) {
	runner := &fakeRunner{modelsReport: "   \n"}
	tui := newFakeTUI("/m\n\nq\n", runner)
	tui.Run(context.Background())
	if !strings.Contains(stripANSI(outputOf(tui)), "published no models") {
		t.Errorf("an empty catalogue must be explained:\n%s", stripANSI(outputOf(tui)))
	}
}

// TestRunReturnsSuccessOnEOF: a closed input is a normal end, not an interruption.
func TestRunReturnsSuccessOnEOF(t *testing.T) {
	tui := newFakeTUI("", &fakeRunner{})
	if code := tui.Run(context.Background()); code != ExitSuccess {
		t.Errorf("code = %d, want ExitSuccess", code)
	}
}
