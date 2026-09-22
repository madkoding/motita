package tui

import (
	"context"
	"strings"
	"testing"

	"github.com/madkoding/starlight/internal/agent"
	"github.com/madkoding/starlight/internal/config"
	"github.com/madkoding/starlight/internal/llm"
	"github.com/madkoding/starlight/internal/logx"
	"github.com/madkoding/starlight/internal/sandbox"
	"github.com/madkoding/starlight/internal/task"
)

// askingAgent is an agent whose turn ends by asking questions, which is the only way to reach the
// wiring that carries them from the run to the window.
type askingAgent struct {
	obs func(agent.TaskResult)
	res agent.TaskResult
	cmd string
}

func (a *askingAgent) SetObserver(f func(agent.TaskResult)) { a.obs = f }
func (a *askingAgent) SetProgress(func(string, ...any))     {}
func (a *askingAgent) SetTranscript([]agent.DialogueTurn)   {}
func (a *askingAgent) Transcript() []agent.DialogueTurn     { return nil }
func (a *askingAgent) Run(context.Context) error {
	if a.obs != nil {
		a.obs(a.res)
	}
	return nil
}
func (a *askingAgent) RunCommand(context.Context, string) (string, int, error) {
	return a.cmd, 0, nil
}

// The questions a turn asked are handed to the interface with the request they clarify, which is
// what lets the window open on them and the answers come back attached to their gaps.
func TestAskingTurnHandsQuestionsToTheInterface(t *testing.T) {
	r := NewAppRunner(&strings.Builder{}, &strings.Builder{}, config.Default(),
		&llm.Client{}, &sandbox.Sandbox{}, logx.Global())
	r.newAgent = func(config.Config, *logx.Logger, *llm.Client, *sandbox.Sandbox, task.Source, bool) AgentRunner {
		return &askingAgent{res: agent.TaskResult{
			NeedsInput: true,
			Question:   "which folder?",
			Questions: []agent.AskItem{
				{Text: "which folder?", Assumption: "the current one", Options: []string{"the current one", "/tmp"}},
				{Text: "do I delete it?"},
			},
		}}
	}

	if _, err := r.RunTask(context.Background(), "review the project", func(string, ...any) {}); err != nil {
		t.Fatalf("RunTask: %v", err)
	}
	items, origin := r.TakePendingQuestions()
	if len(items) != 2 {
		t.Fatalf("both questions should reach the interface, got %d", len(items))
	}
	if items[0].Text != "which folder?" || len(items[0].Options) != 2 {
		t.Fatalf("the question should arrive whole, got %+v", items[0])
	}
	if origin != "review the project" {
		t.Fatalf("origin = %q, want the request that was being clarified", origin)
	}
}

// A turn that does not ask leaves nothing pending, so no window opens on a turn that had nothing
// to ask — and a previous turn's questions are not resurrected.
func TestTurnWithoutQuestionsLeavesNothingPending(t *testing.T) {
	r := NewAppRunner(&strings.Builder{}, &strings.Builder{}, config.Default(),
		&llm.Client{}, &sandbox.Sandbox{}, logx.Global())
	r.newAgent = func(config.Config, *logx.Logger, *llm.Client, *sandbox.Sandbox, task.Source, bool) AgentRunner {
		return &askingAgent{res: agent.TaskResult{Pass: true, Summary: "done"}}
	}
	if _, err := r.RunTask(context.Background(), "do something", func(string, ...any) {}); err != nil {
		t.Fatalf("RunTask: %v", err)
	}
	if items, _ := r.TakePendingQuestions(); items != nil {
		t.Fatalf("nothing should be pending, got %+v", items)
	}
}

// Questions reported without NeedsInput are not a question: the flag is what says the turn ended
// by asking rather than by doing, and a stray list without it must not open a window.
func TestQuestionsWithoutTheFlagAreIgnored(t *testing.T) {
	r := NewAppRunner(&strings.Builder{}, &strings.Builder{}, config.Default(),
		&llm.Client{}, &sandbox.Sandbox{}, logx.Global())
	r.newAgent = func(config.Config, *logx.Logger, *llm.Client, *sandbox.Sandbox, task.Source, bool) AgentRunner {
		return &askingAgent{res: agent.TaskResult{
			Pass:      true,
			Questions: []agent.AskItem{{Text: "should not open"}},
		}}
	}
	if _, err := r.RunTask(context.Background(), "do something", func(string, ...any) {}); err != nil {
		t.Fatalf("RunTask: %v", err)
	}
	if items, _ := r.TakePendingQuestions(); items != nil {
		t.Fatalf("a list without NeedsInput must not become a question, got %+v", items)
	}
}

// The input loop hands the keys to the window while it is open, which is the branch that stops a
// pick from becoming a chat message. It runs through Run, because that is where the dispatch is.
//
// In character mode (which is what the interface runs in on a real terminal) each keystroke
// arrives as its own line, so the sequence below is "pick the second option", then "confirm".
// They must produce ONE turn — the request with its answers — and never a message made of keys.
func TestLoopGivesKeysToTheWindow(t *testing.T) {
	rr := &recordingRunner{}
	tui := &TUI{In: &tabReader{src: []byte("2\n\n")}, Out: &strings.Builder{}, Err: &strings.Builder{},
		Runner: rr, NoColor: true, Width: 80, Height: 40}
	tui.ask = newAsk([]agent.AskItem{{Text: "which one?", Options: []string{"one", "two"}}}, "origin")

	_ = tui.Run(context.Background())

	if tui.asking() {
		t.Fatal("the confirm should have closed the window")
	}
	if len(rr.tasks) != 1 {
		t.Fatalf("the pick and the confirm are ONE turn, got %d: %q", len(rr.tasks), rr.tasks)
	}
	got := rr.tasks[0]
	for _, want := range []string{"origin", "which one?", "two"} {
		if !strings.Contains(got, want) {
			t.Fatalf("the turn should carry %q, got:\n%s", want, got)
		}
	}
	// No turn may be just the keys that were meant for the window.
	for _, task := range rr.tasks {
		if task == "2" || task == "" {
			t.Fatalf("the window's keys leaked into a turn: %q", task)
		}
	}
}
