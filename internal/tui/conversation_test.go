package tui

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/madkoding/starlight/internal/agent"
	"github.com/madkoding/starlight/internal/config"
	"github.com/madkoding/starlight/internal/llm"
	"github.com/madkoding/starlight/internal/logx"
	"github.com/madkoding/starlight/internal/sandbox"
	taskpkg "github.com/madkoding/starlight/internal/task"
)

// The Task-mode conversation has to outlive the turn that produced it.
//
// A turn builds a NEW agent — that is how this interface has always worked — so anything kept
// on the agent is thrown away when the turn ends. If the conversation is not held by the
// runner, every message arrives as the first message: the agent asks a question, the user
// answers, and the agent has no idea what the answer refers to.

// transcriptAgent is an AgentRunner that records what it was handed and what it said, so the
// round trip can be asserted without a real model.
type transcriptAgent struct {
	// got is what the runner handed in.
	got []agent.DialogueTurn
	// says is what this turn appends to the conversation.
	says []agent.DialogueTurn
	// err makes the run fail, to check the conversation survives a failure.
	err error
}

func (a *transcriptAgent) SetTranscript(turns []agent.DialogueTurn) {
	a.got = append([]agent.DialogueTurn(nil), turns...)
}

func (a *transcriptAgent) Transcript() []agent.DialogueTurn {
	return append(append([]agent.DialogueTurn(nil), a.got...), a.says...)
}

func (a *transcriptAgent) Run(context.Context) error {
	if a.err != nil {
		return a.err
	}
	if o, ok := any(a).(interface{ SetObserver(func(agent.TaskResult)) }); ok {
		_ = o
	}
	return nil
}

func (a *transcriptAgent) RunCommand(context.Context, string) (string, int, error) {
	return "", 0, nil
}

// transcriptRunner builds a runner whose every turn returns the given agent.
func transcriptRunner(t *testing.T, made *[]*transcriptAgent) *AppRunner {
	t.Helper()
	r := NewAppRunner(&bytes.Buffer{}, &bytes.Buffer{}, config.Default(), &llm.Client{}, &sandbox.Sandbox{}, logx.Global())
	r.newAgent = func(config.Config, *logx.Logger, *llm.Client, *sandbox.Sandbox, taskpkg.Source, bool) AgentRunner {
		a := &transcriptAgent{}
		*made = append(*made, a)
		return a
	}
	return r
}

// TestTheTurnIsGivenTheEarlierConversation: what was said before must reach the agent, or a
// short answer cannot be interpreted.
func TestTheTurnIsGivenTheEarlierConversation(t *testing.T) {
	var made []*transcriptAgent
	r := transcriptRunner(t, &made)

	// A previous turn that asked something.
	r.remember([]agent.DialogueTurn{{User: "revisa el directorio", Agent: "¿cuál?", Kind: agent.KindAsk}})

	if _, err := r.RunTask(context.Background(), "el actual", func(string, ...any) {}); err != nil {
		t.Fatalf("RunTask: %v", err)
	}
	if len(made) != 1 {
		t.Fatalf("expected one turn, got %d", len(made))
	}
	if len(made[0].got) != 1 {
		t.Fatalf("the turn must be handed the conversation, got %+v", made[0].got)
	}
	if made[0].got[0].Agent != "¿cuál?" {
		t.Errorf("the earlier answer must arrive: %+v", made[0].got[0])
	}
}

// TestWhatATurnSaysIsCarriedForward: the round trip closes. Without this the agent would ask
// the same question every turn, because it never sees that it already asked.
func TestWhatATurnSaysIsCarriedForward(t *testing.T) {
	var made []*transcriptAgent
	r := transcriptRunner(t, &made)
	r.newAgent = func(config.Config, *logx.Logger, *llm.Client, *sandbox.Sandbox, taskpkg.Source, bool) AgentRunner {
		a := &transcriptAgent{says: []agent.DialogueTurn{
			{User: "revisa el directorio", Agent: "¿cuál?", Kind: agent.KindAsk},
		}}
		made = append(made, a)
		return a
	}

	if _, err := r.RunTask(context.Background(), "revisa el directorio", func(string, ...any) {}); err != nil {
		t.Fatalf("RunTask: %v", err)
	}
	// The NEXT turn must receive it.
	if len(r.history()) != 1 {
		t.Fatalf("the turn's contribution must be kept, got %+v", r.history())
	}
	if r.history()[0].Agent != "¿cuál?" {
		t.Errorf("what the agent said must be carried: %+v", r.history()[0])
	}
}

// TestTheConversationSurvivesAFailedTurn: an attempt that failed is still part of what
// happened. Dropping it would make the agent repeat a mistake it cannot see.
func TestTheConversationSurvivesAFailedTurn(t *testing.T) {
	var made []*transcriptAgent
	r := transcriptRunner(t, &made)
	r.newAgent = func(config.Config, *logx.Logger, *llm.Client, *sandbox.Sandbox, taskpkg.Source, bool) AgentRunner {
		a := &transcriptAgent{
			says: []agent.DialogueTurn{{User: "algo", Agent: "no pude", Kind: agent.KindTask}},
			err:  errors.New("the run stopped"),
		}
		made = append(made, a)
		return a
	}

	if _, err := r.RunTask(context.Background(), "algo", func(string, ...any) {}); err == nil {
		t.Fatal("the error must still be reported")
	}
	if len(r.history()) != 1 {
		t.Errorf("a failed turn is part of the conversation, got %+v", r.history())
	}
}

// TestResetClearsTheTaskConversationToo: to the user this is ONE conversation — they do not
// think of themselves as being in two modes — so leaving a subject behind must clear both.
func TestResetClearsTheTaskConversationToo(t *testing.T) {
	var made []*transcriptAgent
	r := transcriptRunner(t, &made)
	r.remember([]agent.DialogueTurn{{User: "algo", Agent: "hecho", Kind: agent.KindTask}})
	if len(r.history()) == 0 {
		t.Fatal("precondition: there is a conversation to clear")
	}

	r.ResetConversation()

	if len(r.history()) != 0 {
		t.Errorf("the Task conversation must be cleared too, got %+v", r.history())
	}
}

// TestHistoryIsCopiedOut: the caller must not be able to mutate the runner's state through the
// slice it is handed, and a turn appending must not change what a previous caller holds.
func TestHistoryIsCopiedOut(t *testing.T) {
	var made []*transcriptAgent
	r := transcriptRunner(t, &made)
	r.remember([]agent.DialogueTurn{{User: "uno", Kind: agent.KindChat}})

	out := r.history()
	out[0].User = "mutado"

	if r.history()[0].User != "uno" {
		t.Error("history must be copied out, not aliased")
	}
}

// TestAChatTurnIsShownAsAReply: the result of a conversational turn is an answer, not a task
// verdict. Rendering it as "completed: conversational reply" would bury the reply the user
// actually wants to read.
func TestAChatTurnIsShownAsAReply(t *testing.T) {
	got := summarise(agent.TaskResult{Kind: agent.KindChat, Reply: "¡Hola! ¿Qué necesitas?"})
	if got != "¡Hola! ¿Qué necesitas?" {
		t.Errorf("a chat turn must show the reply itself, got %q", got)
	}
	for _, unwanted := range []string{"completed", "failed", "done"} {
		if strings.Contains(strings.ToLower(got), unwanted) {
			t.Errorf("a reply must not carry task-verdict wording %q: %q", unwanted, got)
		}
	}
}

// TestAChatTurnWithNoReplyStillSaysSomething: an empty bubble looks like a bug.
func TestAChatTurnWithNoReplyStillSaysSomething(t *testing.T) {
	got := summarise(agent.TaskResult{Kind: agent.KindChat})
	if strings.TrimSpace(got) == "" {
		t.Error("an empty reply must still produce a sentence")
	}
}
