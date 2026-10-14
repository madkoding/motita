package agent

import (
	"context"
	"fmt"
	"strings"

	"github.com/madkoding/motita/internal/policy"
)

func (a *Agent) runActions(ctx context.Context, actions []Command, prefix string) (string, error) {
	return a.runRound(ctx, actions, prefix, nil, 0)
}

// roundRun is one round of actions being executed: where the output of each is collected, and the
// last error any of them gave.
type roundRun struct {
	a       *Agent
	ctx     context.Context
	prefix  string
	mem     *workMemory
	round   int
	sb      strings.Builder
	lastErr error
}

// runRound is runActions with the run's working memory. A read the run already holds, and
// that nothing has invalidated, is answered from memory instead of being executed; a read
// that ran is kept; a write forgets every kept read. mem == nil is the plain behaviour.
func (a *Agent) runRound(
	ctx context.Context, actions []Command, prefix string, mem *workMemory, round int,
) (string, error) {
	rr := &roundRun{a: a, ctx: ctx, prefix: prefix, mem: mem, round: round}
	// The tree as this round finds it. Anything that changed it since the last look (the
	// anchor ran a build, a test wrote a file) makes the kept reads untrustworthy.
	if mem != nil {
		mem.sync(a.cfg.Agent.WorkspaceDir)
	}

	for i, action := range actions {
		rr.run(i, action)
	}
	return rr.sb.String(), rr.lastErr
}

// run executes the i-th action of the round.
func (rr *roundRun) run(i int, action Command) {
	// A library action is not a shell command: it is answered from the procedure library and
	// its result goes back to the model as the output of this step. It is dispatched by
	// kind, because that is how this mode names things — the same four operations the
	// planner exposes as tools, reachable from here too.
	if kind := strings.ToLower(strings.TrimSpace(action.Kind)); kind != "" && kind != "command" {
		if rr.kindAction(i, kind, action) {
			return
		}
	}
	if strings.TrimSpace(action.Command) == "" {
		rr.a.log.Debug(rr.prefix+"action with no command (descriptive)", "description", action.Description)
		return
	}
	if rr.recalled(action) {
		return
	}
	rr.shell(i, action)
}

// kindAction runs an action that names a kind other than a shell command, and reports whether
// it was handled.
func (rr *roundRun) kindAction(i int, kind string, action Command) bool {
	return rr.agentAction(i, kind, action) || rr.fileAction(i, kind, action) ||
		rr.libraryAction(kind, action) || rr.unknownKind(i, kind, action)
}

// agentAction is a background agent started or waited for (see subagents.go).
func (rr *roundRun) agentAction(i int, kind string, action Command) bool {
	a := rr.a
	handled, out, refusal := a.runAgentAction(rr.ctx, kind, action)
	if !handled {
		return false
	}
	fmt.Fprintf(&rr.sb, "[%s] %s\n%s\n", kind, action.Description, out)
	a.report("running: %s %s", kind, firstLineOf(action.Command))
	a.log.Info(rr.prefix+"agent action", "kind", kind, "command", truncate(firstLineOf(action.Command), 120))
	if refusal != nil {
		rr.lastErr = fmt.Errorf("action %d (%s) was refused: %w", i+1, kind, refusal)
	}
	return true
}

// fileAction is a file written by the program itself (see fileactions.go). It is a write, so
// the kept reads are checked against the tree exactly as after a shell write.
func (rr *roundRun) fileAction(i int, kind string, action Command) bool {
	a := rr.a
	handled, out, refusal := a.runFileAction(kind, action)
	if !handled {
		return false
	}
	fmt.Fprintf(&rr.sb, "[%s] %s\n%s\n", kind, action.Description, out)
	a.report("running: %s %s", kind, firstLineOf(action.Command))
	a.log.Info(rr.prefix+"file action", "kind", kind, "path", firstLineOf(action.Command), "result", out)
	if refusal != nil {
		rr.lastErr = fmt.Errorf("action %d (%s) was refused: %w", i+1, kind, refusal)
	}
	if rr.mem != nil {
		rr.mem.executed++
		rr.mem.sync(a.cfg.Agent.WorkspaceDir)
	}
	return true
}

// libraryAction is an operation on the procedure library.
func (rr *roundRun) libraryAction(kind string, action Command) bool {
	handled, out := rr.a.runLibraryAction(kind, action)
	if !handled {
		return false
	}
	fmt.Fprintf(&rr.sb, "[%s] %s\n%s\n", kind, action.Description, out)
	rr.a.log.Info(rr.prefix+"library action", "kind", kind, "description",
		truncate(action.Description, 80))
	return true
}

// unknownKind is a kind this mode does not know, carrying text the model meant as ARGUMENTS.
//
// Falling through to the command path treats that text as a program name and runs
// it: a model that asks for plan mode's `read_file` arrives here with a path, and
// the path is executed as a shell command — `/etc/hostname: Permission denied` is
// the shape of it. It is refused by name instead, because guessing which part is a
// program and which is an argument is exactly the mistake: the model named an
// operation that does not exist, and saying so is what lets it choose one that does.
//
// The refusal is not an error of the run: the turn continues, the model reads this
// and answers with a kind that exists.
func (rr *roundRun) unknownKind(i int, kind string, action Command) bool {
	if strings.TrimSpace(action.Command) == "" && strings.TrimSpace(action.Description) == "" {
		return false
	}
	fmt.Fprintf(&rr.sb, "[%s] %s\n[refused: %q is not an action this mode has. "+
		"Available: command, write_file, edit_file, spawn_agent, wait_agents, list_skills, "+
		"search_skills, read_skill, save_skill. "+
		"To run a shell command, use kind \"command\".]\n",
		kind, action.Description, kind)
	rr.a.log.Warn(rr.prefix+"action with an unknown kind",
		"kind", kind, "command", truncate(action.Command, 80))
	rr.lastErr = fmt.Errorf("action %d names the unknown kind %q", i+1, kind)
	return true
}

// recalled answers a read from the working memory when the run already holds it, and reports
// whether it did.
func (rr *roundRun) recalled(action Command) bool {
	if rr.mem == nil || !isRead(action) {
		return false
	}
	rec, ok := rr.mem.recall(action.Command)
	if !ok {
		return false
	}
	fmt.Fprintf(&rr.sb, "$ %s\n[not run again: the same read was made in round %d and nothing has "+
		"changed since. Its full output is under \"WHAT YOU HAVE ALREADY READ\" in this prompt]\n",
		action.Command, rec.round)
	rr.a.log.Info(rr.prefix+"read answered from memory", "command", truncate(action.Command, 120),
		"round", rec.round)
	rr.mem.recalled++
	return true
}

// shell runs an action as a command line, once the policy has had its say.
func (rr *roundRun) shell(i int, action Command) {
	a := rr.a
	a.report("running: %s", action.Command)
	a.log.Info(rr.prefix+"running in sandbox", "n", i+1, "command", action.Command,
		"description", truncate(action.Description, 120))

	// How the action is executed depends on the mode, and it is the difference
	// between a request and a guarantee:
	//
	//  - read-only (plan mode): the line is split into program and arguments, the
	//    policy checks the program, and NO shell runs. Without a shell there is no
	//    `>`, `>>`, `;`, `&&` or `$(...)`: redirection cannot happen because
	//    nothing interprets it.
	//  - otherwise: the line goes to the shell, because that is what lets the
	//    model use pipes, redirections and globs to do real work — but only once the
	//    policy has had its say. A consequential action is put in front of the user
	//    here, mid-run, because this is the moment it is about to happen.
	plan := a.planRequest(action.Command)
	if plan.Verdict == policy.Deny {
		fmt.Fprintf(&rr.sb, "$ %s\n[refused: %s]\n", action.Command, plan.Reason)
		a.log.Warn(rr.prefix+"action refused by the policy",
			"command", action.Command, "rule", plan.Rule, "mandatory", plan.Mandatory, "reason", plan.Reason)
		rr.lastErr = fmt.Errorf("action %d (%s) was refused: %s", i+1, action.Command, plan.Reason)
		return
	}
	if plan.Verdict == policy.Ask && !rr.approved(i, action, &plan) {
		return
	}

	output, truncated, exit, err := a.exec(rr.ctx, plan.Request)
	rr.record(action, output, truncated, exit, err)
	if err != nil {
		rr.lastErr = fmt.Errorf("action %d (%s) could not run: %w", i+1, action.Command, err)
	}
}

// approved puts an action the policy asks about in front of the user, and reports whether it
// may run; an approved one runs unconfined.
func (rr *roundRun) approved(i int, action Command, plan *RequestPlan) bool {
	a := rr.a
	a.report("asking you to approve: %s", action.Command)
	approved, err := a.approve(rr.ctx, *plan, action.Command)
	if err != nil {
		// There is nobody to ask. This is not a failure of the command, it is the
		// absence of the only thing that could authorise it, and the output says
		// so in those words so the model does not retry the same line.
		fmt.Fprintf(&rr.sb, "$ %s\n[not approved: %s]\n", action.Command, err)
		a.log.Warn(rr.prefix+"action needs approval and there is nobody to ask",
			"command", action.Command, "rule", plan.Rule)
		rr.lastErr = fmt.Errorf("action %d (%s) needs approval and there is nobody to ask: %w",
			i+1, action.Command, err)
		return false
	}
	if !approved {
		fmt.Fprintf(&rr.sb, "$ %s\n[not approved: the user declined this command]\n", action.Command)
		a.log.Info(rr.prefix+"action declined by the user",
			"command", action.Command, "rule", plan.Rule)
		rr.lastErr = fmt.Errorf("action %d (%s) was declined by the user", i+1, action.Command)
		return false
	}
	a.log.Info(rr.prefix+"action approved by the user", "command", action.Command)
	approveRequest(&plan.Request, action.Command)
	return true
}

// record reports what a command printed, adds it to the round's output and lets the working
// memory learn from it.
func (rr *roundRun) record(action Command, output string, truncated bool, exit int, err error) {
	// What the command printed, so the user sees what the agent is looking at, not only
	// that it ran something.
	//
	// It is reported even when the command printed nothing: the exit code is what tells
	// the reader whether it worked, and a silent success (`printf > file`) would
	// otherwise never be marked as finished.
	// What the reader and the terminal see is what the command actually printed, up to a cap
	// that is generous on purpose: a `grep` of a large tree is exactly the output a person
	// asks for, and cutting it to a handful of lines made the terminal useless as a record.
	// What is kept is the whole result of a normal command; only an enormous one is cut, and
	// then the cut says so, with how much was left out.
	rr.a.report("output (exit %d):\n%s", exit,
		truncateMiddle(strings.TrimSpace(output), terminalOutputChars))

	sb := &rr.sb
	fmt.Fprintf(sb, "$ %s\n", action.Command)
	if output != "" {
		sb.WriteString(output)
		if !strings.HasSuffix(output, "\n") {
			sb.WriteString("\n")
		}
	}
	if truncated {
		sb.WriteString("[output truncated by the sandbox limit]\n")
	}
	fmt.Fprintf(sb, "[exit=%d]\n", exit)

	if rr.mem != nil {
		rr.remember(action, output, truncated, exit, err)
	}
}

// remember lets the working memory learn from a command that ran.
func (rr *roundRun) remember(action Command, output string, truncated bool, exit int, err error) {
	mem := rr.mem
	mem.executed++
	mem.noteVerification(action.Command, rr.round, exit, err, output)
	switch {
	case isWrite(action):
		// It looks like it could write; whether it DID is a question about the files.
		mem.sync(rr.a.cfg.Agent.WorkspaceDir)
	case isRead(action) && err == nil && exit == 0 && !truncated:
		mem.remember(action.Command, rr.round, output)
	default:
		// Neither a write nor a clean read: nothing to keep.
	}
}
