// Package tui implements the interactive text-based user interface that is
// launched when starlight is run without arguments and without a configured
// task. It is deliberately built with the Go standard library only.
package tui

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/madkoding/starlight/internal/agent"
	"github.com/madkoding/starlight/internal/config"
	"github.com/madkoding/starlight/internal/llm"
	"github.com/madkoding/starlight/internal/logx"
	"github.com/madkoding/starlight/internal/onboard"
	"github.com/madkoding/starlight/internal/plan"
	"github.com/madkoding/starlight/internal/reward"
	"github.com/madkoding/starlight/internal/sandbox"
	"github.com/madkoding/starlight/internal/session"
	"github.com/madkoding/starlight/internal/skills"
	taskpkg "github.com/madkoding/starlight/internal/task"
)

// Runner is the callback that executes the selected mode. The TUI package uses
// this interface so tests can inject fake implementations.
type Runner interface {
	// RunPlan runs the read-only plan mode with the given user prompt.
	// Progress lines are sent through the callback.
	RunPlan(ctx context.Context, prompt string, progress func(string, ...any)) (string, error)
	// RunTask runs the agent in task mode with the given task description.
	// It returns a human-readable summary of the result. Progress lines are sent
	// through the callback.
	RunTask(ctx context.Context, task string, progress func(string, ...any)) (string, error)
	// RunConfig runs the onboarding wizard.
	RunConfig(ctx context.Context) error
	// ConversationReport describes the session the conversation is kept in: the window,
	// how much of it is in use, and what any compaction carried forward.
	ConversationReport() string
	// ConversationSummary is the same figures in the raw form a status bar draws:
	// the window, the tokens in use and the fraction gone.
	ConversationSummary() session.Snapshot
	// ResetConversation starts a new session, which is how a user leaves a subject behind
	// without leaving the program.
	ResetConversation()
	// RunModels reports the active provider and the models it publishes. It also
	// writes the report to the runner's output, and returns it so a chat view can
	// keep it in its own scrollback.
	RunModels(ctx context.Context) (string, error)
	// Config returns the current configuration (used for the status bar).
	Config() config.Config
	// SetReasoning changes the in-memory reasoning level.
	SetReasoning(level string)
	// RecordVerdict applies the user's verdict on the last turn to the skills it read.
	//
	// It is the ONLY reward signal: nothing is scored unless the user marks it. The note is
	// the user's own words about what was wrong, kept verbatim because it is what a repair can
	// be written from.
	RecordVerdict(good bool, note string) string
	// RewardReport renders what the library has learned, worst first.
	RewardReport() string
}

// AgentRunner is the subset of *agent.Agent that the TUI needs.
type AgentRunner interface {
	Run(ctx context.Context) error
	RunCommand(ctx context.Context, command string) (string, int, error)
	// SetTranscript hands the agent the conversation so far, so a short answer ("yes", "the
	// second one") has something to refer to. Without it every message arrives as the first
	// message and the agent can only re-ask what it already asked.
	SetTranscript(turns []agent.DialogueTurn)
	// Transcript returns the conversation, including what this run appended, so the caller can
	// carry it into the next turn.
	Transcript() []agent.DialogueTurn
}

// agentFactory builds an agent from the current configuration. It is injectable
// so tests can avoid running a real agent.
//
// The agent is told whether there is a user who can answer a question. The interface is the only
// place that knows: a run started from the TUI has one, a task piped in from a script does not,
// and the agent behaves differently — it asks when it can, and proceeds on its stated assumption
// when there is nobody to ask.
type agentFactory func(cfg config.Config, log *logx.Logger, engine *llm.Client, box *sandbox.Sandbox, source taskpkg.Source, interactive bool) AgentRunner

// defaultAgentFactory uses the real agent package.
func defaultAgentFactory(cfg config.Config, log *logx.Logger, engine *llm.Client, box *sandbox.Sandbox, source taskpkg.Source, interactive bool) AgentRunner {
	ag := agent.New(cfg, log, engine, box, source)
	ag.Interactive = interactive
	return ag
}

// AppRunner is the production implementation that calls the real layers.
type AppRunner struct {
	Out      io.Writer
	Err      io.Writer
	Cfg      config.Config
	Engine   *llm.Client
	Box      *sandbox.Sandbox
	Log      *logx.Logger
	newAgent agentFactory
	// listModels is injectable so the menu can be tested without a network.
	listModels func(ctx context.Context, baseURL, apiKey string) ([]string, error)

	// session is the conversation Plan mode continues across turns. It lives on the runner,
	// not on the planner, precisely because each turn builds a new planner: a session created
	// per planner would be a session per question, which is the amnesia this exists to remove.
	//
	// Guarded by sessionMu: the interface can cancel a run and start another, and a session
	// is not safe to rewrite while a turn is reading it.
	sessionMu sync.Mutex
	session   *session.Session

	// pending is the questions the last turn asked and the request they clarify, waiting for
	// the interface to open the window on them. Guarded by pendingMu for the same reason the
	// transcript is: a run can finish while the interface is reading.
	pendingMu     sync.Mutex
	pending       []agent.AskItem
	pendingOrigin string

	// transcript is the Task-mode conversation, and it lives here for the same reason the
	// plan session does: a turn builds a NEW agent, so anything kept on the agent is thrown
	// away when the turn ends. Keeping it on the runner is what turns a series of one-shot
	// tasks into a conversation the user can build on.
	//
	// Guarded because the background goroutine running the turn appends to it while the
	// interface may read it.
	transcriptMu sync.Mutex
	transcript   []agent.DialogueTurn
	// lib is the procedure library, resolved on first use.
	lib *skills.Library

	// rewardMu guards the ledger and the last attribution.
	//
	// The attribution is kept between turns because a verdict arrives AFTER the turn that
	// earned it: the user types /good or /bad as the next thing they do, and by then the run
	// that read the skills has finished. Holding it here is what connects the two.
	rewardMu sync.Mutex
	reward   *reward.Ledger
	lastUsed map[string]int
	lastTask string
}

// NewAppRunner creates the production runner.
func NewAppRunner(out, errs io.Writer, cfg config.Config, engine *llm.Client, box *sandbox.Sandbox, log *logx.Logger) *AppRunner {
	return &AppRunner{
		Out: out, Err: errs, Cfg: cfg, Engine: engine, Box: box, Log: log,
		newAgent:   defaultAgentFactory,
		listModels: llm.ListModels,
	}
}

// Config returns the current configuration.
func (r *AppRunner) Config() config.Config { return r.Cfg }

// SetReasoning changes the in-memory reasoning level.
func (r *AppRunner) SetReasoning(level string) {
	r.Cfg.LLM.Reasoning.Level = level
	r.Cfg.LLM.Reasoning.Enabled = level != "off"
}

// engine returns the injected engine if it exists, otherwise it builds one from
// the current configuration. This lets the TUI start with no engine (for
// example when there is no configuration file yet) and still run plan/task when
// the user chooses them.
func (r *AppRunner) engine() (*llm.Client, error) {
	if r.Engine != nil {
		return r.Engine, nil
	}
	return llm.New(r.Cfg.LLM, r.Log)
}

// RunPlan executes the read-only planner and writes the final answer to Out.
func (r *AppRunner) RunPlan(ctx context.Context, prompt string, progress func(string, ...any)) (string, error) {
	engine, err := r.engine()
	if err != nil {
		return "", err
	}
	cfg := r.Cfg
	cfg.Agent.ReadOnly = true
	ag := r.newAgent(cfg, r.Log, engine, r.Box, nil, true)
	planner := plan.New(engine, ag).
		WithTimeout(planDefaultTimeout(r.Cfg)).
		WithLoops(planDefaultLoops(r.Cfg)).
		WithTrace(progress).
		WithStream(func(s string) {
			progress("%s", s)
		}).
		WithAnswer(func(s string) {}).
		// The conversation is owned by the RUNNER, and handed to the planner built for
		// this turn. A planner is created per turn — that is how this interface has always
		// worked — so a session created inside one would be a session per question, which
		// is the amnesia this feature exists to remove.
		WithSessionPolicy(
			r.Cfg.LLM.Model,
			r.Cfg.LLM.Session.ContextWindow,
			r.Cfg.LLM.Session.Reserve,
			r.Cfg.LLM.Session.CompactAt,
			r.Cfg.LLM.Session.KeepRecent,
		).
		WithSession(r.conversation(engine)).
		WithLibrary(r.library()).
		// The ledger is installed so the search can break ties by what has worked, and so the
		// planner can report which skills this turn read. Both are needed: a verdict has to
		// land on specific skills, and only the planner knows which ones.
		WithReward(r.rewardOrNil())
	answer, err := planner.Run(ctx, prompt)
	// The skills this turn consulted are recorded whatever the outcome: a turn that failed
	// still tells the user which procedure was in play, and that is exactly the turn they are
	// most likely to mark.
	r.rememberUsage(planner.Consulted(), prompt)
	if err != nil {
		return "", err
	}
	return answer, nil
}

// conversation returns the session Plan mode continues in, creating it on first use.
//
// The system prompt is the planner's own, so the conversation opens with exactly the
// instruction the model would have received anyway; the summariser is the engine itself,
// because compacting is a model call like any other and needs no separate configuration.
func (r *AppRunner) conversation(engine session.Summariser) *session.Session {
	r.sessionMu.Lock()
	defer r.sessionMu.Unlock()
	if r.session == nil {
		s := session.New(r.Cfg.LLM.Model, plan.SystemPrompt, r.Cfg.LLM.Session.ContextWindow)
		// The configuration wins where it says anything, so an operator who knows their
		// server is configured lower is obeyed. A zero means "unset" and keeps the default.
		if r.Cfg.LLM.Session.Reserve > 0 {
			s.Reserve = r.Cfg.LLM.Session.Reserve
		}
		if r.Cfg.LLM.Session.CompactAt > 0 {
			s.CompactAt = r.Cfg.LLM.Session.CompactAt
		}
		if r.Cfg.LLM.Session.KeepRecent > 0 {
			s.KeepRecent = r.Cfg.LLM.Session.KeepRecent
		}
		s.Summariser = engine
		r.session = s
	}
	return r.session
}

// library is the procedure library the skill tools read and write.
//
// It is resolved once and kept: the directory does not change during a session, and creating
// it per turn would be a filesystem call for nothing.
func (r *AppRunner) library() *skills.Library {
	r.sessionMu.Lock()
	defer r.sessionMu.Unlock()
	if r.lib == nil {
		dir := r.Cfg.Skills.Dir
		if dir == "" {
			// The home, not the working directory: the library is starlight's own state, and a
			// configuration that names no directory must not scatter it through the project the
			// user happens to be in.
			dir = config.Default().Skills.Dir
		}
		lib := skills.New(dir)
		if r.Cfg.Skills.MaxFileBytes > 0 {
			lib.MaxFileBytes = r.Cfg.Skills.MaxFileBytes
		}
		// The library reads the ledger for its tie-breaks. A ledger that cannot be opened is
		// not fatal: the search then ranks exactly as it did before, which is a working
		// library rather than a broken feature.
		if led := r.rewardOrNil(); led != nil {
			lib.Scorer = led
		}
		r.lib = lib
	}
	return r.lib
}

// rewardOrNil returns the ledger, creating it on first use.
//
// It lives NEXT TO the library, in the same directory, so one thing to copy or back up carries
// both the procedures and what has been learned about them.
func (r *AppRunner) rewardOrNil() *reward.Ledger {
	r.rewardMu.Lock()
	defer r.rewardMu.Unlock()
	if r.reward == nil {
		dir := r.Cfg.Skills.Dir
		if dir == "" {
			// Same reasoning as the library above: the ledger lives with the skills, under the
			// home.
			dir = config.Default().Skills.Dir
		}
		l, err := reward.Open(filepath.Join(dir, ".scores.json"))
		if err != nil {
			// Reported, not swallowed: the scores are the only record of what the user
			// thought of the library, and silently starting empty would hide that it was
			// lost. The feature stays off for this session rather than pretending.
			if r.Log != nil {
				r.Log.Warn("the reward ledger could not be read; value-based ranking is off",
					"error", err)
			}
			return nil
		}
		r.reward = l
	}
	return r.reward
}

// truncateLine bounds a string for a one-line report, on a rune boundary.
func truncateLine(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

// rememberUsage stores which skills the turn consulted, ready for the verdict that follows.
func (r *AppRunner) rememberUsage(used map[string]int, task string) {
	r.rewardMu.Lock()
	defer r.rewardMu.Unlock()
	r.lastUsed = used
	r.lastTask = task
}

// recordVerdict applies a verdict to the skills of the last turn and saves it.
//
// Everything the user needs to know is said in the chat, including the case where there is
// nothing to apply it to: a verdict that quietly did nothing would make the feature look like
// it works when it has nothing to learn from.
func (r *AppRunner) RecordVerdict(good bool, note string) string {
	led := r.rewardOrNil()
	if led == nil {
		return "the reward ledger is unavailable, so the verdict was not recorded."
	}
	r.rewardMu.Lock()
	used := make(map[string]int, len(r.lastUsed))
	for k, v := range r.lastUsed {
		used[k] = v
	}
	task := r.lastTask
	r.rewardMu.Unlock()

	names := make([]string, 0, len(used))
	for n := range used {
		names = append(names, n)
	}
	sort.Strings(names) // deterministic order for the ledger file

	err := led.Attribute(names, used, good, note)
	switch {
	case errors.Is(err, reward.ErrNoSkill):
		// Honest and specific: the turn did not consult the library, so there is no skill for
		// the verdict to land on. Saying which turn it was helps the user see why.
		msg := "no skill took part in the last turn, so there was nothing to learn from it."
		if strings.TrimSpace(task) != "" {
			msg += "\n(last turn: " + truncateLine(task, 90) + ")"
		}
		msg += "\nThe value moves when a turn reads a skill — the library is what this learns about."
		return msg
	}
	// No other error case: Attribute returns ErrNoSkill or nil and nothing else, so a branch
	// for "some other failure" would be unreachable. A new error from it would be caught here
	// by the compiler the moment it is added, because this switch is exhaustive over the cases
	// that exist.
	if err := led.Save(); err != nil {
		return fmt.Sprintf("the verdict was applied but could not be saved: %v", err)
	}

	verdict := "good"
	if !good {
		verdict = "bad"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "recorded: %s", verdict)
	if strings.TrimSpace(note) != "" {
		fmt.Fprintf(&b, " — %q", truncateLine(note, 120))
	}
	fmt.Fprintf(&b, "\napplied to %d skill(s), by how much the turn leaned on each:", len(names))
	for _, n := range names {
		s, _ := led.Get(n)
		fmt.Fprintf(&b, "\n  %s: value %+.2f (%d good, %d bad)", n, s.Value, s.Good, s.Bad)
	}
	if !good && strings.TrimSpace(note) == "" {
		b.WriteString("\n\nA note would make this fixable: /bad <what was wrong> tells the agent which step to repair, instead of only that it did not work.")
	}
	return b.String()
}

// RewardReport renders what the library has learned, worst first.
func (r *AppRunner) RewardReport() string {
	led := r.rewardOrNil()
	if led == nil {
		return "the reward ledger is unavailable: no verdicts have been recorded."
	}
	entries := led.Sorted()
	if len(entries) == 0 {
		return "no verdicts recorded yet.\n\nMark a turn with /good or /bad [what was wrong] right after it runs: the value lands on the skills that turn read, and it is the only thing that changes how the library is searched. An unmarked turn changes nothing."
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%d skill(s) with a verdict. Value is a running average in [-1, 1]; recent verdicts weigh more.\n", len(entries))
	for _, e := range entries {
		fmt.Fprintf(&b, "\n  %-28s value %+.2f   %d good / %d bad", e.Name, e.Score.Value, e.Score.Good, e.Score.Bad)
		if !e.Score.Updated.IsZero() {
			fmt.Fprintf(&b, "   last %s", e.Score.Updated.Format("2006-01-02 15:04"))
		}
		for _, n := range e.Score.Recent(3) {
			mark := "bad "
			if n.Good {
				mark = "good"
			}
			fixed := ""
			if n.Addressed {
				fixed = " [fixed]"
			}
			fmt.Fprintf(&b, "\n      %s  %q%s", mark, truncateLine(n.Text, 80), fixed)
		}
	}
	return b.String()
}

// ConversationSummary returns the session figures for the status bar.
//
// A zero Snapshot means no conversation has started, which the caller renders as no figure
// at all rather than as a percentage of nothing.
func (r *AppRunner) ConversationSummary() session.Snapshot {
	r.sessionMu.Lock()
	defer r.sessionMu.Unlock()
	if r.session == nil {
		return session.Snapshot{}
	}
	return r.session.Snapshot()
}

// ResetConversation drops the current session so the next turn starts a new one.
//
// It exists because a conversation that carries everything forward eventually carries things
// the user has finished with, and the honest way to leave a subject behind is to say so
// rather than to hope the compaction forgets it.
func (r *AppRunner) ResetConversation() {
	r.sessionMu.Lock()
	r.session = nil
	r.sessionMu.Unlock()
	// The Task-mode transcript goes with it. To the user this is ONE conversation — they do
	// not think of themselves as being in two modes — so /new has to clear both, or a fresh
	// start would still know what was said before.
	r.ResetTranscript()
}

// ConversationReport describes the conversation for the interface: how much of the window is
// in use, how it compacts, and what the carried summary holds.
//
// It is exposed because a session the user cannot see is a session they cannot trust: an
// agent whose history was folded without a word looks like one that simply forgot.
func (r *AppRunner) ConversationReport() string {
	r.sessionMu.Lock()
	defer r.sessionMu.Unlock()
	if r.session == nil {
		return "This conversation has not started yet. Ask something in Plan mode."
	}
	s := r.session
	var b strings.Builder
	fmt.Fprintf(&b, "model       %s\n", s.Model)
	fmt.Fprintf(&b, "context     %d tokens\n", s.Window)
	fmt.Fprintf(&b, "in use      %d tokens (%.0f%% of the usable window)\n", s.Tokens(), s.Used()*100)
	fmt.Fprintf(&b, "compaction  at %.0f%%, keeping the last %d messages\n", s.CompactAt*100, s.KeepRecent)
	fmt.Fprintf(&b, "messages    %d held", s.Len())
	if n := s.Compactions(); n > 0 {
		fmt.Fprintf(&b, ", %d compaction(s)", n)
	}
	b.WriteString("\n")
	if s.Summary() != "" {
		b.WriteString("\n--- carried summary ---\n")
		b.WriteString(s.Summary())
		b.WriteString("\n")
	}
	return b.String()
}

func planDefaultTimeout(cfg config.Config) time.Duration {
	if cfg.Sandbox.Timeout > 0 {
		return cfg.Sandbox.Timeout
	}
	return 120 * time.Second
}

func planDefaultLoops(cfg config.Config) int {
	if cfg.Agent.MaxRetries > 0 && cfg.Agent.MaxRetries <= 10 {
		return cfg.Agent.MaxRetries
	}
	return 5
}

// taskObserver is the interface the runner installs on the agent it builds: the
// agent reports its result back through these two hooks.
//
// It is an interface rather than a concrete *agent.Agent so the mapping below can
// be tested with a fake. The previous type assertion meant the whole result path
// was unreachable from a test, and the mapping had no coverage at all.
type taskObserver interface {
	AgentRunner
	// SetProgress receives the human-readable phase lines.
	SetProgress(func(format string, args ...any))
	// SetObserver receives the final task result.
	SetObserver(func(agent.TaskResult))
}

// summarise turns a task result into the sentence the chat shows.
func summarise(tr agent.TaskResult) string {
	switch {
	// A conversational turn. It is shown as what it is: a reply. There is no "done", no
	// "completed", no verdict — nothing ran, because there was nothing to run.
	//
	// It is checked before NeedsInput because a chat turn carries neither a question nor a
	// result, and before Pass because Pass is true for it (nothing failed) which would
	// otherwise render it as "completed: conversational reply".
	case tr.Kind == agent.KindChat:
		if strings.TrimSpace(tr.Reply) != "" {
			return tr.Reply
		}
		return "answered."
	// The agent is asking a question. This comes FIRST and is not a failure: nothing went wrong,
	// information is missing, and the user is the one holding it. Reporting it as "failed" — which
	// is where it landed before — tells the user they did something wrong when they only need to
	// say three more words.
	//
	// The question is shown with the assumption, so the user can confirm in one word instead of
	// writing the request again.
	case tr.NeedsInput:
		var b strings.Builder
		if tr.Question != "" {
			b.WriteString(tr.Question)
		} else {
			b.WriteString("No entendí del todo la petición.")
		}
		if tr.Assumption != "" {
			b.WriteString("\n\nSi no me dices otra cosa, asumiré: ")
			b.WriteString(tr.Assumption)
		}
		return b.String()
	case tr.Pass && tr.Summary != "":
		return tr.Summary
	case tr.Pass:
		out := fmt.Sprintf("completed: %s", tr.Reason)
		if tr.FinalAction != "" && tr.FinalAction != "none" {
			out += fmt.Sprintf(" (final action: %s)", tr.FinalAction)
		}
		return out
	default:
		return fmt.Sprintf("failed: %s", tr.Reason)
	}
}

// RunTask runs the agent with a single text task and returns a human-readable summary.
func (r *AppRunner) RunTask(ctx context.Context, task string, progress func(string, ...any)) (string, error) {
	engine, err := r.engine()
	if err != nil {
		return "", err
	}
	source, err := taskpkg.NewText(task, "tui")
	if err != nil {
		return "", err
	}
	var result string
	ag := r.newAgent(r.Cfg, r.Log, engine, r.Box, source, true)
	// The same library and the same ledger Plan mode uses.
	//
	// Both modes reach one shelf of procedures: a procedure written down while working is
	// available whichever mode does the work next, and a verdict lands on the same skills
	// either way. Two libraries would be two bodies of knowledge that drift apart.
	if lib := r.library(); lib != nil {
		if setter, ok := ag.(interface{ SetLibrary(*skills.Library) }); ok {
			setter.SetLibrary(lib)
		}
	}
	if led := r.rewardOrNil(); led != nil {
		if setter, ok := ag.(interface{ SetReward(*reward.Ledger) }); ok {
			setter.SetReward(led)
		}
	}
	// The conversation goes in before the run and comes back out after it. That round trip is
	// what makes the agent conversational: the turn that asked a question recorded it, and the
	// next turn reads it together with the user's answer, so "yes" means something.
	ag.SetTranscript(r.history())
	if o, ok := ag.(taskObserver); ok {
		o.SetProgress(progress)
		o.SetObserver(func(tr agent.TaskResult) {
			result = summarise(tr)
			// The questions are handed to the caller as STRUCTURE, not only as the sentence
			// above: the window needs the question, its assumption and its options to draw a
			// pickable list, and none of that survives being flattened into a string.
			if tr.NeedsInput && len(tr.Questions) > 0 {
				r.setPendingQuestions(tr.Questions, task)
			}
		})
	}
	runErr := ag.Run(ctx)
	// Kept even when the run failed: the attempt is part of the conversation, and dropping it
	// would make the agent repeat a mistake it cannot see.
	r.remember(ag.Transcript())
	// What this turn read from the library, ready for the verdict that follows it. Recorded on
	// the failure path too: a turn that failed is exactly the one a user marks.
	if consult, ok := ag.(interface{ Consulted() map[string]int }); ok {
		r.rememberUsage(consult.Consulted(), task)
	}
	if runErr != nil {
		return result, runErr
	}
	if result == "" {
		return "the task finished without reporting a result", nil
	}
	return result, nil
}

// setPendingQuestions records the questions a turn asked, for the interface to open a window on.
//
// It is stored on the runner because the turn that asked has already ended: the agent that asked
// is discarded when the run returns, so anything the window needs afterwards has to be kept here.
func (r *AppRunner) setPendingQuestions(items []agent.AskItem, origin string) {
	r.pendingMu.Lock()
	defer r.pendingMu.Unlock()
	r.pending = items
	r.pendingOrigin = origin
}

// TakePendingQuestions returns the questions waiting to be answered and clears them.
//
// Taking CLEARS: the window is opened once per question set. Without that, a repaint would
// reopen a window the user had already closed, and the interface would be arguing with them.
func (r *AppRunner) TakePendingQuestions() ([]agent.AskItem, string) {
	r.pendingMu.Lock()
	defer r.pendingMu.Unlock()
	items, origin := r.pending, r.pendingOrigin
	r.pending, r.pendingOrigin = nil, ""
	return items, origin
}

// history returns the Task-mode conversation to seed a new agent with.
func (r *AppRunner) history() []agent.DialogueTurn {
	r.transcriptMu.Lock()
	defer r.transcriptMu.Unlock()
	return append([]agent.DialogueTurn(nil), r.transcript...)
}

// remember stores the conversation a finished turn ended with.
func (r *AppRunner) remember(turns []agent.DialogueTurn) {
	r.transcriptMu.Lock()
	defer r.transcriptMu.Unlock()
	r.transcript = append([]agent.DialogueTurn(nil), turns...)
}

// ResetTranscript drops the Task-mode conversation, which is how /new leaves a subject
// behind. Plan mode's session is reset alongside it, because to the user they are the same
// conversation.
func (r *AppRunner) ResetTranscript() {
	r.transcriptMu.Lock()
	defer r.transcriptMu.Unlock()
	r.transcript = nil
}

// RunConfig runs the first-run configuration wizard.
func (r *AppRunner) RunConfig(ctx context.Context) error {
	// The same default the first run uses, so the file the wizard writes is the one the program
	// looks for next time. With no HOME it falls back to the working directory.
	path := config.File()
	if path == "" {
		path = "./starlight.yaml"
	}
	_, err := onboard.Run(ctx, os.Stdin, r.Out, path, onboard.Answers{}, time.Now())
	return err
}

// RunModels builds the report of the active setup and the catalogue the provider
// publishes, and returns it as text. It is the caller that decides where the
// report goes: the chat view keeps it in its own scrollback, which is what lets
// it be rendered inside the panel instead of being written over the frame.
//
// It is the answer to "is what I configured actually reachable?", which a user
// cannot check from the menu otherwise. The key is reported as present/absent and
// never printed, so the screen can be shared safely.
func (r *AppRunner) RunModels(ctx context.Context) (string, error) {
	cfg := r.Cfg
	var b strings.Builder
	fmt.Fprintf(&b, "provider : %s\n", cfg.LLM.Provider)
	fmt.Fprintf(&b, "model    : %s\n", cfg.LLM.Model)
	fmt.Fprintf(&b, "base URL : %s\n", cfg.LLM.BaseURL)
	if cfg.LLM.APIKey == "" {
		fmt.Fprintf(&b, "api key  : MISSING (set %s)\n", config.ProviderKeyVariable(cfg.LLM.Provider))
	} else {
		fmt.Fprintf(&b, "api key  : present\n")
	}

	base := cfg.LLM.BaseURL
	if base == "" {
		base = onboard.DefaultBaseURL(cfg.LLM.Provider)
	}
	fmt.Fprintf(&b, "\nmodels published by %s:\n", base)

	lister := r.listModels
	if lister == nil {
		lister = llm.ListModels
	}
	models, err := lister(ctx, base, cfg.LLM.APIKey)
	if err != nil {
		// A listing failure must not look like a broken agent: say what failed
		// and still show the model the configuration will use.
		fmt.Fprintf(&b, "  could not read the catalogue: %v\n", err)
		fmt.Fprintf(&b, "  the configured model %q will still be used.\n", cfg.LLM.Model)
		// The error is reported in the text, not as a Go error: the report was
		// produced, and the caller has something useful to show.
		return b.String(), nil
	}
	for _, m := range models {
		mark := "  "
		if m == cfg.LLM.Model {
			mark = "* "
		}
		fmt.Fprintf(&b, "  %s%s\n", mark, m)
	}
	fmt.Fprintf(&b, "\n  (* is the model this configuration uses)\n")
	return b.String(), nil
}
