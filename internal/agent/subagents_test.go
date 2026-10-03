package agent

import (
	"context"
	"errors"
	"fmt"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/madkoding/motita/internal/config"
	"github.com/madkoding/motita/internal/execx"
	"github.com/madkoding/motita/internal/gitx"
)

// kindAct is one action of any kind, for an execute-phase reply.
type kindAct struct{ kind, command string }

// acts is an execute-phase reply made of actions of any kind.
func acts(done bool, actions ...kindAct) string {
	list := []map[string]string{}
	for _, a := range actions {
		list = append(list, map[string]string{"kind": a.kind, "description": "step", "command": a.command})
	}
	return mustJSON(map[string]any{
		"reasoning": "scripted", "actions": list,
		"final_action": map[string]string{"command": ""}, "done": done,
	})
}

// mergeTarget finds the branch a background agent's report names.
var mergeTarget = regexp.MustCompile(`motita/sub/\S+-a1`)

// isChildPrompt tells a background agent's prompt from the main agent's.
func isChildPrompt(prompt string) bool { return strings.Contains(prompt, "You are a background agent") }

// fleetRun mounts an agent on a script, lets prepare adjust it, runs one task and returns the
// fixture, the outermost result and every progress line.
func fleetRun(t *testing.T, s *scriptServer, cfg func(*config.Config), prepare func(*fixture)) (*fixture, TaskResult, *lineSink) {
	t.Helper()
	srv := httptest.NewServer(s.handler(t))
	t.Cleanup(srv.Close)
	e := mount(t, srv, config.Anchor{
		Kind: "command", Command: "sh", Args: []string{"-c", "exit 0"}, Timeout: 10 * time.Second,
	}, func(c *config.Config) {
		c.Agent.Policy.Enforce = false
		c.Sandbox.ToolsDir = t.TempDir()
		if cfg != nil {
			cfg(c)
		}
	})
	sink := &lineSink{}
	e.agent.Progress = func(format string, args ...any) { sink.add(fmt.Sprintf(format, args...)) }
	if prepare != nil {
		prepare(e)
	}
	var results []TaskResult
	e.agent.Observer = func(r TaskResult) { results = append(results, r) }
	_ = e.agent.Run(context.Background()) // a failed task is an error here; the result says why
	if len(results) == 0 {
		t.Fatal("no result was observed")
	}
	return e, results[len(results)-1], sink
}

// lastAgents is the last agents snapshot among the progress lines.
func (s *lineSink) lastAgents(t *testing.T) []AgentInfo {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := len(s.lines) - 1; i >= 0; i-- {
		if agents, ok := ParseAgentsLine(s.lines[i]); ok {
			return agents
		}
	}
	t.Fatal("no agents snapshot was published")
	return nil
}

// prompts returns the execute prompts of the main agent and of the background agents.
func (s *scriptServer) split() (main, child []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, p := range s.executes {
		if isChildPrompt(p) {
			child = append(child, p)
		} else {
			main = append(main, p)
		}
	}
	return main, child
}

// TestABackgroundAgentWorksOnItsOwnBranchAndReports is the whole path: the main agent hands off a
// piece, waits, reads the report, merges the branch - and the file the background agent wrote is in
// the main checkout. Its worktree is gone, its branch stays, and the fleet saw it finish.
func TestABackgroundAgentWorksOnItsOwnBranchAndReports(t *testing.T) {
	mainRound := 0
	s := &scriptServer{execute: func(_ int, prompt string) string {
		if isChildPrompt(prompt) {
			return step(true, "echo hello > docs.txt")
		}
		mainRound++
		switch mainRound {
		case 1:
			return acts(false, kindAct{"spawn_agent", "write the docs\nCreate docs.txt saying hello."})
		case 2:
			return acts(false, kindAct{"wait_agents", ""})
		default:
			// The model merges the branch its report named, as a real one would.
			return step(true, "git merge --no-edit "+mergeTarget.FindString(prompt))
		}
	}}
	var tools string
	e, result, sink := fleetRun(t, s, func(c *config.Config) {
		inRepo(t)(c)
		tools = c.Sandbox.ToolsDir
	}, nil)
	if !result.Pass {
		main, _ := s.split()
		t.Fatalf("the run must pass: %s\nlast main prompt:\n%s", result.Reason, main[len(main)-1])
	}
	if body, err := os.ReadFile(filepath.Join(e.dir, "docs.txt")); err != nil || strings.TrimSpace(string(body)) != "hello" {
		t.Fatalf("the merged work must be in the checkout: %q %v", body, err)
	}
	main, child := s.split()
	if len(child) == 0 || !strings.Contains(child[0], "write the docs") || !strings.Contains(child[0], "branch motita/sub/") {
		t.Fatalf("the background agent must get its brief and its branch:\n%v", child)
	}
	all := strings.Join(main, "\n")
	for _, want := range []string{"[started background agent a1: write the docs]", "[agent a1 finished: passed]",
		"1 files changed", "To bring it in: commit your own changes first, then git merge --no-edit motita/sub/"} {
		if !strings.Contains(all, want) {
			t.Errorf("the main agent's prompts must show %q", want)
		}
	}
	agents := sink.lastAgents(t)
	if len(agents) != 2 || agents[0].ID != "main" || agents[0].State != AgentPassed ||
		agents[1].ID != "a1" || agents[1].Parent != "main" || agents[1].State != AgentPassed || agents[1].Purpose != "write the docs" {
		t.Fatalf("final snapshot = %+v", agents)
	}
	entries, _ := os.ReadDir(filepath.Join(tools, "worktrees"))
	if len(entries) != 0 {
		t.Errorf("the background agent's worktree must be removed, found %d entries", len(entries))
	}
	out, _ := exec.Command("git", "-C", e.dir, "branch", "--list", "motita/sub/*").CombinedOutput()
	if !strings.Contains(string(out), "motita/sub/") {
		t.Error("the branch is the work: it must outlive the worktree")
	}
}

// The main agent does not have to ask: a background agent that finished reports in the round after,
// while the main agent goes on with its own work.
func TestAFinishedAgentReportsWithoutBeingWaitedFor(t *testing.T) {
	mainRound := 0
	s := &scriptServer{execute: func(_ int, prompt string) string {
		if isChildPrompt(prompt) {
			return step(true, "true")
		}
		mainRound++
		switch {
		case mainRound == 1:
			return acts(false, kindAct{"spawn_agent", "a quick look\nCheck the README."})
		case strings.Contains(prompt, "[agent a1 finished") || mainRound > 40:
			return step(true, "true")
		default:
			return step(false, fmt.Sprintf("sleep 0.1 # own work, part %d", mainRound))
		}
	}}
	_, result, _ := fleetRun(t, s, inRepo(t), nil)
	if !result.Pass || mainRound > 40 {
		t.Fatalf("the report must arrive on its own (rounds %d): %s", mainRound, result.Reason)
	}
}

// Only max_parallel agents run at once; a background agent cannot start agents; a piece with no
// brief gets its purpose as the brief; and a background agent with nothing changed says so.
func TestBackgroundAgentLimits(t *testing.T) {
	mainRound := 0
	childRound := map[string]int{}
	s := &scriptServer{execute: func(_ int, prompt string) string {
		if isChildPrompt(prompt) {
			childRound[prompt[:40]]++
			if strings.Contains(prompt, "cannot start agents") {
				return step(true, "true")
			}
			return acts(false, kindAct{"spawn_agent", "a grandchild\nmore work"})
		}
		mainRound++
		switch mainRound {
		case 1:
			return acts(false, kindAct{"spawn_agent", "only a purpose"}, kindAct{"spawn_agent", "a second one\nbrief"})
		case 2:
			return acts(false, kindAct{"wait_agents", "a1"})
		default:
			return step(true, "true")
		}
	}}
	_, result, _ := fleetRun(t, s, func(c *config.Config) {
		inRepo(t)(c)
		c.Agent.MaxParallel = 1
	}, nil)
	if !result.Pass {
		t.Fatalf("the run must pass: %s", result.Reason)
	}
	main, child := s.split()
	if !strings.Contains(main[1], "already running, the most this run allows") {
		t.Errorf("the second agent must be refused:\n%s", main[1])
	}
	if !strings.Contains(child[0], "only a purpose") {
		t.Errorf("with no brief the purpose is the brief:\n%s", child[0])
	}
	if !strings.Contains(strings.Join(main, "\n"), "0 files changed") {
		t.Error("an agent that changed nothing must say so, with no merge to suggest")
	}
	if strings.Contains(strings.Join(main, "\n"), "To bring it in") {
		t.Error("there is nothing to merge from an agent that changed nothing")
	}
}

// Outside a repository there is no worktree to give: the background agent is read-only, its
// writes are refused, and its answer is accepted as its result.
func TestABackgroundAgentOutsideARepositoryIsReadOnly(t *testing.T) {
	mainRound, childRound := 0, 0
	s := &scriptServer{execute: func(_ int, prompt string) string {
		if isChildPrompt(prompt) {
			childRound++
			if childRound == 1 {
				return step(false, "echo x > written.txt")
			}
			return step(true)
		}
		mainRound++
		if mainRound == 1 {
			return acts(false, kindAct{"spawn_agent", "investigate\nHow is the project laid out?"})
		}
		if mainRound == 2 {
			return acts(false, kindAct{"wait_agents", ""})
		}
		return step(true, "true")
	}}
	e, result, _ := fleetRun(t, s, nil, nil)
	if !result.Pass {
		t.Fatalf("the run must pass: %s", result.Reason)
	}
	main, child := s.split()
	if !strings.Contains(main[1], "READ-ONLY") || !strings.Contains(child[0], "You are READ-ONLY") {
		t.Errorf("both agents must be told it is read-only:\n%s\n---\n%s", main[1], child[0])
	}
	if !strings.Contains(strings.Join(main, "\n"), "[agent a1 finished: passed] investigate") {
		t.Error("the read-only agent's answer is its result")
	}
	if _, err := os.Stat(filepath.Join(e.dir, "written.txt")); err == nil {
		t.Error("a read-only agent must not write")
	}
}

// A claim of done with an agent still running is sent back once; the second claim cancels it, and
// the run does not wait for its work.
func TestDoneWithAnAgentRunningIsSentBackOnceThenCancelsIt(t *testing.T) {
	mainRound := 0
	s := &scriptServer{execute: func(_ int, prompt string) string {
		if isChildPrompt(prompt) {
			return step(false, "sleep 5")
		}
		mainRound++
		if mainRound == 1 {
			return acts(false, kindAct{"spawn_agent", "a long piece\nTake your time."})
		}
		return step(true, "true")
	}}
	start := time.Now()
	_, result, sink := fleetRun(t, s, inRepo(t), nil)
	if !result.Pass {
		t.Fatalf("the run must pass: %s", result.Reason)
	}
	if time.Since(start) > 4*time.Second {
		t.Errorf("the run waited for a cancelled agent: %v", time.Since(start))
	}
	main, _ := s.split()
	if len(main) != 3 || !strings.Contains(main[2], "background agent(s) you started are still running: a1 (a long piece)") {
		t.Fatalf("the first claim must be sent back, naming the agent (%d rounds):\n%s", len(main), main[len(main)-1])
	}
	if agents := sink.lastAgents(t); agents[1].State != AgentCancelled {
		t.Errorf("the agent must end cancelled: %+v", agents[1])
	}
}

// --- the pieces, one by one ---------------------------------------------------------------------

// bareAgent is an agent with a configuration and nothing else, for calling the pieces directly.
func bareAgent(t *testing.T, cfg func(*config.Config)) *Agent {
	t.Helper()
	c := config.Default()
	c.Agent.WorkspaceDir = t.TempDir()
	c.Sandbox.ToolsDir = t.TempDir()
	if cfg != nil {
		cfg(&c)
	}
	return New(c, nil, nil, nil, nil)
}

func TestSpawnRefusals(t *testing.T) {
	ctx := context.Background()
	a := bareAgent(t, nil)
	if out, err := a.spawnAgent(ctx, "x"); err == nil || !strings.Contains(out, "not available in this mode") {
		t.Errorf("outside a run: %q %v", out, err)
	}
	a.startFleet(taskOf("t"))
	defer a.endFleet(TaskResult{})
	if out, err := a.spawnAgent(ctx, "  \n brief"); err == nil || !strings.Contains(out, "purpose on the first line") {
		t.Errorf("no purpose: %q %v", out, err)
	}
	a.cfg.Agent.MaxParallel = 0
	if out, err := a.spawnAgent(ctx, "p\nb"); err == nil || !strings.Contains(out, "turned off") {
		t.Errorf("turned off: %q %v", out, err)
	}
	a.isChild = true
	if out, err := a.spawnAgent(ctx, "p\nb"); err == nil || !strings.Contains(out, "cannot start agents") {
		t.Errorf("a child: %q %v", out, err)
	}
	if handled, _, _ := a.runAgentAction(ctx, "write_file", Command{}); handled {
		t.Error("runAgentAction must leave other kinds alone")
	}
}

// Every way of failing to prepare a worktree is a refusal the model reads, never a crash.
func TestSpawnReportsAPlaceThatCannotBePrepared(t *testing.T) {
	ctx := context.Background()
	boom := errors.New("simulated")
	cases := map[string]func(t *testing.T, a *Agent){
		"snapshot": func(t *testing.T, a *Agent) {
			old := subagentSnapshot
			subagentSnapshot = func(context.Context, string, string) (string, string, error) { return "", "", boom }
			t.Cleanup(func() { subagentSnapshot = old })
		},
		"tools dir": func(t *testing.T, a *Agent) {
			file := filepath.Join(t.TempDir(), "file")
			os.WriteFile(file, []byte("x"), 0o644)
			a.cfg.Sandbox.ToolsDir = file
		},
		"temp dir": func(t *testing.T, a *Agent) {
			old := subagentTempDir
			subagentTempDir = func(string, string) (string, error) { return "", boom }
			t.Cleanup(func() { subagentTempDir = old })
		},
		"worktree": func(t *testing.T, a *Agent) {
			old := subagentWorktree
			subagentWorktree = func(context.Context, string, string, string, string) error { return boom }
			t.Cleanup(func() { subagentWorktree = old })
		},
	}
	for name, breakIt := range cases {
		t.Run(name, func(t *testing.T) {
			a := bareAgent(t, func(c *config.Config) { inRepo(t)(c) })
			a.startFleet(taskOf("t"))
			defer a.endFleet(TaskResult{})
			breakIt(t, a)
			out, err := a.spawnAgent(ctx, "p\nb")
			if err == nil || !strings.Contains(out, "could not be given a place to work") {
				t.Errorf("%s: %q %v", name, out, err)
			}
		})
	}
}

// With no tools directory the worktree goes to the system's temporary directory.
func TestAWorktreeWithoutAToolsDirGoesToTheTempDir(t *testing.T) {
	a := bareAgent(t, func(c *config.Config) {
		inRepo(t)(c)
		c.Sandbox.ToolsDir = ""
	})
	place, err := a.childPlace(context.Background(), "a1")
	if err != nil || place.branch == "" || !strings.HasPrefix(place.dir, os.TempDir()) {
		t.Fatalf("place = %+v, %v", place, err)
	}
	gitx.RemoveWorktree(context.Background(), a.cfg.Agent.WorkspaceDir, place.dir, true)
	os.RemoveAll(place.tmp)
}

// A background agent with no sandbox runs through its parent's executor, and a read-only parent
// makes a read-only child.
func TestANewChildInheritsTheParentsRules(t *testing.T) {
	a := bareAgent(t, func(c *config.Config) { c.Agent.ReadOnly = true })
	ran := false
	a.ExecCommand = func(context.Context, execx.Request) (string, bool, int, error) { ran = true; return "", false, 0, nil }
	child := a.newChild(childPlace{dir: a.cfg.Agent.WorkspaceDir, branch: "b"})
	if !child.cfg.Agent.ReadOnly || child.research || !child.isChild || child.cfg.FinalAction.Kind != "none" {
		t.Fatalf("child = readonly %v research %v child %v final %q", child.cfg.Agent.ReadOnly, child.research, child.isChild, child.cfg.FinalAction.Kind)
	}
	child.ExecCommand(context.Background(), execx.Request{})
	if !ran {
		t.Error("with no sandbox the child must use the parent's executor")
	}
	// A configured anchor states the whole task's goal: the child is held to the project's gate.
	if child.cfg.Anchor.Kind != "auto" || child.cfg.Anchor.Command != "" {
		t.Errorf("child anchor = %+v, want the project's gate", child.cfg.Anchor)
	}
	a.cfg.Anchor = config.Anchor{Kind: "auto", Timeout: time.Minute}
	if got := a.newChild(childPlace{dir: a.cfg.Agent.WorkspaceDir, branch: "b"}).cfg.Anchor; got.Timeout != time.Minute {
		t.Errorf("a project gate must be kept as configured: %+v", got)
	}
}

// TestABackgroundAgentIsHeldToTheProjectsGate: the main agent's anchor is the task's goal, which a
// piece of it cannot meet; the background agent's tree has a gate of its own, and that is what it
// must pass - it fails it once, fixes it, and finishes.
func TestABackgroundAgentIsHeldToTheProjectsGate(t *testing.T) {
	childRound, mainRound := 0, 0
	s := &scriptServer{execute: func(_ int, prompt string) string {
		if isChildPrompt(prompt) {
			childRound++
			if childRound == 1 {
				return step(true, "echo wip > notes.txt") // claims done with the gate still red
			}
			return step(true, "echo ok > gate.flag")
		}
		mainRound++
		switch mainRound {
		case 1:
			return acts(false, kindAct{"spawn_agent", "the gate\nMake the project's gate pass."})
		case 2:
			return acts(false, kindAct{"wait_agents", ""})
		}
		return step(true, "true")
	}}
	_, result, sink := fleetRun(t, s, func(c *config.Config) {
		projectGate(t, "test -f gate.flag")(c)
		// The main agent's own anchor is a stated goal, as in the real run.
		c.Anchor = config.Anchor{Kind: "command", Command: "sh", Args: []string{"-c", "exit 0"}, Timeout: 10 * time.Second}
	}, nil)
	if !result.Pass {
		t.Fatalf("the run must pass: %s", result.Reason)
	}
	if childRound != 2 {
		t.Errorf("the background agent must be sent back by its gate once: %d rounds", childRound)
	}
	if agents := sink.lastAgents(t); len(agents) != 2 || agents[1].State != AgentPassed {
		t.Fatalf("final snapshot = %+v", agents)
	}
}

// How an agent ended decides its state, and a commit that fails or a worktree that will not go
// away is said, not hidden.
func TestFinishChildRecordsHowItEnded(t *testing.T) {
	a := bareAgent(t, nil)
	a.startFleet(taskOf("t"))
	defer a.endFleet(TaskResult{})
	old := subagentCommit
	subagentCommit = func(context.Context, string, string) (bool, error) { return false, errors.New("disk full") }
	t.Cleanup(func() { subagentCommit = old })

	finish := func(id string, place childPlace, r TaskResult, cancelled bool) string {
		c := &subagent{id: id, purpose: "p", cancel: func() {}, done: make(chan struct{})}
		a.subs.children = append(a.subs.children, c)
		child := &Agent{member: a.fleet.add("main", id, "p", place.branch)}
		a.finishChild(c, child, place, r, cancelled)
		return c.report
	}
	gone := filepath.Join(t.TempDir(), "gone")
	if got := finish("a1", childPlace{dir: gone, branch: "motita/sub/a1"}, TaskResult{Reason: "it broke"}, false); !strings.Contains(got, "finished: failed") ||
		!strings.Contains(got, "could not be committed: disk full") || !strings.Contains(got, "0 files changed") || !strings.Contains(got, "it broke") {
		t.Errorf("failed report = %q", got)
	}
	if got := finish("a2", childPlace{dir: gone}, TaskResult{Pass: true}, true); !strings.Contains(got, "finished: cancelled") {
		t.Errorf("cancelled report = %q", got)
	}
}

func TestWaitAgentsEdges(t *testing.T) {
	ctx := context.Background()
	a := bareAgent(t, func(c *config.Config) { c.Sandbox.CheckTimeout = 20 * time.Millisecond })
	if got := a.waitAgents(ctx, ""); !strings.Contains(got, "no background agents in this run") {
		t.Errorf("outside a run: %q", got)
	}
	a.startFleet(taskOf("t"))
	defer func() {
		for _, c := range a.subs.children {
			if !c.finished() {
				close(c.done)
			}
		}
		a.fleet.close()
	}()
	if got := a.waitAgents(ctx, ""); !strings.Contains(got, "no background agent had anything to report") {
		t.Errorf("nothing started: %q", got)
	}
	if got := a.waitAgents(ctx, "zz"); !strings.Contains(got, "no background agent named zz") {
		t.Errorf("unknown id: %q", got)
	}
	stuck := &subagent{id: "a1", purpose: "p", cancel: func() {}, done: make(chan struct{})}
	a.subs.children = append(a.subs.children, stuck)
	if got := a.waitAgents(ctx, "a1"); !strings.Contains(got, "still running after the wait: a1") {
		t.Errorf("timeout: %q", got)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	a.cfg.Sandbox.CheckTimeout = 0 // the default bound; the cancelled context ends the wait first
	if got := a.waitAgents(cancelled, ""); !strings.Contains(got, "still running after the wait: a1") {
		t.Errorf("cancelled: %q", got)
	}
}

// TestABackgroundAgentAsksThroughTheMainApproverMarked: a background agent's question reaches the
// main agent's approver marked as background and named, so the interface can answer it from a
// standing decision; a run with no approver gives its children none.
func TestABackgroundAgentAsksThroughTheMainApproverMarked(t *testing.T) {
	var got []ApprovalRequest
	var mu sync.Mutex
	mainRound := 0
	s := &scriptServer{execute: func(_ int, prompt string) string {
		if isChildPrompt(prompt) {
			return step(true, "rm -rf ../outside-of-the-tree")
		}
		mainRound++
		switch mainRound {
		case 1:
			return acts(false, kindAct{"spawn_agent", "tidy\nRemove the old folder."})
		case 2:
			return acts(false, kindAct{"wait_agents", ""})
		}
		return step(true, "true")
	}}
	fleetRun(t, s, func(c *config.Config) {
		inRepo(t)(c)
		c.Agent.Policy.Enforce = true
	}, func(e *fixture) {
		e.agent.approver = func(_ context.Context, req ApprovalRequest) (bool, error) {
			mu.Lock()
			got = append(got, req)
			mu.Unlock()
			return false, nil
		}
	})
	mu.Lock()
	defer mu.Unlock()
	var background []ApprovalRequest
	for _, r := range got {
		if r.Background {
			background = append(background, r)
		}
	}
	if len(background) == 0 || background[0].Agent != "a1" || !strings.Contains(background[0].Command, "outside-of-the-tree") {
		t.Fatalf("the child's question must reach the main approver, marked: %+v", got)
	}
}
