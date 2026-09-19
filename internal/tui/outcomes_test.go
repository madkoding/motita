package tui

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

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
	for _, tc := range []struct {
		name   string
		runner *fakeRunner
		input  string
	}{
		{"task", &fakeRunner{taskBlock: make(chan struct{})}, "una tarea\n"},
		{"plan", &fakeRunner{planBlock: make(chan struct{})}, "/p\nun prompt\n\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tui := newFakeTUI(tc.input, tc.runner)
			ctx, cancel := context.WithCancel(context.Background())
			done := make(chan int, 1)
			go func() { done <- tui.Run(ctx) }()

			// Give the run a moment to start, then cancel it.
			time.Sleep(60 * time.Millisecond)
			cancel()

			select {
			case code := <-done:
				if code != ExitInterrupted {
					t.Errorf("code = %d, want ExitInterrupted", code)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("the interface did not return after the context was cancelled")
			}
		})
	}
}

// TestRunModelsAndConfigCancelled: the two action views must also honour a
// cancellation, since the catalogue call can block on the network.
func TestRunModelsAndConfigCancelled(t *testing.T) {
	runner := &fakeRunner{modelsBlock: make(chan struct{})}
	tui := newFakeTUI("/m\n\n", runner)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan int, 1)
	go func() { done <- tui.Run(ctx) }()
	time.Sleep(60 * time.Millisecond)
	cancel()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("the models view did not return after the context was cancelled")
	}
}

// TestChatTopRowAlwaysHasARule: the rule needs no clamp because the width is
// already floored. This pins the invariant: if either the floor or the longest
// title changes, the border must still be well formed, and the test says so
// instead of the layout panicking in strings.Repeat.
func TestChatTopRowAlwaysHasARule(t *testing.T) {
	for _, w := range []int{1, minWidth, 80} {
		for _, s := range screenOrder {
			tui := newFakeTUI("q\n", &fakeRunner{})
			tui.Width = w
			tui.screen = s
			top := stripANSI(strings.Join(tui.chatTopRow(), "\n"))
			if !strings.HasPrefix(top, "  "+glyphTopLeft) || !strings.HasSuffix(top, glyphTopRight) {
				t.Errorf("width %d, screen %s: the border is malformed: %q", w, s, top)
			}
			if !strings.Contains(top, strings.TrimSpace(s.String())) {
				t.Errorf("width %d: the title is missing from %q", w, top)
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
	tui := newFakeTUI("una tarea\nq\n", runner)
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
