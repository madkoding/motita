package tui

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/madkoding/starlight/internal/config"
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
	lastProgress []string
	mu           sync.Mutex
	out          io.Writer
	cfg          config.Config
}

func (f *fakeRunner) RunPlan(ctx context.Context, prompt string, progress func(string, ...any)) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.planCalled = true
	f.lastPrompt = prompt
	if progress != nil {
		progress("analysing...")
	}
	if f.planErr != nil {
		return "", f.planErr
	}
	return f.planAnswer, nil
}

func (f *fakeRunner) RunTask(ctx context.Context, task string, progress func(string, ...any)) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.taskCalled = true
	f.lastTask = task
	if progress != nil {
		progress("working...")
	}
	if f.taskErr != nil {
		return "", f.taskErr
	}
	return "completed: mock result", nil
}

func (f *fakeRunner) RunConfig(ctx context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.configCalled = true
	return f.configErr
}

func (f *fakeRunner) RunModels(ctx context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.modelsCalled = true
	return f.modelsErr
}

func (f *fakeRunner) Config() config.Config {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.cfg.LLM.Provider == "" {
		f.cfg = config.Default()
	}
	return f.cfg
}

func (f *fakeRunner) SetReasoning(level string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.cfg.LLM.Reasoning.Level = level
	f.cfg.LLM.Reasoning.Enabled = level != "off"
}

func newFakeTUI(inputs string, runner Runner) *TUI {
	out := &bytes.Buffer{}
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

func TestRunInterruptedByContext(t *testing.T) {
	r, _ := io.Pipe()
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

func TestRunTaskByDefault(t *testing.T) {
	runner := &fakeRunner{}
	tui := newFakeTUI("my task\n\n", runner)
	tui.Run(context.Background())
	if !runner.taskCalled {
		t.Fatal("RunTask was not called by default")
	}
	if runner.lastTask != "my task" {
		t.Errorf("task = %q", runner.lastTask)
	}
	if !strings.Contains(outputOf(tui), "completed: mock result") {
		t.Errorf("task result not shown: %q", outputOf(tui))
	}
}

func TestRunTaskEmptyThenQuit(t *testing.T) {
	runner := &fakeRunner{}
	tui := newFakeTUI("\nq\n", runner)
	tui.Run(context.Background())
	if runner.taskCalled {
		t.Fatal("RunTask should not be called for an empty task")
	}
}

func TestRunTaskError(t *testing.T) {
	runner := &fakeRunner{taskErr: errors.New("boom")}
	tui := newFakeTUI("task\n\n", runner)
	tui.Run(context.Background())
	if !strings.Contains(outputOf(tui), "error: boom") {
		t.Errorf("error not reported: %q", outputOf(tui))
	}
}

func TestSwitchToPlanAndBack(t *testing.T) {
	runner := &fakeRunner{planAnswer: "the plan"}
	tui := newFakeTUI("/p\nprompt\n\n/t\ntask\n\nq\n", runner)
	tui.Run(context.Background())
	if !runner.planCalled {
		t.Fatal("RunPlan was not called")
	}
	if runner.lastPrompt != "prompt" {
		t.Errorf("prompt = %q", runner.lastPrompt)
	}
	if !runner.taskCalled {
		t.Fatal("RunTask was not called after switching back")
	}
	if !strings.Contains(outputOf(tui), "completed: mock result") {
		t.Errorf("task result not shown after switching back: %q", outputOf(tui))
	}
}

func TestPlanEmptyPrompt(t *testing.T) {
	runner := &fakeRunner{}
	tui := newFakeTUI("/p\n\nq\n", runner)
	tui.Run(context.Background())
	if runner.planCalled {
		t.Fatal("RunPlan should not be called for an empty prompt")
	}
}

func TestPlanError(t *testing.T) {
	runner := &fakeRunner{planErr: errors.New("llm down")}
	tui := newFakeTUI("/p\nprompt\n\nq\n", runner)
	tui.Run(context.Background())
	if !strings.Contains(outputOf(tui), "error: llm down") {
		t.Errorf("error not reported: %q", outputOf(tui))
	}
}

func TestRunConfig(t *testing.T) {
	runner := &fakeRunner{}
	tui := newFakeTUI("/c\n\nq\n", runner)
	tui.Run(context.Background())
	if !runner.configCalled {
		t.Fatal("RunConfig was not called")
	}
}

func TestRunConfigError(t *testing.T) {
	runner := &fakeRunner{configErr: errors.New("wizard failed")}
	tui := newFakeTUI("/c\n\nq\n", runner)
	tui.Run(context.Background())
	if !strings.Contains(outputOf(tui), "config error: wizard failed") {
		t.Errorf("error not reported: %q", outputOf(tui))
	}
}

func TestRunModels(t *testing.T) {
	runner := &fakeRunner{}
	tui := newFakeTUI("/m\n\nq\n", runner)
	tui.Run(context.Background())
	if !runner.modelsCalled {
		t.Fatal("RunModels was not called")
	}
}

func TestRunModelsError(t *testing.T) {
	runner := &fakeRunner{modelsErr: errors.New("catalogue unavailable")}
	tui := newFakeTUI("/m\n\nq\n", runner)
	tui.Run(context.Background())
	if !strings.Contains(outputOf(tui), "models error: catalogue unavailable") {
		t.Errorf("error not reported: %q", outputOf(tui))
	}
}

func TestRunHelp(t *testing.T) {
	tui := newFakeTUI("h\nq\n", &fakeRunner{})
	tui.Run(context.Background())
	if !strings.Contains(outputOf(tui), "Starlight chat") {
		t.Errorf("help not printed: %q", outputOf(tui))
	}
}

func TestRunQuit(t *testing.T) {
	tui := newFakeTUI("q\n", &fakeRunner{})
	if code := tui.Run(context.Background()); code != 0 {
		t.Fatalf("code = %d", code)
	}
}

func TestRunUnknownInputIsTask(t *testing.T) {
	runner := &fakeRunner{}
	tui := newFakeTUI("some random task\nq\n", runner)
	tui.Run(context.Background())
	if !runner.taskCalled {
		t.Fatal("unknown input in Task screen should be treated as a task")
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

func TestCycleReasoning(t *testing.T) {
	runner := &fakeRunner{cfg: config.Default()}
	runner.cfg.LLM.Reasoning.Level = "medium"
	tui := newFakeTUI("/r\n/r\n/r\n/r\nq\n", runner)
	tui.Run(context.Background())
	if runner.cfg.LLM.Reasoning.Level != "medium" {
		t.Errorf("expected to cycle back to medium, got %q", runner.cfg.LLM.Reasoning.Level)
	}
	if !runner.cfg.LLM.Reasoning.Enabled {
		t.Error("reasoning should be enabled after the last /r set it to medium")
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
	if tui.screen != ScreenTask {
		t.Errorf("default screen = %v, want Task", tui.screen)
	}
}
