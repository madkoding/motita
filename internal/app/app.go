// Package app holds the program logic: command-line parsing, construction of the
// three layers and the graceful-shutdown loop.
//
// It is deliberately separated from cmd/agent: a `main` with all the logic inside
// cannot be tested (it is not callable from a test without re-executing the whole
// process), so every configuration decision would be left without coverage. Here
// the entry point is `Run`, which receives its dependencies (output, errors,
// signals, version) and returns an exit code instead of calling os.Exit: it can be
// tested end to end in memory.
package app

import (
	"context"
	"errors"
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
	"github.com/madkoding/starlight/internal/task"
	"github.com/madkoding/starlight/internal/tui"
)

// Program exit codes. They are part of the public contract (cron and systemd use
// them to decide whether something failed), so they are named.
const (
	// Success: every task passed the anchor.
	Success = 0
	// RunError: some task failed or the agent could not start.
	RunError = 1
	// ConfigError: the configuration is not valid. It is distinguished from the
	// one above so a misconfigured deployment is not confused with a task
	// failure.
	ConfigError = 2
	// ChildError: the sandbox's child process could not run the command.
	ChildError = 126
	// InterruptedError: second signal received, forced exit.
	InterruptedError = 130
)

// Options are the dependencies of one run. Everything that touches the outside
// world is injected so the paths can be tested without launching processes.
type Options struct {
	// Args is the command line without the program name.
	Args []string
	// Out and Errs are the output streams (os.Stdout/os.Stderr in production).
	Out io.Writer
	Err io.Writer
	// Version is the value injected through -ldflags.
	Version string
	// Goos and Goarch are shown by -version.
	Goos, Goarch string
	// Signals receives SIGINT/SIGTERM. When nil no handler is installed (test
	// mode or non-interactive runs).
	Signals <-chan os.Signal
	// BaseCtx is the parent context for the run. When it is cancelled (for
	// example by a Ctrl+C handler in main), interactive modes return
	// immediately. If nil, context.Background() is used.
	BaseCtx context.Context

	// Construction hooks, to replace the layers in tests.
	NewLogger  func(config.Agent) (*logx.Logger, error)
	NewSandbox func(sandbox.Options) (*sandbox.Sandbox, error)
	NewEngine  func(config.LLM, *logx.Logger) (*llm.Client, error)
	RunAgent   func(context.Context, *agent.Agent) error

	// RunOnboard is the first-run wizard. Injected so the flag can be tested
	// without a terminal, and so the conversation itself can be driven from a
	// test (it lives in internal/onboard, which takes its input as a reader).
	RunOnboard func(context.Context, io.Reader, io.Writer, string, onboard.Answers) (onboard.Result, error)
	// Stdin is the input of the wizard.
	Stdin io.Reader

	// RunTUI replaces the interactive menu in tests.
	RunTUI func(context.Context, config.Config, *llm.Client, *sandbox.Sandbox, *logx.Logger) int
	// RunChild is the sandbox's child mode. It is injected so the success path
	// can be tested without syscall.Exec replacing the test process (which is
	// exactly what used to make the coverage profile disappear).
	RunChild func([]string) error
	// Exit is the forced exit from the second signal or the shutdown deadline.
	// Injectable because os.Exit ends the process and would leave the path with
	// no possible coverage.
	Exit func(int)

	// waitSignal is the countdown function for forced shutdown.
	waitSignal func(time.Duration) <-chan time.Time
}

// Usage is the command-line help text.
const Usage = `Usage: starlight [options]

Options:
  -config string     path to the YAML configuration file
  -task string       process a single task (ignores the configured source)
  -task-file string  process the task contained in a file
  -plan              enter read-only plan/chat mode (implies -tui when no prompt is given)
  -p string          one-shot plan/chat prompt (implies -plan)
  -tui               start the interactive text user interface (default when no task is given)
  -init              first-run wizard: choose the provider, the model and the
                     check, and write a working configuration
  -validate-config   validate the configuration and exit (does not call the LLM)
  -isolation         print the available sandbox isolation and exit
  -version           print the version and exit

Environment variables: STARLIGHT_* (see README.md; also accepts OPENAI_API_KEY).
`

// flags parsed out of Args.
type flags struct {
	configPath     string
	task           string
	taskFile       string
	plan           bool
	tui            bool
	prompt         string
	validateConfig bool
	version        bool
	isolation      bool
	initConfig     bool
}

// Run is the program's entry point: it parses the arguments, builds the three
// layers and returns the exit code.
func Run(op Options) int {
	op.complete()

	// The sandbox re-executes itself to apply the limits in the child. It is
	// handled before parsing flags: the child's command line belongs to the
	// sandbox, not to the user.
	if sandbox.IsChildExecution(op.Args) {
		if err := op.RunChild(op.Args); err != nil {
			fmt.Fprintf(op.Err, "starlight[sandbox]: %v\n", err)
			return ChildError
		}
		return Success
	}

	fl, err := parse(op.Args)
	if err != nil {
		fmt.Fprintf(op.Err, "❌ %v\n\n%s", err, Usage)
		return ConfigError
	}

	if fl.version {
		fmt.Fprintf(op.Out, "starlight %s (%s/%s)\n", op.Version, op.Goos, op.Goarch)
		return Success
	}

	if fl.initConfig {
		return op.initConfig(fl)
	}

	return op.run(fl)
}

// complete fills in the default values of the dependencies.
func (op *Options) complete() {
	if op.Out == nil {
		op.Out = io.Discard
	}
	if op.Stdin == nil {
		op.Stdin = os.Stdin
	}
	if op.RunOnboard == nil {
		op.RunOnboard = func(ctx context.Context, in io.Reader, out io.Writer, path string, preset onboard.Answers) (onboard.Result, error) {
			return onboard.Run(ctx, in, out, path, preset, time.Now())
		}
	}
	if op.BaseCtx == nil {
		op.BaseCtx = context.Background()
	}
	if op.Err == nil {
		op.Err = io.Discard
	}
	if op.Goos == "" {
		op.Goos = "linux"
	}
	if op.waitSignal == nil {
		op.waitSignal = func(d time.Duration) <-chan time.Time { return time.After(d) }
	}
	if op.RunChild == nil {
		op.RunChild = sandbox.RunAsChild
	}
	if op.Exit == nil {
		op.Exit = os.Exit
	}
}

// parse reads the accepted flags. It is done by hand, and not with the flag
// package over os.Args, so the function stays pure and does not depend on the
// process' global state (which is what used to make it impossible to test).
func parse(args []string) (flags, error) {
	var b flags

	for i := 0; i < len(args); i++ {
		arg := args[i]
		// Both forms are accepted: -flag value and -flag=value.
		name, value, hasValue := strings.Cut(arg, "=")

		next := func() (string, error) {
			if hasValue {
				return value, nil
			}
			if i+1 >= len(args) {
				return "", fmt.Errorf("%s is missing its value", name)
			}
			i++
			return args[i], nil
		}

		switch name {
		case "-config", "--config":
			v, err := next()
			if err != nil {
				return b, err
			}
			b.configPath = v
		case "-task", "--task":
			v, err := next()
			if err != nil {
				return b, err
			}
			b.task = v
		case "-task-file", "--task-file":
			v, err := next()
			if err != nil {
				return b, err
			}
			b.taskFile = v
		case "-plan", "--plan":
			if hasValue {
				b.plan = value == "true" || value == "1"
			} else {
				b.plan = true
			}
		case "-tui", "--tui":
			b.tui = true
		case "-p", "--prompt":
			v, err := next()
			if err != nil {
				return b, err
			}
			b.prompt = v
		case "-init", "--init":
			b.initConfig = true
		case "-validate-config", "--validate-config":
			b.validateConfig = true
		case "-version", "--version":
			b.version = true
		case "-isolation", "--isolation":
			b.isolation = true
		case "-h", "-help", "--help":
			return b, fmt.Errorf("help requested")
		case "":
			return b, fmt.Errorf("empty argument")
		default:
			return b, fmt.Errorf("unknown flag: %q", arg)
		}
	}
	return b, nil
}

// initConfig runs the first-run wizard. The default destination is
// ./starlight.yaml next to wherever the agent is being set up, which is what the
// summary then tells the user to pass with -config.
func (op Options) initConfig(fl flags) int {
	path := fl.configPath
	if path == "" {
		path = "./starlight.yaml"
	}

	fmt.Fprintf(op.Out, "Welcome to starlight.\n")
	fmt.Fprintf(op.Out, "This wizard writes a working configuration in %s.\n", path)
	fmt.Fprintf(op.Out, "Nothing is written until every answer is in: press q to cancel at any point.\n")

	res, err := op.RunOnboard(op.BaseCtx, op.Stdin, op.Out, path, onboard.Answers{})
	if err != nil {
		if errors.Is(err, onboard.ErrCancelled) {
			fmt.Fprintf(op.Out, "\nCancelled: nothing was written.\n")
			return Success
		}
		fmt.Fprintf(op.Err, "❌ %v\n", err)
		return ConfigError
	}

	// Prove right away that the file works: if the wizard wrote something the
	// program cannot load, the user must know now and not on the first task.
	cfg, err := config.LoadWithoutKey(res.ConfigPath)
	if err != nil {
		fmt.Fprintf(op.Err, "❌ the generated configuration does not load: %v\n", err)
		return ConfigError
	}
	fmt.Fprintf(op.Out, "✅ the configuration loads: %s\n", DescribeConfig(cfg))
	return Success
}

// run is the main body: it loads the configuration, prepares the layers and
// launches the agent with graceful shutdown.
func (op Options) run(fl flags) int {
	// The modes that do not call the LLM do not require the key, but an invalid
	// file is ALWAYS an error: reporting "valid configuration" after replacing
	// the file with the default values would hide exactly the failure being
	// looked for.
	//
	// When no explicit -config is given, look for starlight.yaml in the current
	// directory. If that also does not exist, start from defaults so that the
	// TUI or wizard can run without a file.
	var cfg config.Config
	var err error
	switch {
	case fl.configPath != "":
		cfg, err = config.Load(fl.configPath)
		if err != nil {
			if (fl.validateConfig || fl.isolation) && strings.Contains(err.Error(), "LLM key is missing") {
				cfg, err = config.LoadWithoutKey(fl.configPath)
			}
			if err != nil {
				fmt.Fprintf(op.Err, "❌ %v\n", err)
				return ConfigError
			}
		}
	case func() bool { _, e := os.Stat("starlight.yaml"); return e == nil }():
		cfg, err = config.Load("starlight.yaml")
		if err != nil {
			if (fl.validateConfig || fl.isolation) && strings.Contains(err.Error(), "LLM key is missing") {
				cfg, err = config.LoadWithoutKey("starlight.yaml")
			}
			if err != nil {
				fmt.Fprintf(op.Err, "❌ %v\n", err)
				return ConfigError
			}
		}
	default:
		cfg, err = config.LoadOrDefault("")
		if err != nil {
			fmt.Fprintf(op.Err, "❌ %v\n", err)
			return ConfigError
		}
	}

	// When entering the conversational TUI, keep the chat clean by writing
	// structured logs only to the file, not to the terminal.
	if fl.tui {
		cfg.Agent.LogConsole = false
	}

	log, err := op.newLogger(cfg.Agent)
	if err != nil {
		fmt.Fprintf(op.Err, "❌ %v\n", err)
		return ConfigError
	}
	logx.Install(log)
	defer log.Close()

	// Layer C: the sandbox.
	box, err := op.newSandbox(SandboxOptions(cfg, log))
	if err != nil {
		log.Error("could not prepare the sandbox", "error", err)
		fmt.Fprintf(op.Err, "❌ could not prepare the sandbox: %v\n", err)
		return RunError
	}
	defer box.Close()

	if fl.isolation {
		fmt.Fprintf(op.Out, "applied isolation: %v\n", box.Isolation())
		if notApplied := box.NotApplied(); len(notApplied) > 0 {
			fmt.Fprintf(op.Out, "requested and not applied:\n")
			for _, s := range notApplied {
				fmt.Fprintf(op.Out, "  - %s\n", s)
			}
		}
		return Success
	}

	if fl.validateConfig {
		fmt.Fprintf(op.Out, "✅ valid configuration: %s\n", DescribeConfig(cfg))
		fmt.Fprintf(op.Out, "   sandbox isolation: %v\n", box.Isolation())
		return Success
	}

	ctx, wait := op.contextWithShutdown(op.BaseCtx, cfg, log)
	defer wait()

	// Layer B: the reasoning engine. In TUI mode the engine may be nil (for
	// example when there is no configuration file yet); the TUI creates it lazily
	// when the user actually starts plan, task or model listing. For all non-TUI
	// modes the engine is required up front.
	var engine *llm.Client
	if fl.tui || op.defaultToTUI(fl, cfg) {
		return op.runTUI(ctx, fl, cfg, nil, box, log)
	}

	engine, err = op.newEngine(cfg.LLM, log)
	if err != nil {
		log.Error("could not prepare the reasoning engine", "error", err)
		fmt.Fprintf(op.Err, "❌ %v\n", err)
		return ConfigError
	}

	if fl.plan || fl.prompt != "" {
		return op.runPlan(ctx, fl, cfg, engine, box, log)
	}

	// Task source (a single task takes priority over the configured one).
	source, err := BuildSource(cfg, fl.task, fl.taskFile, log)
	if err != nil {
		log.Error("could not build the task source", "error", err)
		fmt.Fprintf(op.Err, "❌ %v\n", err)
		return ConfigError
	}

	ag := agent.New(cfg, log, engine, box, source)

	started := time.Now()
	runErr := op.runAgent(ctx, ag)

	if runErr != nil {
		log.Error("the agent finished with errors", "error", runErr, "duration", time.Since(started).String())
		fmt.Fprintf(op.Err, "❌ %v (see the log in %s)\n", runErr, LogPath(cfg))
		return RunError
	}
	log.Info("run completed", "duration", time.Since(started).String())
	return Success
}

// newLogger builds the structured log with rotation.
func (op Options) newLogger(a config.Agent) (*logx.Logger, error) {
	if op.NewLogger != nil {
		return op.NewLogger(a)
	}
	level, err := logx.ParseLevel(a.LogLevel)
	if err != nil {
		return nil, err
	}
	return logx.New(logx.Options{
		Path:    a.LogFile,
		Level:   level,
		Console: a.LogConsole,
		MaxMB:   a.LogMaxMB,
		Backups: a.LogBackups,
	})
}

// newSandbox builds the sandbox (Layer C).
func (op Options) newSandbox(o sandbox.Options) (*sandbox.Sandbox, error) {
	if op.NewSandbox != nil {
		return op.NewSandbox(o)
	}
	return sandbox.New(o)
}

// newEngine builds the reasoning engine (Layer B).
func (op Options) newEngine(c config.LLM, log *logx.Logger) (*llm.Client, error) {
	if op.NewEngine != nil {
		return op.NewEngine(c, log)
	}
	return llm.New(c, log)
}

// runAgent launches the agent (replaceable in tests).
func (op Options) runAgent(ctx context.Context, ag *agent.Agent) error {
	if op.RunAgent != nil {
		return op.RunAgent(ctx, ag)
	}
	return ag.Run(ctx)
}

// contextWithShutdown installs graceful shutdown: the first signal cancels the
// work in progress and grants the configured deadline; the second forces the
// exit.
//
// It returns the context and a function to uninstall the handler (needed so no
// goroutines are left alive between tests).
func (op Options) contextWithShutdown(parent context.Context, cfg config.Config, log *logx.Logger) (context.Context, func()) {
	ctx, cancel := context.WithCancel(parent)
	if op.Signals == nil {
		return ctx, cancel
	}
	finished := make(chan struct{})

	done := make(chan struct{})
	go func() {
		defer close(finished)
		select {
		case s, open := <-op.Signals:
			if !open {
				return
			}
			log.Warn("signal received: shutting down gracefully", "signal", s.String(),
				"timeout", cfg.Agent.ShutdownTimeout.String())
			cancel()
		case <-done:
			return
		}

		// The countdown must also listen to `done`. If it did not, this
		// goroutine would stay alive after the work finished and, once the
		// deadline expired, would kill the process with os.Exit even though
		// everything had ended well (and without letting the process write its
		// closing profiles/records). Detected because the package's coverage
		// profile came out empty: the os.Exit from this goroutine killed the test
		// binary before it reported.
		select {
		case s, open := <-op.Signals:
			if !open {
				return
			}
			log.Error("second signal received: immediate exit", "signal", s.String())
			op.Exit(InterruptedError)
		case <-op.waitSignal(cfg.Agent.ShutdownTimeout):
			log.Error("graceful shutdown exceeded the deadline: forced exit",
				"timeout", cfg.Agent.ShutdownTimeout.String())
			op.Exit(InterruptedError)
		case <-done:
			return
		}
	}()

	// The teardown is idempotent and waits for the goroutine to finish, so none
	// is left alive after control returns.
	var once sync.Once
	return ctx, func() {
		once.Do(func() { close(done); cancel() })
		<-finished
	}
}

// SandboxOptions translates the configuration into the sandbox's options.
func SandboxOptions(cfg config.Config, log *logx.Logger) sandbox.Options {
	op := sandbox.Options{
		Dir: cfg.Agent.WorkspaceDir,
		Limits: sandbox.Limits{
			MemoryMB:      cfg.Sandbox.MemoryMB,
			CPUSeconds:    cfg.Sandbox.CPUSeconds,
			Processes:     cfg.Sandbox.Processes,
			OpenFiles:     cfg.Sandbox.OpenFiles,
			MaxFileSizeMB: cfg.Sandbox.MaxFileSizeMB,
		},
		CgroupRoot:  cfg.Sandbox.CgroupRoot,
		Timeout:     cfg.Sandbox.Timeout,
		MaxOutputKB: cfg.Sandbox.MaxOutputKB,
		Keep:        cfg.Sandbox.KeepEphemeral,
		Log:         log,
	}

	switch cfg.Sandbox.Kind {
	case "none":
		// No filesystem isolation: only a temporary directory.
		op.Limits = sandbox.Limits{}
		op.UseCgroups = false
		op.UseChroot = false
	case "chroot":
		op.UseChroot = true
		op.Root = cfg.Sandbox.Root
		op.UseCgroups = WantsCgroups(cfg)
	default:
		op.UseCgroups = WantsCgroups(cfg)
	}

	if cfg.Sandbox.User != "" {
		if uid, gid, err := config.ParseUser(cfg.Sandbox.User); err == nil {
			op.Uid, op.Gid, op.DropPrivs = uid, gid, true
		}
	}
	op.Limits.NoNetwork = cfg.Sandbox.IsolateNetwork
	return op
}

// WantsCgroups decides whether cgroups v1 should be attempted.
func WantsCgroups(cfg config.Config) bool {
	switch cfg.Sandbox.Cgroups {
	case "on":
		return true
	case "off":
		return false
	default: // auto: it is attempted and degrades with a warning if unavailable
		return true
	}
}

// BuildSource resolves where the tasks come from: a task from the command line
// takes priority over the file, and the file over the configured source.
func BuildSource(cfg config.Config, directTask, file string, log *logx.Logger) (task.Source, error) {
	switch {
	case directTask != "":
		return task.NewText(directTask, "command-line")
	case file != "":
		return task.NewFile(file)
	default:
		return task.New(cfg.TaskSource, log)
	}
}

// DescribeConfig summarises the active configuration for the diagnostic modes.
func DescribeConfig(cfg config.Config) string {
	record := cfg.Agent.LogFile
	if record == "" {
		record = "(console only)"
	}
	return fmt.Sprintf("task_source=%s llm=%s/%s anchor=%s sandbox=%s workspace=%s log=%s",
		cfg.TaskSource.Kind, cfg.LLM.Provider, cfg.LLM.Model, cfg.Anchor.Kind,
		cfg.Sandbox.Kind, cfg.Agent.WorkspaceDir, record)
}

// LogPath returns where the log is, so it can be named in errors.
func LogPath(cfg config.Config) string {
	if cfg.Agent.LogFile == "" {
		return "stderr"
	}
	return cfg.Agent.LogFile
}

// defaultToTUI decides whether to start the interactive menu when the user did
// not ask for a task explicitly.
func (op Options) defaultToTUI(fl flags, cfg config.Config) bool {
	return fl.configPath == "" && fl.task == "" && fl.taskFile == "" && len(op.Args) == 0
}

// runTUI starts the interactive text user interface.
func (op Options) runTUI(ctx context.Context, fl flags, cfg config.Config, engine *llm.Client, box *sandbox.Sandbox, log *logx.Logger) int {
	if op.RunTUI != nil {
		return op.RunTUI(ctx, cfg, engine, box, log)
	}
	// Built through the constructor, not a struct literal: the literal leaves the
	// injected factory nil and RunPlan then calls it, which panics. The constructor
	// is the only place that sets every dependency.
	runner := tui.NewAppRunner(op.Out, op.Err, cfg, engine, box, log)
	ui := tui.New(runner)
	ui.In = op.Stdin
	return ui.Run(ctx)
}

// runPlan runs the read-only plan/chat mode. It uses the reasoning engine and the
// sandbox, but it never delegates to the task/anchor flow.
func (op Options) runPlan(ctx context.Context, fl flags, cfg config.Config, engine *llm.Client, box *sandbox.Sandbox, log *logx.Logger) int {
	cfg.Agent.ReadOnly = true
	ag := agent.New(cfg, log, engine, box, nil)

	planner := plan.New(engine, ag).
		WithTimeout(planDefaultTimeout(cfg)).
		WithLoops(planDefaultLoops(cfg)).
		WithTrace(func(format string, args ...any) { fmt.Fprintf(op.Err, format, args...) })

	if fl.prompt != "" {
		answer, err := planner.Run(ctx, fl.prompt)
		if err != nil {
			fmt.Fprintf(op.Err, "❌ %v\n", err)
			return RunError
		}
		fmt.Fprintln(op.Out, answer)
		return Success
	}

	// No one-shot prompt: send the user to the TUI, which has a dedicated plan mode.
	return op.runTUI(ctx, fl, cfg, engine, box, log)
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
