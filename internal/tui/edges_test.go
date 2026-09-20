package tui

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/madkoding/starlight/internal/agent"
	"github.com/madkoding/starlight/internal/config"
	"github.com/madkoding/starlight/internal/llm"
	"github.com/madkoding/starlight/internal/logx"
	"github.com/madkoding/starlight/internal/sandbox"
	taskpkg "github.com/madkoding/starlight/internal/task"
)

// The last reachable branches: the engine failing because there is no key, the
// reuse of an in-flight run, the literal word "tab", and the input error paths.

// TestRunPlanAndRunTaskWithoutAnEngine: with no key configured there is no engine,
// and both modes must report that instead of calling a nil client.
func TestRunPlanAndRunTaskWithoutAnEngine(t *testing.T) {
	cfg := config.Default()
	cfg.LLM.Provider = "openai"
	cfg.LLM.APIKey = ""
	r := NewAppRunner(&bytes.Buffer{}, &bytes.Buffer{}, cfg, nil, nil, logx.Global())

	if _, err := r.RunPlan(context.Background(), "un prompt", func(string, ...any) {}); err == nil {
		t.Error("RunPlan must fail without an engine")
	}
	if _, err := r.RunTask(context.Background(), "una tarea", func(string, ...any) {}); err == nil {
		t.Error("RunTask must fail without an engine")
	}
}

// TestTypingTheWordTabNavigates: typing "tab" is the escape hatch for a terminal
// that sends a Tab the interface cannot see, so the word has to keep working.
func TestTypingTheWordTabNavigates(t *testing.T) {
	tui := newFakeTUI("tab\ntab\nq\n", &fakeRunner{})
	tui.Run(context.Background())
	// Two Tabs are a round trip: the toggle is Task <-> Plan.
	if tui.screen != ScreenTask {
		t.Errorf("screen = %v, want Task after two 'tab' words", tui.screen)
	}
}

// TestRunTaskWithoutAResultIsExplained: an agent that finishes silently still
// shows a sentence, because an empty bubble reads as a bug.
func TestRunTaskWithoutAResultIsExplained(t *testing.T) {
	runner := &fakeRunner{silentTask: true}
	tui := newFakeTUI("una tarea\nq\n", runner)
	tui.Run(context.Background())
	if !strings.Contains(stripANSI(outputOf(tui)), "without reporting a result") {
		t.Errorf("a silent task must be explained:\n%s", stripANSI(outputOf(tui)))
	}
}

// TestAStaleRunIsCancelledBeforeTheNextOne: a second turn must cancel the first,
// otherwise two runs would write into the same conversation.
func TestAStaleRunIsCancelledBeforeTheNextOne(t *testing.T) {
	runner := &fakeRunner{}
	tui := newFakeTUI("q\n", runner)

	var cancelled bool
	tui.cancelRun = func() { cancelled = true }
	tui.runTask(context.Background(), "una tarea")
	if !cancelled {
		t.Error("starting a task must cancel whatever was still running")
	}

	cancelled = false
	tui.cancelRun = func() { cancelled = true }
	tui.runPlan(context.Background(), "un prompt")
	if !cancelled {
		t.Error("starting a plan must cancel whatever was still running")
	}
}

// TestReadLineReportsAFailingInput: an unreadable input ends the session instead of
// blocking or spinning.
func TestReadLineReportsAFailingInput(t *testing.T) {
	tui := &TUI{
		In:      &failingReader{},
		Out:     &bytes.Buffer{},
		Err:     &bytes.Buffer{},
		Runner:  &fakeRunner{},
		NoColor: true,
	}
	if _, ok := tui.readLine(context.Background()); ok {
		t.Error("a failing input must end the session")
	}
}

// failingReader yields one byte and then fails, which is what a terminal that
// disappears mid-line looks like.
type failingReader struct{ n int }

func (r *failingReader) Read(p []byte) (int, error) {
	if r.n == 0 {
		r.n++
		p[0] = 'x'
		return 1, nil
	}
	return 0, errors.New("the terminal went away")
}

// TestRunPlanReportsARejectedPrompt: the planner refuses an empty instruction, and
// that refusal must reach the chat rather than being swallowed.
func TestRunPlanReportsARejectedPrompt(t *testing.T) {
	r := NewAppRunner(&bytes.Buffer{}, &bytes.Buffer{}, config.Default(), &llm.Client{}, &sandbox.Sandbox{}, logx.Global())
	if _, err := r.RunPlan(context.Background(), "   ", func(string, ...any) {}); err == nil {
		t.Error("an empty prompt must be refused by the planner")
	}
}

// TestRunPlanWithAnAgentThatNeverAnswers: a planner whose agent reports nothing
// still returns, so the interface cannot hang on it. The engine has to be a real
// one for this path: the planner calls it directly.
func TestRunPlanWithAnAgentThatNeverAnswers(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"choices":[{"message":{"content":"listo"}}]}`)
	}))
	defer srv.Close()

	cfg := config.Default()
	cfg.LLM.Provider = "openai"
	cfg.LLM.APIKey = "k"
	cfg.LLM.BaseURL = srv.URL
	cfg.LLM.MaxAttempts = 1
	cfg.Sandbox.Kind = "none"
	r := NewAppRunner(&bytes.Buffer{}, &bytes.Buffer{}, cfg, nil, nil, logx.Global())
	engine, err := llm.New(cfg.LLM, r.Log)
	if err != nil {
		t.Fatalf("llm.New: %v", err)
	}
	r.Engine = engine
	r.newAgent = func(config.Config, *logx.Logger, *llm.Client, *sandbox.Sandbox, taskpkg.Source, bool) AgentRunner {
		return &resultAgent{}
	}
	if _, err := r.RunPlan(context.Background(), "un prompt", func(string, ...any) {}); err != nil {
		t.Errorf("RunPlan: %v", err)
	}
}

// TestIntermediateEscapesContinueTheSequence: the ANSI rule is that bytes in
// 0x20..0x2F are intermediates and the sequence ends at the first byte >= 0x30.
// Both the multi-intermediate form and the "intermediate then final" form have to
// be swallowed, or the measured width of a decorated line is wrong.
func TestIntermediateEscapesContinueTheSequence(t *testing.T) {
	cases := []struct {
		in   string
		want int
	}{
		{"\x1b((Babc", 3},       // intermediate, intermediate, final
		{"\x1b(Babc", 3},        // intermediate then final
		{"\x1b#8abc", 3},        // a digit final after an intermediate
		{"\x1b(\x1b[31mabc", 3}, // a nested escape restarts the parser
		{"\x1b7abc", 3},         // a two-byte escape (save cursor)
		{"\x1b=abc", 3},         // a two-byte escape (keypad mode)
	}
	for _, tc := range cases {
		if got := visibleLen(tc.in); got != tc.want {
			t.Errorf("visibleLen(%q) = %d, want %d", tc.in, got, tc.want)
		}
		if got := stripANSI(tc.in); got != "abc" {
			t.Errorf("stripANSI(%q) = %q, want %q", tc.in, got, "abc")
		}
	}
}

var _ AgentRunner = (*resultAgent)(nil)
var _ = agent.TaskResult{}
