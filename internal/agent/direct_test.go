package agent

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/madkoding/motita/internal/anchor"
	"github.com/madkoding/motita/internal/config"
	"github.com/madkoding/motita/internal/llm"
	"github.com/madkoding/motita/internal/task"
)

// scriptedTools answers each turn with the next reply of its script and keeps what it was sent.
type scriptedTools struct {
	mu      sync.Mutex
	turns   [][]llm.StreamChunk
	sent    [][]llm.Message
	tools   []llm.Tool
	onAsked func(int)
}

func (s *scriptedTools) CompleteToolsStream(_ context.Context, msgs []llm.Message, tools []llm.Tool) <-chan llm.StreamChunk {
	s.mu.Lock()
	n := len(s.sent)
	s.sent = append(s.sent, append([]llm.Message(nil), msgs...))
	s.tools = tools
	var turn []llm.StreamChunk
	if n < len(s.turns) {
		turn = s.turns[n]
	} else {
		turn = []llm.StreamChunk{{Event: llm.StreamText, Text: "done"}}
	}
	s.mu.Unlock()
	if s.onAsked != nil {
		s.onAsked(n)
	}
	ch := make(chan llm.StreamChunk, len(turn)+2)
	ch <- llm.StreamChunk{Event: llm.StreamThinking, Text: "hmm"}
	for _, c := range turn {
		ch <- c
	}
	ch <- llm.StreamChunk{Event: llm.StreamDone}
	close(ch)
	return ch
}

// call is a tool-call chunk.
func call(id, name string, args map[string]string) llm.StreamChunk {
	b, _ := json.Marshal(args)
	return llm.StreamChunk{Event: llm.StreamToolCall,
		Call: &llm.ToolCall{ID: id, Type: "function", Function: llm.FunctionCall{Name: name, Arguments: b}}}
}

func text(s string) llm.StreamChunk { return llm.StreamChunk{Event: llm.StreamText, Text: s} }

// directAgent is an agent in direct mode whose engine follows the script, over a gate that passes
// when ok.txt exists.
func directAgent(t *testing.T, turns ...[]llm.StreamChunk) (*fixture, *scriptedTools) {
	t.Helper()
	srv := httptest.NewServer(nil)
	t.Cleanup(srv.Close)
	e := mount(t, srv, config.Anchor{Kind: "command", Command: "sh", Args: []string{"-c", "test -f ok.txt"},
		Timeout: 10 * time.Second}, func(c *config.Config) { c.Agent.Direct = true })
	s := &scriptedTools{turns: turns}
	e.agent.toolEngine = s
	return e, s
}

func runDirect(e *fixture, request string) TaskResult {
	return e.agent.processTask(context.Background(), task.Task{Description: request, Origin: "test"}, 0)
}

// A change is made with the tools, the gate passes, and the answer is the model's own words.
func TestDirectModeChangesAndPassesTheGate(t *testing.T) {
	e, s := directAgent(t,
		[]llm.StreamChunk{text("writing it"),
			call("c1", "write_file", map[string]string{"path": "ok.txt", "content": "yes\n", "description": "Writing"}),
			call("c2", "shell", map[string]string{"command": "cat ok.txt"})},
		[]llm.StreamChunk{text("Created ok.txt and checked it.")},
	)
	r := runDirect(e, "create ok.txt")
	if !r.Pass || r.Kind != KindTask || r.Summary != "Created ok.txt and checked it." || r.Validation == nil {
		t.Fatalf("result: %+v", r)
	}
	if b, err := os.ReadFile(filepath.Join(e.dir, "ok.txt")); err != nil || string(b) != "yes\n" {
		t.Fatalf("ok.txt: %q %v", b, err)
	}
	second := s.sent[1]
	if got := second[len(second)-1]; got.Role != "tool" || !strings.Contains(got.Content, "yes") {
		t.Fatalf("the shell output did not reach the model: %+v", got)
	}
	if !strings.Contains(second[0].Content, e.dir) || !strings.Contains(second[0].Content, "Conversation so far") {
		t.Errorf("the system prompt: %q", second[0].Content)
	}
	names := []string{}
	for _, tool := range s.tools {
		names = append(names, tool.Function.Name)
	}
	if strings.Join(names, ",") != "shell,write_file,edit_file,recall,remember" {
		t.Errorf("tools: %v", names)
	}
}

// A request that only needed an answer is a reply: nothing changed, no gate.
func TestDirectModeAnswerIsAReply(t *testing.T) {
	e, _ := directAgent(t, []llm.StreamChunk{text("Hello!")})
	r := runDirect(e, "hi")
	if !r.Pass || r.Kind != KindChat || r.Reply != "Hello!" || r.Validation != nil {
		t.Fatalf("result: %+v", r)
	}
	if tr := e.agent.Transcript(); len(tr) == 0 || tr[len(tr)-1].Agent != "Hello!" {
		t.Errorf("the reply is not in the conversation: %+v", tr)
	}
}

// A red gate goes back into the conversation, at most directGateRetries times.
func TestDirectModeHandsARedGateBack(t *testing.T) {
	e, s := directAgent(t,
		[]llm.StreamChunk{call("c1", "write_file", map[string]string{"path": "other.txt", "content": "x"})},
		[]llm.StreamChunk{text("done")},
		[]llm.StreamChunk{text("still done")},
		[]llm.StreamChunk{text("really done")},
	)
	r := runDirect(e, "make it")
	if r.Pass || r.Validation == nil || r.Summary != "really done" {
		t.Fatalf("result: %+v", r)
	}
	last := s.sent[len(s.sent)-1]
	fb := last[len(last)-1].Content
	if !strings.Contains(fb, "did not pass") || !strings.Contains(fb, "test -f ok.txt") {
		t.Errorf("gate feedback: %q", fb)
	}
	if len(s.sent) != 4 {
		t.Errorf("turns = %d, want 4", len(s.sent))
	}
}

// Every tool maps to the action a round runs; a bad call is told, not run.
func TestDirectCommand(t *testing.T) {
	tc := func(name string, args map[string]string) llm.ToolCall { return *call("x", name, args).Call }
	cases := []struct {
		call       llm.ToolCall
		kind, body string
	}{
		{tc("shell", map[string]string{"command": "ls"}), "command", "ls"},
		{tc("write_file", map[string]string{"path": "a", "content": "b"}), "write_file", "a\nb"},
		{tc("edit_file", map[string]string{"path": "a", "search": "x", "replace": "y"}), "edit_file",
			"a\n<<<<<<< SEARCH\nx\n=======\ny\n>>>>>>> REPLACE"},
		{tc("recall", map[string]string{"query": "q"}), "recall", "q"},
		{tc("remember", map[string]string{"text": "e :: m"}), "remember", "e :: m"},
	}
	for _, c := range cases {
		got, err := directCommand(c.call)
		if err != nil || got.Kind != c.kind || got.Command != c.body {
			t.Errorf("%s: %+v %v", c.call.Function.Name, got, err)
		}
	}
	if _, err := directCommand(tc("rm", nil)); err == nil {
		t.Error("an unknown tool was accepted")
	}
	bad := llm.ToolCall{Function: llm.FunctionCall{Name: "shell", Arguments: json.RawMessage(`[1]`)}}
	if _, err := directCommand(bad); err == nil {
		t.Error("unreadable arguments were accepted")
	}
}

// A bad tool call, a command with no output and a refused one are answered in words.
func TestDirectRunAnswersInWords(t *testing.T) {
	e, _ := directAgent(t)
	if got := e.agent.directRun(context.Background(), *call("x", "nope", nil).Call); !strings.HasPrefix(got, "Error:") {
		t.Errorf("unknown tool: %q", got)
	}
	if got := e.agent.directRun(context.Background(),
		*call("x", "shell", map[string]string{"command": "true"}).Call); !strings.Contains(got, "true") {
		t.Errorf("a quiet command: %q", got)
	}
	e.agent.ExecCommand = nil
	if got := e.agent.directRun(context.Background(),
		*call("x", "shell", map[string]string{"command": "true"}).Call); !strings.Contains(got, "no command executor") {
		t.Errorf("a command that could not run: %q", got)
	}
	if got := e.agent.directRun(context.Background(),
		*call("x", "edit_file", map[string]string{"path": "missing", "search": "a", "replace": "b"}).Call); got == "" {
		t.Error("a failed edit said nothing")
	}
}

// What the person types while it works reaches the next turn; an engine error ends the run.
func TestDirectModeSteeringAndEngineError(t *testing.T) {
	e, s := directAgent(t, []llm.StreamChunk{call("c1", "shell", map[string]string{"command": "true"})})
	s.onAsked = func(n int) {
		if n == 0 {
			e.agent.Steer("also say hi")
		}
	}
	runDirect(e, "go")
	found := false
	for _, m := range s.sent[1] {
		found = found || strings.Contains(m.Content, "also say hi")
	}
	if !found {
		t.Error("the steering message did not reach the model")
	}

	e2, s2 := directAgent(t, []llm.StreamChunk{{Event: llm.StreamError, Error: errors.New("down")}})
	r := runDirect(e2, "go")
	if r.Pass || !strings.Contains(r.Reason, "down") || len(s2.sent) != 1 {
		t.Fatalf("result: %+v", r)
	}
}

// The real engine is used when none was set, and a run that never stops calling tools ends.
func TestDirectModeBudgetAndDefaultEngine(t *testing.T) {
	e, s := directAgent(t)
	loop := []llm.StreamChunk{call("c", "shell", map[string]string{"command": "true"})}
	e.agent.cfg.Agent.MaxSteps = 1
	for i := 0; i < e.agent.maxSteps()*maxBudgetExtensions; i++ {
		s.turns = append(s.turns, loop)
	}
	r := runDirect(e, "spin")
	if r.Pass || !strings.Contains(r.Reason, "not finished") {
		t.Fatalf("result: %+v", r)
	}
	if r.Attempts != e.agent.maxSteps()*maxBudgetExtensions {
		t.Errorf("attempts = %d", r.Attempts)
	}

	e.agent.toolEngine = nil
	if _, err := e.agent.directReply(context.Background(), nil); err == nil {
		t.Error("the default engine (no server behind it) answered")
	}
}

// The system prompt carries the brief when the turn has one, and a red gate names only the checks
// that failed.
func TestDirectSystemAndGateFeedback(t *testing.T) {
	e, _ := directAgent(t)
	e.agent.SetPinnedPlan(func() string { return "1. the pinned step" })
	if got := e.agent.directSystem(context.Background(), task.Task{Description: "go"}); !strings.Contains(got,
		"the pinned step") {
		t.Errorf("the brief is missing: %q", got)
	}
	fb := gateFeedback(anchor.Result{Reason: "1 of 2 failed", Checks: []anchor.CheckLog{
		{Command: "make lint", Pass: true, Output: "clean"},
		{Command: "make test", Exit: 2, Output: "FAIL x"},
	}})
	if strings.Contains(fb, "make lint") || !strings.Contains(fb, "make test (exit 2)") {
		t.Errorf("feedback: %q", fb)
	}
}
