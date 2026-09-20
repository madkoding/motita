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
	"sync"
	"time"

	"github.com/madkoding/starlight/internal/anchor"
	"github.com/madkoding/starlight/internal/config"
	"github.com/madkoding/starlight/internal/execx"
	"github.com/madkoding/starlight/internal/llm"
	"github.com/madkoding/starlight/internal/logx"
	"github.com/madkoding/starlight/internal/reward"
	"github.com/madkoding/starlight/internal/sandbox"
	"github.com/madkoding/starlight/internal/skills"
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
}

// SetObserver allows external callers (such as the TUI) to register a callback
// that receives the task result when it is produced. It is the same hook as the
// Observer field; the method exists so interfaces that wrap *agent.Agent can
// expose the hook without exporting the field itself.
func (a *Agent) SetObserver(fn func(TaskResult)) {
	a.Observer = fn
}

// SetLibrary installs the procedure library.
//
// It is the same library the planner receives, and sharing it is the point: a procedure
// written down in one mode is available in the other, which is what makes the library a single
// body of knowledge rather than two that drift apart.
func (a *Agent) SetLibrary(lib *skills.Library) { a.library = lib }

// SetReward installs the long-term value ledger.
func (a *Agent) SetReward(l *reward.Ledger) { a.reward = l }

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
func (a *Agent) exec(ctx context.Context, p execx.Request) (string, bool, int, error) {
	if a.ExecCommand == nil {
		return "", false, -1, errors.New("the agent has no command executor configured")
	}
	return a.ExecCommand(ctx, p)
}

// RunCommand executes a single command line under the agent's policy and sandbox.
// It is the entry point used by interactive modes (plan/chat) where the model
// requests a tool call instead of emitting JSON.
func (a *Agent) RunCommand(ctx context.Context, command string) (string, int, error) {
	request, refused := a.buildRequest(command)
	if refused != "" {
		return fmt.Sprintf("[refused: %s]\n", refused), 1, fmt.Errorf("command was refused: %s", refused)
	}
	output, _, exit, err := a.exec(ctx, request)
	return output, exit, err
}

// --- Conversation -----------------------------------------------------------

// DialogueTurn is one exchange in the conversation: what the user said and what the agent
// answered. Both are kept because the meaning lives in the pair — a user's "yes" is only
// interpretable next to the question it answers.
type DialogueTurn struct {
	User string
	// Agent is the reply, the question, or a one-line account of what was done. It is what the
	// agent said, in the agent's own words.
	Agent string
	// Kind records what the turn was, so the transcript can be summarised honestly: a chat turn
	// and a finished task are not the same thing to a reader.
	Kind string
}

// SetTranscript seeds the conversation, which is how an interface hands over what the user has
// already seen. It replaces anything held.
func (a *Agent) SetTranscript(turns []DialogueTurn) {
	a.transcriptMu.Lock()
	defer a.transcriptMu.Unlock()
	a.transcript = append([]DialogueTurn(nil), turns...)
}

// Transcript returns a copy of the conversation.
func (a *Agent) Transcript() []DialogueTurn {
	a.transcriptMu.Lock()
	defer a.transcriptMu.Unlock()
	return append([]DialogueTurn(nil), a.transcript...)
}

// converse appends the user's message and the agent's answer to the conversation.
func (a *Agent) converse(t task.Task, reply string) {
	a.transcriptMu.Lock()
	defer a.transcriptMu.Unlock()
	a.transcript = append(a.transcript, DialogueTurn{
		User:  t.Description,
		Agent: reply,
		Kind:  KindChat,
	})
}

// note appends what the agent did about a task, so the next turn can refer to it.
//
// A task that ran is part of the conversation too: "and now do the same for the other one" only
// means something if the agent can see what it just did.
func (a *Agent) note(t task.Task, outcome string, kind string) {
	a.transcriptMu.Lock()
	defer a.transcriptMu.Unlock()
	a.transcript = append(a.transcript, DialogueTurn{
		User:  t.Description,
		Agent: outcome,
		Kind:  kind,
	})
}

// dialogue renders the conversation for the prompt.
//
// It is bounded on purpose. The transcript is fed to the analysis phase on every turn, so an
// unbounded one would grow the prompt until it crowded out the task itself — and an old greeting
// is not worth more than the request being made now. The most recent turns are what a short
// answer refers to, so the tail is kept and the beginning is dropped with a note that says so.
func (a *Agent) dialogue() string {
	turns := a.Transcript()
	if len(turns) == 0 {
		return "(this is the first message: there is no earlier conversation)"
	}
	const keep = 12
	dropped := 0
	if len(turns) > keep {
		dropped = len(turns) - keep
		turns = turns[dropped:]
	}
	var b strings.Builder
	if dropped > 0 {
		fmt.Fprintf(&b, "(%d earlier exchanges omitted)\n", dropped)
	}
	for _, turn := range turns {
		fmt.Fprintf(&b, "user: %s\n", truncate(collapse(turn.User), 500))
		said := truncate(collapse(turn.Agent), 500)
		switch turn.Kind {
		case KindChat:
			fmt.Fprintf(&b, "you: %s\n", said)
		case KindTask:
			fmt.Fprintf(&b, "you (did it): %s\n", said)
		default:
			fmt.Fprintf(&b, "you: %s\n", said)
		}
	}
	return b.String()
}

// collapse folds a multi-line string into one line, so one turn stays one line in the prompt.
func collapse(s string) string {
	return strings.Join(strings.Fields(s), " ")
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
	// "hola" produces a task plan, and a question about what it just did produces another
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

	// Reply is the conversational answer, used when Kind is "chat".
	//
	// It is prose for a person, in their language, and it is NOT a task summary: it answers
	// what they said. Keeping it a separate field is what lets the interface show a reply
	// without pretending a task ran.
	Reply string `json:"reply"`
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
	// Summary is a human-readable answer produced by the model and shown in the UI.
	Summary string `json:"summary,omitempty"`
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
}

// --- Main loop --------------------------------------------------------------

// report sends a progress line to the conversational UI when one is attached.
func (a *Agent) report(format string, args ...any) {
	if a.Progress != nil {
		a.Progress(format, args...)
	}
}

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
	// awaiting counts the tasks that stopped to ask a question.
	awaiting := 0
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
			awaiting++
			a.log.Info("task is waiting for the user", "task", truncate(t.Description, 120),
				"question", truncate(result.Question, 200))
			continue
		}

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

	a.log.Info("agent finished", "tasks", total, "failed", failed, "awaiting_input", awaiting)
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

	// proceededOnAssumption records the reading the agent adopted when it had nobody to ask, so
	// the final report can say what it assumed instead of presenting the result as unqualified.
	proceededOnAssumption := ""

	prefix := strings.Repeat("  ", depth)
	a.log.Info(prefix+"task received", "origin", t.Origin, "depth", depth, "description", truncate(t.Description, 200))

	// [2] Extract context and success criteria: the statement of the validation
	// rules shown to the LLM is built here, so it knows what it will be measured
	// against.
	rules := a.describeRules()

	// [3] Analysis.
	a.report("analysing the task...")
	analysis := a.analysisPhase(ctx, t, rules, depth)

	// The message is not always a task. A greeting, a question about what just happened, or
	// thinking out loud has nothing to do, and running the loop for it would plan work nobody
	// asked for and report a result nobody read.
	//
	// This is checked FIRST, before the ask/assume logic below, because it is a different
	// question: "is there work here?" comes before "can I read the work well enough?".
	switch analysis.resolveKind() {
	case KindChat:
		reply := strings.TrimSpace(analysis.Reply)
		if reply == "" {
			// The model classified it as chat but wrote nothing. Saying so is better than
			// falling through to the loop: the classification is still the model's judgement,
			// and a task plan is the one answer the user did not ask for.
			reply = "No hay nada que ejecutar en tu mensaje."
		}
		res.Kind = KindChat
		res.Reply = reply
		res.Pass = true
		res.Reason = "conversational reply"
		res.DurationMS = time.Since(start).Milliseconds()
		a.report("%s", reply)
		a.converse(t, reply)
		a.log.Info(prefix+"answered as chat", "reply", truncate(reply, 200))
		return res
	case KindAsk:
		// Forced into the asking path even when the model left "understandable" true: the kind
		// is the newer signal and saying "ask" is unambiguous.
		analysis.Understandable = false
	}

	if !analysis.Understandable || analysis.Question != "" {
		// The request could not be read well enough to act on. ASK, do not refuse.
		//
		// This used to end the turn with "the task was declared not understandable", which throws
		// away everything the user typed and makes them write it again — for a request they can
		// usually clarify in three words. A user mistypes, abbreviates, and omits what they think
		// is obvious; the agent's job is to close that gap, not to report it.
		//
		// The question goes back through the interface as a normal result, so the user sees it in
		// the conversation and answers with their next message. The assumption travels with it:
		// it is what the agent would do without an answer, which lets the user confirm with a
		// single word, and it is also the honest fallback if the interface has nobody to ask.
		res.NeedsInput = true
		res.Question = strings.TrimSpace(analysis.Question)
		res.Assumption = strings.TrimSpace(analysis.Assumption)
		res.Reason = strings.Join(analysis.Risks, "; ")
		res.DurationMS = time.Since(start).Milliseconds()

		// Nothing to ask AND nothing to assume is a genuine dead end, and it stays a failure.
		//
		// The difference is worth being precise about. A QUESTION is a request the agent can
		// name and the user can answer; an ASSUMPTION is a reading the agent can act on. With
		// neither, there is nothing to ask and nothing to do, and inventing a generic "what did
		// you mean?" would be asking the user to repeat what they just said — the report is
		// clearer, and a batch run still exits non-zero for cron to notice.
		if res.Question == "" && res.Assumption == "" {
			res.NeedsInput = false
			res.Question = ""
			res.Reason = strings.Join(analysis.Risks, "; ")
			a.report("task not understandable: %s", res.Reason)
			return res
		}
		if res.Question == "" {
			// The model named an assumption but no question. Asking is still right — the user can
			// confirm or correct the reading in one word — so the question is derived from it.
			res.Question = "¿Voy bien encaminado? Si no, dime qué quieres exactamente."
		}

		// Nobody to ask: proceed on the assumption instead of stalling.
		//
		// A batch run has no user at the other end, so a question would wait forever. The
		// assumption the model itself proposed is the reading to act on — and acting on a stated
		// assumption is what an engineer does when the ticket is thin, not refusing to work.
		if !a.Interactive && res.Assumption != "" {
			a.log.Info("no user to ask: proceeding on the stated assumption",
				"question", truncate(res.Question, 160), "assumption", truncate(res.Assumption, 200))
			a.report("no user to ask; assuming: %s", res.Assumption)
			analysis.Understandable = true
			analysis.Question = ""
			// No fallback for an empty summary is needed: analysisPhase already replaced it with
			// the task description when the model left it blank, so by this point it always has
			// something. A branch here would be unreachable.
			//
			// The result describes the run that is now PROCEEDING, not the question that was
			// skipped: leaving these set would report a finished run as one that is still waiting
			// for an answer. They are re-applied below if the run later fails.
			res.NeedsInput = false
			res.Question = ""
			res.Assumption = ""
			proceededOnAssumption = analysis.Summary
		} else {
			a.report("need clarification: %s", res.Question)
			a.log.Info("asking the user instead of guessing", "question", truncate(res.Question, 200),
				"assumption", truncate(res.Assumption, 200))
			// The question is recorded in the conversation. Their next message answers it, and
			// without this the answer arrives with nothing to refer to — which is exactly the
			// amnesia that makes a clarifying question useless.
			said := res.Question
			if res.Assumption != "" {
				said += "\n\nSi no me dices otra cosa, asumiré: " + res.Assumption
			}
			a.note(t, said, KindAsk)
			return res
		}
	}
	a.report("understood: %s", analysis.Summary)

	// [4] Plan.
	a.report("planning...")
	plan := a.planPhase(ctx, t, analysis, depth)
	a.report("plan ready: %d steps", len(plan.Steps))

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
		a.report("deciding action (attempt %d/%d)...", attempt, a.cfg.Agent.MaxRetries+1)
		action, err := a.actionPhase(ctx, t, plan, failedAttempts, attempt, prefix)
		if err != nil {
			res.Reason = "could not obtain the action from the LLM: " + err.Error()
			res.DurationMS = time.Since(start).Milliseconds()
			a.report("failed to get an action: %v", err)
			a.escalate(ctx, prefix)
			return res
		}
		a.report("action: %s", truncate(action.Reasoning, 120))

		// [7] Run in the sandbox.
		runOutput, runErr := a.runActions(ctx, action.Actions, prefix)

		// [8] Validate with the anchor, always.
		a.report("validating with anchor...")
		validation := anchor.New(a.cfg.Anchor, a.cfg.Agent.WorkspaceDir, a.sandbox).Validate(ctx)
		res.Validation = &validation

		if validation.Pass && runErr == nil {
			// [9] Validation PASS: now the final action.
			a.report("validation passed; running final action...")
			finalAction, finalErr := a.runFinalAction(ctx, action.Final, prefix)
			res.FinalAction = finalAction
			if finalErr != nil {
				detail := fmt.Sprintf("validation passed but the final action failed: %v\nOutput: %s", finalErr, finalAction)
				failedAttempts = append(failedAttempts, detail)
				a.report("final action failed: %v", finalErr)
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
			a.report("task complete: %s", validation.Reason)
			if a.cfg.Prompts.Synthesize.User != "" && a.cfg.Prompts.Synthesize.System != "" {
				a.report("synthesizing answer...")
				summary := a.synthesizePhase(ctx, t, runOutput, validation)
				if summary != "" {
					res.Summary = summary
					a.report("%s", summary)
				}
			}
			// When there was nobody to ask, the run proceeded on a reading the agent chose. Saying
			// so is what makes the result honest: the answer is correct GIVEN that reading, and a
			// user reading it later needs to know which one was taken — otherwise a reasonable
			// assumption looks like a wrong answer.
			if proceededOnAssumption != "" {
				res.Assumption = proceededOnAssumption
				a.report("(proceeded assuming: %s)", proceededOnAssumption)
			}
			a.log.Info(prefix+"validation passed", "attempt", attempt, "final_action", finalAction,
				"assumed", truncate(proceededOnAssumption, 160))
			return res
		}

		// FAIL: the records are accumulated so the LLM can correct with data.
		detail := a.summariseFailure(action, runOutput, validation, runErr)
		failedAttempts = append(failedAttempts, detail)
		a.report("attempt failed: %s", validation.Reason)
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
	a.report("%s", res.Reason)
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
		// A malformed analysis is OUR problem, not the user's: the model failed to follow the
		// format, and reporting the parse error to the user asks them to fix something they did
		// not do. The honest answer is that the request could not be read, with a question that
		// lets them proceed.
		a.log.Error("the analysis has an unexpected format", "error", err, "response", truncate(text, 300))
		return Analysis{
			Understandable: false,
			Risks:          []string{"the analysis could not be parsed: " + err.Error()},
			Question:       "No pude interpretar la petición. ¿Puedes decirme, en una frase, qué quieres que haga y sobre qué?",
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

// synthesizePhase asks the LLM for a concise, evidence-based answer after the
// actions have run and the anchor has validated them.
func (a *Agent) synthesizePhase(ctx context.Context, t task.Task, output string, validation anchor.Result) string {
	vars := a.baseVariables(t)
	vars["output"] = truncate(output, 4000)
	vars["validation"] = validation.Reason

	text, err := a.ask(ctx, a.cfg.Prompts.Synthesize, vars, "synthesize")
	if err != nil {
		a.log.Warn("synthesis phase failed", "error", err)
		return ""
	}
	var reply struct {
		Summary string `json:"summary"`
	}
	if err := llm.DecodeJSON(text, &reply); err != nil {
		a.log.Warn("synthesis response has an unexpected format", "error", err, "response", truncate(text, 300))
		return ""
	}
	return strings.TrimSpace(reply.Summary)
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
		"history":      a.dialogue(),
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
// runLibraryAction answers one library operation, and reports whether it was one.
//
// An unknown kind returns handled=false so it falls through to the command path, where it is
// reported as a command with no command — the honest outcome for a kind nothing recognises.
func (a *Agent) runLibraryAction(kind string, action Command) (bool, string) {
	switch kind {
	case "read_skill", "search_skills", "list_skills", "save_skill":
	default:
		return false, ""
	}
	if a.library == nil {
		return true, "Error: no procedure library is configured."
	}
	// The kind is validated above, so this switch is exhaustive over what can arrive: a final
	// "not handled" return would be unreachable. Adding an operation means adding it to both
	// switches, and the compiler will not let the second one fall through.
	switch kind {
	case "list_skills":
		return true, a.listSkills()
	case "search_skills":
		return true, a.searchSkills(action.Command)
	case "read_skill":
		return true, a.readSkill(action.Command)
	default: // save_skill
		return true, a.saveSkill(action.Command)
	}
}

// listSkills reports the index, with each skill's accumulated value when there is one.
func (a *Agent) listSkills() string {
	all, err := a.library.List()
	if err != nil {
		return fmt.Sprintf("Error: could not read the library: %v", err)
	}
	if len(all) == 0 {
		return "The library is empty. Work from your own knowledge, and save what you learn."
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%d skill(s). Use read_skill with a name to read one in full.\n", len(all))
	for _, s := range all {
		fmt.Fprintf(&b, "\n- %s: %s\n  %s%s%s", s.Name, s.Title, s.Summary,
			a.historySuffix(s.Name), a.feedbackSuffix(s.Name))
	}
	return b.String()
}

// searchSkills finds procedures by what the work is about.
func (a *Agent) searchSkills(query string) string {
	query = strings.TrimSpace(query)
	if query == "" {
		return "Error: search_skills needs what the work is about, in plain words."
	}
	hits, err := a.library.Search(query, 10)
	if err != nil {
		return fmt.Sprintf("Error: could not search the library: %v", err)
	}
	if len(hits) == 0 {
		return fmt.Sprintf("No skill matches %q. Work from your own knowledge, and save a skill afterwards if what you work out is worth keeping.", query)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%d skill(s) match %q. Read one in full with read_skill.\n", len(hits), query)
	for _, s := range hits {
		fmt.Fprintf(&b, "\n- %s: %s\n  %s%s", s.Name, s.Title, s.Summary, a.historySuffix(s.Name))
	}
	for _, s := range hits {
		b.WriteString(a.feedbackSuffix(s.Name))
	}
	return b.String()
}

// readSkill returns one procedure in full, and records that it was relied on.
func (a *Agent) readSkill(name string) string {
	s, err := a.library.Get(strings.TrimSpace(name))
	if err != nil {
		if errors.Is(err, skills.ErrNotFound) {
			return fmt.Sprintf("No skill named %q. Use list_skills to see what the library holds.", name)
		}
		return fmt.Sprintf("Error: %v", err)
	}
	// The read is the moment the credit becomes knowable, and the only one.
	a.consult(s.Name)
	var b strings.Builder
	fmt.Fprintf(&b, "# skill: %s\n(source: %s)\n\n", s.Name, s.Path)
	b.WriteString(s.Body)
	b.WriteString(a.feedbackSuffix(s.Name))
	return b.String()
}

// saveSkill writes a procedure. The argument is "name :: body", because this mode's actions
// carry a single string rather than structured arguments.
func (a *Agent) saveSkill(arg string) string {
	name, body, ok := strings.Cut(arg, "::")
	if !ok {
		return "Error: save_skill expects \"name :: the document in markdown\"."
	}
	s, err := a.library.Save(strings.TrimSpace(name), strings.TrimSpace(body))
	if err != nil {
		return fmt.Sprintf("Error: %v", err)
	}
	// A save that replaces a skill with an outstanding complaint is the fix being attempted.
	// Marking it here is what stops the same complaint being handed out on every later turn.
	marked := ""
	if a.reward != nil && a.reward.Addressed(s.Name) {
		if err := a.reward.Save(); err == nil {
			marked = " The complaint that was recorded against this skill is now marked as addressed."
		}
	}
	return fmt.Sprintf("Saved %q (%d bytes).%s", s.Name, len(body), marked)
}

// historySuffix renders a skill's accumulated value, or nothing when it has no history.
//
// Numbers and counts, with no adjective: "RELIABLE" would be this program's interpretation
// presented as evidence. An unconsulted skill shows nothing rather than "0.0", which would
// read as "this failed".
func (a *Agent) historySuffix(name string) string {
	if a.reward == nil {
		return ""
	}
	s, ok := a.reward.Get(name)
	if !ok || s.Uses() == 0 {
		return ""
	}
	return fmt.Sprintf("  [used %d, value %+.2f]", s.Uses(), s.Value)
}

// feedbackSuffix states the user's outstanding complaints for a skill, quoted verbatim.
//
// The number says a skill failed; the user's own words say what was wrong with it, which is
// the only form of the complaint a repair can be written from. Only complaints with no fix
// attempted are shown: once it has been rewritten the complaint is answered.
func (a *Agent) feedbackSuffix(name string) string {
	if a.reward == nil {
		return ""
	}
	s, ok := a.reward.Get(name)
	if !ok {
		return ""
	}
	notes := s.Unaddressed()
	if len(notes) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n  !! the user reported this skill failing, and the procedure has NOT been revised since:")
	for i, n := range notes {
		fmt.Fprintf(&b, "\n     %d. %s", i+1, n.Text)
	}
	b.WriteString("\n     Read the procedure again, work out which step the report is about, and save the")
	b.WriteString("\n     corrected version with save_skill. Fixing it is worth more than avoiding it.")
	return b.String()
}

func (a *Agent) runActions(ctx context.Context, actions []Command, prefix string) (string, error) {
	var sb strings.Builder
	var lastErr error

	for i, action := range actions {
		// A library action is not a shell command: it is answered from the procedure library and
		// its result goes back to the model as the output of this step. It is dispatched by
		// kind, because that is how this mode names things — the same four operations the
		// planner exposes as tools, reachable from here too.
		if kind := strings.ToLower(strings.TrimSpace(action.Kind)); kind != "" && kind != "command" {
			if handled, out := a.runLibraryAction(kind, action); handled {
				fmt.Fprintf(&sb, "[%s] %s\n%s\n", kind, action.Description, out)
				a.log.Info(prefix+"library action", "kind", kind, "description",
					truncate(action.Description, 80))
				continue
			}
		}
		if strings.TrimSpace(action.Command) == "" {
			a.log.Debug(prefix+"action with no command (descriptive)", "description", action.Description)
			continue
		}
		a.report("running: %s", action.Command)
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
