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
	configCalls  int
	// silentTask makes RunTask report nothing, which exercises the explanation the
	// interface shows instead of an empty bubble.
	silentTask   bool
	modelsCalled bool
	modelsCalls  int
	modelsReport string
	planProgress []string
	// The *Block channels make a run hang until the test closes them, which is how
	// cancellation in the middle of a turn is exercised.
	taskBlock   chan struct{}
	planBlock   chan struct{}
	modelsBlock chan struct{}
	// The *Started channels are closed as soon as the fake is inside the run, so a
	// cancellation test can wait for that fact instead of sleeping and hoping. A
	// timing race here is what made the coverage gate flake on a slower machine.
	taskStarted   chan struct{}
	planStarted   chan struct{}
	modelsStarted chan struct{}
	planAnswer    string
	planErr       error
	// report is what ConversationReport returns, and reset records that the session
	// was dropped. Both are fields rather than live behaviour because a fake that
	// reached a real session would depend on the network.
	report       string
	reset        bool
	taskErr      error
	configErr    error
	modelsErr    error
	lastPrompt   string
	lastTask     string
	lastProgress []string
	mu           sync.Mutex
	out          io.Writer
	cfg          config.Config
	// cfgSet records that the test supplied a configuration of its own, which is
	// what makes an empty provider a meaningful value rather than "unset".
	cfgSet bool
}

func (f *fakeRunner) RunPlan(ctx context.Context, prompt string, progress func(string, ...any)) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.planCalled = true
	f.lastPrompt = prompt
	if f.planBlock != nil {
		if f.planStarted != nil {
			close(f.planStarted)
		}
		select {
		case <-f.planBlock:
		case <-ctx.Done():
		}
		return "", ctx.Err()
	}
	if progress != nil {
		if len(f.planProgress) > 0 {
			for _, p := range f.planProgress {
				progress("%s", p)
			}
		} else {
			progress("analysing...")
		}
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
	if f.taskBlock != nil {
		if f.taskStarted != nil {
			close(f.taskStarted)
		}
		select {
		case <-f.taskBlock:
		case <-ctx.Done():
		}
		return "", ctx.Err()
	}
	if progress != nil {
		progress("working...")
	}
	if f.taskErr != nil {
		return "", f.taskErr
	}
	if f.silentTask {
		return "", nil
	}
	return "completed: mock result", nil
}

func (f *fakeRunner) RunConfig(ctx context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.configCalled = true
	f.configCalls++
	return f.configErr
}

func (f *fakeRunner) RunModels(ctx context.Context) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.modelsCalled = true
	f.modelsCalls++
	if f.modelsBlock != nil {
		if f.modelsStarted != nil {
			close(f.modelsStarted)
		}
		select {
		case <-f.modelsBlock:
		case <-ctx.Done():
		}
		return "", ctx.Err()
	}
	return f.modelsReport, f.modelsErr
}

// Config returns the configuration under test. The default is only filled in when
// the test asked for nothing at all: an explicit configuration whose provider is
// empty is a case worth testing, and substituting a default for it would silently
// hide the behaviour under test.
func (f *fakeRunner) Config() config.Config {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.cfgSet {
		f.cfg = config.Default()
	}
	return f.cfg
}

func (f *fakeRunner) ConversationReport() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.report
}

func (f *fakeRunner) ResetConversation() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reset = true
}

func (f *fakeRunner) SetReasoning(level string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.cfgSet {
		f.cfg = config.Default()
		f.cfgSet = true
	}
	f.cfg.LLM.Reasoning.Level = level
	f.cfg.LLM.Reasoning.Enabled = level != "off"
}

// cancelOnceRunning starts the interface, waits until the runner reports that it is
// inside the turn, and only then cancels the context. Waiting for the signal rather
// than sleeping is what makes the cancellation coverage deterministic: with a sleep
// the run may not have started yet on a slow machine, the cancel lands before the
// select, and the branch this test exists for is never taken.
func cancelOnceRunning(t *testing.T, tui *TUI, started <-chan struct{}) int {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan int, 1)
	go func() { done <- tui.Run(ctx) }()

	select {
	case <-started:
	case <-time.After(5 * time.Second):
		cancel()
		t.Fatal("the run never started, so cancellation could not be exercised")
	}
	cancel()

	select {
	case code := <-done:
		return code
	case <-time.After(5 * time.Second):
		t.Fatal("the interface did not return after the context was cancelled")
		return 0
	}
}

// configWithKey builds a configuration with the given API key, for the tests that
// depend on the key being present or absent rather than on its value.
func configWithKey(key string) config.Config {
	cfg := config.Default()
	cfg.LLM.APIKey = key
	return cfg
}

func newFakeTUI(inputs string, runner Runner) *TUI {
	out := &bytes.Buffer{}
	if fr, ok := runner.(*fakeRunner); ok {
		fr.out = out
	}
	return &TUI{
		In:      &tabReader{src: []byte(inputs)},
		Out:     out,
		Err:     &bytes.Buffer{},
		Runner:  runner,
		NoColor: true,
		// A pinned size keeps the layout deterministic: without it the frame
		// would depend on COLUMNS/LINES in the environment that runs the test,
		// which is how a test starts passing or failing for no reason.
		Width:  80,
		Height: 40,
	}
}

// tabReader stands in for a terminal delivering keystrokes. It turns the literal
// two-character sequence "	" into a single Tab byte, so tests can be written with
// readable input like "	\nq\n".
//
// It hands out ONE byte per Read on purpose. bufio fills its whole buffer with a
// single Read call, so a reader that returns the entire string at once would have
// its later "	" sequences delivered verbatim: only the first one would be
// converted, and the rest would arrive as a backslash and a letter. Feeding one
// byte at a time is what a terminal actually does, and it is what makes the
// conversion apply to every Tab in the input.
type tabReader struct {
	src []byte
	idx int
}

func (r *tabReader) Read(p []byte) (int, error) {
	if r.idx >= len(r.src) {
		return 0, io.EOF
	}
	if len(p) == 0 {
		return 0, nil
	}
	if r.idx+1 < len(r.src) && r.src[r.idx] == '\\' && r.src[r.idx+1] == 't' {
		p[0] = '	'
		r.idx += 2
		return 1, nil
	}
	p[0] = r.src[r.idx]
	r.idx++
	return 1, nil
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

func TestPlanAnswerIsShown(t *testing.T) {
	runner := &fakeRunner{planAnswer: "the plan result"}
	tui := newFakeTUI("/p\nprompt\n\nq\n", runner)
	tui.Run(context.Background())
	if !strings.Contains(outputOf(tui), "the plan result") {
		t.Errorf("plan answer not shown: %q", outputOf(tui))
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
	if !strings.Contains(outputOf(tui), "the wizard failed: wizard failed") {
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
	if !strings.Contains(outputOf(tui), "the catalogue could not be read: catalogue unavailable") {
		t.Errorf("error not reported: %q", outputOf(tui))
	}
}

func TestRunHelp(t *testing.T) {
	tui := newFakeTUI("h\nq\n", &fakeRunner{})
	tui.Run(context.Background())
	// The panel is a window: the help is longer than the frame, so the assertion is on
	// what the help actually brought to the screen, not on its opening line.
	if !strings.Contains(outputOf(tui), "switch mode") {
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
