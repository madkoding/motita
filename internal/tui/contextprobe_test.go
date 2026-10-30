package tui

import (
	"context"
	"strings"
	"testing"

	"github.com/madkoding/motita/internal/agent"
	"github.com/madkoding/motita/internal/config"
	"github.com/madkoding/motita/internal/llm"
	"github.com/madkoding/motita/internal/logx"
	"github.com/madkoding/motita/internal/sandbox"
	taskpkg "github.com/madkoding/motita/internal/task"
)

// A conversation that goes back and forth between asking and doing, the way a person uses it: a
// plan is asked for, a piece of work is done, and then a question about that work. The question
// is answered in chat, so the chat must see what the work did: before, the chat's conversation
// held only chat turns, and a task done in between was invisible to it until the window moved on.
func TestTheChatSeesTheWorkDoneBetweenItsTurns(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	srv, bodies := streamingLLM(t, "1. a\n2. b\n3. c", "It renamed the TASK-FACT file.")
	r := planRunner(t, srv.URL, "conv-probe")
	r.Cfg.Anchor = config.Anchor{Kind: "command", Command: "true"}
	plain := r.newAgent
	r.newAgent = func(cfg config.Config, log *logx.Logger, e *llm.Client, b *sandbox.Sandbox, s taskpkg.Source, ro bool) AgentRunner {
		if cfg.Agent.ReadOnly {
			return plain(cfg, log, e, b, s, ro)
		}
		return &transcriptAgent{says: []agent.DialogueTurn{{
			Kind: agent.KindTask, User: "rename the file", Agent: "Renamed TASK-FACT.txt to notes.txt.",
		}}}
	}

	if _, err := r.RunPlan(context.Background(), "plan the cleanup", func(string, ...any) {}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.RunTask(context.Background(), "rename the file", func(string, ...any) {}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.RunPlan(context.Background(), "what did you rename?", func(string, ...any) {}); err != nil {
		t.Fatal(err)
	}
	last := (*bodies)[len(*bodies)-1]
	for _, want := range []string{"plan the cleanup", "rename the file", "TASK-FACT", "what did you rename?"} {
		if !strings.Contains(last, want) {
			t.Errorf("the chat question was sent without %q", want)
		}
	}
}

// pinAgent records the pinned plan hook the runner installs.
type pinAgent struct {
	transcriptAgent
	pinned func() string
}

func (a *pinAgent) SetPinnedPlan(fn func() string) { a.pinned = fn }

// A task is handed the plan the chat pinned, read when the task needs it.
func TestTheTaskIsGivenThePinnedPlan(t *testing.T) {
	r := newContextRunner(t, config.Default())
	ag := &pinAgent{}
	r.equipAgent(ag)
	if ag.pinned == nil {
		t.Fatal("the pinned plan was not installed")
	}
	r.conversation(config.Default(), nil).Pin("1. a\n2. b\n3. c")
	if got := ag.pinned(); got != "1. a\n2. b\n3. c" {
		t.Errorf("pinned = %q", got)
	}
}

// A session with nothing recorded yet has nothing to share.
func TestShareLastTurnWithNoTurns(t *testing.T) {
	r := newContextRunner(t, config.Default())
	s := r.conversation(config.Default(), nil)
	r.shareLastTurn()
	if n := len(s.Messages()); n != 1 {
		t.Errorf("messages = %d, want only the system prompt", n)
	}
}
