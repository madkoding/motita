package plan

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/madkoding/motita/internal/llm"
)

// roundsEngine answers each request with the next scripted round of chunks.
type roundsEngine struct {
	rounds [][]llm.StreamChunk
	asked  int
}

func (e *roundsEngine) CompleteToolsStream(context.Context, []llm.Message, []llm.Tool) <-chan llm.StreamChunk {
	round := e.rounds[min(e.asked, len(e.rounds)-1)]
	e.asked++
	ch := make(chan llm.StreamChunk, len(round)+1)
	for _, c := range round {
		ch <- c
	}
	close(ch)
	return ch
}

func (e *roundsEngine) Complete(context.Context, []llm.Message) (string, error) { return "forced", nil }

// stepLog records what a plan run reports through Steps.
type stepLog struct{ commands, outputs, thoughts, lives []string }

func (l *stepLog) steps() Steps {
	return Steps{
		Command: func(c string) { l.commands = append(l.commands, c) },
		Output: func(exit int, out string) {
			l.outputs = append(l.outputs, strings.TrimSpace(out)+"|"+string(rune('0'+exit)))
		},
		Thought: func(t string) { l.thoughts = append(l.thoughts, t) },
		Live:    func(t string) { l.lives = append(l.lives, t) },
	}
}

// TestAPlanRunReportsItsCommandsAndItsReasoning: the terminal and thinking drawers read these, and a
// question answered in plan mode used to leave both empty.
func TestAPlanRunReportsItsCommandsAndItsReasoning(t *testing.T) {
	liveThoughtEvery = 0
	t.Cleanup(func() { liveThoughtEvery = 250 * time.Millisecond })
	a, ex := makeAgent(t, true)
	ex.defaultOutput = "file.txt"
	args, _ := json.Marshal(map[string]string{"command": "ls"})
	call := &llm.ToolCall{ID: "c1", Function: llm.FunctionCall{Name: "execute_command", Arguments: args}}
	engine := &roundsEngine{rounds: [][]llm.StreamChunk{
		{
			{Event: llm.StreamThinking, Text: "I should list "},
			{Event: llm.StreamThinking, Text: "the folder."},
			{Event: llm.StreamText, Text: "Looking."},
			{Event: llm.StreamToolCall, Call: call},
			{Event: llm.StreamDone, Reply: llm.Reply{Content: "Looking.", Calls: []llm.ToolCall{*call}}},
		},
		{{Event: llm.StreamText, Text: "One file."}, {Event: llm.StreamDone, Reply: llm.Reply{Content: "One file."}}},
	}}
	var log stepLog
	var traced []string
	p := New(engine, a).WithSteps(log.steps()).
		WithTrace(func(f string, args ...any) { traced = append(traced, f) })
	out, err := p.Run(context.Background(), "what is here?")
	if err != nil || out != "One file." {
		t.Fatalf("Run = %q, %v", out, err)
	}
	if len(log.commands) != 1 || log.commands[0] != "ls" {
		t.Errorf("commands = %v", log.commands)
	}
	if len(log.outputs) != 1 || !strings.HasPrefix(log.outputs[0], "file.txt|") {
		t.Errorf("outputs = %v", log.outputs)
	}
	want := []string{"I should list the folder.", "Looking."}
	if strings.Join(log.thoughts, "/") != strings.Join(want, "/") {
		t.Errorf("thoughts = %q, want %q", log.thoughts, want)
	}
	if len(log.lives) != 2 || log.lives[1] != "I should list the folder." {
		t.Errorf("live snapshots = %q", log.lives)
	}
	for _, f := range traced {
		if strings.Contains(f, "using tool") {
			t.Errorf("a reported command must not also be traced as a tool: %q", f)
		}
	}
}

// TestAPlanRunWithoutStepsKeepsItsTrace: without Steps the tool is announced as before, and a
// closed stream without a done chunk still reports what was reasoned.
func TestAPlanRunWithoutStepsKeepsItsTrace(t *testing.T) {
	a, _ := makeAgent(t, true)
	p := New(&roundsEngine{rounds: [][]llm.StreamChunk{{
		{Event: llm.StreamThinking, Text: "ignored"}, {Event: llm.StreamText, Text: "ok"},
	}}}, a)
	if out, err := p.Run(context.Background(), "q"); err != nil || out != "ok" {
		t.Fatalf("Run = %q, %v", out, err)
	}
	var traced []string
	p2 := New(&roundsEngine{rounds: [][]llm.StreamChunk{{{Event: llm.StreamText, Text: "x"}}}}, a).
		WithTrace(func(f string, args ...any) { traced = append(traced, f) })
	p2.dispatchTool(context.Background(), llm.ToolCall{Function: llm.FunctionCall{Name: "list_skills"}})
	if len(traced) != 1 {
		t.Errorf("a tool must be announced without Steps: %v", traced)
	}
	// With Steps, a tool other than execute_command is announced too.
	var log stepLog
	var traced2 []string
	p3 := New(&roundsEngine{rounds: [][]llm.StreamChunk{{{Event: llm.StreamText, Text: "x"}}}}, a).
		WithSteps(log.steps()).
		WithTrace(func(f string, args ...any) { traced2 = append(traced2, f) })
	p3.dispatchTool(context.Background(), llm.ToolCall{Function: llm.FunctionCall{Name: "list_skills"}})
	if len(traced2) != 1 {
		t.Errorf("another tool must still be announced: %v", traced2)
	}
}
