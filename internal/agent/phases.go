package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/madkoding/motita/internal/anchor"
	"github.com/madkoding/motita/internal/config"
	"github.com/madkoding/motita/internal/llm"
	"github.com/madkoding/motita/internal/task"
	"github.com/madkoding/motita/internal/template"
)

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
		// A malformed analysis is OUR problem, not the user's: the model failed to follow the
		// format, and reporting the parse error to the user asks them to fix something they did
		// not do. The honest answer is that the request could not be read, with a question that
		// lets them proceed.
		a.log.Error("the analysis has an unexpected format", "error", err, "response", truncate(text, 300))
		return Analysis{
			Understandable: false,
			Risks:          []string{"the analysis could not be parsed: " + err.Error()},
			Question:       "I could not read the request. Could you tell me, in one sentence, what you want done and to what?",
		}
	}
	if analysis.Summary == "" {
		analysis.Summary = truncate(t.Description, 200)
	}
	a.log.Info("analysis completed", "summary", truncate(analysis.Summary, 160), "criteria", len(analysis.SuccessCrit))
	return analysis
}

func (a *Agent) planPhase(ctx context.Context, t task.Task, analysis Analysis, depth int) Plan {
	vars := a.baseVariables(t)
	vars["analysis"] = describeAnalysis(analysis)

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

// actionPhase asks for the next round's actions.
//
// The analysis travels with the plan. Without it the execute phase saw only the TASK, which on a
// turn that answers a question is the answer itself - "yes, do it" - and the success criteria the
// work is judged by were never in front of the phase that does the work.
//
// An empty list of actions is NOT an error here: "done": true with nothing left to run is how a
// finished task is reported, and it used to fail the whole task with "the LLM proposed no
// action". The loop decides what an empty list means, because only the loop knows "done".
func (r *taskRun) actionPhase() (Action, error) {
	a := r.a
	vars := a.baseVariables(r.t)
	vars["analysis"] = describeAnalysis(r.analysis)
	vars["plan"] = describePlan(r.plan)
	vars["history"] = renderRounds(r.journal) + r.mem.render()
	vars["attempt"] = fmt.Sprint(r.round)
	vars["max_attempts"] = fmt.Sprint(a.cfg.Agent.MaxRetries + 1)

	text, err := a.ask(llm.WithJSONSchema(r.ctx, actionSchema), a.cfg.Prompts.Execute, vars, "execute")
	if err != nil {
		return Action{}, err
	}
	action, err := decodeAction(text)
	if err != nil {
		return Action{}, err
	}
	a.log.Info(r.prefix+"action proposed", "reasoning", truncate(action.Reasoning, 200),
		"actions", len(action.Actions), "done", action.isDone())
	return action, nil
}

// describeAnalysis renders the analysis for the phases after it.
func describeAnalysis(analysis Analysis) string {
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
	return sb.String()
}

// synthesizePhase asks the LLM for a concise, evidence-based answer after the
// actions have run and the anchor has validated them.
func (a *Agent) synthesizePhase(ctx context.Context, t task.Task, output string, validation anchor.Result) *Report {
	vars := a.baseVariables(t)
	// The output of every round, already cut in its middle (workLog.text): the start says what
	// was found, the end says where the work landed.
	vars["output"] = output
	vars["validation"] = validation.Reason

	text, err := a.ask(ctx, a.cfg.Prompts.Synthesize, vars, "synthesize")
	if err != nil {
		a.log.Warn("synthesis phase failed", "error", err)
		return nil
	}
	var rep Report
	if err := llm.DecodeJSON(text, &rep); err != nil {
		a.log.Warn("synthesis response has an unexpected format", "error", err, "response", truncate(text, 300))
		return nil
	}
	rep.normalize(validation.Pass)
	if a.cfg.Agent.AutoModeAskAllSuggestions {
		rep.askAllSuggestions()
	}
	// Not on the turn that applies suggestions: its own are asked about, so this cannot chain.
	rep.AutoApply = a.cfg.Agent.AutoApplySuggestions && len(rep.Suggestions) > 0 &&
		!IsApplyingTurn(t.Description)
	if rep.Summary == "" {
		return nil
	}
	return &rep
}

// ask renders the prompt and calls the LLM, logging any variables that were
// missing (unfilled gaps in a user template).
func (a *Agent) ask(ctx context.Context, p config.Template, vars map[string]string, phase string) (string, error) {
	// Every call this agent makes is counted on its row of the fleet.
	if a.member != nil {
		ctx = llm.WithMeter(ctx, a.member.meter)
	}
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
	// A picture of the desktop the last round took goes to the round that reads its result.
	var shots []llm.Image
	if phase == "execute" {
		shots = a.takeShot()
	}
	// The pictures the person attached are shown to every round that carries their message.
	shots = append(shots, llm.AttachedImages(a.cfg.Agent.WorkspaceDir, user)...)
	messages = append(messages, llm.Message{Role: "user", Content: user, Images: shots})

	start := time.Now()
	var text string
	var err error
	engine := a.phaseEngine(phase)
	if a.streamThoughts && a.Progress != nil {
		live := &liveView{a: a, phase: phase}
		text, err = engine.CompleteStream(ctx, messages, live.add)
	} else {
		text, err = engine.Complete(ctx, messages)
	}
	if errors.Is(err, llm.ErrToolCallForText) && ctx.Err() == nil {
		// Reported from real use: "I ask it for things and the agent does nothing". The model
		// answered a phase that wants a JSON TEXT with a tool call (search_skills, ...); every
		// round asked the same way and got the same call, so each subtask died on round 1
		// with no work done. One retry that SAYS what went wrong is what makes it answerable
		// - the tools it reached for are still available as actions inside the JSON.
		a.log.Warn("the model answered with a tool call; asking once more for text", "phase", phase)
		retry := append(append([]llm.Message{}, messages...), llm.Message{Role: "user", Content: "Your last reply was a " +
			"tool call, and this step cannot run tool calls. Reply with ONLY the JSON object the instructions " +
			"describe. If you wanted to use a tool, put it in the \"actions\" list of that JSON (kind and command)."})
		text, err = engine.Complete(ctx, retry)
	}
	if err != nil {
		return "", err
	}
	// Info, not Debug: where a turn's wait goes (which phase, how big a prompt) is what a slow run
	// is diagnosed from, and it has to be in the log of a run nobody was watching with debug on.
	a.log.Info("LLM phase", "phase", phase, "model", engine.Model(), "ms", time.Since(start).Milliseconds(),
		"prompt_bytes", len(system)+len(user), "reply_bytes", len(text))
	return text, nil
}

// phaseEngine is the engine a phase runs on. The criteria audit and the synthesis only READ what
// the rounds did and write a short verdict, so they run on the summary model when one is
// assigned: the same answer at a fraction of the cost and the wait. The phases that decide what
// to do (analysis, plan, every round) keep the agent's own engine. Without a summary model, or
// outside a session that hands engines out, every phase runs on the agent's own.
func (a *Agent) phaseEngine(phase string) *llm.Client {
	if phase == "audit" || phase == "synthesize" {
		return a.engineOf(config.TaskSummary, "")
	}
	return a.engine
}

// baseVariables are the variables available in every template.
func (a *Agent) baseVariables(t task.Task) map[string]string {
	vars := map[string]string{
		"task":         t.Description,
		"workspace":    a.cfg.Agent.WorkspaceDir,
		"project":      a.projectNote + memoryContext(a.cfg.Agent.WorkspaceDir),
		"origin":       t.Origin,
		"attempt":      "1",
		"max_attempts": fmt.Sprint(a.cfg.Agent.MaxRetries + 1),
		"rules":        a.describeRules(),
		"analysis":     "",
		"plan":         "",
		"history":      a.dialogue(),
		"model":        a.cfg.LLM.Model,
		"provider":     a.cfg.LLM.Provider,
		"tools":        describeTools(a.cfg.Sandbox, a.sandbox.Home()) + describeShell(shellFor(a.cfg)),
		"desktop":      a.desktopDocs(),
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
		return a.describeCommandRules()
	case "auto":
		return a.describeAutoRules()
	default:
		return "- No deterministic validation is configured (anchor.kind=none). " +
			"The agent will not declare PASS on its own: review the configuration."
	}
}

// describeCommandRules names the command, and the checks, that kind=command runs.
func (a *Agent) describeCommandRules() string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "- It will run: %s %s\n", a.cfg.Anchor.Command, strings.Join(a.cfg.Anchor.Args, " "))
	fmt.Fprintf(&sb, "- It must finish with exit code %d.\n", a.cfg.Anchor.ExpectExit)
	if a.cfg.Anchor.ExpectOutput != "" {
		fmt.Fprintf(&sb, "- Its output must match the regular expression: %s\n", a.cfg.Anchor.ExpectOutput)
	}
	for _, c := range a.cfg.Anchor.Checks {
		fmt.Fprintf(&sb, "- In addition: %s %s (expected exit code %d)\n",
			c.Command, strings.Join(c.Args, " "), c.ExpectExit)
	}
	return sb.String()
}

// describeAutoRules names the gate the PROJECT declares, which is what kind=auto runs. The model
// is told the exact commands, because the difference between a run that finishes and one that
// spends its attempts is whether it ran those same commands itself before claiming done.
func (a *Agent) describeAutoRules() string {
	checks := anchor.New(a.cfg.Anchor, a.cfg.Agent.WorkspaceDir, nil).Planned()
	var sb strings.Builder
	if len(checks) == 0 {
		sb.WriteString("- This project declares NO gate yet (looked for .motita/anchor, a Makefile with check or test, " +
			"go.mod, package.json lint/typecheck/test, Cargo.toml, pyproject.toml), so a claim of done will be REFUSED.\n" +
			"- Declare it: find how the project checks itself (README, CI workflow, package.json scripts, Makefile) " +
			"and write those commands, one per line, in .motita/anchor. Run them yourself before claiming done.\n" +
			"- A gate declared during the run is put to the user before its PASS is accepted.\n")
		return sb.String()
	}
	sb.WriteString("- The project's own gate runs after you claim done, in the project directory. " +
		"Each of these must pass:\n")
	for _, c := range checks {
		fmt.Fprintf(&sb, "  - %s %s (must exit %d)\n", c.Command, strings.Join(c.Args, " "), c.ExpectExit)
	}
	sb.WriteString("- Run these same commands yourself BEFORE claiming done and fix what they report: a claim the gate " +
		"refuses costs one of your limited attempts, and running them first costs nothing.\n" +
		"- Do not change the gate itself (.motita/anchor, the Makefile, package.json scripts): a PASS from a changed " +
		"gate is not accepted without the user's approval.\n")
	return sb.String()
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
