package agent

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/madkoding/motita/internal/anchor"
	"github.com/madkoding/motita/internal/config"
	"github.com/madkoding/motita/internal/gitx"
	"github.com/madkoding/motita/internal/task"
)

// BACKGROUND AGENTS: work the run hands off and carries on beside.
//
// A request is often several pieces that do not depend on each other - the feature, its tests,
// its documentation, an investigation of how something else works - and one agent does them one
// after the other, each waiting for the last. spawn_agent starts a whole agent on one piece and
// returns at once; the run keeps working, and the piece's result arrives as a report in the round
// after it finishes (or when the run asks for it with wait_agents).
//
// Each agent that may write works in a git worktree of its own, on a branch of its own, started
// from the tree as the run has it right now - uncommitted work included, through a Snapshot - so
// two agents never write over each other and nothing lands in the run's checkout until the run
// merges the branch itself. In a directory that is not a repository there is no such place, so an
// agent there is READ-ONLY: it can investigate and answer, not change files.
//
// The limits are deliberate. Only the main agent may start agents (a tree of agents starting
// agents is a cost nobody decided to pay); agent.max_parallel bounds how many run at once; a
// background agent has nobody to ask, so a command that needs approval is refused for it; and a
// run never ends with an agent still running behind it - a claim of done is sent back once, and
// whatever is still running when the run ends is cancelled and cleaned up.

// subagentWaitDefault bounds wait_agents when no check timeout is configured.
const subagentWaitDefault = 15 * time.Minute

// The steps that touch the repository and the disk, as variables so a test can make each fail.
var (
	subagentSnapshot = gitx.Snapshot
	subagentWorktree = gitx.AddWorktreeAt
	subagentTempDir  = os.MkdirTemp
	subagentCommit   = gitx.CommitAll
)

// subagents is the main agent's record of the agents it started.
type subagents struct {
	mu         sync.Mutex
	seq        int
	children   []*subagent
	challenged bool // a claim of done was already sent back over running agents
	wg         sync.WaitGroup
}

// subagent is one background agent.
type subagent struct {
	id, purpose string
	branch      string // "" for a read-only agent
	cancel      context.CancelFunc
	done        chan struct{}
	report      string // set once it has finished
	delivered   bool
}

// running is how many started agents have not finished.
func (s *subagents) running() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, c := range s.children {
		if !c.finished() {
			n++
		}
	}
	return n
}

// finished reports whether the agent has ended (its report is ready).
func (c *subagent) finished() bool {
	select {
	case <-c.done:
		return true
	default:
		return false
	}
}

// startFleet begins a run's fleet: the main agent is its first member, and the run may start
// agents of its own. It runs for the outermost task only; a background agent runs as a nested task
// and is a member of its parent's fleet instead.
func (a *Agent) startFleet(t task.Task) {
	a.fleet = newFleet(func(line string) {
		if a.Progress != nil {
			a.Progress("%s", line)
		}
	})
	a.member = a.fleet.add("", "main", truncate(collapse(t.Description), 120), "")
	a.subs = &subagents{}
}

// endFleet ends a run's fleet: the agents still running are cancelled and cleaned up, the main
// agent's row records how the run ended, and the final snapshot is published.
func (a *Agent) endFleet(r TaskResult) {
	a.stopChildren()
	state := AgentFailed
	if r.Pass {
		state = AgentPassed
	}
	a.fleet.finish(a.member, state, "")
	a.fleet.close()
}

// stopChildren cancels every agent still running and waits for their cleanup.
func (a *Agent) stopChildren() {
	a.subs.mu.Lock()
	for _, c := range a.subs.children {
		c.cancel()
	}
	a.subs.mu.Unlock()
	a.subs.wg.Wait()
}

// noteRound tells the fleet which round this agent is on.
func (a *Agent) noteRound(round int) { a.fleet.setRound(a.member, round) }

// noteActivity tells the fleet what this agent is doing. Snapshots of reasoning and of the fleet
// itself are not activities.
func (a *Agent) noteActivity(line string) {
	if a.member == nil || strings.HasPrefix(line, LivePrefix) || strings.HasPrefix(line, AgentsPrefix) {
		return
	}
	a.fleet.setActivity(a.member, line)
}

// runAgentAction carries out spawn_agent and wait_agents. It reports whether the kind was one of
// them, what to show the model, and an error for a REFUSAL (the run's rules said no).
func (a *Agent) runAgentAction(ctx context.Context, kind string, action Command) (bool, string, error) {
	switch kind {
	case "spawn_agent":
		out, err := a.spawnAgent(ctx, action.Command)
		return true, out, err
	case "wait_agents":
		return true, a.waitAgents(ctx, action.Command), nil
	}
	return false, "", nil
}

// refused is a refusal shown to the model and returned as the action's error.
func refused(format string, args ...any) (string, error) {
	err := fmt.Errorf(format, args...)
	return "[refused: " + err.Error() + "]", err
}

// spawnAgent starts one background agent. The command is the purpose on its first line and the
// brief after it.
func (a *Agent) spawnAgent(ctx context.Context, command string) (string, error) {
	if a.isChild {
		return refused("a background agent cannot start agents of its own; do this piece yourself")
	}
	if a.subs == nil {
		return refused("background agents are not available in this mode")
	}
	limit := a.cfg.Agent.MaxParallel
	if limit <= 0 {
		return refused("background agents are turned off (agent.max_parallel is 0)")
	}
	purpose, brief, _ := strings.Cut(command, "\n")
	purpose, brief = strings.TrimSpace(purpose), strings.TrimSpace(brief)
	if purpose == "" {
		return refused("spawn_agent needs the agent's purpose on the first line of \"command\" and its brief after it")
	}
	if brief == "" {
		brief = purpose
	}
	if n := a.subs.running(); n >= limit {
		return refused("%d background agents are already running, the most this run allows (agent.max_parallel); "+
			"use wait_agents before starting another, or do this piece yourself", n)
	}

	a.subs.mu.Lock()
	a.subs.seq++
	id := fmt.Sprintf("a%d", a.subs.seq)
	a.subs.mu.Unlock()

	place, err := a.childPlace(ctx, id)
	if err != nil {
		return refused("the background agent could not be given a place to work: %v", err)
	}
	child := a.newChild(place)
	child.member = a.fleet.add(a.member.id, id, truncate(collapse(purpose), 120), place.branch)
	// The main agent's approver, marked: a session where the user allowed every command lets a
	// background agent run what the main agent could, and any other question is refused instead of
	// being asked about a run nobody is watching. With no approver at all, a child has none either.
	if parent := a.approver; parent != nil {
		child.approver = func(ctx context.Context, req ApprovalRequest) (bool, error) {
			req.Background, req.Agent = true, id
			return parent(ctx, req)
		}
	}

	cctx, cancel := context.WithCancel(ctx)
	c := &subagent{id: id, purpose: purpose, branch: place.branch, cancel: cancel, done: make(chan struct{})}
	a.subs.mu.Lock()
	a.subs.children = append(a.subs.children, c)
	a.subs.mu.Unlock()
	a.subs.wg.Add(1)
	go func() {
		defer a.subs.wg.Done()
		defer cancel()
		r := child.processTask(cctx, task.Task{
			Description: childBrief(purpose, brief, place),
			Origin:      "spawn_agent " + id,
		}, 1)
		a.finishChild(c, child, place, r, cctx.Err() != nil)
	}()
	a.log.Info("background agent started", "id", id, "purpose", truncate(purpose, 120),
		"workspace", place.dir, "branch", place.branch)

	where := "It is READ-ONLY (this directory is not a git repository): it investigates and answers, it changes no file."
	if place.branch != "" {
		where = fmt.Sprintf("It works in a worktree of its own on branch %s, started from your current tree; "+
			"nothing lands in your checkout until you merge that branch.", place.branch)
	}
	return fmt.Sprintf("[started background agent %s: %s]\n%s\nIts report arrives in the round after it "+
		"finishes; use wait_agents to wait for it.", id, purpose, where), nil
}

// childPlace is where a background agent works and what it may do there.
type childPlace struct {
	dir    string // its working directory
	branch string // its branch, or "" for a read-only agent in the run's own directory
	start  string // the commit its branch started from
	tmp    string // the directory to remove when it is done
}

// childPlace prepares a background agent's working directory: a worktree on a new branch when the
// workspace is the root of a repository with a commit, the workspace itself (read-only) otherwise.
func (a *Agent) childPlace(ctx context.Context, id string) (childPlace, error) {
	dir := a.cfg.Agent.WorkspaceDir
	root, err := gitx.Root(ctx, dir)
	if err != nil || !gitx.SamePath(root, dir) || !gitx.HasCommits(ctx, dir) {
		return childPlace{dir: dir}, nil
	}
	unique := fmt.Sprintf("%d-%d", os.Getpid(), fleetNow().UnixNano())
	ref := "refs/motita/sub/" + unique + "-" + id
	_, snap, err := subagentSnapshot(ctx, dir, ref)
	if err != nil {
		return childPlace{}, err
	}
	// The branch keeps the snapshot reachable once it exists; the reference was only its bridge.
	defer gitx.DropRef(ctx, dir, ref)
	parent := ""
	if tools := strings.TrimSpace(a.cfg.Sandbox.ToolsDir); tools != "" {
		parent = filepath.Join(tools, "worktrees")
		if err := os.MkdirAll(parent, 0o755); err != nil {
			return childPlace{}, err
		}
	}
	tmp, err := subagentTempDir(parent, "motita-sub-")
	if err != nil {
		return childPlace{}, err
	}
	place := childPlace{dir: filepath.Join(tmp, "tree"), branch: "motita/sub/" + unique + "-" + id, start: snap, tmp: tmp}
	if err := subagentWorktree(ctx, dir, place.dir, place.branch, snap); err != nil {
		os.RemoveAll(tmp)
		return childPlace{}, err
	}
	gitx.LinkDependencyDirs(dir, place.dir)
	return place, nil
}

// newChild builds the background agent: the main agent's configuration, engine, sandbox rules and
// library, in its own directory, with nobody to ask and no final action of its own.
func (a *Agent) newChild(place childPlace) *Agent {
	cfg := a.cfg
	cfg.Agent.WorkspaceDir = place.dir
	cfg.Agent.ReadOnly = a.cfg.Agent.ReadOnly || place.branch == ""
	cfg.FinalAction = config.FinalAction{Kind: "none"}
	cfg.Agent.OnFailure = config.OnFailure{}
	// A background agent is judged by the PROJECT'S gate, never by a check the configuration
	// wrote. That check states what the whole task must achieve, and one piece of it cannot pass
	// it: measured on a real run, a child asked to write b.txt was held to `test -s a.txt && test -s
	// b.txt`, a file another agent was writing. The main agent's anchor still judges the merged
	// result.
	if !strings.EqualFold(cfg.Anchor.Kind, "auto") {
		cfg.Anchor = config.Anchor{Kind: "auto", Baseline: a.cfg.Anchor.Baseline}
	}
	box := a.sandbox
	if box != nil {
		box = box.WithDir(place.dir)
	}
	child := New(cfg, a.log, a.engine, box, nil)
	if box == nil {
		child.ExecCommand = a.ExecCommand
	}
	child.library = a.library
	child.isChild = true
	child.research = place.branch == ""
	child.fleet = a.fleet
	child.Progress = func(format string, args ...any) { child.noteActivity(fmt.Sprintf(format, args...)) }
	return child
}

// childBrief is the task a background agent is given: the brief, and what it is.
func childBrief(purpose, brief string, place childPlace) string {
	var b strings.Builder
	b.WriteString(brief)
	b.WriteString("\n\nYou are a background agent, started by another agent for ONE piece of a larger task: ")
	b.WriteString(purpose)
	b.WriteString(". Do that piece and only that piece; the other agent does the rest. Nobody can answer " +
		"questions: decide on the most reasonable reading and say what you assumed.")
	if place.branch != "" {
		fmt.Fprintf(&b, " Your working directory is a git worktree of your own on branch %s, started from "+
			"the other agent's current tree. When your piece is written and checked, commit it on this branch "+
			"(git add -A && git commit -m \"...\"); the other agent merges the branch.", place.branch)
	} else {
		b.WriteString(" You are READ-ONLY: investigate and answer in your final report; you cannot change files.")
	}
	return b.String()
}

// finishChild records how a background agent ended, keeps its work on its branch, removes its
// worktree and queues its report for the main agent.
func (a *Agent) finishChild(c *subagent, child *Agent, place childPlace, r TaskResult, cancelled bool) {
	// Its context may be the reason it ended; the cleanup still has to happen.
	ctx, stop := context.WithTimeout(context.Background(), 2*time.Minute)
	defer stop()
	state := AgentFailed
	switch {
	case cancelled:
		state = AgentCancelled
	case r.Pass:
		state = AgentPassed
	}
	summary := strings.TrimSpace(r.Summary)
	if summary == "" {
		summary = strings.TrimSpace(r.Reason)
	}

	var b strings.Builder
	fmt.Fprintf(&b, "[agent %s finished: %s] %s\n", c.id, state, c.purpose)
	if place.branch != "" {
		// What it did not commit itself would go with the worktree.
		if _, err := subagentCommit(ctx, place.dir, "motita: work of background agent "+c.id+": "+
			truncate(collapse(c.purpose), 60)); err != nil {
			fmt.Fprintf(&b, "(its uncommitted work could not be committed: %v)\n", err)
			a.log.Warn("a background agent's work could not be committed", "id", c.id, "error", err)
		}
		changes, _ := gitx.ChangesSince(ctx, place.dir, place.start)
		fmt.Fprintf(&b, "branch %s: %d files changed\n", place.branch, len(changes))
		if len(changes) > 0 {
			b.WriteString("To bring it in: commit your own changes first, then git merge --no-edit " + place.branch + "\n")
		}
		if err := gitx.RemoveWorktree(ctx, a.cfg.Agent.WorkspaceDir, place.dir, true); err != nil {
			a.log.Warn("a background agent's worktree could not be removed", "id", c.id, "error", err)
		}
		os.RemoveAll(place.tmp)
	}
	if summary != "" {
		b.WriteString(summary + "\n")
	}
	a.fleet.finish(child.member, state, summary)
	a.log.Info("background agent finished", "id", c.id, "state", state)
	a.queueReport(c, b.String())
}

// queueReport stores a finished agent's report for the main agent and marks it finished.
func (a *Agent) queueReport(c *subagent, report string) {
	a.subs.mu.Lock()
	c.report = report
	a.subs.mu.Unlock()
	close(c.done)
}

// takeReports returns the reports of the finished agents not yet shown to the model (only those
// named in ids, when ids is not empty), and marks them shown.
func (a *Agent) takeReports(ids map[string]bool) string {
	if a.subs == nil {
		return ""
	}
	a.subs.mu.Lock()
	defer a.subs.mu.Unlock()
	var b strings.Builder
	for _, c := range a.subs.children {
		if c.delivered || !c.finished() || (len(ids) > 0 && !ids[c.id]) {
			continue
		}
		c.delivered = true
		b.WriteString(c.report)
	}
	return b.String()
}

// waitAgents waits for the named agents (every running one when none is named) and returns their
// reports. It is bounded: an agent that outlives the wait is named, and keeps running.
func (a *Agent) waitAgents(ctx context.Context, command string) string {
	if a.subs == nil {
		return "[no background agents in this run]"
	}
	want := map[string]bool{}
	for _, id := range strings.Fields(command) {
		want[id] = true
	}
	a.subs.mu.Lock()
	var waiting []*subagent
	known := map[string]bool{}
	for _, c := range a.subs.children {
		known[c.id] = true
		if len(want) == 0 || want[c.id] {
			waiting = append(waiting, c)
		}
	}
	a.subs.mu.Unlock()
	var unknown []string
	for id := range want {
		if !known[id] {
			unknown = append(unknown, id)
		}
	}
	sort.Strings(unknown)

	limit := a.cfg.Sandbox.CheckTimeout
	if limit <= 0 {
		limit = subagentWaitDefault
	}
	deadline := time.NewTimer(limit)
	defer deadline.Stop()
	a.report("waiting for %d background agent(s)...", len(waiting))
	var late []string
	for _, c := range waiting {
		select {
		case <-c.done:
		case <-ctx.Done():
			late = append(late, c.id)
		case <-deadline.C:
			late = append(late, c.id)
		}
	}

	var b strings.Builder
	if len(unknown) > 0 {
		fmt.Fprintf(&b, "[no background agent named %s]\n", strings.Join(unknown, ", "))
	}
	b.WriteString(a.takeReports(want))
	if len(late) > 0 {
		fmt.Fprintf(&b, "[still running after the wait: %s]\n", strings.Join(late, ", "))
	}
	if b.Len() == 0 {
		return "[no background agent had anything to report]"
	}
	return b.String()
}

// holdForChildren decides a claim of done made while background agents are running: the first
// such claim is sent back (true, with the reason in the journal), a later one cancels them and
// lets the claim go on (false).
func (a *Agent) holdForChildren(round int, commands string, journal *[]roundRecord) bool {
	if a.subs == nil {
		return false
	}
	a.subs.mu.Lock()
	var running []string
	for _, c := range a.subs.children {
		if !c.finished() {
			running = append(running, c.id+" ("+truncate(collapse(c.purpose), 80)+")")
		}
	}
	first := !a.subs.challenged
	if len(running) > 0 {
		a.subs.challenged = true
	}
	a.subs.mu.Unlock()
	if len(running) == 0 {
		return false
	}
	if first {
		a.report("done claimed while %d background agent(s) are still running; asking the model first", len(running))
		*journal = append(*journal, roundRecord{
			round: round, kind: roundProgress, commands: commands,
			detail: fmt.Sprintf("You reported \"done\": true, but %d background agent(s) you started are still "+
				"running: %s. Finishing now cancels them and their work and reports are lost. Use wait_agents "+
				"to wait for them and bring in what they did, or report \"done\": true again to cancel them.",
				len(running), strings.Join(running, ", ")),
		})
		return true
	}
	a.report("cancelling %d background agent(s): the run is finishing without them", len(running))
	a.stopChildren()
	return false
}

// validateClaim is the verdict on a claim of done. A read-only background agent cannot change
// anything a check could see, so its answer is its result; every other agent goes to the anchor.
func (a *Agent) validateClaim(ctx context.Context, before *runBaseline) anchor.Result {
	if a.research {
		return anchor.Result{Pass: true, Checks: []anchor.CheckLog{},
			Reason: "read-only background agent: its answer is the result, and it changed nothing to validate"}
	}
	v := anchor.New(a.cfg.Anchor, a.cfg.Agent.WorkspaceDir, a.sandbox).
		WithTools(a.cfg.Sandbox.ToolsDir, a.cfg.Sandbox.CheckTimeout).Validate(ctx)
	// A background agent's tree with no gate to run has nothing to be measured against here; its
	// branch is measured when the main agent merges it and claims done.
	if a.isChild && !v.Pass && len(v.Checks) == 0 {
		return anchor.Result{Pass: true, Checks: []anchor.CheckLog{},
			Reason: "no project gate in the background agent's tree: its branch is validated by the main agent's anchor once merged"}
	}
	return before.judge(ctx, v)
}
