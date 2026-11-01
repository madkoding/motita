package agent

import (
	"context"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/madkoding/motita/internal/config"
	"github.com/madkoding/motita/internal/llm"
	"github.com/madkoding/motita/internal/task"
)

func TestGoalOf(t *testing.T) {
	cases := []struct {
		in   string
		want string
		ok   bool
	}{
		{"/goal add a login page", "add a login page", true},
		{"  /goal   fix the build ", "fix the build", true},
		{"/goal", "/goal", false},
		{"/goalpost review", "/goalpost review", false},
		{"fix the build", "fix the build", false},
	}
	for _, c := range cases {
		got, ok := goalOf(c.in)
		if got != c.want || ok != c.ok {
			t.Errorf("goalOf(%q) = %q, %v; want %q, %v", c.in, got, ok, c.want, c.ok)
		}
	}
}

func goalRun(t *testing.T, s *scriptServer, text string) (*fixture, TaskResult) {
	t.Helper()
	srv := httptest.NewServer(s.handler(t))
	t.Cleanup(srv.Close)
	e := mount(t, srv, config.Anchor{
		Kind: "command", Command: "sh", Args: []string{"-c", "exit 0"},
		Timeout: 10 * time.Second, ExpectExit: 0,
	}, nil)
	src, err := task.NewText(text, "test")
	if err != nil {
		t.Fatal(err)
	}
	e.agent.source = src
	var got TaskResult
	e.agent.Observer = func(r TaskResult) { got = r }
	if err := e.agent.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	return e, got
}

// A goal never asks and is told to decide alone.
func TestAGoalIsToldToDecideAlone(t *testing.T) {
	s := &scriptServer{
		execute: func(int, string) string { return step(true, "true") },
	}
	e, result := goalRun(t, s, "/goal add login")
	if !result.Pass || e.agent.Interactive {
		t.Fatalf("pass=%v interactive=%v", result.Pass, e.agent.Interactive)
	}
	if len(s.analyses) == 0 || !strings.Contains(s.analyses[0], "This is a GOAL") ||
		!strings.Contains(s.analyses[0], "add login") {
		t.Errorf("the model must be told it is a goal:\n%v", s.analyses)
	}
}

// goalScript runs one goal on a scripted model over the given gate and returns its result.
func goalScript(t *testing.T, s *scriptServer, gate, text string, cfg func(*config.Config)) (*fixture, TaskResult) {
	t.Helper()
	srv := httptest.NewServer(s.handler(t))
	t.Cleanup(srv.Close)
	e := mount(t, srv, config.Anchor{
		Kind: "command", Command: "sh", Args: []string{"-c", gate},
		Timeout: 10 * time.Second, ExpectExit: 0,
	}, cfg)
	return e, e.agent.processTask(context.Background(), task.Task{Description: text, Origin: "test"}, 0)
}

// A goal is work: an analysis that reads it as chat does not turn it into a reply.
func TestAGoalIsAlwaysWork(t *testing.T) {
	s := &scriptServer{
		analyze: func(string) string { return `{"kind":"chat","reply":"you could do it like this"}` },
		execute: func(int, string) string { return step(true, "touch made.txt") },
	}
	e, r := goalScript(t, s, "test -f made.txt", "/goal make it", nil)
	if !r.Pass || r.Kind == KindChat || len(s.executes) == 0 {
		t.Fatalf("a goal must run, not chat: %+v (executes %d)", r, len(s.executes))
	}
	if e.agent.isGoal() {
		t.Error("the goal must be closed when its task ends")
	}
}

// The person is asked again after a goal: Interactive comes back as it was.
func TestAGoalRestoresInteractive(t *testing.T) {
	s := &scriptServer{execute: func(int, string) string { return step(true, "touch made.txt") }}
	srv := httptest.NewServer(s.handler(t))
	t.Cleanup(srv.Close)
	e := mount(t, srv, config.Anchor{Kind: "command", Command: "sh", Args: []string{"-c", "exit 0"},
		Timeout: 10 * time.Second}, nil)
	e.agent.Interactive = true
	e.agent.processTask(context.Background(), task.Task{Description: "/goal make it", Origin: "test"}, 0)
	if !e.agent.Interactive || e.agent.goal != nil {
		t.Fatalf("interactive=%v goal=%v", e.agent.Interactive, e.agent.goal)
	}
	e.agent.endGoal() // nothing open: nothing changes
	if !e.agent.Interactive {
		t.Fatal("endGoal with no goal must not change Interactive")
	}
}

// A goal that spends its budget goes on without asking, up to the ceiling.
func TestAGoalContinuesItsBudgetWithoutAsking(t *testing.T) {
	srv := alwaysWorkingServer(t)
	defer srv.Close()
	e := mount(t, srv, config.Anchor{Kind: "command", Command: "sh", Args: []string{"-c", "exit 0"},
		Timeout: 10 * time.Second}, func(c *config.Config) { c.Agent.MaxSteps = 2 })
	e.agent.SetApprover(func(_ context.Context, req ApprovalRequest) (bool, error) {
		if req.Rule == BudgetRule {
			t.Errorf("a goal must not ask to continue: %q", req.Command)
		}
		return true, nil
	})
	r := e.agent.processTask(context.Background(), task.Task{Description: "/goal keep at it", Origin: "test"}, 0)
	if r.Pass || r.Attempts != 2*maxBudgetExtensions || !strings.Contains(r.Reason, "continuations") {
		t.Fatalf("the goal must run to the ceiling: attempts=%d reason=%q", r.Attempts, r.Reason)
	}
}

// A goal is audited with a single criterion, sent back up to goalAuditChallenges times, and a
// criterion still not shown is named in the result.
func TestAGoalIsAuditedHarder(t *testing.T) {
	s := &scriptServer{
		analyze: func(string) string {
			return `{"kind":"task","understandable":true,"summary":"w","success_criteria":["the page works"]}`
		},
		execute: func(round int, _ string) string { return step(true, fmt.Sprintf("echo %d > f.txt", round)) },
		audit: func(string) string {
			return `{"criteria":[{"criterion":"the page works","met":false,"evidence":"never opened"}]}`
		},
	}
	_, r := goalScript(t, s, "exit 0", "/goal ship the page", nil)
	if s.audits != goalAuditChallenges {
		t.Errorf("audits = %d, want %d", s.audits, goalAuditChallenges)
	}
	if !r.Pass || !strings.Contains(r.Reason, "NOT SHOWN: the page works") {
		t.Fatalf("the unmet criterion must be named: %+v", r)
	}
	// A plain task with one criterion is not audited at all.
	s2 := &scriptServer{analyze: s.analyze, execute: s.execute, audit: s.audit}
	_, r2 := goalScript(t, s2, "exit 0", "ship the page", nil)
	if s2.audits != 0 || strings.Contains(r2.Reason, "NOT SHOWN") {
		t.Errorf("plain task: audits=%d reason=%q", s2.audits, r2.Reason)
	}
}

// subtaskGoalServer splits a goal in two subtasks; failing makes the first one fail.
func subtaskGoalServer(failing bool) *scriptServer {
	return &scriptServer{
		plan: func(p string) string {
			if strings.Contains(p, "subtask 1 of") || strings.Contains(p, "subtask 2 of") {
				return `{"plan":[{"step":1,"action":"do it"}],"subtasks":[]}`
			}
			return `{"plan":[],"subtasks":["write a.txt","write b.txt"]}`
		},
		execute: func(_ int, p string) string {
			first := strings.Contains(p, "[THIS ONE] write a.txt")
			switch {
			case strings.Contains(p, "This GOAL was split"):
				return step(true, "touch whole.txt")
			case first && failing:
				return `not json`
			case first:
				return step(true, "touch a.txt")
			default:
				return step(true, "touch b.txt")
			}
		},
	}
}

// A goal split into subtasks is checked whole afterwards, even when a subtask failed.
func TestAGoalIsClosedAfterItsSubtasks(t *testing.T) {
	for _, failing := range []bool{false, true} {
		s := subtaskGoalServer(failing)
		if !failing {
			// An answer with nothing to say: the goal keeps what its subtasks said.
			s.synthReply = `{"summary":""}`
		}
		e, r := goalScript(t, s, "exit 0", "/goal write both", func(c *config.Config) {
			c.Agent.SubtaskRetries = 0
		})
		if !r.Pass || r.Subtasks != map[bool]int{false: 2, true: 1}[failing] {
			t.Fatalf("failing=%v: the goal must be finished whole: %+v", failing, r)
		}
		if _, err := os.Stat(filepath.Join(e.dir, "whole.txt")); err != nil {
			t.Errorf("failing=%v: the closing round never ran: %v", failing, err)
		}
		if last := s.executes[len(s.executes)-1]; !strings.Contains(last, "This GOAL was split") {
			t.Errorf("failing=%v: the closing round must see the subtasks", failing)
		}
		if !failing && !strings.Contains(r.Summary, "write b.txt") {
			t.Errorf("the subtasks' summary must be kept: %+v", r)
		}
	}
}

// A subtask, a plain task and a cancelled goal are not closed again.
func TestCloseGoalLeavesOthersAlone(t *testing.T) {
	a := &Agent{}
	sub := TaskResult{Reason: "as it was"}
	r := &taskRun{a: a, ctx: context.Background()}
	if got := r.closeGoal(sub, nil); got.Reason != "as it was" {
		t.Errorf("plain task: %+v", got)
	}
	a.goal = &goalState{}
	r.depth = 1
	if got := r.closeGoal(sub, nil); got.Reason != "as it was" {
		t.Errorf("subtask: %+v", got)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r.depth, r.ctx = 0, ctx
	if got := r.closeGoal(sub, nil); got.Reason != "as it was" {
		t.Errorf("cancelled: %+v", got)
	}
}

func TestGoalSubtasksNote(t *testing.T) {
	pass := goalSubtasksNote([]string{"a", "b"}, TaskResult{Pass: true, Summary: "1. a - ok"})
	if !strings.Contains(pass, "1. a\n2. b") || !strings.Contains(pass, "What they did:\n1. a - ok") ||
		!strings.Contains(pass, "All of them passed") {
		t.Errorf("pass: %q", pass)
	}
	fail := goalSubtasksNote([]string{"a"}, TaskResult{Reason: "subtask 1 broke"})
	if strings.Contains(fail, "What they did") || !strings.Contains(fail, "did NOT all pass: subtask 1 broke") {
		t.Errorf("fail: %q", fail)
	}
}

// The progress of a goal is written to the turn the person typed, so it survives a stop.
func TestAGoalKeepsItsTrail(t *testing.T) {
	a := &Agent{}
	run := a.prepareGoal(task.Task{Description: "/goal do it"})
	a.begin(task.Task{Description: "/goal do it"})
	a.trail(run, []string{"round 1: touch x"})
	if got := a.workSoFar(run); got != "round 1: touch x" {
		t.Fatalf("work so far = %q", got)
	}
	if a.turnUser("something else") != "something else" {
		t.Error("another description is its own key")
	}
}

// In direct mode a goal checks its work before it stops: always once, again only after it went
// back to work, and its red gate is handed back more times.
func TestADirectGoalChecksBeforeStopping(t *testing.T) {
	write := func(id, path string) []llm.StreamChunk {
		return []llm.StreamChunk{call(id, "write_file", map[string]string{"path": path, "content": "x"})}
	}
	e, s := directAgent(t,
		write("c1", "a.txt"),
		[]llm.StreamChunk{text("done with a")},
		write("c2", "ok.txt"),
		[]llm.StreamChunk{text("now everything")},
		[]llm.StreamChunk{text("all parts checked")},
	)
	r := runDirect(e, "/goal make a and ok")
	if !r.Pass || r.Summary != "all parts checked" {
		t.Fatalf("result: %+v", r)
	}
	checks := 0
	for _, m := range s.sent[len(s.sent)-1] {
		if m.Content == goalCheckPrompt {
			checks++
		}
	}
	if checks != 2 || len(s.sent) != 5 {
		t.Errorf("checks = %d, turns = %d; want 2 and 5", checks, len(s.sent))
	}

	// A red gate: handed back goalDirectGateRetries times.
	turns := [][]llm.StreamChunk{write("c1", "other.txt")}
	for range goalDirectGateRetries + 2 {
		turns = append(turns, []llm.StreamChunk{text("done")})
	}
	e2, s2 := directAgent(t, turns...)
	r2 := runDirect(e2, "/goal make it")
	if r2.Pass || len(s2.sent) != goalDirectGateRetries+3 {
		t.Errorf("red gate: pass=%v turns=%d", r2.Pass, len(s2.sent))
	}
}
