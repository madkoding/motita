// Package tui implements the interactive text-based user interface that is
// launched when starlight is run without arguments and without a configured
// task. It is deliberately built with the Go standard library only.
package tui

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/madkoding/starlight/internal/agent"
	"github.com/madkoding/starlight/internal/config"
	"github.com/madkoding/starlight/internal/llm"
	"github.com/madkoding/starlight/internal/logx"
	"github.com/madkoding/starlight/internal/onboard"
	"github.com/madkoding/starlight/internal/plan"
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
}

// AgentRunner is the subset of *agent.Agent that the TUI needs.
type AgentRunner interface {
	Run(ctx context.Context) error
	RunCommand(ctx context.Context, command string) (string, int, error)
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
	// lib is the procedure library, resolved on first use.
	lib *skills.Library
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
		WithLibrary(r.library())
	answer, err := planner.Run(ctx, prompt)
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
			dir = "skills"
		}
		lib := skills.New(dir)
		if r.Cfg.Skills.MaxFileBytes > 0 {
			lib.MaxFileBytes = r.Cfg.Skills.MaxFileBytes
		}
		r.lib = lib
	}
	return r.lib
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
	defer r.sessionMu.Unlock()
	r.session = nil
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
	if o, ok := ag.(taskObserver); ok {
		o.SetProgress(progress)
		o.SetObserver(func(tr agent.TaskResult) { result = summarise(tr) })
	}
	if err := ag.Run(ctx); err != nil {
		return result, err
	}
	if result == "" {
		return "the task finished without reporting a result", nil
	}
	return result, nil
}

// RunConfig runs the first-run configuration wizard.
func (r *AppRunner) RunConfig(ctx context.Context) error {
	path := "./starlight.yaml"
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
