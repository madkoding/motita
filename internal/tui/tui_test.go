package tui

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"
)

type fakeRunner struct {
	planCalled   bool
	taskCalled   bool
	configCalled bool
	modelsCalled bool
	planAnswer   string
	planErr      error
	taskErr      error
	configErr    error
	modelsErr    error
	lastPrompt   string
	lastTask     string
	lastTrace    []string
	out          io.Writer
}

func (f *fakeRunner) RunPlan(ctx context.Context, prompt string, trace func(string, ...any)) (string, error) {
	f.planCalled = true
	f.lastPrompt = prompt
	if trace != nil {
		trace("trace line")
	}
	if f.planErr != nil {
		return "", f.planErr
	}
	// The real runner writes the answer to Out; the TUI does not print it again.
	if f.planAnswer != "" && f.out != nil {
		fmt.Fprintln(f.out, f.planAnswer)
	}
	return f.planAnswer, nil
}

func (f *fakeRunner) RunTask(ctx context.Context, task string) error {
	f.taskCalled = true
	f.lastTask = task
	return f.taskErr
}

func (f *fakeRunner) RunConfig(ctx context.Context) error {
	f.configCalled = true
	return f.configErr
}

func (f *fakeRunner) RunModels(ctx context.Context) error {
	f.modelsCalled = true
	return f.modelsErr
}

func newFakeTUI(inputs string, runner Runner) *TUI {
	out := &bytes.Buffer{}
	// A fakeRunner needs somewhere to write the plan answer, the way the real one
	// writes to the TUI's output.
	if fr, ok := runner.(*fakeRunner); ok {
		fr.out = out
	}
	return &TUI{
		In:      strings.NewReader(inputs),
		Out:     out,
		Err:     &bytes.Buffer{},
		Runner:  runner,
		NoColor: true,
	}
}

func outputOf(t *TUI) string { return t.Out.(*bytes.Buffer).String() }
func errOf(t *TUI) string    { return t.Err.(*bytes.Buffer).String() }

// TestRunInterruptedByContext: Ctrl+C is simulated by cancelling the context
// while the TUI is waiting for the menu choice.
func TestRunInterruptedByContext(t *testing.T) {
	r, _ := io.Pipe() // blocks forever, so only the context can end the wait
	tui := &TUI{In: r, Out: &bytes.Buffer{}, Err: &bytes.Buffer{}, Runner: &fakeRunner{}, NoColor: true}
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()
	if code := tui.Run(ctx); code != ExitInterrupted {
		t.Fatalf("code = %d, want ExitInterrupted", code)
	}
}

func TestRunSelectPlanMode(t *testing.T) {
	runner := &fakeRunner{planAnswer: "the plan"}
	tui := newFakeTUI("1\nprompt\n\n", runner)
	tui.Run(context.Background())
	if !runner.planCalled {
		t.Fatal("RunPlan was not called")
	}
	if runner.lastPrompt != "prompt" {
		t.Errorf("prompt = %q", runner.lastPrompt)
	}
	if !strings.Contains(outputOf(tui), "the plan") {
		t.Errorf("the runner's answer must reach the screen: %q", outputOf(tui))
	}
	// The TUI must not print it a second time.
	if strings.Count(outputOf(tui), "the plan") != 1 {
		t.Errorf("the answer appears more than once: %q", outputOf(tui))
	}
}

func TestRunSelectPlanModeByLetter(t *testing.T) {
	runner := &fakeRunner{planAnswer: "the plan"}
	tui := newFakeTUI("p\nprompt\n\n", runner)
	tui.Run(context.Background())
	if !runner.planCalled {
		t.Fatal("RunPlan was not called")
	}
}

func TestRunPlanEmptyPrompt(t *testing.T) {
	runner := &fakeRunner{}
	tui := newFakeTUI("1\n\n\nq\n", runner)
	tui.Run(context.Background())
	if runner.planCalled {
		t.Fatal("RunPlan should not be called for an empty prompt")
	}
}

func TestRunPlanError(t *testing.T) {
	runner := &fakeRunner{planErr: errors.New("llm down")}
	tui := newFakeTUI("1\nprompt\n\n", runner)
	tui.Run(context.Background())
	if !strings.Contains(errOf(tui), "llm down") {
		t.Errorf("error not reported: %q", errOf(tui))
	}
}

func TestRunPlanAnswerEmpty(t *testing.T) {
	runner := &fakeRunner{planAnswer: ""}
	tui := newFakeTUI("1\nprompt\n\n", runner)
	tui.Run(context.Background())
	if !runner.planCalled {
		t.Fatal("RunPlan should have been called")
	}
}

func TestRunSelectTaskMode(t *testing.T) {
	runner := &fakeRunner{}
	tui := newFakeTUI("2\nmy task\n\n", runner)
	tui.Run(context.Background())
	if !runner.taskCalled {
		t.Fatal("RunTask was not called")
	}
	if runner.lastTask != "my task" {
		t.Errorf("task = %q", runner.lastTask)
	}
}

func TestRunSelectTaskByLetter(t *testing.T) {
	runner := &fakeRunner{}
	tui := newFakeTUI("t\ntask\n\n", runner)
	tui.Run(context.Background())
	if !runner.taskCalled {
		t.Fatal("RunTask was not called")
	}
}

func TestRunTaskError(t *testing.T) {
	runner := &fakeRunner{taskErr: errors.New("boom")}
	tui := newFakeTUI("2\ntask\n\n", runner)
	tui.Run(context.Background())
	if !strings.Contains(errOf(tui), "boom") {
		t.Errorf("error not reported: %q", errOf(tui))
	}
}

func TestRunTaskEmpty(t *testing.T) {
	runner := &fakeRunner{}
	tui := newFakeTUI("2\n\nq\n", runner)
	tui.Run(context.Background())
	if runner.taskCalled {
		t.Fatal("RunTask should not be called for an empty task")
	}
}

func TestRunSelectConfig(t *testing.T) {
	runner := &fakeRunner{}
	tui := newFakeTUI("3\n\n", runner)
	tui.Run(context.Background())
	if !runner.configCalled {
		t.Fatal("RunConfig was not called")
	}
}

func TestRunSelectConfigByLetter(t *testing.T) {
	runner := &fakeRunner{}
	tui := newFakeTUI("c\n\n", runner)
	tui.Run(context.Background())
	if !runner.configCalled {
		t.Fatal("RunConfig was not called")
	}
}

func TestRunConfigError(t *testing.T) {
	runner := &fakeRunner{configErr: errors.New("wizard failed")}
	tui := newFakeTUI("3\n\n", runner)
	tui.Run(context.Background())
	if !strings.Contains(errOf(tui), "wizard failed") {
		t.Errorf("error not reported: %q", errOf(tui))
	}
}

func TestRunSelectHelp(t *testing.T) {
	tui := newFakeTUI("5\n\nq\n", &fakeRunner{})
	tui.Run(context.Background())
	if !strings.Contains(outputOf(tui), "Starlight interactive menu") {
		t.Errorf("help not printed: %q", outputOf(tui))
	}
}

func TestRunSelectModels(t *testing.T) {
	runner := &fakeRunner{}
	tui := newFakeTUI("4\n\nq\n", runner)
	tui.Run(context.Background())
	if !runner.modelsCalled {
		t.Fatal("RunModels was not called")
	}
}

func TestRunModelsByLetter(t *testing.T) {
	runner := &fakeRunner{}
	tui := newFakeTUI("m\n\nq\n", runner)
	tui.Run(context.Background())
	if !runner.modelsCalled {
		t.Fatal("RunModels was not called")
	}
}

func TestRunModelsError(t *testing.T) {
	runner := &fakeRunner{modelsErr: errors.New("catalogue unavailable")}
	tui := newFakeTUI("m\n\n", runner)
	tui.Run(context.Background())
	if !strings.Contains(errOf(tui), "catalogue unavailable") {
		t.Errorf("error not reported: %q", errOf(tui))
	}
}

func TestRunHelpByLetter(t *testing.T) {
	tui := newFakeTUI("h\nq\n", &fakeRunner{})
	tui.Run(context.Background())
	if !strings.Contains(outputOf(tui), "Starlight interactive menu") {
		t.Errorf("help not printed: %q", outputOf(tui))
	}
}

func TestRunSelectExit(t *testing.T) {
	tui := newFakeTUI("6\n", &fakeRunner{})
	if code := tui.Run(context.Background()); code != 0 {
		t.Fatalf("code = %d", code)
	}
}

func TestRunSelectExitByLetter(t *testing.T) {
	tui := newFakeTUI("e\n", &fakeRunner{})
	tui.Run(context.Background())
}

func TestRunQuitByLetter(t *testing.T) {
	tui := newFakeTUI("q\n", &fakeRunner{})
	if code := tui.Run(context.Background()); code != 0 {
		t.Fatalf("code = %d", code)
	}
}

func TestRunUnknownOption(t *testing.T) {
	tui := newFakeTUI("x\n\nq\n", &fakeRunner{})
	tui.Run(context.Background())
	if !strings.Contains(outputOf(tui), "Unknown option") {
		t.Errorf("unknown option message missing: %q", outputOf(tui))
	}
}

func TestRunEOF(t *testing.T) {
	tui := newFakeTUI("", &fakeRunner{})
	if code := tui.Run(context.Background()); code != 0 {
		t.Fatalf("code = %d", code)
	}
}

func TestNoColor(t *testing.T) {
	tui := &TUI{NoColor: true}
	if got := tui.color(1, 0, "x"); got != "x" {
		t.Errorf("NoColor returned %q", got)
	}
}

func TestColor(t *testing.T) {
	tui := &TUI{NoColor: false}
	got := tui.color(1, 0, "x")
	if !strings.HasPrefix(got, "\x1b[") {
		t.Errorf("color returned %q", got)
	}
}

func TestColorBackground(t *testing.T) {
	tui := &TUI{NoColor: false}
	got := tui.color(1, 2, "x")
	if !strings.Contains(got, ";4") {
		t.Errorf("background color missing: %q", got)
	}
}

func TestMenuOptionStrings(t *testing.T) {
	want := []string{"Plan mode", "Task mode", "Configuration", "Models & providers", "Help", "Exit"}
	for i, opt := range menuOptions {
		if opt.String() != want[i] {
			t.Errorf("option %d = %q, expected %q", i, opt.String(), want[i])
		}
	}
}

func TestMenuOptionKeys(t *testing.T) {
	want := []string{"p", "t", "c", "m", "h", "e"}
	for i, opt := range menuOptions {
		if opt.Key() != want[i] {
			t.Errorf("key %d = %q, expected %q", i, opt.Key(), want[i])
		}
	}
}

func TestAppRunnerImplementsRunner(t *testing.T) {
	var _ Runner = (*AppRunner)(nil)
}

func TestReadLineEOF(t *testing.T) {
	tui := newFakeTUI("", &fakeRunner{})
	if _, ok := tui.readLine(context.Background()); ok {
		t.Error("expected false on EOF")
	}
}

// TestWaitEnterCancelsOnContext: Ctrl+C while waiting for "Enter" must return.
func TestWaitEnterCancelsOnContext(t *testing.T) {
	r, _ := io.Pipe()
	tui := &TUI{In: r, Out: &bytes.Buffer{}, Err: &bytes.Buffer{}, Runner: &fakeRunner{}, NoColor: true}
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()
	done := make(chan struct{})
	go func() {
		tui.waitEnter(ctx)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("waitEnter did not return on context cancellation")
	}
}

func TestWaitEnterEOF(t *testing.T) {
	tui := newFakeTUI("", &fakeRunner{})
	tui.waitEnter(context.Background()) // must return without panic
}

func TestWaitEnterReadsEnter(t *testing.T) {
	tui := newFakeTUI("\n", &fakeRunner{})
	tui.waitEnter(context.Background())
}

func TestRunPlanEOFOnPrompt(t *testing.T) {
	runner := &fakeRunner{}
	tui := newFakeTUI("1\n", runner)
	tui.Run(context.Background())
	if runner.planCalled {
		t.Fatal("RunPlan should not be called after EOF on prompt")
	}
}

func TestRunTaskEOFOnTask(t *testing.T) {
	runner := &fakeRunner{}
	tui := newFakeTUI("2\n", runner)
	tui.Run(context.Background())
	if runner.taskCalled {
		t.Fatal("RunTask should not be called after EOF on task")
	}
}

func TestRunPlanTracePrinted(t *testing.T) {
	runner := &fakeRunner{planAnswer: "answer"}
	tui := newFakeTUI("1\nprompt\n\n", runner)
	tui.Run(context.Background())
	if !strings.Contains(errOf(tui), "trace line") {
		t.Errorf("trace not printed: %q", errOf(tui))
	}
}

func TestMenuOptionInvalid(t *testing.T) {
	defer func() { recover() }()
	_ = MenuOption(99).String()
	t.Error("expected panic")
}

func TestMenuOptionKeyInvalid(t *testing.T) {
	defer func() { recover() }()
	_ = MenuOption(99).Key()
	t.Error("expected panic")
}

func TestMenuOptionActionInvalid(t *testing.T) {
	defer func() { recover() }()
	MenuOption(99).action()
	t.Error("expected panic")
}

func TestNewDefaults(t *testing.T) {
	tui := New(&fakeRunner{})
	if tui.In == nil || tui.Out == nil || tui.Err == nil || tui.Runner == nil {
		t.Error("New produced an incomplete TUI")
	}
	if tui.input() == nil {
		t.Error("input returned nil")
	}
	if tui.input() != tui.input() {
		t.Error("input should reuse the same reader")
	}
}
