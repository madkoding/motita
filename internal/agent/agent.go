// Package agent implements the agent's main loop, which orchestrates the three
// layers:
//
//	Layer A (anchor)  validates deterministically, without reasoning
//	Layer B (llm)     analyses, plans and proposes actions
//	Layer C (sandbox) runs actions in isolation
//
// The loop is the one from the architecture document:
//
//	[1] read task             [6] LLM: generate the action
//	[2] extract context       [7] run in the sandbox
//	[3] LLM: analyse          [8] validate with the anchor
//	[4] LLM: plan             [9] PASS -> final action / FAIL -> retry
//	[5] split into subtasks       and once retries are exhausted -> escalate
//
// The agent never decides on its own that it has finished: only the anchor can
// declare PASS.
package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/madkoding/starlight/internal/anchor"
	"github.com/madkoding/starlight/internal/config"
	"github.com/madkoding/starlight/internal/execx"
	"github.com/madkoding/starlight/internal/llm"
	"github.com/madkoding/starlight/internal/logx"
	"github.com/madkoding/starlight/internal/sandbox"
	"github.com/madkoding/starlight/internal/task"
	"github.com/madkoding/starlight/internal/template"
)

// Agent orchestrates the three layers.
type Agent struct {
	cfg     config.Config
	log     *logx.Logger
	engine  *llm.Client
	sandbox *sandbox.Sandbox
	source  task.Source
	http    *http.Client

	// ExecCommand is the agent's command execution point. By default it uses the
	// sandbox (Layer C); it is injected so actions that depend on external
	// programs (git_commit, for instance) can be tested without depending on the
	// program existing, on its configuration or on its interactive behaviour.
	ExecCommand func(context.Context, execx.Request) (string, bool, int, error)

	// Observer, when not nil, receives the result of every task on completion.
	// It is the integration point for metrics or for whoever embeds the agent,
	// and it is also what the tests use to inspect the verdict without reading
	// the log.
	Observer func(TaskResult)
}

// New builds the agent with all of its dependencies already constructed.
func New(cfg config.Config, log *logx.Logger, engine *llm.Client, box *sandbox.Sandbox, source task.Source) *Agent {
	if log == nil {
		log = logx.Global()
	}
	a := &Agent{
		cfg:     cfg,
		log:     log,
		engine:  engine,
		sandbox: box,
		source:  source,
		http:    &http.Client{Timeout: 60 * time.Second},
	}
	if box != nil {
		a.ExecCommand = box.Run
	}
	return a
}

// exec runs a command with the agent's executor (the sandbox by default). It
// returns a clear error when none is configured.
func (a *Agent) exec(ctx context.Context, p execx.Request) (string, bool, int, error) {
	if a.ExecCommand == nil {
		return "", false, -1, errors.New("the agent has no command executor configured")
	}
	return a.ExecCommand(ctx, p)
}

// --- Result of each phase ---------------------------------------------------

// Analysis is the structured output of phase [3].
type Analysis struct {
	Understandable bool     `json:"understandable"`
	Summary        string   `json:"summary"`
	SuccessCrit    []string `json:"success_criteria"`
	Risks          []string `json:"risks"`
	NeedsSubtasks  bool     `json:"needs_subtasks"`
}

// Plan is the structured output of phase [4].
type Plan struct {
	Steps          []Step   `json:"plan"`
	Subtasks       []string `json:"subtasks"`
	ExpectedResult string   `json:"expected_result"`
}

// Step is one step of the plan.
type Step struct {
	Number  int    `json:"step"`
	Action  string `json:"action"`
	Command string `json:"command"`
}

// Action is what phase [6] returns.
type Action struct {
	Reasoning string    `json:"reasoning"`
	Actions   []Command `json:"actions"`
	Final     Command   `json:"final_action"`
}

// Command is an executable action.
type Command struct {
	Kind        string `json:"kind"`
	Description string `json:"description"`
	Command     string `json:"command"`
}

// TaskResult is the final verdict of one task.
type TaskResult struct {
	Task        string         `json:"task"`
	Pass        bool           `json:"pass"`
	Attempts    int            `json:"attempts"`
	Subtasks    int            `json:"subtasks"`
	DurationMS  int64          `json:"duration_ms"`
	Validation  *anchor.Result `json:"validation,omitempty"`
	FinalAction string         `json:"final_action,omitempty"`
	Reason      string         `json:"reason"`
}

// --- Main loop --------------------------------------------------------------

// Run processes tasks from the source until it is exhausted (io.EOF) or the
// context is cancelled (graceful shutdown).
func (a *Agent) Run(ctx context.Context) error {
	if strings.EqualFold(a.cfg.Anchor.Kind, "none") || a.cfg.Anchor.Kind == "" {
		// Without an anchor there is no authority to declare PASS, so every task
		// would end up escalated after burning attempts against the LLM. Failing
		// now, with the concrete fix, is cheaper and more honest.
		return errors.New("anchor.kind=none: this agent only declares a task complete " +
			"when a deterministic validator gives PASS, and none is configured.\n" +
			"   Configure a real anchor, for example:\n" +
			"     anchor:\n       kind: command\n       command: make\n       args: [test]\n" +
			"   If you only want to exercise the loop without validating anything, make it explicit:\n" +
			"     anchor:\n       kind: command\n       command: true\n" +
			"   Check the configuration with: starlight -config <file> -validate-config")
	}

	a.log.Info("agent started",
		"source", a.source.Describe(),
		"provider", a.cfg.LLM.Provider,
		"model", a.cfg.LLM.Model,
		"anchor", a.cfg.Anchor.Kind,
		"sandbox", strings.Join(modesToStrings(a.sandbox.Isolation()), ", "),
		"workspace", a.cfg.Agent.WorkspaceDir,
		"max_retries", a.cfg.Agent.MaxRetries)

	defer a.source.Close()

	total := 0
	failed := 0
	reasons := []string{}
	// Consecutive source failures are counted separately: a source that fails
	// forever (a directory that disappeared, an unreachable API) must not spin
	// the loop at full speed burning CPU and log. After a few in a row the agent
	// gives up with a clear error instead of hanging silently.
	consecutive := 0
	sourceFailures := 0
	const maxConsecutive = 5

	for {
		select {
		case <-ctx.Done():
			a.log.Info("shutdown requested, finishing", "tasks_processed", total)
			return nil
		default:
		}

		if a.cfg.Agent.MaxTasks > 0 && total >= a.cfg.Agent.MaxTasks {
			a.log.Info("agent.max_tasks reached", "tasks", total)
			break
		}

		t, err := a.source.Next(ctx)
		if errors.Is(err, io.EOF) {
			a.log.Info("no more tasks", "tasks_processed", total)
			break
		}
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			// A source failure is NOT a failed task: no task was taken. Counting
			// it as one produced nonsense totals such as "2 of 1 tasks failed".
			// It is tracked on its own and surfaced through the error below.
			sourceFailures++
			consecutive++
			a.log.Error("could not obtain the task", "error", err,
				"consecutive", consecutive, "source_failures", sourceFailures)

			if consecutive >= maxConsecutive {
				// The source is broken, not just momentarily empty. Returning
				// makes the failure visible and actionable (cron/systemd see a
				// non-zero exit) instead of looping forever.
				return fmt.Errorf("the task source failed %d times in a row, giving up: %w", consecutive, err)
			}
			continue
		}
		consecutive = 0
		if strings.TrimSpace(t.Description) == "" {
			continue
		}

		total++
		result := a.processTask(ctx, t, 0)
		if result.Pass {
			a.log.Info("task completed",
				"task", truncate(t.Description, 120),
				"attempts", result.Attempts,
				"duration_ms", result.DurationMS,
				"final_action", result.FinalAction)
		} else {
			failed++
			reasons = append(reasons, fmt.Sprintf("%q: %s", truncate(t.Description, 80), result.Reason))
			a.log.Error("task failed",
				"task", truncate(t.Description, 120),
				"attempts", result.Attempts,
				"reason", result.Reason)
		}
	}

	a.log.Info("agent finished", "tasks", total, "failed", failed)
	if failed > 0 {
		// The reason of every failure is included: whoever reads the error (or
		// cron's output) must be able to act without digging through the log.
		return fmt.Errorf("%d of %d tasks failed: %s", failed, total, strings.Join(reasons, " | "))
	}
	return nil
}

// processTask runs the 9-step loop for one task and notifies the observer of the
// result (exactly once, whatever the exit path).
func (a *Agent) processTask(ctx context.Context, t task.Task, depth int) TaskResult {
	r := a.loop(ctx, t, depth)
	if a.Observer != nil {
		a.Observer(r)
	}
	return r
}

// loop is the 9-step loop itself.
func (a *Agent) loop(ctx context.Context, t task.Task, depth int) TaskResult {
	start := time.Now()
	res := TaskResult{Task: t.Description, Attempts: 0}

	prefix := strings.Repeat("  ", depth)
	a.log.Info(prefix+"task received", "origin", t.Origin, "depth", depth, "description", truncate(t.Description, 200))

	// [2] Extract context and success criteria: the statement of the validation
	// rules shown to the LLM is built here, so it knows what it will be measured
	// against.
	rules := a.describeRules()

	// [3] Analysis.
	analysis := a.analysisPhase(ctx, t, rules, depth)
	if !analysis.Understandable {
		res.Reason = "the task was declared not understandable: " + strings.Join(analysis.Risks, "; ")
		res.DurationMS = time.Since(start).Milliseconds()
		a.log.Warn(prefix+"task not understandable, discarded", "reason", res.Reason)
		return res
	}

	// [4] Plan.
	plan := a.planPhase(ctx, t, analysis, depth)

	// [5] Splitting into subtasks: each one re-enters the same flow, one level
	// further down. The limit is respected to avoid infinite recursion.
	if analysis.NeedsSubtasks && len(plan.Subtasks) > 0 {
		if depth >= a.cfg.Agent.SubtaskDepth {
			a.log.Warn(prefix+"subtask splitting reached the configured limit; continuing as a single task",
				"depth", depth, "limit", a.cfg.Agent.SubtaskDepth,
				"subtasks", len(plan.Subtasks))
		} else {
			a.log.Info(prefix+"splitting into subtasks", "count", len(plan.Subtasks))
			passed := 0
			for _, sub := range plan.Subtasks {
				if ctx.Err() != nil {
					res.Reason = "cancelled during the subtasks"
					res.DurationMS = time.Since(start).Milliseconds()
					return res
				}
				if strings.TrimSpace(sub) == "" {
					continue
				}
				res.Subtasks++
				subResult := a.processTask(ctx, task.Task{
					Description: sub,
					Origin:      t.Origin + " (subtask)",
				}, depth+1)
				if subResult.Pass {
					passed++
				}
			}
			res.Pass = passed == res.Subtasks && res.Subtasks > 0
			res.Attempts = 1
			res.DurationMS = time.Since(start).Milliseconds()
			if res.Pass {
				res.Reason = fmt.Sprintf("%d subtasks completed", passed)
			} else {
				res.Reason = fmt.Sprintf("only %d of %d subtasks passed validation", passed, res.Subtasks)
			}
			return res
		}
	}

	// [6]-[9] Execution and validation cycle.
	failedAttempts := []string{}

	for attempt := 1; attempt <= a.cfg.Agent.MaxRetries+1; attempt++ {
		res.Attempts = attempt

		// [6] The LLM proposes the concrete action.
		action, err := a.actionPhase(ctx, t, plan, failedAttempts, attempt, prefix)
		if err != nil {
			res.Reason = "could not obtain the action from the LLM: " + err.Error()
			res.DurationMS = time.Since(start).Milliseconds()
			a.escalate(ctx, prefix)
			return res
		}

		// [7] Run in the sandbox.
		runOutput, runErr := a.runActions(ctx, action.Actions, prefix)

		// [8] Validate with the anchor, always. The anchor runs even when the
		// execution failed, because its job is to describe the real state of the
		// system, not to opine about what the agent did.
		validation := anchor.New(a.cfg.Anchor, a.cfg.Agent.WorkspaceDir, a.sandbox).Validate(ctx)
		res.Validation = &validation

		if validation.Pass && runErr == nil {
			// [9] Validation PASS: now the final action, which is also part of
			// the contract (commit, publish, notify). If the final action fails,
			// the task is NOT complete: reporting "completed" while the commit or
			// the publication failed would be exactly the kind of false success
			// this architecture exists to prevent.
			finalAction, finalErr := a.runFinalAction(ctx, action.Final, prefix)
			res.FinalAction = finalAction
			if finalErr != nil {
				detail := fmt.Sprintf("validation passed but the final action failed: %v\nOutput: %s", finalErr, finalAction)
				failedAttempts = append(failedAttempts, detail)
				a.log.Error(prefix+"final action failed", "attempt", attempt, "final_action", finalAction, "error", finalErr)
				if attempt > a.cfg.Agent.MaxRetries {
					res.Reason = detail
					res.DurationMS = time.Since(start).Milliseconds()
					a.escalate(ctx, prefix)
					return res
				}
				continue
			}
			res.Pass = true
			res.Reason = validation.Reason
			res.DurationMS = time.Since(start).Milliseconds()
			a.log.Info(prefix+"validation passed", "attempt", attempt, "final_action", finalAction)
			return res
		}

		// FAIL: the records are accumulated so the LLM can correct with data.
		detail := a.summariseFailure(action, runOutput, validation, runErr)
		failedAttempts = append(failedAttempts, detail)
		a.log.Warn(prefix+"attempt failed",
			"attempt", attempt, "max_attempts", a.cfg.Agent.MaxRetries+1,
			"validation", validation.Reason)

		if attempt > a.cfg.Agent.MaxRetries {
			break
		}
	}

	// Attempts exhausted: escalate.
	res.Reason = fmt.Sprintf("all %d attempts were exhausted without passing validation", a.cfg.Agent.MaxRetries+1)
	res.DurationMS = time.Since(start).Milliseconds()
	a.escalate(ctx, prefix)
	return res
}

// --- Phases -----------------------------------------------------------------

func (a *Agent) analysisPhase(ctx context.Context, t task.Task, rules string, depth int) Analysis {
	vars := a.baseVariables(t)
	vars["rules"] = rules
	vars["attempt"] = "1"
	vars["max_attempts"] = fmt.Sprint(a.cfg.Agent.MaxRetries + 1)

	text, err := a.ask(ctx, a.cfg.Prompts.Analyze, vars, "analyze")
	if err != nil {
		a.log.Error("analysis phase failed", "error", err)
		return Analysis{Understandable: false, Risks: []string{err.Error()}}
	}

	var analysis Analysis
	if err := llm.DecodeJSON(text, &analysis); err != nil {
		a.log.Error("the analysis has an unexpected format", "error", err, "response", truncate(text, 300))
		return Analysis{Understandable: false, Risks: []string{"the LLM's analysis was not valid JSON: " + err.Error()}}
	}
	if analysis.Summary == "" {
		analysis.Summary = truncate(t.Description, 200)
	}
	a.log.Info("analysis completed", "summary", truncate(analysis.Summary, 160), "criteria", len(analysis.SuccessCrit))
	return analysis
}

func (a *Agent) planPhase(ctx context.Context, t task.Task, analysis Analysis, depth int) Plan {
	vars := a.baseVariables(t)
	var sb strings.Builder
	if analysis.Summary != "" {
		fmt.Fprintf(&sb, "- Summary: %s\n", analysis.Summary)
	}
	for _, c := range analysis.SuccessCrit {
		fmt.Fprintf(&sb, "- Success criterion: %s\n", c)
	}
	for _, r := range analysis.Risks {
		fmt.Fprintf(&sb, "- Risk: %s\n", r)
	}
	vars["analysis"] = sb.String()

	text, err := a.ask(ctx, a.cfg.Prompts.Plan, vars, "plan")
	if err != nil {
		a.log.Error("planning phase failed", "error", err)
		return Plan{}
	}
	var plan Plan
	if err := llm.DecodeJSON(text, &plan); err != nil {
		a.log.Error("the plan has an unexpected format", "error", err, "response", truncate(text, 300))
		return Plan{}
	}
	a.log.Info("plan generated", "steps", len(plan.Steps), "subtasks", len(plan.Subtasks))
	return plan
}

func (a *Agent) actionPhase(ctx context.Context, t task.Task, plan Plan, failures []string, attempt int, prefix string) (Action, error) {
	vars := a.baseVariables(t)
	vars["plan"] = describePlan(plan)
	vars["history"] = template.History(failures)
	vars["attempt"] = fmt.Sprint(attempt)
	vars["max_attempts"] = fmt.Sprint(a.cfg.Agent.MaxRetries + 1)

	text, err := a.ask(ctx, a.cfg.Prompts.Execute, vars, "execute")
	if err != nil {
		return Action{}, err
	}
	var action Action
	if err := llm.DecodeJSON(text, &action); err != nil {
		return Action{}, err
	}
	if len(action.Actions) == 0 {
		return Action{}, errors.New("the LLM proposed no action")
	}
	a.log.Info(prefix+"action proposed", "reasoning", truncate(action.Reasoning, 200), "actions", len(action.Actions))
	return action, nil
}

// ask renders the prompt and calls the LLM, logging any variables that were
// missing (unfilled gaps in a user template).
func (a *Agent) ask(ctx context.Context, p config.Template, vars map[string]string, phase string) (string, error) {
	system, missingS := template.Render(p.System, vars)
	user, missingU := template.Render(p.User, vars)
	if len(missingS)+len(missingU) > 0 {
		missing := append(append([]string{}, missingS...), missingU...)
		a.log.Warn("the template has variables with no value", "phase", phase, "variables", strings.Join(missing, ","))
	}

	messages := []llm.Message{}
	if strings.TrimSpace(system) != "" {
		messages = append(messages, llm.Message{Role: "system", Content: system})
	}
	messages = append(messages, llm.Message{Role: "user", Content: user})

	start := time.Now()
	text, err := a.engine.Complete(ctx, messages)
	if err != nil {
		return "", err
	}
	a.log.Debug("LLM response", "phase", phase, "ms", time.Since(start).Milliseconds(), "bytes", len(text))
	return text, nil
}

// baseVariables are the variables available in every template.
func (a *Agent) baseVariables(t task.Task) map[string]string {
	vars := map[string]string{
		"task":         t.Description,
		"workspace":    a.cfg.Agent.WorkspaceDir,
		"origin":       t.Origin,
		"attempt":      "1",
		"max_attempts": fmt.Sprint(a.cfg.Agent.MaxRetries + 1),
		"rules":        a.describeRules(),
		"analysis":     "",
		"plan":         "",
		"history":      "",
		"model":        a.cfg.LLM.Model,
		"provider":     a.cfg.LLM.Provider,
	}
	for k, v := range t.Context {
		vars["context_"+k] = v
	}
	return vars
}

// describeRules turns the anchor into text for the prompt: the LLM must know
// exactly what it will be measured against.
func (a *Agent) describeRules() string {
	switch strings.ToLower(a.cfg.Anchor.Kind) {
	case "command":
		var sb strings.Builder
		fmt.Fprintf(&sb, "- It will run: %s %s\n", a.cfg.Anchor.Command, strings.Join(a.cfg.Anchor.Args, " "))
		fmt.Fprintf(&sb, "- It must finish with exit code %d.\n", a.cfg.Anchor.ExpectExit)
		if a.cfg.Anchor.ExpectOutput != "" {
			fmt.Fprintf(&sb, "- Its output must match the regular expression: %s\n", a.cfg.Anchor.ExpectOutput)
		}
		for _, c := range a.cfg.Anchor.Checks {
			fmt.Fprintf(&sb, "- In addition: %s %s (expected exit code %d)\n", c.Command, strings.Join(c.Args, " "), c.ExpectExit)
		}
		return sb.String()
	default:
		return "- No deterministic validation is configured (anchor.kind=none). The agent will not declare PASS on its own: review the configuration."
	}
}

func describePlan(p Plan) string {
	if len(p.Steps) == 0 {
		return "(no plan)"
	}
	var sb strings.Builder
	for _, step := range p.Steps {
		fmt.Fprintf(&sb, "%d. %s", step.Number, step.Action)
		if step.Command != "" {
			fmt.Fprintf(&sb, "  ->  %s", step.Command)
		}
		sb.WriteString("\n")
	}
	if p.ExpectedResult != "" {
		fmt.Fprintf(&sb, "Expected result: %s\n", p.ExpectedResult)
	}
	return sb.String()
}

// --- Execution --------------------------------------------------------------

// runActions runs every action in the sandbox and returns their combined output.
func (a *Agent) runActions(ctx context.Context, actions []Command, prefix string) (string, error) {
	var sb strings.Builder
	var lastErr error

	for i, action := range actions {
		if strings.TrimSpace(action.Command) == "" {
			a.log.Debug(prefix+"action with no command (descriptive)", "description", action.Description)
			continue
		}
		a.log.Info(prefix+"running in sandbox", "n", i+1, "command", action.Command, "description", truncate(action.Description, 120))

		// How the action is executed depends on the mode, and it is the difference
		// between a request and a guarantee:
		//
		//  - read-only (plan mode): the line is split into program and arguments, the
		//    policy checks the program, and NO shell runs. Without a shell there is no
		//    `>`, `>>`, `;`, `&&` or `$(...)`: redirection cannot happen because
		//    nothing interprets it.
		//  - otherwise: the line goes to the shell, because that is what lets the
		//    model use pipes and redirections to do real work.
		request, refused := a.buildRequest(action.Command)
		if refused != "" {
			fmt.Fprintf(&sb, "$ %s\n[refused: %s]\n", action.Command, refused)
			a.log.Warn(prefix+"action refused in read-only mode",
				"command", action.Command, "reason", refused)
			lastErr = fmt.Errorf("action %d (%s) was refused: %s", i+1, action.Command, refused)
			continue
		}

		output, truncated, exit, err := a.exec(ctx, request)

		fmt.Fprintf(&sb, "$ %s\n", action.Command)
		if output != "" {
			sb.WriteString(output)
			if !strings.HasSuffix(output, "\n") {
				sb.WriteString("\n")
			}
		}
		if truncated {
			sb.WriteString("[output truncated by the sandbox limit]\n")
		}
		fmt.Fprintf(&sb, "[exit=%d]\n", exit)

		if err != nil {
			lastErr = fmt.Errorf("action %d (%s) could not run: %w", i+1, action.Command, err)
		}
	}
	return sb.String(), lastErr
}

// summariseFailure builds the block handed to the LLM on the next attempt.
func (a *Agent) summariseFailure(action Action, execution string, validation anchor.Result, runErr error) string {
	var sb strings.Builder
	sb.WriteString("Proposed actions:\n")
	for _, c := range action.Actions {
		fmt.Fprintf(&sb, "- %s\n", c.Command)
	}
	if execution != "" {
		sb.WriteString("\nOutput of the execution in the sandbox:\n")
		sb.WriteString(truncate(execution, 3000))
		sb.WriteString("\n")
	}
	if runErr != nil {
		fmt.Fprintf(&sb, "\nExecution error: %s\n", runErr)
	}
	sb.WriteString("\nResult of the deterministic validation (JSON):\n")
	sb.WriteString(validation.JSON())
	sb.WriteString("\n")
	return sb.String()
}

// --- Final action and escalation --------------------------------------------

// runFinalAction runs the action planned for after a PASS. It returns a readable
// description and an error when the action could not be completed (including a
// non-zero exit code, which is a failure even with no execution error).
func (a *Agent) runFinalAction(ctx context.Context, c Command, prefix string) (string, error) {
	final := a.cfg.FinalAction

	switch strings.ToLower(final.Kind) {
	case "", "none":
		return "none", nil

	case "command":
		command := final.Command
		args := final.Args
		if strings.TrimSpace(c.Command) != "" {
			// The LLM may propose the concrete final command.
			command, args = "/bin/sh", []string{"-c", c.Command}
		}
		if strings.TrimSpace(command) == "" {
			a.log.Warn(prefix + "final_action.kind=command with no command: nothing is done")
			return "none", nil
		}
		a.log.Info(prefix+"running the final action", "command", command)
		output, _, exit, err := a.exec(ctx, execx.Request{Command: command, Args: args, Timeout: a.cfg.Sandbox.Timeout})
		description := fmt.Sprintf("command exit=%d output=%s", exit, truncate(output, 300))
		if err != nil {
			a.log.Error(prefix+"the final action failed", "error", err, "output", truncate(output, 500))
			return description, fmt.Errorf("the final action could not run: %w", err)
		}
		if exit != 0 {
			return description, fmt.Errorf("the final action finished with exit code %d", exit)
		}
		return description, nil

	case "api":
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

	case "git_commit":
		message, _ := template.Render(final.CommitMessage, map[string]string{"task": c.Description})
		if strings.TrimSpace(message) == "" {
			message = "agent: validated changes"
		}
		sequence := []string{
			"git add -A",
			fmt.Sprintf("git commit -m %s", shellQuote(message)),
		}
		var outputs []string
		for _, cmd := range sequence {
			output, _, exit, err := a.exec(ctx, execx.Request{
				Command: "/bin/sh", Args: []string{"-c", cmd}, Timeout: a.cfg.Sandbox.Timeout,
			})
			outputs = append(outputs, fmt.Sprintf("%s -> exit=%d %s", cmd, exit, truncate(output, 200)))
			if err != nil {
				a.log.Error(prefix+"git_commit failed", "command", cmd, "error", err)
				return "git_commit: " + strings.Join(outputs, " | "), fmt.Errorf("git_commit: %s failed: %w", cmd, err)
			}
			if exit != 0 {
				return "git_commit: " + strings.Join(outputs, " | "), fmt.Errorf("git_commit: %s finished with exit code %d", cmd, exit)
			}
		}
		return "git_commit: " + strings.Join(outputs, " | "), nil

	default:
		return "none", fmt.Errorf("unknown final_action.kind: %q", final.Kind)
	}
}

// escalate fires the configured escalation action when the task does not pass.
func (a *Agent) escalate(ctx context.Context, prefix string) {
	if a.cfg.Agent.OnFailure.Kind != "command" || strings.TrimSpace(a.cfg.Agent.OnFailure.Command) == "" {
		return
	}
	a.log.Warn(prefix+"escalating after exhausting the attempts", "command", a.cfg.Agent.OnFailure.Command)
	output, _, exit, err := a.exec(ctx, execx.Request{
		Command: "/bin/sh",
		Args:    []string{"-c", a.cfg.Agent.OnFailure.Command},
		Timeout: a.cfg.Sandbox.Timeout,
	})
	if err != nil {
		a.log.Error(prefix+"the escalation action failed", "error", err)
		return
	}
	a.log.Info(prefix+"escalation executed", "exit", exit, "output", truncate(output, 300))
}

// shellQuote quotes text for passing to sh -c.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func modesToStrings(modes []sandbox.Mode) []string {
	out := make([]string, 0, len(modes))
	for _, m := range modes {
		out = append(out, string(m))
	}
	return out
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}
