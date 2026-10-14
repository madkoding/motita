package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/madkoding/motita/internal/execx"
	"github.com/madkoding/motita/internal/policy"
	"github.com/madkoding/motita/internal/semantic"
	"github.com/madkoding/motita/internal/template"
)

// --- Final action and escalation --------------------------------------------

// runConfigured runs a command the OPERATOR wrote: the `final_action`, the `on_failure`
// escalation, the fixed `git add` / `git commit` sequence.
//
// These used to go straight to `/bin/sh -c`, which made them the hole in the policy: the
// operator's own `final_action` ran whatever it said while the model's actions were being
// checked, and the mandatory floor — `rm -rf /`, `mkfs`, `shutdown` — was reachable through
// a configuration field. They are checked now.
//
// One difference from an action the MODEL proposed, and it is deliberate: a line that would
// be asked about runs, because the operator is the one who wrote it. Nobody is surprised by
// their own configuration, and a task that cannot notify, commit or escalate because a
// prompt had no user at the keyboard would be broken by the very mechanism meant to protect
// it. The floor still applies — an operator does not get to `mkfs` their own filesystem
// either — and that is the line this function will not cross.
//
// It is one function rather than three checks because it is one decision, and three copies
// of a check is how one of them ends up missing an approval.
func (a *Agent) runConfigured(ctx context.Context, req execx.Request, line string) (string, int, error) {
	plan := a.planRequest(line)
	switch plan.Verdict {
	case policy.Deny:
		a.log.Error("a configured action was refused by the policy",
			"command", line, "rule", plan.Rule, "mandatory", plan.Mandatory, "reason", plan.Reason)
		return "", 1, fmt.Errorf("the configured command %q was refused by the policy: %s", line, plan.Reason)
	case policy.Ask:
		a.log.Info("a configured action runs without confirming: the operator wrote it",
			"command", line, "rule", plan.Rule, "reason", plan.Reason)
	default:
		// Allowed: it runs as configured.
	}
	// The request the operator configured is used as written — program and arguments — so
	// an interpreter with its own flags (`sh -c …`) keeps working. The policy was applied to
	// its LINE, which is what the checks can read.
	req.Timeout = a.cfg.Sandbox.Timeout
	output, _, exit, err := a.exec(ctx, req)
	return output, exit, err
}

// runModelLine is the composition of a `final_action` whose payload the MODEL wrote.
//
// It is kept as its own function because the line it checks is the model's, not the
// operator's, even though the interpreter around it is the operator's: `final_action` with
// a command is a configuration that says "when the work is validated, do this", and when the
// model supplies the text, the thing being checked is that text. The mandatory floor is what
// makes the difference moot for anything unrecoverable, and it is applied to the composed
// line so that a payload of `rm -rf /` is caught where it actually lives — inside the quotes.
//
// The operator's exemption from asking does NOT carry over: the operator did not write this
// text, and a prompt-injected `git push --force` or `curl … | sh` would otherwise run after the
// PASS with nobody asked. A line the policy would ask about goes through the same approval as
// any other action the model proposes, and with nobody to ask it does not run.
func (a *Agent) runModelLine(ctx context.Context, line string) (string, int, error) {
	if plan := a.planRequest(line); plan.Verdict == policy.Ask {
		approved, err := a.approve(ctx, plan, line)
		if err != nil {
			return "", 1, fmt.Errorf("the final action %q was not run: %w", line, err)
		}
		if !approved {
			return "", 1, fmt.Errorf("the final action %q was not approved", line)
		}
	}
	return a.runConfigured(ctx,
		a.shellRequest(line), line)
}

// runFinalAction runs the action planned for after a PASS. It returns a readable
// description and an error when the action could not be completed (including a
// non-zero exit code, which is a failure even with no execution error).
func (a *Agent) runFinalAction(ctx context.Context, c Command, prefix string) (string, error) {
	final := a.cfg.FinalAction

	switch strings.ToLower(final.Kind) {
	case "", "none":
		return "none", nil
	case "command":
		return a.finalCommand(ctx, c, prefix)
	case "api":
		return a.finalAPI(ctx, c, prefix)
	case "git_commit":
		return a.finalGitCommit(ctx, c, prefix)
	default:
		return "none", fmt.Errorf("unknown final_action.kind: %q", final.Kind)
	}
}

// finalCommand runs a final action of kind command: the one the model proposed, or else the one
// configured.
func (a *Agent) finalCommand(ctx context.Context, c Command, prefix string) (string, error) {
	final := a.cfg.FinalAction
	var output string
	var exit int
	var err error
	if strings.TrimSpace(c.Command) != "" {
		// The model proposed the concrete final command: the operator's configuration
		// supplied the slot, and the text in it is the model's.
		a.log.Info(prefix+"running the final action the model proposed", "command", c.Command)
		output, exit, err = a.runModelLine(ctx, c.Command)
	} else {
		if strings.TrimSpace(final.Command) == "" {
			a.log.Warn(prefix + "final_action.kind=command with no command: nothing is done")
			return "none", nil
		}
		a.log.Info(prefix+"running the final action", "command", final.Command)
		output, exit, err = a.runConfigured(ctx,
			execx.Request{Command: final.Command, Args: final.Args}, final.Command)
	}
	description := fmt.Sprintf("command exit=%d output=%s", exit, truncate(output, 300))
	if err != nil {
		a.log.Error(prefix+"the final action failed", "error", err, "output", truncate(output, 500))
		return description, fmt.Errorf("the final action could not run: %w", err)
	}
	if exit != 0 {
		return description, fmt.Errorf("the final action finished with exit code %d", exit)
	}
	return description, nil
}

// finalAPI runs a final action of kind api: the result is posted to the configured URL.
func (a *Agent) finalAPI(ctx context.Context, c Command, prefix string) (string, error) {
	final := a.cfg.FinalAction
	body := map[string]any{
		"task":    c.Description,
		"command": c.Command,
		"status":  "pass",
	}
	data, _ := json.Marshal(body)
	method := final.Method
	if method == "" {
		method = http.MethodPost
	}
	req, err := http.NewRequestWithContext(ctx, method, final.URL, bytes.NewReader(data))
	if err != nil {
		a.log.Error(prefix+"invalid final request", "error", err)
		return "error: " + err.Error(), fmt.Errorf("the final action (API) has an invalid URL: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := a.http.Do(req)
	if err != nil {
		a.log.Error(prefix+"the final notification failed", "error", err)
		return "error: " + err.Error(), fmt.Errorf("the final action (API) failed: %w", err)
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
	description := fmt.Sprintf("api %s %s -> HTTP %d", method, final.URL, resp.StatusCode)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return description, fmt.Errorf("the final action (API) returned HTTP %d", resp.StatusCode)
	}
	return description, nil
}

// finalGitCommit runs a final action of kind git_commit: everything is staged and committed.
func (a *Agent) finalGitCommit(ctx context.Context, c Command, prefix string) (string, error) {
	final := a.cfg.FinalAction
	message, _ := template.Render(final.CommitMessage, map[string]string{"task": c.Description})
	// A commit made by the agent is a semantic one like every other: the configured
	// message is kept when it already is, and is given the chore(agent) prefix when not.
	message = semantic.Coerce(message, "agent")
	// The staging line is a fixed command of this program, and it is checked like any
	// other so that a workspace pointed at a strange place cannot turn it into
	// something else. Nothing here is configurable as a command: the operator chooses
	// that a commit happens, not how.
	sequence := []string{
		"git add -A",
		fmt.Sprintf("git commit -m %s", execx.Quote(shellFor(a.cfg), message)),
	}
	var outputs []string
	for _, cmd := range sequence {
		output, exit, err := a.runConfigured(ctx,
			a.shellRequest(cmd), cmd)
		outputs = append(outputs, fmt.Sprintf("%s -> exit=%d %s", cmd, exit, truncate(output, 200)))
		if err != nil {
			a.log.Error(prefix+"git_commit failed", "command", cmd, "error", err)
			return gitCommitPrefix + strings.Join(outputs, " | "),
				fmt.Errorf("git_commit: %s failed: %w", cmd, err)
		}
		if exit != 0 {
			return gitCommitPrefix + strings.Join(outputs, " | "),
				fmt.Errorf("git_commit: %s finished with exit code %d", cmd, exit)
		}
	}
	return gitCommitPrefix + strings.Join(outputs, " | "), nil
}

// escalate fires the configured escalation action when the task does not pass.
func (a *Agent) escalate(ctx context.Context, prefix string) {
	if a.cfg.Agent.OnFailure.Kind != "command" || strings.TrimSpace(a.cfg.Agent.OnFailure.Command) == "" {
		return
	}
	a.log.Warn(prefix+"escalating after exhausting the attempts", "command", a.cfg.Agent.OnFailure.Command)
	output, exit, err := a.runConfigured(ctx,
		a.shellRequest(a.cfg.Agent.OnFailure.Command),
		a.cfg.Agent.OnFailure.Command)
	if err != nil {
		a.log.Error(prefix+"the escalation action failed", "error", err)
		return
	}
	a.log.Info(prefix+"escalation executed", "exit", exit, "output", truncate(output, 300))
}
