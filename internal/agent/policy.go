package agent

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/madkoding/motita/internal/config"
	"github.com/madkoding/motita/internal/execx"
	"github.com/madkoding/motita/internal/policy"
	"github.com/madkoding/motita/internal/readonly"
)

// RequestPlan is what may be done with a line the model proposed.
//
// It replaces the old (request, refusal string) pair because there are now three answers
// and not two. "Refused" covered both "never" and "not until somebody says yes", and
// collapsing those is how a policy ends up with no middle: everything either runs or is
// refused, and the honest answer for most consequential actions — ask — has nowhere to go.
type RequestPlan struct {
	// Request is what to run. It is filled in for Allow and for Ask (so an approval can
	// be executed without re-deriving it); it is meaningless for Deny.
	Request execx.Request
	// Verdict is the answer.
	Verdict policy.Verdict
	// Reason explains it, in a form the user or the model can act on.
	Reason string
	// Rule names the rule that produced the verdict, for the log and for tests.
	Rule string
	// Mandatory is true when no setting may relax this refusal.
	Mandatory bool
}

// ApprovalRequest is what a user is asked before a consequential command runs.
type ApprovalRequest struct {
	// Command is the line, exactly as it would run. It is the whole point of the
	// question: the user is approving THIS text, not a summary of it.
	Command string
	// Reason says why it is being asked about.
	Reason string
	// Rule names the policy rule, so a repeat complaint can be traced to a line of code.
	Rule string
}

// Approver asks the user whether a command may run. It returns false to refuse.
//
// It is injected, and it is nil in a run with nobody at the other end. That absence is
// meaningful rather than a nuisance: a command that needs approval and has nobody to ask
// is REFUSED, because running it would be deciding on the user's behalf the one thing the
// policy deliberately does not decide.
type Approver func(ctx context.Context, req ApprovalRequest) (bool, error)

// SetApprover installs the channel a consequential command is confirmed through. It is
// the interface's job to provide one; a batch run leaves it nil.
func (a *Agent) SetApprover(fn Approver) { a.approver = fn }

// policyMode is the operator's policy configuration, as the agent applies it.
func (a *Agent) policyMode() policy.Mode {
	return policy.Mode{
		Enforce: a.cfg.Agent.Policy.Enforce,
		Strict:  a.cfg.Agent.Policy.Strict,
	}
}

// policyDir is the directory the policy measures "inside" against: the workspace the
// commands actually run in.
//
// It is resolved to an absolute path HERE, in the parent, while the working directory the
// configuration was written for is still in effect. A relative path handed to a child is
// resolved twice, in two processes, and the comparison then measures the wrong directory —
// the same trap the sandbox documents.
func (a *Agent) policyDir() string {
	dir := strings.TrimSpace(a.cfg.Agent.WorkspaceDir)
	if dir == "" || dir == "." {
		if wd, err := filepath.Abs("."); err == nil {
			return wd
		}
		return "."
	}
	if abs, err := filepath.Abs(dir); err == nil {
		return abs
	}
	return dir
}

// planRequest turns a line written by the model into the request that will be executed,
// and reports what may be done with it.
//
// Three modes, and the differences matter:
//
//   - read-only (plan mode): the command is split with a shell-aware tokeniser and checked
//     against the reader allowlist. The request runs the program DIRECTLY, with no shell,
//     so `;`, `>`, `>>`, `&&` or `$(...)` are impossible: there is no interpreter to honour
//     them. A command that is not known to read is refused.
//
// otherwise, policy on: the mandatory floor is applied to the LINE first, the program is
// classified, and a consequential action comes back as Ask for the user to approve. The
// line still runs through the shell — pipes, redirections and globs are what let it do
// real work — but only after the verdict allowed it.
//   - otherwise, policy off: the line goes to the shell unchanged, which is the behaviour
//     this project had before the policy existed. The mandatory floor is applied even
//     here: an operator may choose to run without confirming, and nobody chooses `mkfs`
//     on their own filesystem.
func (a *Agent) planRequest(command string) RequestPlan {
	timeout := a.cfg.Sandbox.Timeout

	if a.readOnly() {
		// The tokeniser is the SHARED one, in readonly, so that the mode that runs programs
		// directly and the mode that classifies them cannot drift into two different ideas
		// of where a line divides. It refuses shell metacharacters, which is what makes
		// read-only mode a guarantee rather than a request: there is no interpreter to
		// honour a pipe or a redirection, so a line containing one cannot run as written.
		name, args, err := readonly.SplitCommand(command)
		if err != nil {
			return RequestPlan{Verdict: policy.Deny, Reason: err.Error(), Rule: "readonly-split"}
		}
		decision := readonly.Check(name, args)
		if !decision.Allowed {
			return RequestPlan{Verdict: policy.Deny, Reason: decision.Reason, Rule: "readonly"}
		}
		return RequestPlan{
			Request: execx.Request{Command: name, Args: args, Timeout: timeout},
			Verdict: policy.Allow,
			Reason:  decision.Reason,
			Rule:    "readonly",
		}
	}

	dir := a.policyDir()
	mode := a.policyMode()

	// The line is analysed as a whole, in segments, because that is what a line is: `ls`
	// followed by `> file` is a reader and a write, and judging it by its first word would
	// let the second half run. The mandatory floor is applied inside, on the raw text,
	// before anything is classified.
	d := mode.DecideLine(command, dir)
	return RequestPlan{
		Request:   execx.Request{Command: shellFor(a.cfg), Args: []string{"-c", command}, Timeout: timeout},
		Verdict:   d.Verdict,
		Reason:    d.Reason,
		Rule:      d.Rule,
		Mandatory: d.Mandatory,
	}
}

// approve asks the user about a consequential command.
//
// It returns an error when there is nobody to ask, and the error is not a failure to
// report but the ANSWER: the request could not be approved because no approval was
// possible, and the action does not run. Saying that plainly is better than a silent
// refusal, which reads as "the agent decided not to".
func (a *Agent) approve(ctx context.Context, plan RequestPlan, command string) (bool, error) {
	if a.approver == nil {
		return false, fmt.Errorf("this action needs your approval and there is nobody to ask " +
			"(a run started from a script or a job has no user): set agent.policy.enforce=false " +
			"to run such actions without asking")
	}
	return a.approver(ctx, ApprovalRequest{
		Command: command,
		Reason:  plan.Reason,
		Rule:    plan.Rule,
	})
}

// readOnly reports whether the agent is in the mode that changes nothing.
func (a *Agent) readOnly() bool {
	return a.cfg.Agent.ReadOnly
}

// shellFor returns the interpreter for the platform this binary runs on. It is a
// small indirection so the agent does not import the platform files of cmd/chat.
func shellFor(cfg config.Config) string {
	if cfg.Agent.Shell != "" {
		return cfg.Agent.Shell
	}
	return defaultShell()
}
