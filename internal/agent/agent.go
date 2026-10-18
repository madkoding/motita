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
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/madkoding/motita/internal/anchor"
	"github.com/madkoding/motita/internal/config"
	"github.com/madkoding/motita/internal/execx"
	"github.com/madkoding/motita/internal/llm"
	"github.com/madkoding/motita/internal/logx"
	"github.com/madkoding/motita/internal/policy"
	"github.com/madkoding/motita/internal/redact"
	"github.com/madkoding/motita/internal/reward"
	"github.com/madkoding/motita/internal/sandbox"
	"github.com/madkoding/motita/internal/skills"
	"github.com/madkoding/motita/internal/task"
	"github.com/madkoding/motita/internal/usage"
)

// Literals used more than once in this file.
const (
	gitCommitPrefix = "git_commit: "
)

// Agent orchestrates the three layers.
type Agent struct {
	cfg     config.Config
	log     *logx.Logger
	engine  *llm.Client
	sandbox *sandbox.Sandbox
	source  task.Source
	http    *http.Client

	// Progress, if set, receives human-readable updates such as "analysing..." or
	// "running: git status". It is used by the TUI to keep the user informed.
	Progress func(format string, args ...any)

	// ExecCommand is the agent's command execution point. By default it uses the
	// sandbox (Layer C); it is injected so actions that depend on external
	// programs (git_commit, for instance) can be tested without depending on the
	// program existing, on its configuration or on its interactive behaviour.
	ExecCommand func(context.Context, execx.Request) (string, bool, int, error)

	// Interactive reports whether there is a user who can answer a question.
	//
	// It decides what to do when the request cannot be read well enough to act on. With a user on
	// the other end, the agent ASKS: the request is incomplete rather than impossible, and a
	// three-word answer unblocks it. Without one — a batch run, a cron job, a piped task — there is
	// nobody to ask, so asking would only stall: the agent proceeds on the assumption it stated.
	//
	// Both are better than refusing, which is what the agent used to do in either case.
	Interactive bool
	// steer is what the person says while a task runs: see steer.go.
	steer steering

	// library is the procedure library, when one is configured. It is the SAME library Plan
	// mode uses: one shelf of procedures, reachable from both modes, because a procedure is
	// not specific to how the work is executed.
	//
	// Task mode reaches it through the action protocol rather than tool calls — its actions are
	// JSON — so the four operations are named by the action's "kind" instead of by a tool name.
	library *skills.Library

	// reward is the long-term value per skill, and consulted counts what this run read.
	//
	// Same pair as the planner's, and for the same reason: a verdict has to land on specific
	// skills, and the moment that is knowable is the read.
	reward    *reward.Ledger
	usage     *usage.Ledger
	consulted map[string]int

	// transcript is what has been said in this conversation, oldest first, and it is what makes
	// the agent conversational rather than stateless.
	//
	// Without it every message is the first message: "yes" or "the second one" has nothing to
	// refer to, so the agent can only re-ask what it already asked, and the user has to write
	// their request out again in full. The dialogue IS the context — each answer narrows down
	// what they want — and dropping it throws away the progress the user just made.
	//
	// It is guarded because a turn appends to it from the goroutine running the task while the
	// interface reads it to render the count.
	transcriptMu sync.Mutex
	transcript   []DialogueTurn

	// Observer, when not nil, receives the result of every task on completion.
	// It is the integration point for metrics or for whoever embeds the agent,
	// and it is also what the tests use to inspect the verdict without reading
	// the log.
	Observer func(TaskResult)

	// streamThoughts shows the model's reasoning WHILE it is being written, as LivePrefix
	// progress lines. Off by default: it changes the request to a streamed one, and only an
	// interface that renders the snapshots should pay for them. See SetLiveThinking.
	streamThoughts bool

	// approver asks the user before a consequential command runs, and nil means there is
	// nobody to ask — which is a real state, not a missing dependency: a task piped in from
	// a script has no user, and a command that needs approval in that situation is refused
	// rather than run on the user's behalf. See Approver.
	approver Approver

	// fleet is the run's agents and member is this agent's row in it; subs is what the main agent
	// started with spawn_agent. isChild marks a background agent (it cannot start agents), and
	// research one that is read-only because there was no worktree to give it. See subagents.go.
	fleet    *Fleet
	member   *fleetMember
	subs     *subagents
	isChild  bool
	research bool
	// roleEngines is the engine a background agent of a role runs on, handed by the session that
	// runs this agent; nil (or a nil answer) means this agent's own engine. See SetRoleEngines.
	roleEngines func(role string) *llm.Client
	// tierEngines is the engine of a tier (cheap or strong), nil when the tier has no model
	// assigned; classify says how hard a piece of work is. See SetTiers.
	tierEngines func(tier string) *llm.Client
	classify    func(ctx context.Context, purpose, brief string) string

	// projectNote is what the session tells the agent about where it works: the project it
	// belongs to, its root, how it is run and its other repositories. "" for a session that
	// belongs to none. It reaches the prompts as {{project}} and is inherited by background
	// agents, so the whole team works inside the same project. See SetProjectNote.
	projectNote string

	// gate is the fingerprint of the project's gate (anchor kind auto) as the task found it; a
	// background agent inherits its main agent's. See guardGate.
	gate string
}

// SetObserver allows external callers (such as the TUI) to register a callback
// that receives the task result when it is produced. It is the same hook as the
// Observer field; the method exists so interfaces that wrap *agent.Agent can
// expose the hook without exporting the field itself.
func (a *Agent) SetObserver(fn func(TaskResult)) {
	a.Observer = fn
}

// SetRoleEngines installs how a background agent's engine is chosen from its role. The front end
// that owns the session hands it, so the agents of a run only ever use that session's engines.
func (a *Agent) SetRoleEngines(fn func(role string) *llm.Client) { a.roleEngines = fn }

// SetTiers installs the cheap/strong routing of background agents: engine returns the engine of a
// tier (nil when none is assigned), classify answers TierCheap or TierStrong for a piece of work.
func (a *Agent) SetTiers(engine func(tier string) *llm.Client,
	classify func(ctx context.Context, purpose, brief string) string) {
	a.tierEngines, a.classify = engine, classify
}

// SetLibrary installs the procedure library.
//
// It is the same library the planner receives, and sharing it is the point: a procedure
// written down in one mode is available in the other, which is what makes the library a single
// body of knowledge rather than two that drift apart.
func (a *Agent) SetLibrary(lib *skills.Library) { a.library = lib }

// SetReward installs the long-term value ledger.
func (a *Agent) SetReward(l *reward.Ledger) { a.reward = l }

// SetUsage installs the per-skill telemetry sidecar, so the library's "used N×" counts what
// this run searched and read.
func (a *Agent) SetUsage(u *usage.Ledger) { a.usage = u }

// saveUsage writes the counts a run accumulated, once, when it ends: a plain task run has no
// review fork to persist them, and a write per read would be a write per tool call.
func (a *Agent) saveUsage() {
	if a.usage != nil {
		_ = a.usage.Save()
	}
}

// Consulted returns the skills this run read, and how many times each.
//
// It is what the caller needs to turn the user's verdict into value: the skills are known here
// and nowhere else.
func (a *Agent) Consulted() map[string]int {
	if len(a.consulted) == 0 {
		return nil
	}
	out := make(map[string]int, len(a.consulted))
	for k, v := range a.consulted {
		out[k] = v
	}
	return out
}

// consult records that a skill was read.
func (a *Agent) consult(name string) {
	if a.consulted == nil {
		a.consulted = map[string]int{}
	}
	a.consulted[name]++
}

// SetProjectNote sets what the agent is told about the project it works in (see projectNote).
func (a *Agent) SetProjectNote(note string) { a.projectNote = strings.TrimSpace(note) }

// SetLiveThinking turns on the live view of the model's reasoning: while a phase is being
// generated, snapshots of what the model has written so far are reported as progress lines
// starting with LivePrefix, at most one per liveInterval.
//
// Reported from real use: "I still cannot see what the model decides, reasons or thinks while
// I wait - only states". The phases took tens of seconds each and all the interface could say
// was "deciding action...".
func (a *Agent) SetLiveThinking(on bool) { a.streamThoughts = on }

// LivePrefix marks a progress line that is a SNAPSHOT of the reasoning being written right
// now. Each one replaces the one before it, so an interface shows the latest and a log keeps
// none: the phase's final reasoning is reported again, whole, as a ThinkingPrefix line.
const LivePrefix = "live: "

// AnswerPrefix marks a SNAPSHOT of the answer being written right now, whole so far. Like a
// LivePrefix line each one replaces the one before, so an interface shows it growing where the
// answer will be and a log keeps none: the answer itself arrives whole when the run ends.
const AnswerPrefix = "live answer: "

// ThinkingPrefix marks the finished reasoning of a phase: what the model understood, planned
// or decided, in its own words.
const ThinkingPrefix = "thinking: "

// SetProgress registers the callback that receives the human-readable phase
// lines ("running: ls", "validating…"). It is the same hook as the Progress
// field, behind a method so an embedder can install it through an interface
// without reaching into the struct.
func (a *Agent) SetProgress(fn func(format string, args ...any)) {
	a.Progress = fn
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
//
// Every command's output reaches the model provider's prompt and the saved
// transcript from here, so this is the one place where the secrets it printed (a
// token in `git remote -v`, a key in `cat .env`) are masked.
func (a *Agent) exec(ctx context.Context, p execx.Request) (string, bool, int, error) {
	if a.ExecCommand == nil {
		return "", false, -1, errors.New("the agent has no command executor configured")
	}
	output, truncated, exit, err := a.ExecCommand(ctx, p)
	return redact.String(output), truncated, exit, err
}

// RunCommand executes a single command line under the agent's policy and sandbox.
// It is the entry point used by interactive modes (plan/chat) where the model
// requests a tool call instead of emitting JSON.
func (a *Agent) RunCommand(ctx context.Context, command string) (string, int, error) {
	p := a.planRequest(command)
	if p.Verdict == policy.Deny {
		return fmt.Sprintf("[refused: %s]\n", p.Reason), 1, fmt.Errorf("command was refused: %s", p.Reason)
	}
	if p.Verdict == policy.Ask {
		approved, err := a.approve(ctx, p, command)
		if err != nil {
			return fmt.Sprintf("[not approved: %s]\n", err), 1,
				fmt.Errorf("command needs approval and it could not be obtained: %w", err)
		}
		if !approved {
			return "[not approved: the user declined]\n", 1,
				fmt.Errorf("the user declined to run: %s", command)
		}
		// A person said yes to this line by name, including to where it writes.
		approveRequest(&p.Request, command)
	}
	output, _, exit, err := a.exec(ctx, p.Request)
	return output, exit, err
}

// --- Result of each phase ---------------------------------------------------

// Analysis is the structured output of phase [3].
type Analysis struct {
	// Kind decides what the agent does with the message, and it is resolved BEFORE anything
	// else. It is the difference between a conversational agent and a task runner:
	//
	//	"task" — there is something to do; analyse it and run the loop.
	//	"chat" — there is nothing to do (a greeting, a question about the conversation, an
	//	         observation). Answer in Reply and stop, without inventing work.
	//	"ask"  — the request cannot be read well enough to act on and guessing risks the
	//	         wrong thing. Ask in Question.
	//
	// Without this an agent has only one response to every message, which is to plan work:
	// "hello" produces a task plan, and a question about what it just did produces another
	// task plan. The user asked to be talked to.
	//
	// An empty Kind means the model did not answer, and is treated as "task": that is the
	// behaviour of every prompt that predates this field, so an existing configuration keeps
	// working unchanged.
	Kind string `json:"kind"`

	Understandable bool     `json:"understandable"`
	Summary        string   `json:"summary"`
	SuccessCrit    []string `json:"success_criteria"`
	Risks          []string `json:"risks"`
	NeedsSubtasks  bool     `json:"needs_subtasks"`

	// Question is what to ask the user when the request cannot be understood well enough to act
	// on. It is the difference between an agent that stops and one that asks.
	//
	// Users mistype, abbreviate, and leave out what they consider obvious. A request the model
	// can only half interpret is not a failure: it is incomplete information, and the interface
	// has a user on the other end who can complete it. Refusing outright — which is what this
	// used to do — throws away the turn and makes the user rephrase from scratch.
	Question string `json:"question"`
	// Assumption is what the agent WOULD do if it had to proceed without an answer. Asking with
	// a stated assumption lets the user confirm in one word instead of writing a new request, and
	// it gives the agent a defensible reading if nobody answers.
	Assumption string `json:"assumption"`

	// Decision marks a question the agent must not answer for the user: the work would be
	// destructive or hard to undo depending on the answer, or the readings lead to materially
	// different results. Any other question with an assumption is assumed and the work goes on
	// (see agent.assume_first); the assumption is stated in the report so it can be corrected.
	Decision bool `json:"decision"`

	// Questions are the questions to ask, in order, when more than one thing is unclear.
	//
	// The single Question/Comment pair above covers the common case; this covers the case where
	// the request has several independent gaps. Asking them one turn at a time costs a round
	// trip each and makes the user re-explain the request three times; asking them together lets
	// the user move between them and confirm once.
	//
	// When Questions is empty the interface falls back to the single Question, so a prompt that
	// does not produce a list keeps working exactly as before.
	Questions []AskItem `json:"questions"`

	// Options are concrete answers the user can pick from instead of typing.
	//
	// A question is easier to answer when the plausible answers are already on screen: "which
	// folder?" with [the current one, /tmp, tell me] is one keypress, and the user can still
	// write something else. Without them the interface can only echo the question and leave the
	// user to compose a reply from nothing.
	//
	// They are only meaningful with Question, and an EMPTY list is the normal case: a question
	// whose answer is genuinely open ("what are you trying to achieve?") has nothing to offer, and
	// inventing options there would push the user toward answers they did not mean.
	Options []string `json:"options"`

	// Reply is the conversational answer, used when Kind is "chat".
	//
	// It is prose for a person, in their language, and it is NOT a task summary: it answers
	// what they said. Keeping it a separate field is what lets the interface show a reply
	// without pretending a task ran.
	Reply string `json:"reply"`
}

// AskItem is one question put to the user, with the answers they could pick.
//
// It is what the interface needs to draw a navigable list: the question, what the agent will
// assume if the user says nothing, and the concrete answers it is choosing between.
type AskItem struct {
	// Text is the question, in the user's language.
	Text string `json:"text"`
	// Assumption is what the agent will do if this one is left unanswered. It is shown next to
	// the question so the user can accept it without typing anything.
	Assumption string `json:"assumption,omitempty"`
	// Options are the concrete answers to offer, in the user's words. Empty is normal: it means
	// the question has no short list of plausible answers.
	Options []string `json:"options,omitempty"`
	// Proposal marks an item that offers a change the agent found but did not make, rather than
	// a question about the request. Its options are the fixed ProposalOptions, and answering one
	// can settle the items after it (see ProposalOptions).
	Proposal bool `json:"proposal,omitempty"`
	// Detail is what a proposal would change and why, shown under its title.
	Detail string `json:"detail,omitempty"`
	// Needed marks a proposal auto mode applies without asking (see ReportSuggestion.Needed), and
	// Why says what makes it needed.
	Needed bool   `json:"needed,omitempty"`
	Why    string `json:"why,omitempty"`
	// AutoApply marks a proposal the configuration applies without asking, in any mode
	// (agent.auto_apply_suggestions), whether or not it is needed.
	AutoApply bool `json:"auto_apply,omitempty"`
}

// Kinds of message the analysis can report.
const (
	// KindTask means there is work to do.
	KindTask = "task"
	// KindChat means there is nothing to do: answer and stop.
	KindChat = "chat"
	// KindAsk means the agent cannot tell what to do and must ask.
	KindAsk = "ask"
)

// resolveKind returns the kind with the default applied.
//
// An unrecognised or empty kind is "task", which is what every prompt written before this
// field existed meant. That default is deliberate: a misconfigured or older prompt must keep
// the previous behaviour rather than silently turn every request into a chat.
func (a Analysis) resolveKind() string {
	switch strings.ToLower(strings.TrimSpace(a.Kind)) {
	case KindChat:
		return KindChat
	case KindAsk:
		return KindAsk
	default:
		return KindTask
	}
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

// Action is the structured output of phase [6].
type Action struct {
	Reasoning string    `json:"reasoning"`
	Actions   []Command `json:"actions"`
	Final     Command   `json:"final_action"`
	// Done is the model's own report that the task is FINISHED, and it is what turns the
	// loop from a retry loop into a progress loop.
	//
	// Without it the loop returned the moment the anchor was happy — and the anchor checks
	// the state of the PROJECT, not how much of the plan was carried out, so a healthy
	// repository passed before any work had happened. Measured: a plan of eleven steps, one
	// batch of four actions, "task completed" in twelve seconds.
	//
	// It defaults to TRUE when absent, which is what keeps every existing configuration
	// working: a prompt that predates this field does not send it, and reading that as "not
	// finished" would spend the whole step budget on a task that was already done.
	Done *bool `json:"done"`
	// Notes is the model's own running summary: what it has decided, what is done, what is
	// left. It is kept whole between rounds and replaced whenever a reply carries new ones.
	Notes string `json:"notes"`
}

// isDone reports whether the model considers the task finished.
//
// A nil pointer means the field was absent, and absent means finished: the behaviour of
// every prompt that predates it.
func (a Action) isDone() bool {
	return a.Done == nil || *a.Done
}

// Command is an executable action.
type Command struct {
	Kind        string `json:"kind"`
	Description string `json:"description"`
	Command     string `json:"command"`
	// Role is what a spawn_agent's agent is for: code (the default), review or research. It
	// picks the model the agent runs on (the models block) and whether it may write.
	Role string `json:"role,omitempty"`
	// Tier is how strong a model a spawn_agent's agent needs: cheap, strong, or empty to let the
	// run judge the piece. It only matters when the models block assigns the tier.
	Tier string `json:"tier,omitempty"`
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
	// Summary is a human-readable answer produced by the model and shown in the UI.
	Summary string `json:"summary,omitempty"`
	// Report is the same account as structured data, for a front end that lays it out itself.
	// It is nil when the model produced no summary; Summary is then empty too.
	Report *Report `json:"report,omitempty"`
	// NeedsInput is set when the agent could not interpret the request well enough to act and is
	// asking the user instead of guessing. Question carries what to ask and Assumption what it
	// would do without an answer.
	//
	// This is not a failure and must not be reported as one: nothing went wrong, information is
	// missing, and the user is the one holding it.
	NeedsInput bool   `json:"needs_input,omitempty"`
	Question   string `json:"question,omitempty"`
	Assumption string `json:"assumption,omitempty"`

	// Kind is what the message turned out to be: "task", "chat" or "ask". The interface uses it
	// to decide how to present the result — a reply is shown as a reply, not as a task that ran.
	Kind string `json:"kind,omitempty"`
	// Reply is the conversational answer when Kind is "chat". It is the whole output of that
	// turn: no task ran, and nothing was validated, because there was nothing to do.
	Reply string `json:"reply,omitempty"`

	// Options are the concrete answers offered with a Question, so the interface can show them
	// as a pickable list. Empty means the question has no short list of plausible answers, which
	// is normal and is not a failure.
	Options []string `json:"options,omitempty"`

	// Questions are the questions of a turn that needed several answered together. It is empty
	// when there is a single Question, so a caller can read Question alone and still work.
	Questions []AskItem `json:"questions,omitempty"`
}

// Answers pairs a question with what the user replied, and is how the interface hands a
// multi-question turn back to the agent.
//
// Kept as question-and-answer rather than a bare list of strings so the answers cannot drift out
// of step with the questions they belong to: the agent re-reads the request with the answers
// attached to the exact gaps they fill.
type Answers struct {
	Question string `json:"question"`
	Answer   string `json:"answer"`
}

// --- Main loop --------------------------------------------------------------

// report sends a progress line to the conversational UI when one is attached.
func (a *Agent) report(format string, args ...any) {
	if a.Progress != nil {
		a.Progress(format, args...)
	}
	// The main agent's latest line is its activity in the fleet. A background agent's Progress
	// already is exactly that, and nothing else.
	if a.member != nil && !a.isChild {
		a.noteActivity(fmt.Sprintf(format, args...))
	}
}

// Run processes tasks from the source until it is exhausted (io.EOF) or the
// context is cancelled (graceful shutdown).
func (a *Agent) Run(ctx context.Context) error {
	defer a.saveUsage()
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
			"   Check the configuration with: motita -config <file> -validate-config")
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

	var n runTally
	for {
		stop, err := a.runStep(ctx, &n)
		if stop == stopNow {
			return err
		}
		if stop == stopAndReport {
			break
		}
	}

	a.log.Info("agent finished", "tasks", n.total, "failed", n.failed, "awaiting_input", n.awaiting)
	if n.failed > 0 {
		// The reason of every failure is included: whoever reads the error (or
		// cron's output) must be able to act without digging through the log.
		return fmt.Errorf("%d of %d tasks failed: %s", n.failed, n.total, strings.Join(n.reasons, " | "))
	}
	return nil
}

// maxConsecutive is how many source failures in a row make Run give up.
const maxConsecutive = 5

// runTally is what Run counts across the tasks it takes.
type runTally struct {
	total  int
	failed int
	// awaiting counts the tasks that stopped to ask a question.
	awaiting int
	reasons  []string
	// Consecutive source failures are counted separately: a source that fails
	// forever (a directory that disappeared, an unreachable API) must not spin
	// the loop at full speed burning CPU and log. After a few in a row the agent
	// gives up with a clear error instead of hanging silently.
	consecutive    int
	sourceFailures int
}

// runStop is what one step of Run says about the steps after it.
type runStop int

const (
	// keepRunning takes the next task.
	keepRunning runStop = iota
	// stopAndReport ends the run with its summary.
	stopAndReport
	// stopNow ends the run at once, with the error given.
	stopNow
)

// runStep takes one task from the source and processes it.
func (a *Agent) runStep(ctx context.Context, n *runTally) (runStop, error) {
	select {
	case <-ctx.Done():
		a.log.Info("shutdown requested, finishing", "tasks_processed", n.total)
		return stopNow, nil
	default:
		// Not cancelled: go on.
	}

	if a.cfg.Agent.MaxTasks > 0 && n.total >= a.cfg.Agent.MaxTasks {
		a.log.Info("agent.max_tasks reached", "tasks", n.total)
		return stopAndReport, nil
	}

	t, err := a.source.Next(ctx)
	if errors.Is(err, io.EOF) {
		a.log.Info("no more tasks", "tasks_processed", n.total)
		return stopAndReport, nil
	}
	if err != nil {
		return a.sourceFailed(ctx, n, err)
	}
	n.consecutive = 0
	if strings.TrimSpace(t.Description) == "" {
		return keepRunning, nil
	}

	n.total++
	a.tally(n, t, a.processTask(ctx, t, 0))
	return keepRunning, nil
}

// sourceFailed handles a failure of the task source.
func (a *Agent) sourceFailed(ctx context.Context, n *runTally, err error) (runStop, error) {
	if ctx.Err() != nil {
		return stopNow, nil
	}
	// A source failure is NOT a failed task: no task was taken. Counting
	// it as one produced nonsense totals such as "2 of 1 tasks failed".
	// It is tracked on its own and surfaced through the error below.
	n.sourceFailures++
	n.consecutive++
	a.log.Error("could not obtain the task", "error", err,
		"consecutive", n.consecutive, "source_failures", n.sourceFailures)

	if n.consecutive >= maxConsecutive {
		// The source is broken, not just momentarily empty. Returning
		// makes the failure visible and actionable (cron/systemd see a
		// non-zero exit) instead of looping forever.
		return stopNow, fmt.Errorf("the task source failed %d times in a row, giving up: %w", n.consecutive, err)
	}
	return keepRunning, nil
}

// tally counts the result of one task.
func (a *Agent) tally(n *runTally, t task.Task, result TaskResult) {
	// A task that ended in a QUESTION is not a task that failed.
	//
	// It did not run, and nothing went wrong: the agent could not read the request well
	// enough and is asking. Counting it as a failure is what turned a perfectly good question
	// into "1 of 1 tasks failed" in the interface, which tells the user they did something
	// wrong at the exact moment the agent is asking for their help.
	//
	// It is reported as a non-failure so the interface can show the question, and it does not
	// make the run exit non-zero: the run is WAITING, not broken.
	if result.NeedsInput {
		n.awaiting++
		a.log.Info("task is waiting for the user", "task", truncate(t.Description, 120),
			"question", truncate(result.Question, 200))
		return
	}

	if result.Pass {
		a.log.Info("task completed",
			"task", truncate(t.Description, 120),
			"attempts", result.Attempts,
			"duration_ms", result.DurationMS,
			"final_action", result.FinalAction)
		return
	}
	n.failed++
	n.reasons = append(n.reasons, fmt.Sprintf("%q: %s", truncate(t.Description, 80), result.Reason))
	a.log.Error("task failed",
		"task", truncate(t.Description, 120),
		"attempts", result.Attempts,
		"reason", result.Reason)
}

// processTask runs the 9-step loop for one task and notifies the observer of the
// result (exactly once, whatever the exit path).
func (a *Agent) processTask(ctx context.Context, t task.Task, depth int) TaskResult {
	if depth == 0 {
		a.begin(t)
		a.startFleet(t)
	}
	run := t
	isGoal := false
	if depth == 0 {
		run, isGoal = a.prepareGoal(t)
	}
	r := a.loop(ctx, run, depth)
	if isGoal && r.Report != nil {
		a.saveGoalDecisions(t, r.Report)
	}
	if depth == 0 {
		// Whatever way the loop ended, nothing it started keeps running behind it.
		a.endFleet(r)
	}
	if a.Observer != nil {
		a.Observer(r)
	}
	// A task that RAN is part of the conversation, whatever it ended up doing: it passed, it failed,
	// or it stopped to ask something. Recording it here rather than on the success path is what
	// makes that true on every exit - `loop` returns from seven places, and a turn that failed is
	// exactly the one the next turn needs to be able to see.
	//
	// Only the OUTERMOST call records. A task that split into subtasks goes through here once per
	// subtask, and five subtasks would otherwise read back to the user as five separate things they
	// asked for, when they asked for one.
	//
	// The turns that are already recorded keep their own record: a chat reply and a clarifying
	// question are written where they are decided, because what they said is known only there. What
	// this adds is the outcome of work that was actually done.
	if depth == 0 && r.Kind != KindChat && !r.NeedsInput {
		a.noteOutcome(t, r)
	}
	return r
}

// prepareGoal is the task as the outermost run works on it: a goal becomes autonomous, and the
// decisions taken in earlier goals of this workspace are added to it. It reports whether the task
// is a goal.
func (a *Agent) prepareGoal(t task.Task) (task.Task, bool) {
	run := t
	isGoal := false
	if goal, ok := goalOf(t.Description); ok {
		// A goal is autonomous: nobody is asked, the stated assumption is the answer.
		a.Interactive = false
		run.Description = goal + goalDirective
		isGoal = true
	}
	if ctxText := decisionsContext(a.cfg.Agent.WorkspaceDir); ctxText != "" {
		run.Description += ctxText
	}
	return run, isGoal
}

// saveGoalDecisions keeps the decisions a goal's run reported, for the goals after it.
func (a *Agent) saveGoalDecisions(t task.Task, report *Report) {
	if err := recordDecisions(a.cfg.Agent.WorkspaceDir, t.Description, report.Decisions); err != nil {
		a.log.Warn("could not save the goal's decisions", "error", err)
	}
}

// noteOutcome records the outcome of a task that ran in the conversation.
func (a *Agent) noteOutcome(t task.Task, r TaskResult) {
	outcome := taskOutcome(r)
	// A run that did not finish keeps what it had done: the result alone ("cancelled",
	// "stalled") would erase the only record of the work, and the next turn - or a
	// person reading the conversation - starts from nothing.
	if !r.Pass {
		if work := a.workSoFar(t); work != "" {
			outcome += "\nWork done before it stopped:\n" + work
		}
	}
	a.note(t, outcome, KindTask)
}

// goalPrefix is what the user types to hand over a goal: "/goal <what they want>".
const goalPrefix = "/goal"

// goalDirective is appended to a goal's description. Spec-driven work normally stops at a real
// decision and lets the user choose; a goal is the user saying "decide for me", so the agent picks
// the option it would recommend, and reports every decision it made so they can be audited later.
const goalDirective = "\n\nThis is a GOAL: the user delegated every decision. Do NOT ask questions " +
	"and do NOT offer alternatives. Where a choice is needed, take the option you would recommend, " +
	"and list each decision you took, with one line of why, in the final report."

// goalOf reports whether a task is a goal and returns it without the prefix.
func goalOf(description string) (string, bool) {
	d := strings.TrimSpace(description)
	if d == goalPrefix || !strings.HasPrefix(d, goalPrefix+" ") {
		return description, false
	}
	return strings.TrimSpace(d[len(goalPrefix):]), true
}

// taskOutcome is the one line a finished task leaves in the conversation.
//
// It prefers what the agent said in its own words - the synthesised summary - and falls back to the
// verdict, so a turn is never recorded as having done something without saying what it was.
func taskOutcome(r TaskResult) string {
	if s := strings.TrimSpace(r.Summary); s != "" {
		return s
	}
	if s := strings.TrimSpace(r.Reply); s != "" {
		return s
	}
	if s := strings.TrimSpace(r.Reason); s != "" {
		return s
	}
	// A result with nothing to say is not a reason to record an empty turn: an empty turn would read
	// back as a message the user never sent.
	if r.Pass {
		return "the task completed"
	}
	return "the task did not complete"
}

// readOnlyWarnAt is how many progress rounds in a row may only read before the model is told to
// write. Five is enough to explore a real project and too few to re-read it twice.
const readOnlyWarnAt = 5

// doneGuardAfter is how many rounds that only read must have gone by before a claim of "done"
// over an unchanged tree is questioned. A run that claims done at once is not second-guessed
// here (its check is the anchor and the synthesis, which says plainly what was changed); the
// pattern this exists for is the one that explores for many rounds and then closes.
const doneGuardAfter = 2

// maxToolingRetries is how many refusals caused by a missing tool are not charged to max_retries.
const maxToolingRetries = 3

// missingTooling reports whether every check that failed did so because a program or module it
// needs is not installed, which is a fact about the machine and not about the change.
func missingTooling(v anchor.Result) bool {
	failed := 0
	for _, c := range v.Checks {
		if c.Pass {
			continue
		}
		failed++
		out := strings.ToLower(c.Output + " " + c.Error)
		hit := false
		for _, sig := range []string{"command not found", ": not found", "cannot find module", "no module named",
			"module_not_found", "cannot find package", "executable file not found", "is not recognized as",
			"err_module_not_found", "could not find a package.json", "eslint: command not found"} {
			if strings.Contains(out, sig) {
				hit = true
				break
			}
		}
		if !hit {
			return false
		}
	}
	return failed > 0
}

// maxUnusableReplies is how many replies IN A ROW may be unusable before the run stops.
// One malformed reply is noise a model recovers from when told; three in a row is a model
// that cannot follow the format, and more rounds will not change that.
const maxUnusableReplies = 5

// stallWarnAt and stallStopAt bound a run that repeats itself: after stallWarnAt identical
// progress rounds in a row the model is warned, and at stallStopAt the run stops. Identical
// means the same actions AND the same output, so a poll whose output changes is not a stall.
const (
	stallWarnAt = 2
	stallStopAt = 4
)

// keepRoundsInFull is how many of the latest rounds the next round reads in full. Older
// rounds shrink to one line each: a long task would otherwise grow the prompt by up to 3 KB
// per round until the history crowded out the task - a hundred rounds is 300 KB.
//
// It was 6. Reported from a real session: the model said "the previous output was not visible
// in my context" and read the same region of tui.go five times. Most of its reads were not kept
// as evidence (they wrote to /tmp, or exported a PATH first, so they were not provably reads),
// which left the journal as the only copy, and six rounds is about one explore-write-check
// cycle. Ten keeps two of them, for at most ~10 KB more than before on the older rounds.
const keepRoundsInFull = 10

// keepRoundsWhole is how many of the newest rounds keep their output up to roundDetailOutput;
// the other rounds shown in full are cut to roundDetailOlder. The round just run is the one the
// next decision is about, and its output was cut at 3000 bytes from the HEAD - so a 300-line
// file read lost its end, and a check lost the summary line that says whether it passed.
const (
	keepRoundsWhole   = 2
	roundDetailOutput = 12000
	roundDetailOlder  = 3000
)

// roundKind is what a round of the loop turned out to be. See the loop contract in loop.
type roundKind int

const (
	roundProgress roundKind = iota
	roundRejected
	roundUnusable
)

// label is the one-word name of the kind, for a round compacted to one line.
func (k roundKind) label() string {
	switch k {
	case roundProgress:
		return "progress"
	case roundRejected:
		return "rejected"
	default:
		return "unusable reply"
	}
}

// heading is what the model is told the round WAS. Getting this right is the point of the
// journal: a progress round labelled as a failure is how a model ends up undoing its own work.
func (k roundKind) heading() string {
	switch k {
	case roundProgress:
		return "PROGRESS: these actions ran and you reported more work left"
	case roundRejected:
		return "REJECTED: you reported the task done and it was NOT accepted; fix what this says"
	default:
		return "UNUSABLE: your reply could not be used"
	}
}

// roundRecord is one round, as the rounds after it are told about it.
type roundRecord struct {
	round    int
	kind     roundKind
	commands string // one line, for when the round is compacted
	detail   string // the full account, for the latest rounds
}

// renderRounds is the journal the execute phase reads as {{history}}.
func renderRounds(rounds []roundRecord) string {
	if len(rounds) == 0 {
		return "## PREVIOUS ROUNDS\n(none: this is the first round)"
	}
	var b strings.Builder
	b.WriteString("## PREVIOUS ROUNDS\n")
	b.WriteString("Everything already done for this task, oldest first. Continue from where it " +
		"stands: do not redo a round that made progress.\n")
	older := len(rounds) - keepRoundsInFull
	whole := len(rounds) - keepRoundsWhole
	for i, r := range rounds {
		if i < older {
			fmt.Fprintf(&b, "- Round %d (%s): %s\n", r.round, r.kind.label(), r.commands)
			continue
		}
		detail := r.detail
		if i < whole {
			detail = truncateMiddle(detail, roundDetailOlder)
		}
		fmt.Fprintf(&b, "\n### Round %d - %s\n%s\n", r.round, r.kind.heading(), detail)
	}
	return b.String()
}

// commandList renders the actions of a round, one per line.
func commandList(action Action) string {
	var b strings.Builder
	for _, c := range action.Actions {
		line := strings.TrimSpace(c.Command)
		if k := strings.ToLower(strings.TrimSpace(c.Kind)); k != "" && k != "command" {
			line = "[" + k + "] " + line
		}
		fmt.Fprintf(&b, "- %s\n", line)
	}
	return b.String()
}

// nonBlank returns the entries that carry text, trimmed.
func nonBlank(in []string) []string {
	var out []string
	for _, s := range in {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out
}

// runSubtasks carries out a task the plan split, one subtask at a time, in order.
//
// THE SUBTASK CONTRACT:
//
//   - A subtask is told what it is part of. It used to receive its own line and nothing
//     else - "update the callers" with no idea of which function, which request, or what
//     the subtask before it had just done - so it was analysed and planned from scratch,
//     and often re-did or contradicted its siblings.
//   - The subtasks run IN ORDER and the first failure stops the rest. The plan lists them
//     in the order the work goes, so a later one routinely builds on an earlier one, and
//     running it on top of a failure spends its whole budget on a broken base.
//   - The parent passes only when every subtask passed, and its answer is what they did,
//     not "3 subtasks completed".
func (a *Agent) runSubtasks(ctx context.Context, t task.Task, analysis Analysis, subs []string,
	depth int, start time.Time, prefix string) TaskResult {
	res := TaskResult{Task: t.Description, Attempts: 1}
	a.log.Info(prefix+"splitting into subtasks", "count", len(subs))
	var outcomes []string
	for i, sub := range subs {
		if ctx.Err() != nil {
			res.Reason = "cancelled during the subtasks"
			res.DurationMS = time.Since(start).Milliseconds()
			return res
		}
		res.Subtasks++
		a.report("subtask %d/%d: %s", i+1, len(subs), truncate(sub, 160))
		subResult := a.processTask(ctx, task.Task{
			Description: subtaskBrief(t.Description, analysis.Summary, subs, i, outcomes),
			Origin:      t.Origin + " (subtask)",
			Context:     t.Context,
		}, depth+1)
		subResult = a.retrySubtask(ctx, subtaskTry{t, analysis, subs, i, outcomes, depth}, subResult)
		res.Validation = subResult.Validation
		if !subResult.Pass {
			skipped := len(subs) - i - 1
			res.Reason = fmt.Sprintf("subtask %d of %d did not pass (%s): %s", i+1, len(subs),
				truncate(sub, 120), subResult.Reason)
			if skipped > 0 {
				res.Reason += fmt.Sprintf("; the %d after it were not started, "+
					"because later subtasks build on earlier ones", skipped)
			}
			res.Summary = subtaskSummary(subs, outcomes)
			res.DurationMS = time.Since(start).Milliseconds()
			a.report("%s", res.Reason)
			return res
		}
		outcomes = append(outcomes, taskOutcome(subResult))
	}
	res.Pass = true
	res.Reason = fmt.Sprintf("%d subtasks completed", len(subs))
	res.Summary = subtaskSummary(subs, outcomes)
	res.DurationMS = time.Since(start).Milliseconds()
	return res
}

// subtaskBrief is the description a subtask is analysed, planned and executed from.
//
// The subtask's own line goes FIRST, so a log line or a truncated prompt still shows what it
// is; the request it belongs to and the state of its siblings follow.
func subtaskBrief(parent, summary string, subs []string, i int, outcomes []string) string {
	var b strings.Builder
	b.WriteString(subs[i])
	fmt.Fprintf(&b, "\n\n(This is subtask %d of %d of a larger request. Do ONLY this subtask: "+
		"the others are carried out separately, before or after it.)\n", i+1, len(subs))
	fmt.Fprintf(&b, "Overall request: %s\n", truncate(collapse(parent), 1000))
	if s := strings.TrimSpace(summary); s != "" && s != strings.TrimSpace(parent) {
		fmt.Fprintf(&b, "What the request has to achieve: %s\n", truncate(collapse(s), 500))
	}
	b.WriteString("Subtasks:\n")
	for j, s := range subs {
		switch {
		case j < len(outcomes):
			fmt.Fprintf(&b, "%d. [done] %s - %s\n", j+1, s, truncate(collapse(outcomes[j]), 300))
		case j == i:
			fmt.Fprintf(&b, "%d. [THIS ONE] %s\n", j+1, s)
		default:
			fmt.Fprintf(&b, "%d. [later] %s\n", j+1, s)
		}
	}
	return b.String()
}

// subtaskSummary is the parent's answer: what each finished subtask did.
func subtaskSummary(subs []string, outcomes []string) string {
	if len(outcomes) == 0 {
		return ""
	}
	var b strings.Builder
	for j, o := range outcomes {
		fmt.Fprintf(&b, "%d. **%s** - %s\n", j+1, subs[j], strings.TrimSpace(o))
	}
	return strings.TrimRight(b.String(), "\n")
}

// maxBudgetExtensions bounds how many times the loop may ASK to continue. It is a
// ceiling on the ceiling: a gateway whose approver answers "yes" without a human
// behind it (a script, a test) would otherwise loop for ever. Reaching it means a
// hundred answered questions, which no real session does.
const maxBudgetExtensions = 10

// continueBudget asks the user whether the run should keep going with another full
// budget, after one has been spent with work left.
//
// It is the same approval mechanism a consequential command uses, which is the point:
// "may I keep working" is a question the user answers, and the interface that already
// knows how to ask it is this one. No approver (a run from a script) means no answer,
// and no answer means stop - the same conservative default the command approval uses.
func (a *Agent) continueBudget(ctx context.Context, rounds, maxSteps int, prefix string) (bool, error) {
	if a.approver == nil {
		return false, nil
	}
	ok, err := a.approver(ctx, ApprovalRequest{
		Command: fmt.Sprintf("continue for another %d rounds (%d used, work left)", maxSteps, rounds),
		Reason: "the step budget ran out and the model still reports work left; " +
			"continuing keeps everything already done instead of starting over",
		Rule: BudgetRule,
	})
	if err != nil || !ok {
		a.log.Warn(prefix+"the budget continuation was not approved", "rounds", rounds)
		return false, err
	}
	a.log.Info(prefix+"continuing with another budget", "rounds", rounds, "max_steps", maxSteps)
	return true, nil
}

// BudgetRule is the Rule of the question "may this run spend another budget?". It is not a
// command, so an approver that approves every COMMAND on the user's behalf must not answer it:
// the user said yes to running commands, not to working for ever.
const BudgetRule = "agent.max_steps"

// maxSteps is the round budget in force, defaulted when none was configured.
func (a *Agent) maxSteps() int {
	if a.cfg.Agent.MaxSteps > 0 {
		return a.cfg.Agent.MaxSteps
	}
	return defaultMaxSteps
}

// defaultMaxSteps is how many rounds one task may take when the configuration names no
// bound. It is generous because "do what I asked" is usually a plan and not a single step,
// and it is a CHECKPOINT rather than a wall: a run that reaches it with work left asks the
// user before spending another, so the bound protects against a loop that cannot converge
// without cutting off honest work.
const defaultMaxSteps = 100

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
	if missingTooling(validation) {
		sb.WriteString("\nEvery failed check failed because a tool or module is NOT INSTALLED here, which says " +
			"nothing about your change. Install the project's dependencies the way the project does " +
			"(npm ci, pip install -r requirements.txt, go mod download, bundle install...) and claim done again. " +
			"This attempt is not counted against you.\n")
	}
	// What the baseline measured, in words: the JSON carries the flags, but a model skimming it
	// is exactly the one that chased old failures for fifteen rounds.
	var broke, old []string
	for _, c := range validation.Checks {
		if c.PassedBefore {
			broke = append(broke, c.Name)
		}
		if c.PreExisting {
			old = append(old, c.Name)
		}
	}
	if len(broke) > 0 {
		fmt.Fprintf(&sb, "\nThis check passed before your change (on the code the run started from), so "+
			"your change is what broke it: %s. Fix that first.\n", strings.Join(broke, ", "))
	}
	if len(old) > 0 {
		fmt.Fprintf(&sb, "\nAlready failing before your change, the same way (not yours; do not spend "+
			"rounds on them): %s.\n", strings.Join(old, ", "))
	}
	sb.WriteString("\nResult of the deterministic validation (JSON):\n")
	sb.WriteString(validation.JSON())
	sb.WriteString("\n")
	return sb.String()
}

// proposedCommands renders the commands of an attempt as a single line for the log.
//
// The retry loop stops when the counter runs out, and its whole design assumes that a retry can
// converge. Whether it does is a question about the DATA, not about the code: a run that is
// correcting itself proposes different commands on each attempt, while a thrashing one proposes
// the same thing and collects the same failure. Only the commands tell those two apart — the
// validation reason is identical for both. This is what makes that question answerable from the
// log after a few weeks of real use, instead of by guessing.
func proposedCommands(action Action) string {
	parts := make([]string, 0, len(action.Actions))
	for _, c := range action.Actions {
		command := strings.TrimSpace(c.Command)
		if command == "" {
			// A command-less entry (an empty proposal, or one that only carries a description)
			// still happened; dropping it would make the line look like fewer commands ran.
			command = "(empty)"
		}
		parts = append(parts, command)
	}
	if len(parts) == 0 {
		return "(no commands proposed)"
	}
	// Truncated because a proposal can be arbitrarily long and this record is for counting and
	// comparing, not for reading in full: summariseFailure already carries the complete text.
	return truncate(strings.Join(parts, " | "), 500)
}

func modesToStrings(modes []sandbox.Mode) []string {
	out := make([]string, 0, len(modes))
	for _, m := range modes {
		out = append(out, string(m))
	}
	return out
}

// truncate shortens s to at most max bytes plus an ellipsis, never cutting a character in
// half. A byte cut used to split a multi-byte rune - any accented letter of a Spanish request -
// and the invalid UTF-8 went into prompts, logs and the chat.
func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	cut := max
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + "..."
}

// Caps on how much of one command's output is passed on. See the two uses in runActions:
// terminalOutputChars is what the user reads (the chat step and the terminal drawer, which is
// the record of the run), and roundOutputChars is what the model is handed to work with.
//
// The terminal's cap is the larger of the two: a person asking for a `grep` wants the lines,
// and a drawer that is silently cut is a worse record than a long one. The model's is smaller
// because it goes into a prompt, where the same text costs tokens on every round.
const (
	terminalOutputChars = 32 << 10
	roundOutputChars    = 16 << 10
)

// truncateMiddle keeps the first and the last part of s and drops the middle, when s is
// longer than max bytes. Both ends are cut on a character boundary, and the marker says how
// many bytes are missing, so a reader can tell a short answer from a cut one.
func truncateMiddle(s string, max int) string {
	if len(s) <= max {
		return s
	}
	head := max / 3
	tail := max - head
	start := len(s) - tail
	for start < len(s) && !utf8.RuneStart(s[start]) {
		start++
	}
	omitted := len(s) - head - (len(s) - start)
	return truncate(s, head) + fmt.Sprintf("\n[... %d of %d bytes omitted ...]\n", omitted, len(s)) + s[start:]
}
