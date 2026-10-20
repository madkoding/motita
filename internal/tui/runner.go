// Package tui implements the interactive text-based user interface that is
// launched when motita is run without arguments and without a configured
// task. It is deliberately built with the Go standard library only.
package tui

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/madkoding/motita/internal/agent"
	"github.com/madkoding/motita/internal/config"
	"github.com/madkoding/motita/internal/llm"
	"github.com/madkoding/motita/internal/logx"
	"github.com/madkoding/motita/internal/notes"
	"github.com/madkoding/motita/internal/plan"
	"github.com/madkoding/motita/internal/procedures"
	"github.com/madkoding/motita/internal/projectskills"
	"github.com/madkoding/motita/internal/review"
	"github.com/madkoding/motita/internal/sandbox"
	"github.com/madkoding/motita/internal/session"
	taskpkg "github.com/madkoding/motita/internal/task"
	"github.com/madkoding/motita/internal/webfetch"
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
	// SetLLM changes the in-memory provider and/or model.
	SetLLM(provider, model string)
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
type agentFactory func(cfg config.Config, log *logx.Logger, engine *llm.Client, box *sandbox.Sandbox,
	source taskpkg.Source, interactive bool) AgentRunner

// defaultAgentFactory uses the real agent package.
func defaultAgentFactory(cfg config.Config, log *logx.Logger, engine *llm.Client, box *sandbox.Sandbox,
	source taskpkg.Source, interactive bool) AgentRunner {
	ag := agent.New(cfg, log, engine, box, source)
	ag.Interactive = interactive
	return ag
}

// AppRunner is the production implementation that calls the real layers.
type AppRunner struct {
	Out io.Writer
	Err io.Writer
	// Cfg and Engine are what the next turn runs with. They are guarded by cfgMu because a front
	// end changes them (the model, the reasoning level) while a turn may be running.
	Cfg    config.Config
	Engine *llm.Client
	cfgMu  sync.Mutex
	// uiLanguage is the ui.language setting saved through /language in this process.
	uiLanguage string
	Box        *sandbox.Sandbox
	Log        *logx.Logger
	newAgent   agentFactory
	// listModels and claudeModels are injectable so the menu can be tested without a network
	// or a claude CLI.
	listModels   func(ctx context.Context, baseURL, apiKey string) ([]string, error)
	claudeModels func(ctx context.Context) (llm.ClaudeCodeCatalogue, error)
	// endpoints remembers the base URL and key of each provider the session has used, so a
	// switch back restores what the configuration said (see switchEndpoint).
	endpoints map[string]llmEndpoint
	// taskEngines are the engines built for the kinds of work the models block assigns a model
	// of its own, keyed by kind. They belong to this runner - one session - and are dropped with
	// Engine whenever the settings they were built from change. Guarded by cfgMu.
	taskEngines map[string]*llm.Client

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
	pendingMu sync.Mutex
	pending   []agent.AskItem
	// report is the structured account of the last finished task, kept for TakeReport.
	report *agent.Report
	// asked is the questions the last finished turn asked, kept for TakeQuestions. Unlike pending
	// it holds only questions about the request, never proposals: a client that lays the report
	// out itself already has those.
	asked         []agent.AskItem
	pendingOrigin string

	// review is the background self-improvement fork, and reviewMu guards it together with
	// the counter below. It lives HERE and not on a planner because a planner is built per
	// turn: one installed on a single turn would review that turn and then never be seen
	// again, which is exactly the state the web interface was in — skills written by hand,
	// and nothing ever reviewing the conversation they came from.
	reviewMu sync.Mutex
	review   *review.Review
	// reviewIters accumulates the per-turn iteration count, because the budget the fork
	// keys on spans turns and only the owner of the conversation can hold it.
	reviewIters int

	// transcript is the Task-mode conversation, and it lives here for the same reason the
	// plan session does: a turn builds a NEW agent, so anything kept on the agent is thrown
	// away when the turn ends. Keeping it on the runner is what turns a series of one-shot
	// tasks into a conversation the user can build on.
	//
	// Guarded because the background goroutine running the turn appends to it while the
	// interface may read it.
	transcriptMu sync.Mutex
	// live is the agent of the task in flight, which Steer talks to.
	live       liveAgent
	transcript []agent.DialogueTurn
	// store is the procedure library and its ledger, resolved on first use.
	//
	// It is built by internal/procedures, which is also what the command line's task and plan
	// runs use. A front end having its own idea of what the library is, is how the interactive
	// interface ended up with one while the other two promised the model a library and then
	// answered that none was configured.
	store *procedures.Store
	// shared is the store before any project scope: the one UseStore installed or procedures()
	// built. A scope is always made from it, never from a store that is already scoped, so a
	// second SetProjectScope replaces the first instead of layering a project over another.
	shared *procedures.Store
	// projectScope is the project this runner's library belongs to, remembered even before a
	// store exists: the gateway tells a session which project it is in when the session is
	// created or restored, which can happen before anything has run a turn.
	//
	// Guarded by sessionMu, like store, because both are read while a turn runs.
	projectScope string
	// runtimeNote is the sentence added to the system prompt (how services are run, then the
	// project note), kept so a change can swap it. Guarded by sessionMu, like the session it edits.
	runtimeNote string
	// projectNote is what the gateway tells the agent about the project's other repositories,
	// added after the runtime sentence and swapped with it. Guarded by sessionMu too.
	projectNote string
	threads     func(string) string // see runner_equip.go

	// toolsHome is the HOME, inside the tools directory, the sandbox gives this session's
	// commands: one per project (see SetProjectScope), the shared one otherwise. rootDir is the
	// workspace the runner was born in - for a gateway, the root that holds every project and
	// session - which a command confined to a session's own folder may not read. Both are guarded
	// by cfgMu, like the sandbox they shape.
	toolsHome string
	rootDir   string

	// rewardMu guards the last attribution.
	//
	// The attribution is kept between turns because a verdict arrives AFTER the turn that
	// earned it: the user types /good or /bad as the next thing they do, and by then the run
	// that read the skills has finished. Holding it here is what connects the two.
	rewardMu sync.Mutex
	lastUsed map[string]int
	lastTask string

	// approver is how a consequential command reaches the user, installed by the interface.
	// Guarded like the transcript: a run reads it while the interface may set it.
	approverMu sync.Mutex
	approver   agent.Approver
}

// NewAppRunner creates the production runner.
func NewAppRunner(out, errs io.Writer, cfg config.Config, engine *llm.Client, box *sandbox.Sandbox,
	log *logx.Logger) *AppRunner {
	root := ""
	if dir := strings.TrimSpace(cfg.Agent.WorkspaceDir); dir != "" {
		root, _ = filepath.Abs(dir)
	}
	return &AppRunner{
		Out: out, Err: errs, Cfg: cfg, Engine: engine, Box: box, Log: log,
		newAgent:     defaultAgentFactory,
		claudeModels: llm.ClaudeCodeModels,
		rootDir:      root,
	}
}

// SetApprover stores the callback a consequential command is confirmed through, and passes it to
// the agent of every turn from then on.
//
// It lives on the RUNNER, not on the agent, for the same reason the transcript does: a turn
// builds a NEW agent, so anything installed on one is thrown away when it ends. A confirmation
// channel installed on a single agent would work for exactly one turn, which is the sort of bug
// that looks like "the agent stopped asking" much later.
func (r *AppRunner) SetApprover(fn agent.Approver) {
	r.approverMu.Lock()
	defer r.approverMu.Unlock()
	r.approver = fn
}

// approverOrNil returns the installed approver, or nil when the interface installed none.
func (r *AppRunner) approverOrNil() agent.Approver {
	r.approverMu.Lock()
	defer r.approverMu.Unlock()
	return r.approver
}

// soul returns the system prompt for this runner's conversations: the user's
// ~/.motita/SOUL.md if it exists, otherwise the embedded default. Both are
// followed by the operational prompt, so the personality changes but the
// procedure does not. The home directory is resolved once per call, which is
// fine because conversation() caches the session it creates.
func (r *AppRunner) soul() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return plan.SystemPrompt
	}
	return plan.ResolveSoul(home)
}

// approverInstaller is the optional half of AgentRunner: the ability to receive a confirmation
// channel.
//
// It is an OPTIONAL interface rather than a method on AgentRunner, following the same pattern the
// library and the reward ledger already use here. The reason is concrete: the many small agents
// the tests build never run a real turn and therefore never ask anything, and forcing every one
// of them to grow a method that does nothing would be noise in exchange for a guarantee they do
// not need. The real agent implements it, and the type assertion below fails closed — no
// installer means no confirmation channel, which means a consequential action is refused rather
// than run unreviewed.
type approverInstaller interface {
	SetApprover(agent.Approver)
}

// installApproverOn gives the agent the confirmation channel, when it can take one.
func installApproverOn(ag AgentRunner, fn agent.Approver) {
	if fn == nil {
		return
	}
	if setter, ok := ag.(approverInstaller); ok {
		setter.SetApprover(fn)
	}
}

// RunShell runs one command line a PERSON typed, under the same policy and sandbox as the
// agent's own commands, and confirms it through the approver it is handed.
//
// It builds an agent only to reach its policy and its executor: no model is called, so it works
// without a key and does not touch the conversation. The approver is a parameter and not the
// runner's installed one, because that one belongs to whichever run is in flight and a shell
// command typed meanwhile must not borrow its question.
func (r *AppRunner) RunShell(ctx context.Context, command string, approver agent.Approver) (string, int, error) {
	r.cfgMu.Lock()
	cfg, engine := r.Cfg, r.Engine
	r.cfgMu.Unlock()
	ag := r.newAgent(cfg, r.Log, engine, r.Box, nil, true)
	installApproverOn(ag, approver)
	return ag.RunCommand(ctx, command)
}

// Config returns the current configuration.
func (r *AppRunner) Config() config.Config {
	r.cfgMu.Lock()
	defer r.cfgMu.Unlock()
	return r.Cfg
}

// SetReasoning changes the in-memory reasoning level.
//
// It moves the LEVEL and the FLAG together, and that is the whole point of it being one call: the
// level arrives on its own from a front end, and a level left beside `enabled: false` is read by
// config.normalize as "nobody applied this" - it is turned back into the default and the request
// builders send nothing. The pair has one invariant: anything other than "off" means reasoning is
// on.
//
// The ENGINE is dropped exactly as SetLLM drops it, and for the same reason: the client given at
// construction was built from a snapshot of the settings, and it reads that snapshot for every
// request. Keeping it meant the level changed what the status line and the interface showed while
// the model was still asked at the old one - measured on a served gateway, `POST
// /sessions/{id}/reasoning high` left the body sent upstream at `reasoning_effort: "medium"`,
// while the same level through PATCH /config (which calls SetLLM first) arrived as "high".
//
// Both setters take cfgMu: a front end calls them while a turn of the same session may be reading
// the configuration, and a model or level written without the lock was a data race.
func (r *AppRunner) SetReasoning(level string) {
	r.cfgMu.Lock()
	defer r.cfgMu.Unlock()
	r.Cfg.LLM.Reasoning.Level = level
	r.Cfg.LLM.Reasoning.Enabled = level != "off"
	r.dropEngines()
}

// SetLLM changes the in-memory provider and/or model. An empty argument means "leave
// unchanged". The engine is built per turn from the current configuration, so the next
// turn uses the new values — but the cached engine is dropped when either field
// changes, so a caller that reads it back does not get the one built from the old
// settings.
func (r *AppRunner) SetLLM(provider, model string) {
	r.cfgMu.Lock()
	defer r.cfgMu.Unlock()
	if provider != "" && !strings.EqualFold(provider, r.Cfg.LLM.Provider) {
		r.switchEndpoint(provider)
		r.Cfg.LLM.Provider = provider
		r.dropEngines()
	}
	if model != "" {
		r.Cfg.LLM.Model = model
		r.dropEngines()
	}
}

// switchEndpoint moves the key and the base URL along with a provider switch. Called with cfgMu held.
//
// They used to stay behind: switching from openai to anthropic in the model picker sent the
// next request to api.openai.com with the OpenAI key and an Anthropic model id, and switching
// to Ollama sent the OpenAI key to ollama.com. Each provider now gets its own: the endpoint and
// key it was configured with, if it was the configured one, otherwise its default endpoint and
// the key its own variable holds (or none, for a stored login or a keyless provider).
func (r *AppRunner) switchEndpoint(provider string) {
	if r.endpoints == nil {
		r.endpoints = map[string]llmEndpoint{}
	}
	cur := strings.ToLower(r.Cfg.LLM.Provider)
	// The outgoing provider's settings are remembered as they are now.
	r.endpoints[cur] = llmEndpoint{baseURL: r.Cfg.LLM.BaseURL, apiKey: r.Cfg.LLM.APIKey}
	ep := r.endpointOf(provider)
	r.Cfg.LLM.BaseURL, r.Cfg.LLM.APIKey = ep.baseURL, ep.apiKey
}

// RefreshProvider replaces the key and the endpoint one provider runs with, after they were
// changed in the settings: the session that runs on it takes them on its next turn, and one that
// switches to it later gets them instead of what it remembered.
func (r *AppRunner) RefreshProvider(provider, apiKey, baseURL string) {
	r.cfgMu.Lock()
	defer r.cfgMu.Unlock()
	delete(r.endpoints, strings.ToLower(provider))
	if strings.EqualFold(r.Cfg.LLM.Provider, provider) {
		r.Cfg.LLM.APIKey, r.Cfg.LLM.BaseURL = apiKey, baseURL
		r.dropEngines()
	}
}

// llmEndpoint is the base URL and key one provider runs with.
type llmEndpoint struct {
	baseURL, apiKey string
}

func sameOpenAIAccount(a, b string) bool {
	isOpenAI := func(p string) bool { return p == "openai" || p == "codex" }
	return isOpenAI(a) && isOpenAI(b)
}

// SetWorkspace changes the directory the agent works in. A session that
// belongs to a project runs with its workspace set to the project's directory,
// so the agent is confined to that folder for every command it runs.
//
// The sandbox is rebuilt so the new directory takes effect for subsequent runs.
func (r *AppRunner) SetWorkspace(dir string) {
	if dir == "" {
		return
	}
	r.cfgMu.Lock()
	r.Cfg.Agent.WorkspaceDir = dir
	cfg, home, root := r.Cfg, r.toolsHome, r.rootDir
	r.cfgMu.Unlock()
	// Rebuild the sandbox with the new workspace directory so commands
	// actually execute there, not in the directory the runner was born with.
	op := sandbox.Options{
		Dir: cfg.Agent.WorkspaceDir,
		Limits: sandbox.Limits{
			MemoryMB:      cfg.Sandbox.MemoryMB,
			CPUSeconds:    cfg.Sandbox.CPUSeconds,
			Processes:     cfg.Sandbox.Processes,
			OpenFiles:     cfg.Sandbox.OpenFiles,
			MaxFileSizeMB: cfg.Sandbox.MaxFileSizeMB,
		},
		CgroupRoot:    cfg.Sandbox.CgroupRoot,
		Timeout:       cfg.Sandbox.Timeout,
		MaxOutputKB:   cfg.Sandbox.MaxOutputKB,
		Keep:          cfg.Sandbox.KeepEphemeral,
		ToolsDir:      cfg.Sandbox.ToolsDir,
		ConfineWrites: cfg.Sandbox.ConfineWrites,
		FullDisk:      cfg.Sandbox.FullDisk(),
		GitAuthDir:    config.AuthDir(),
		GitHome:       config.HomeDir(),
		GPG:           sandbox.GPGAccess(cfg.Sandbox.GPGSigning, config.HomeDir(), r.Log),
		HiddenPaths:   config.SecretFiles(""),
		Home:          home,
		PrivateRoots:  privateRoots(root, dir),
		Log:           r.Log,
	}
	if r.Box != nil {
		// The sandbox this one replaces may hide more: the file the run was configured from.
		op.HiddenPaths = r.Box.HiddenPaths()
	}
	switch cfg.Sandbox.Kind {
	case "none":
		op.Limits = sandbox.Limits{}
		op.UseCgroups = false
		op.UseChroot = false
	case "chroot":
		op.UseChroot = true
		op.Root = cfg.Sandbox.Root
	default:
		op.UseCgroups = false
	}
	if cfg.Sandbox.User != "" {
		if uid, gid, err := config.ParseUser(cfg.Sandbox.User); err == nil {
			op.Uid, op.Gid, op.DropPrivs = uid, gid, true
		}
	}
	op.Limits.NoNetwork = cfg.Sandbox.IsolateNetwork
	box, err := sandbox.New(op)
	if err != nil {
		// The old sandbox is NOT kept as it is: its commands would run in the directory the runner
		// was born with - for a gateway, the root that holds every project - while the file
		// actions follow the new one. The same isolation moved to the new directory keeps the
		// two together.
		if r.Log != nil {
			r.Log.Warn("the sandbox could not be rebuilt for the new workspace; the current one moves there",
				"dir", dir, "error", err.Error())
		}
		if r.Box == nil {
			return
		}
		box = r.Box.WithDir(dir)
	}
	r.cfgMu.Lock()
	r.Box = box
	r.cfgMu.Unlock()
}

// SandboxScope is the HOME this session's commands get and the folders kept private from them,
// for the settings page to show.
func (r *AppRunner) SandboxScope() (home string, private []string) {
	r.cfgMu.Lock()
	box := r.Box
	r.cfgMu.Unlock()
	return box.Home(), box.PrivateRoots()
}

// privateRoots is the root the runner was born in, when the session now works somewhere else
// inside it or beside it: the other projects and sessions there are none of this one's business.
func privateRoots(root, dir string) []string {
	// Abs fails only when the working directory is gone, and then dir is compared as it is.
	abs, _ := filepath.Abs(dir)
	if root == "" || filepath.Clean(abs) == root {
		return nil
	}
	return []string{root}
}

// SetDiskAccess changes how much of the disk the agent reaches: "full" for the whole disk, anything
// else for the workspace alone. The next command and the next turn's file actions follow it; a
// command already running keeps the rules it started with.
func (r *AppRunner) SetDiskAccess(mode string) {
	r.cfgMu.Lock()
	r.Cfg.Sandbox.DiskAccess = mode
	box := r.Box
	r.cfgMu.Unlock()
	if box != nil {
		box.SetFullDisk(mode == config.DiskFull)
	}
}

// SetAskUnconfinedRuns says whether the agent asks before the project's own code runs where
// nothing confines a command (sandbox.ask_unconfined_runs). The next command follows it.
func (r *AppRunner) SetAskUnconfinedRuns(ask bool) {
	r.cfgMu.Lock()
	r.Cfg.Sandbox.AskUnconfinedRuns = ask
	r.cfgMu.Unlock()
}

// SetAutoApplySuggestions says whether the suggestions of a finished task are applied without
// asking (agent.auto_apply_suggestions). The next report follows it.
func (r *AppRunner) SetAutoApplySuggestions(on bool) {
	r.cfgMu.Lock()
	r.Cfg.Agent.AutoApplySuggestions = on
	r.cfgMu.Unlock()
}

// SetDesktopControl says whether the agent may see and drive this computer's desktop
// (agent.desktop_control). The next turn follows it.
func (r *AppRunner) SetDesktopControl(on bool) {
	r.cfgMu.Lock()
	r.Cfg.Agent.DesktopControl = on
	r.cfgMu.Unlock()
}

// setBoxNetwork is the sandbox's SetIsolateNetwork, as a variable so a test can choose what the
// machine answers.
var setBoxNetwork = (*sandbox.Sandbox).SetIsolateNetwork

// networkIsolated reports whether the commands run without a network right now.
func (r *AppRunner) networkIsolated() bool {
	r.cfgMu.Lock()
	defer r.cfgMu.Unlock()
	return r.Cfg.Sandbox.IsolateNetwork
}

// SetIsolateNetwork says whether the commands run without a network (sandbox.isolate_network). The
// next command follows it; a command already running keeps what it started with. Where the machine
// cannot isolate, the sandbox stays without it and says why in the log.
func (r *AppRunner) SetIsolateNetwork(on bool) {
	r.cfgMu.Lock()
	r.Cfg.Sandbox.IsolateNetwork = on
	box := r.Box
	r.cfgMu.Unlock()
	if box == nil {
		return
	}
	if err := setBoxNetwork(box, on); err != nil && r.Log != nil {
		r.Log.Warn("the commands keep the network: this machine cannot isolate them", "error", err.Error())
	}
}

// SetProjectNote sets what the agent is told about the project it works in (its other
// repositories), at the end of the system prompt. An empty note clears it.
func (r *AppRunner) SetProjectNote(note string) {
	r.sessionMu.Lock()
	defer r.sessionMu.Unlock()
	r.projectNote = note
	if r.session != nil {
		r.applyRuntimeNote()
	}
}

// applyRuntimeNote rewrites the runtime and project sentences at the end of the system prompt.
// The caller holds sessionMu and has a session.
func (r *AppRunner) applyRuntimeNote() {
	note := r.projectContext()
	r.session.System = strings.TrimSuffix(r.session.System, r.runtimeNote) + note
	r.runtimeNote = note
}

// servicesNote is told to every session: a project's services are started in a box, a container
// no other machine can reach, and never with a published port.
const servicesNote = "\n\nTo run a service of this project (a server, a database, a compose stack), start it with " +
	"the box_up action, never with `podman run` or `docker run` and never with a published port: the box is " +
	"a container only motita can reach, and it is removed when the run ends."

// projectContext is the runtime and project sentences, as they go at the end of the system
// prompt. The caller holds sessionMu.
func (r *AppRunner) projectContext() string {
	return servicesNote + r.projectNote
}

// SetProjectScope makes this runner's procedure library belong to ONE project.
//
// It is the piece the whole feature rests on. Every conversation in a gateway process shares
// one library (that sharing is deliberate — one ledger, one truth), so scoping cannot be a
// property of the library itself: it has to be a property of the RUNNER, applied when the
// gateway tells a session which project it belongs to.
//
// The library is COPIED rather than mutated, and that is the point: mutating the shared one
// would scope every other conversation in the process to the project that happened to be set
// last, which is a bug that would only show up once two projects were open at once.
//
// An empty projectDir is the "no project" case and clears the scope, leaving the shared shelf.
func (r *AppRunner) SetProjectScope(projectDir string) {
	// The HOME follows the project too: what one project's tools leave there (logins, caches,
	// history) must not be what another project's commands find.
	home := ""
	if projectDir != "" {
		home = sandbox.ProjectHome(projectskills.Key(projectDir))
	}
	r.cfgMu.Lock()
	r.toolsHome = home
	if r.Box != nil {
		r.Box = r.Box.WithHome(home)
	}
	r.cfgMu.Unlock()

	r.sessionMu.Lock()
	defer r.sessionMu.Unlock()
	r.projectScope = projectDir
	if r.shared == nil {
		r.shared = r.store
	}
	if r.shared == nil {
		// Nothing has built the library yet. The scope is remembered and applied when one is
		// built, rather than building one here: a runner that has never run a turn has no
		// business creating a directory.
		return
	}
	r.store = procedures.Scoped(r.shared, projectDir)
}

// engine returns the injected engine if it exists, otherwise it builds one from
// the current configuration. This lets the TUI start with no engine (for
// example when there is no configuration file yet) and still run plan/task when
// the user chooses them.
func (r *AppRunner) engine() (config.Config, *llm.Client, error) {
	r.cfgMu.Lock()
	cfg, cached := r.Cfg, r.Engine
	r.cfgMu.Unlock()
	if cached != nil {
		return cfg, cached, nil
	}
	engine, err := llm.New(cfg.LLM, r.Log)
	return cfg, engine, err
}

// SetReview installs the background self-improvement fork.
//
// It is installed on the RUNNER rather than on a planner for the reason the session is: each
// turn builds a new planner, so a fork handed to one would review that turn and never be seen
// again. Nil turns the feature off, which is what a caller that builds its own planner gets.
//
// Only PLAN mode carries the fork. It is the one path here that builds a planner at all: Task
// mode goes through internal/agent, whose actions reach the same library but which exposes no
// planner to install a review on. Wiring the fork into Task mode is a change to internal/agent
// and does not belong to this feature.
func (r *AppRunner) SetReview(v *review.Review) {
	r.reviewMu.Lock()
	defer r.reviewMu.Unlock()
	r.review = v
}

// reviewOrNil returns the installed fork, nil when there is none.
func (r *AppRunner) reviewOrNil() *review.Review {
	r.reviewMu.Lock()
	defer r.reviewMu.Unlock()
	return r.review
}

// countReviewIters folds a finished turn's iteration count into the running budget.
//
// A turn that called save_skill reports zero, so a turn that already wrote a skill down brings
// the next review no closer — the planner resets it for exactly that reason.
func (r *AppRunner) countReviewIters(turn int) int {
	r.reviewMu.Lock()
	defer r.reviewMu.Unlock()
	r.reviewIters += turn
	return r.reviewIters
}

// CompleteIntent is auto mode's model call: one completion, no tools, for the message the rules
// could not route. It runs on the model the models block gives small calls (summary), the
// session's own when it gives none.
func (r *AppRunner) CompleteIntent(ctx context.Context, system, user string) (string, error) {
	_, engine, err := r.engineFor(config.TaskSummary)
	if err != nil {
		return "", err
	}
	return engine.Complete(ctx, []llm.Message{{Role: "system", Content: system}, {Role: "user", Content: user}})
}

// liveAnswerEvery is the least time between two snapshots of the answer being written: often enough
// for the page to show it growing, rarely enough that a long answer is not sent whole a thousand
// times. A variable so a test can send every one.
var liveAnswerEvery = 100 * time.Millisecond

// livePlanText is where the planner's streamed text goes: as a SNAPSHOT of the answer being written
// (agent.AnswerPrefix), never as progress lines of its own.
//
// Reported from real use: "instead of showing the text appearing in the answer, it piled up as
// actions". Each fragment of the streamed answer was forwarded as a progress line, so the web filed
// every one as a step of the turn's trail - the answer, the same text the voice reads aloud, cut
// into dozens of "actions". A snapshot is shown growing where the answer will be instead (the
// terminal drops it), and the answer still arrives whole when the plan ends. A tool announcement
// starts a new snapshot: the text before it was the model talking while it worked, and the tool
// itself is already reported through the trace.
func livePlanText(progress func(string, ...any)) func(string) {
	var live strings.Builder
	var last time.Time
	return func(s string) {
		if strings.HasPrefix(s, "[using tool: ") {
			live.Reset()
			progress("%s", agent.AnswerPrefix)
			return
		}
		live.WriteString(s)
		if now := time.Now(); now.Sub(last) >= liveAnswerEvery {
			last = now
			progress("%s%s", agent.AnswerPrefix, live.String())
		}
	}
}

// planSteps reports a plan run's commands and reasoning as the progress lines a task run writes for
// the same things, so the web's terminal and thinking drawers, and the turn's checkpoint, fill the
// same way whichever mode answered.
func planSteps(progress func(string, ...any)) plan.Steps {
	return plan.Steps{
		Command: func(command string) { progress("running: %s", command) },
		Output:  func(exit int, output string) { progress("output (exit %d):\n%s", exit, output) },
		Thought: func(text string) { progress("%s%s", agent.ThinkingPrefix, text) },
		Live:    func(text string) { progress("%s%s", agent.LivePrefix, text) },
	}
}

// RunPlan executes the read-only planner and writes the final answer to Out.
func (r *AppRunner) RunPlan(ctx context.Context, prompt string, progress func(string, ...any)) (string, error) {
	cfg, engine, err := r.engineFor(config.TaskPlan)
	if err != nil {
		return "", err
	}
	readOnly := cfg
	readOnly.Agent.ReadOnly = true
	ag := r.newAgent(readOnly, r.Log, engine, r.Box, nil, true)
	// Plan mode is read-only, so the policy refuses rather than asks — but the approver is
	// installed anyway: the mode is a CONFIGURATION, and a run whose configuration changes under
	// it must not silently lose the channel that answers its questions.
	installApproverOn(ag, r.approverOrNil())
	planner := plan.New(engine, ag).
		WithTimeout(planDefaultTimeout(cfg)).
		WithLoops(planDefaultLoops(cfg)).
		WithTrace(progress).
		WithStream(livePlanText(progress)).
		WithSteps(planSteps(progress)).
		WithAnswer(func(s string) {
			// The answer is already streamed through progress; nothing else to print.
		}).
		// The conversation is owned by the RUNNER, and handed to the planner built for
		// this turn. A planner is created per turn — that is how this interface has always
		// worked — so a session created inside one would be a session per question, which
		// is the amnesia this feature exists to remove.
		WithSessionPolicy(
			cfg.LLM.Model,
			cfg.LLM.Session.ContextWindow,
			cfg.LLM.Session.Reserve,
			cfg.LLM.Session.CompactAt,
			cfg.LLM.Session.KeepRecent,
		).
		WithSession(r.conversation(cfg, r.summaryEngine(engine))).
		WithLibrary(r.library()).
		// The ledger is installed so the search can break ties by what has worked, and so the
		// planner can report which skills this turn read. Both are needed: a verdict has to
		// land on specific skills, and only the planner knows which ones.
		WithReward(r.rewardOrNil()).
		// The telemetry sidecar, so the skill tools count what this turn read and wrote.
		WithUsage(r.procedures().Usage).
		// And the fork itself, which is the only way the conversation this turn ran gets
		// reviewed at all. Both come from the RUNNER because a planner does not outlive
		// its turn.
		WithReview(r.reviewOrNil()).
		WithCodeIndex(cfg.CodeIndex.Enabled, cfg.CodeIndex.Options()).
		WithNotes(notesVault()).
		// Reading a page follows the commands' network, which Settings → Security can change
		// while the session is open, so it is asked at every fetch.
		WithWeb(webfetch.New(r.networkIsolated))
	answer, err := planner.Run(ctx, prompt)
	// The turn's iteration count is folded into the runner's running total whatever the
	// outcome: it is what decides whether the fork that just looked at this turn fires
	// again on the next one.
	r.countReviewIters(planner.ItersSinceSkill())
	// The skills this turn consulted are recorded whatever the outcome: a turn that failed
	// still tells the user which procedure was in play, and that is exactly the turn they are
	// most likely to mark.
	r.rememberUsage(planner.Consulted(), prompt)
	// And the turn itself goes into the conversation a front end draws. This is the SAME blank
	// screen the Task path already fixed, and it has to be done here because the two paths
	// record separately: the session is what the model is given, the transcript is what the user
	// sees, and joining the first without the second leaves a client that reconnects looking at
	// a conversation in which the question was never asked.
	//
	// Recorded on the FAILURE path too, and for the same reason the Task path keeps a failed
	// run: the attempt is part of the conversation, and dropping it makes the user repeat
	// themselves to an agent that cannot see what they already asked.
	//
	// The answer is recorded as empty rather than as an error string when the run failed: the
	// transcript says what was ASKED either way, and inventing a reply the model never gave
	// would be worse than showing none.
	r.rememberTurn(prompt, answer, err)
	if err != nil {
		return "", err
	}
	return answer, nil
}

// rememberTurn appends one finished turn to the conversation, keeping what is already there.
//
// It APPENDS rather than replacing, unlike remember() on the Task path: that one takes the
// agent's whole transcript, which is authoritative for a task because the agent owns it. Here the
// planner holds its own session in a different shape, so the runner adds the turn it can see
// instead of overwriting a conversation it did not build.
func (r *AppRunner) rememberTurn(question, answer string, runErr error) {
	if strings.TrimSpace(question) == "" {
		return
	}
	if runErr != nil {
		answer = ""
	}
	r.transcriptMu.Lock()
	defer r.transcriptMu.Unlock()
	r.transcript = append(r.transcript, agent.DialogueTurn{
		User:  question,
		Agent: answer,
		Kind:  "plan",
	})
}

// conversation returns the session Plan mode continues in, creating it on first use.
//
// The system prompt is the planner's own — the soul (embedded or from ~/.motita/SOUL.md)
// followed by the operational instructions — so the conversation opens with exactly the
// instruction the model would have received anyway; the summariser is the engine itself,
// because compacting is a model call like any other and needs no separate configuration.
//
// The summariser is the engine of THIS turn, set on every call: the model or the reasoning level
// may have changed since the session was created, and compaction should use what the turn uses.
func (r *AppRunner) conversation(cfg config.Config, engine session.Summariser) *session.Session {
	r.sessionMu.Lock()
	defer r.sessionMu.Unlock()
	if r.session == nil {
		s := session.New(cfg.LLM.Model, r.soul(), cfg.LLM.Session.ContextWindow)
		// The configuration wins where it says anything, so an operator who knows their
		// server is configured lower is obeyed. A zero means "unset" and keeps the default.
		if cfg.LLM.Session.Reserve > 0 {
			s.Reserve = cfg.LLM.Session.Reserve
		}
		if cfg.LLM.Session.CompactAt > 0 {
			s.CompactAt = cfg.LLM.Session.CompactAt
		}
		if cfg.LLM.Session.KeepRecent > 0 {
			s.KeepRecent = cfg.LLM.Session.KeepRecent
		}
		r.session = s
		r.runtimeNote = ""
		r.applyRuntimeNote()
	}
	r.session.Summariser = engine
	return r.session
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
		return questionText(tr)
	// The structured report is shown as plain text: this is the answer the terminal draws, and the
	// fallback for any client that does not lay the report out itself.
	case tr.Pass && tr.Report != nil && tr.Report.Summary != "":
		return tr.Report.Text()
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

// questionText is the question of a turn that needs the user, with the assumption the agent
// would proceed on.
func questionText(tr agent.TaskResult) string {
	// Several questions are all written out, so the conversation above the window (and any client
	// that shows only this text) carries every one, not just the first.
	if listed := agent.AskedText(tr.Questions); listed != "" {
		return listed
	}
	var b strings.Builder
	if tr.Question != "" {
		b.WriteString(tr.Question)
	} else {
		b.WriteString("I did not fully understand the request.")
	}
	if tr.Assumption != "" {
		// The assumption is shown ON ITS OWN, with no sentence of the interface's invention
		// in front of it — the same rule the QUESTION above already follows, for the same
		// reason: both fields are written by the MODEL, in the language of the request.
		//
		// There used to be a lead here, written in Spanish. It was wrong in every
		// conversation that was not Spanish: a user writing English read a Spanish sentence
		// prefixed to an English assumption, so the line mixed two languages and read as a
		// system error rather than as the agent proposing something. The field already says
		// what the agent would do — that is its whole purpose — so it needs no introduction.
		b.WriteString("\n\n")
		b.WriteString(tr.Assumption)
	}
	return b.String()
}

// RunTask runs the agent with a single text task and returns a human-readable summary.
func (r *AppRunner) RunTask(ctx context.Context, task string, progress func(string, ...any)) (string, error) {
	cfg, engine, err := r.engineFor(config.TaskCode)
	if err != nil {
		return "", err
	}
	source, err := taskpkg.NewText(task, "tui")
	if err != nil {
		return "", err
	}
	var result string
	ag := r.newAgent(cfg, r.Log, engine, r.Box, source, true)
	// The confirmation channel goes in FIRST, before the run: an action the policy would ask
	// about is proposed in the middle of the turn, and an approver installed afterwards would
	// arrive after the question had already been answered with a refusal.
	installApproverOn(ag, r.approverOrNil())
	r.equipAgent(ag)
	// The project this session works in, the same sentences Plan mode's system prompt ends with:
	// a turn in Task mode is as bound to the project as a question in Plan mode, and it is the one
	// that writes.
	if setter, ok := ag.(interface{ SetProjectNote(string) }); ok {
		r.sessionMu.Lock()
		note := r.projectContext()
		r.sessionMu.Unlock()
		setter.SetProjectNote(note)
	}
	// The background agents this turn starts run on the model their role is assigned, built by
	// THIS runner: they are part of this session's team and of no other.
	if setter, ok := ag.(interface {
		SetRoleEngines(func(role string) *llm.Client)
	}); ok {
		setter.SetRoleEngines(r.roleEngine)
	}
	// The cheap and strong tiers: a piece of work handed to a background agent is judged and
	// routed to the model that suits it, and a cheap agent that fails is run again on the strong one.
	if setter, ok := ag.(interface {
		SetTiers(func(string) *llm.Client, func(context.Context, string, string) string)
	}); ok {
		setter.SetTiers(r.tierEngine, r.classifyPiece)
	}
	// The conversation goes in before the run and comes back out after it. That round trip is
	// what makes the agent conversational: the turn that asked a question recorded it, and the
	// next turn reads it together with the user's answer, so "yes" means something.
	ag.SetTranscript(r.history())
	// The model's reasoning is shown while it is written. Every front end reached through here
	// either renders the snapshots (the web view replaces one with the next) or drops them
	// (this package's own view, see progressSender).
	if live, ok := ag.(interface{ SetLiveThinking(bool) }); ok {
		live.SetLiveThinking(true)
	}
	if o, ok := ag.(taskObserver); ok {
		r.observe(o, task, progress, &result)
	}
	r.live.set(ag)
	runErr := ag.Run(ctx)
	r.live.set(nil)
	// Kept even when the run failed: the attempt is part of the conversation, and dropping it
	// would make the agent repeat a mistake it cannot see.
	r.remember(ag.Transcript())
	// What this turn read from the library, ready for the verdict that follows it. Recorded on
	// the failure path too: a turn that failed is exactly the one a user marks.
	if consult, ok := ag.(interface{ Consulted() map[string]int }); ok {
		r.rememberUsage(consult.Consulted(), task)
	}
	// A STOPPED turn is reported as stopped. The agent ends its loop quietly when its context is
	// cancelled - that is right for a daemon being shut down - so without this a turn the user
	// stopped came back as a finished one: the gateway recorded it as done, and went on to ask the
	// model for the conversation's title while still holding the conversation, refusing the
	// user's next message for as long as that took.
	if runErr == nil && ctx.Err() != nil {
		runErr = ctx.Err()
	}
	if runErr != nil {
		return result, runErr
	}
	if result == "" {
		return "the task finished without reporting a result", nil
	}
	return result, nil
}

// observe has the agent report its progress and, when the turn ends, its result into *result.
func (r *AppRunner) observe(o taskObserver, task string, progress func(string, ...any), result *string) {
	o.SetProgress(progress)
	o.SetObserver(func(tr agent.TaskResult) {
		*result = summarise(tr)
		r.setReport(tr.Report)
		r.setAsked(tr)
		// The questions are handed to the caller as STRUCTURE, not only as the sentence
		// above: the window needs the question, its assumption and its options to draw a
		// pickable list, and none of that survives being flattened into a string.
		//
		// A finished task that found more it could do hands its suggestions over the same way,
		// as proposals: the window asks whether to apply each one, and nothing is applied that
		// the user did not accept.
		switch {
		case tr.NeedsInput && len(tr.Questions) > 0:
			r.setPendingQuestions(tr.Questions, task)
		case tr.Pass && tr.Kind != agent.KindChat:
			if items := agent.ProposalItems(tr.Report); len(items) > 0 {
				r.setPendingQuestions(items, task)
			}
		default:
			// Nothing to ask.
		}
	})
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

// setReport records the structured report of the turn that just finished. A turn without one
// (a question, a chat reply, a failure) clears the previous, so a stale report is never attached
// to an unrelated answer.
func (r *AppRunner) setReport(rep *agent.Report) {
	r.pendingMu.Lock()
	defer r.pendingMu.Unlock()
	r.report = rep
}

// setAsked records the questions of the turn that just finished, or none when it asked nothing, so a
// turn that did not ask never hands over the previous turn's questions.
func (r *AppRunner) setAsked(tr agent.TaskResult) {
	var items []agent.AskItem
	if tr.NeedsInput {
		items = tr.Questions
	}
	r.pendingMu.Lock()
	defer r.pendingMu.Unlock()
	r.asked = items
}

// TakeQuestions returns the questions the last turn asked and clears them, or nil when it asked
// none. It is the gateway's copy for clients that draw the questions themselves (the web), and it
// leaves TakePendingQuestions alone: the terminal's window still gets its own.
func (r *AppRunner) TakeQuestions() []agent.AskItem {
	r.pendingMu.Lock()
	defer r.pendingMu.Unlock()
	items := r.asked
	r.asked = nil
	return items
}

// TakeReport returns the structured report of the last task and clears it, or nil when that turn
// produced none. Taking clears for the reason TakePendingQuestions does: one turn, one delivery.
func (r *AppRunner) TakeReport() *agent.Report {
	r.pendingMu.Lock()
	defer r.pendingMu.Unlock()
	rep := r.report
	r.report = nil
	return rep
}

// history returns the Task-mode conversation to seed a new agent with.
func (r *AppRunner) history() []agent.DialogueTurn {
	r.transcriptMu.Lock()
	defer r.transcriptMu.Unlock()
	return append([]agent.DialogueTurn(nil), r.transcript...)
}

// Transcript returns the Task-mode conversation, which is what a front end draws.
//
// It is the EXPORTED half of history(), and it exists so that the gateway's Service contract can
// reach it without this package importing internal/gateway - the interface is declared
// structurally, so the method name is the whole coupling. The copy is deliberate: the caller gets
// the turns as they were, and a turn appended a moment later cannot change the slice it holds.
func (r *AppRunner) Transcript() []agent.DialogueTurn { return r.history() }

// remember stores the conversation a finished turn ended with.
func (r *AppRunner) remember(turns []agent.DialogueTurn) {
	r.transcriptMu.Lock()
	defer r.transcriptMu.Unlock()
	r.transcript = append([]agent.DialogueTurn(nil), turns...)
}

// RestoreTranscript loads a saved conversation into the runner, replacing
// anything it currently holds. It is how a session persisted to disk and
// reloaded on startup carries its history forward.
func (r *AppRunner) RestoreTranscript(turns []agent.DialogueTurn) {
	// Loading a saved conversation is storing it, exactly as a finished turn does.
	r.remember(turns)
}

// ResetTranscript drops the Task-mode conversation, which is how /new leaves a subject
// behind. Plan mode's session is reset alongside it, because to the user they are the same
// conversation.
func (r *AppRunner) ResetTranscript() {
	r.transcriptMu.Lock()
	defer r.transcriptMu.Unlock()
	r.transcript = nil
}

// notesVault is the person's note vault, or nil when there is no home to keep it in.
func notesVault() *notes.Vault {
	if dir := config.NotesDir(); dir != "" {
		return notes.Open(dir)
	}
	return nil
}
