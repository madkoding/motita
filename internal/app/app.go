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
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/madkoding/motita/internal/agent"
	"github.com/madkoding/motita/internal/config"
	"github.com/madkoding/motita/internal/curator"
	"github.com/madkoding/motita/internal/doctor"
	"github.com/madkoding/motita/internal/gateway"
	"github.com/madkoding/motita/internal/gitforge"
	"github.com/madkoding/motita/internal/llm"
	"github.com/madkoding/motita/internal/logx"
	"github.com/madkoding/motita/internal/onboard"
	"github.com/madkoding/motita/internal/procedures"
	"github.com/madkoding/motita/internal/review"
	"github.com/madkoding/motita/internal/sandbox"
	"github.com/madkoding/motita/internal/task"
	"github.com/madkoding/motita/internal/tui"
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

	// DoctorEnv is the machine `motita doctor` checks. Nil means this process's (doctor.System).
	DoctorEnv *doctor.Env

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
	// NewClient builds the gateway client the interface speaks through. It is a seam so a test
	// can observe WHICH gateway, and which session, the process decided to attach to - that
	// decision is the whole feature and it is otherwise invisible.
	//
	// It returns the tui.Runner rather than a *gateway.Client because that is all the interface
	// needs, and a test double for it does not have to be a client at all.
	NewClient func(baseURL, token, session string) tui.Runner
	// ServeGateway replaces the gateway's serve loop. It is a seam of the same shape as Exit
	// and waitSignal: the loop only returns an error when the listener breaks under it, so a
	// test that wants to see what happens when it does needs to be able to make it fail.
	ServeGateway func(*gateway.Server) error
	// StartGatewayForTest replaces the in-process bind for the interface's own gateway. It is a
	// seam because binding a real port in a test is a dependency on the machine, and the decision
	// under test is WHICH gateway the interface ends up speaking through, not that a socket can be
	// opened. It reports the base URL, the token and the error, and nothing else about the server
	// is reached from here.
	StartGatewayForTest func(owned bool) (baseURL, token string, err error)
	// WriteServiceFile publishes where the gateway is. It is a seam for the same reason the
	// filesystem calls in gateway/token.go are: the branch it guards - the description of a running
	// gateway that cannot be written - is a read-only home or a full disk, and provoking it with
	// permissions does not work because root ignores a directory's mode, which is how this
	// repository's tests are run.
	WriteServiceFile func(path string, svc gateway.ServiceFile) error
	// CloseGateway replaces the gateway's shutdown. Injecting it is how the "did not shut down
	// cleanly" report is reached, and that report matters: a shutdown that failed is the
	// difference between a client that was cut off and one that was waited for.
	CloseGateway func(*gateway.Server, context.Context) error
	// NewGateway builds the gateway. It is a seam of the same shape as the ones above, and it
	// exists because the OPTIONS a gateway is started with are a decision this package makes and
	// nothing else can see: the directory a schedule is persisted to and the resolution at which
	// a due task is noticed are assembled here from the configuration, and a gateway started for
	// real would prove the wiring only by leaving the state behind on disk. It receives the
	// options as built and the configuration they were derived from, and reports the server the
	// real one would have returned.
	NewGateway func(gateway.Options, config.Config) (*gateway.Server, error)
	// NewReview builds the background self-improvement fork. It is a seam of the same shape as
	// NewEngine and NewGateway: the injected function receives exactly what the real call
	// receives, so what a test observes is the wiring rather than a copy of it. Without it the
	// fork's presence in the served gateway is only provable by holding a conversation with a
	// model and waiting to see whether a skill appears.
	NewReview func(config.Review, *llm.Client, *procedures.Store, *logx.Logger, review.RunnerBuilder) *review.Review
	// RunChild is the sandbox's child mode. It is injected so the success path
	// can be tested without syscall.Exec replacing the test process (which is
	// exactly what used to make the coverage profile disappear).
	RunChild func([]string) error
	// Exit is the forced exit from the second signal or the shutdown deadline.
	// Injectable because os.Exit ends the process and would leave the path with
	// no possible coverage.
	Exit func(int)

	// ServiceFile is where the description of a running gateway is read and written. It is a seam
	// so a test can point it at a temporary directory instead of the user's home.
	ServiceFile string
	// ExePath is the program re-executed for `gateway start`. Injectable because in a test the
	// program under test is not a motita that can serve.
	ExePath string
	// InstallAppArmor and RemoveAppArmor are `motita sandbox apparmor install` and `remove`. They
	// change /etc and need root, so a test replaces them; nil means the sandbox package's.
	InstallAppArmor func(exe string) error
	RemoveAppArmor  func() error
	// AppArmorHint says how to let this binary make user namespaces, or "" when nothing is needed
	// (not Linux, no restriction, already allowed). SudoAppArmor runs the install through sudo, at
	// the person's request in `gateway setup`. Nil means the profile is never offered, which is what
	// a test wants: the machine running it may be an Ubuntu 24.04.
	AppArmorHint func() string
	SudoAppArmor func(exe string) error
	// SpawnGateway brings the service up. It is a seam of the same shape as Exit and RunChild: the
	// real one re-executes the program detached, and a test cannot do that without starting a
	// second agent.
	SpawnGateway func(ctx context.Context, spec spawnSpec) error
	// SignalProcess asks the gateway to stop. Injectable because killing a real process from a test
	// would leave the assertion racing the operating system.
	SignalProcess func(pid int) error
	// ShutdownGateway asks a running gateway to stop over its local API (POST /v1/shutdown), which
	// is how `gateway stop` reaches the graceful path where there is no SIGTERM. A seam for the
	// same reason SignalProcess is one.
	ShutdownGateway func(ctx context.Context, baseURL, token string) error
	// GatewayWait bounds how long start and stop wait for the gateway to answer or to go away.
	GatewayWait time.Duration
	// DiscoverGateway asks whether a gateway is running. It is a seam so a test can produce the
	// states that only a race produces in reality, such as the service file changing between the
	// confirmation that a gateway came up and the report of where it is.
	DiscoverGateway func(ctx context.Context, path string) (gateway.Found, bool, error)
	// ProbeGatewayHealth asks a gateway at an address which build it is. A seam for the same
	// reason as DiscoverGateway: the client's version row must be assertable without a live
	// gateway on the machine running the tests.
	ProbeGatewayHealth func(ctx context.Context, address string) (gateway.Health, error)
	// InterfaceEnabled reports whether the gateway serves the browser interface. A seam for the
	// same reason as the two above: whether the link is announced is a behaviour worth pinning,
	// and pinning it must not depend on what this machine's configuration file happens to say.
	InterfaceEnabled func() bool
	// Interactive reports whether a person is at the terminal to answer a question. Nil means
	// nobody is: `gateway start` then asks nothing and binds what the configuration says.
	Interactive func() bool
	// Tailscale finds Tailscale and publishes the gateway inside the tailnet. Nil means it is
	// never looked for, which is what a test wants: the machine running it may have Tailscale.
	Tailscale gateway.TailscaleService

	// waitSignal is the countdown function for forced shutdown.
	waitSignal func(time.Duration) <-chan time.Time
}

// Usage is the command-line help text.
//
// The gateway's default address is INTERPOLATED from the configuration rather than written into
// this string, and that is not cosmetic: help text is the first thing anyone reads and the last
// thing anyone updates, so a hardcoded address is how it went on advertising `127.0.0.1:0` long
// after the default became a fixed port. Reading it from the same place the program reads it means a
// change to the default breaks the test that checks this text instead of silently turning the help
// into a lie.
func Usage() string {
	return fmt.Sprintf(`Usage: motita [options]

Options:
  -config string     path to the YAML configuration file
  -task string       process a single task (ignores the configured source)
  -task-file string  process the task contained in a file
  -plan              enter read-only plan/chat mode (implies -tui when no prompt is given)
  -p string          one-shot plan/chat prompt (implies -plan)
  -tui               start the interactive text user interface (default when no task is given)
  -serve             run the gateway only: no interface, for clients on other machines
  -gateway string    gateway listen address (default %s; "off" disables it)
  -connect string    connect to a gateway somebody else is running, as a client
                     (this process then builds no sandbox and runs no commands)
  -session string    which conversation to attach to (default "default")
  -token-only        with gateway start: print only the access token (for scripts)
  -json              with gateway start: print the url, pid, version, token and links as JSON
  -validate-config   validate the configuration and exit (does not call the LLM)
  -isolation         print the available sandbox isolation and exit
  -version           print the version and exit

Commands:
  config             setup wizard: choose the provider, the model and the check,
                     and write a working configuration
  doctor             check this machine: platform, shell, DNS, PATH, the
                     configuration and the sandbox
  index status       the code index kept on disk: where, how many files, how big
  index clear        delete the code index files (the next search builds them again)
  sandbox apparmor   print, install or remove the AppArmor profile that lets commands
                     run without a network on Ubuntu 24.04 and later
                     (print | install | remove; install and remove need sudo)
  gateway start      bring the gateway up as a service and leave it running
  gateway stop       stop the gateway named by the service file
  gateway status     report whether a gateway is running

  curator status     report the thresholds, the last run and the lifecycle counts
  curator run        run one maintenance pass now (--consolidate, --dry-run)
  curator pin|unpin  exempt a skill from every automatic transition, or stop
  curator restore    bring an archived skill back
  curator list-archived  everything in the skills archive
  curator list-proposed  skills the background review proposed, waiting for you
  curator show-proposed|accept|reject  read one, put it in the library, or delete it

The gateway listens on loopback by default, so only this machine reaches it.
To reach it from another machine, prefer an SSH tunnel; or set gateway.listen to a LAN
address or 0.0.0.0:7477 - it speaks plain HTTP, so the token travels unencrypted - and
narrow who may connect with gateway.allow: "lan", an address, a network, or "!any".

Environment variables: MOTITA_* (see README.md; also accepts OPENAI_API_KEY).
`, config.Default().GatewayListen())
}

// flags parsed out of Args.
type flags struct {
	// notice is what the interface says first, when something happened before it opened: the
	// first-run setup that just finished, or was cancelled. It is not a command-line flag.
	notice         string
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
	doctor         bool
	// indexAction is the action of `motita index <action>`.
	indexAction string
	// sandboxAction is the action of `motita sandbox apparmor <action>`.
	sandboxAction string
	// tokenOnly and jsonOut shape `gateway start` for scripts; boolean on purpose, like the
	// curator switches, so neither swallows the word after it.
	tokenOnly bool
	jsonOut   bool
	serve     bool
	gateway   string
	// connect is the address of a gateway somebody else is running. Non-empty means this
	// process is a CLIENT: it builds no sandbox, opens no procedure library and creates no
	// reasoning engine, because all three are the server's job.
	connect string
	// session is the conversation to attach to, by id.
	session string
	// gatewayAction is the action of `motita gateway <action>`. A POSITIONAL argument, not a
	// flag, which is why it is read before the flag loop rather than inside it.
	gatewayAction string
	// gatewayCode is the pairing code `gateway allow` and `gateway deny` answer.
	gatewayCode string
	// curatorAction is the action of `motita curator <action>`, curatorSkill the name some
	// of them take, and the two switches are `curator run`'s. Positional like
	// gatewayAction, and read the same way and for the same reason.
	//
	// The two switches take NO value and are deliberately ABSENT from valueFlags: a
	// boolean flag that consumed the next argument would swallow a word the user meant as
	// a command.
	curatorAction      string
	curatorSkill       string
	curatorConsolidate bool
	curatorDryRun      bool
}

// Run is the program's entry point: it parses the arguments, builds the three
// layers and returns the exit code.
func Run(op Options) int {
	op.complete()

	// The claude-code provider points claude at this program as its MCP server. That
	// command line is claude's, not the user's, so it is handled before parsing too.
	if len(op.Args) == 2 && op.Args[0] == llm.ClaudeCodeMCPCommand {
		// main catches SIGINT and SIGTERM for its graceful shutdown, which this server has
		// none of: claude ends it with SIGTERM, and it has to die of it.
		signal.Reset(syscall.SIGINT, syscall.SIGTERM)
		if err := llm.ServeClaudeCodeMCP(op.Stdin, op.Out, op.Args[1]); err != nil {
			fmt.Fprintf(op.Err, "motita[claude-code-mcp]: %v\n", err)
			return RunError
		}
		return Success
	}

	// git runs this program as its credential helper, and the agent runs `motita forge` from
	// its shell. Both command lines are another program's or a script's, not a user's flags, so
	// they are read before parsing, like the two above.
	if len(op.Args) >= 1 && op.Args[0] == gitforge.HelperCommand {
		return op.runGitCredential(op.BaseCtx, op.Args)
	}
	if len(op.Args) >= 1 && op.Args[0] == forgeCommand {
		return op.runForge(op.BaseCtx, op.Args[1:])
	}

	// The sandbox re-executes itself to apply the limits in the child. It is
	// handled before parsing flags: the child's command line belongs to the
	// sandbox, not to the user.
	if sandbox.IsChildExecution(op.Args) {
		if err := op.RunChild(op.Args); err != nil {
			fmt.Fprintf(op.Err, "motita[sandbox]: %v\n", err)
			return ChildError
		}
		return Success
	}

	fl, err := parse(op.Args)
	if err != nil {
		fmt.Fprintf(op.Err, "❌ %v\n\n%s", err, Usage())
		return ConfigError
	}

	if fl.version {
		fmt.Fprintf(op.Out, "motita %s (%s/%s)\n", op.Version, op.Goos, op.Goarch)
		return Success
	}

	if fl.initConfig {
		return op.runConfigCommand(fl)
	}

	if fl.doctor {
		return op.runDoctor(fl)
	}

	if fl.localCommand() {
		return op.runLocalCommand(fl)
	}

	// A subcommand is dispatched here, before the main path, because it pays for neither the
	// reasoning engine nor a sandbox: `gateway start` re-executes the program rather than serving in
	// this process, and `stop` and `status` only read a file and ask a port a question.
	if fl.gatewayAction != "" {
		return op.runGatewayCommand(op.BaseCtx, fl.gatewayAction, fl)
	}

	// The curator is the same kind of command and is checked after it, so a line carrying
	// both is not ambiguous: the gateway action is read first, and a `curator` word on the
	// same line is read by ITS dispatch.
	if fl.curatorAction != "" {
		return op.runCuratorCommand(op.BaseCtx, fl)
	}

	return op.run(fl)
}

// complete fills in the default values of the dependencies.
func (op *Options) complete() {
	op.completeIO()
	op.completeProcess()
	op.completeGateway()
}

// completeIO fills in the streams, the onboarding wizard and the identity of the build.
func (op *Options) completeIO() {
	if op.Out == nil {
		op.Out = io.Discard
	}
	if op.Stdin == nil {
		op.Stdin = os.Stdin
	}
	if op.RunOnboard == nil {
		op.RunOnboard = func(
			ctx context.Context, in io.Reader, out io.Writer, path string, preset onboard.Answers,
		) (onboard.Result, error) {
			// The menus follow the arrow keys when the answers come from a terminal; from a pipe
			// they are read as lines, so a script can still answer them.
			//
			// The wizard writes colour unconditionally, so the decision is made here, in front of
			// it: the filter drops the colour where nothing can show it and swaps the glyphs a
			// legacy Windows console cannot draw. Detect also switches that console into virtual
			// terminal mode, without which it printed every colour code as text.
			out = outputProfile(out).Writer(out)
			return onboard.RunWithKeys(ctx, in, out, path, preset, time.Now(), tui.KeyModeFor(in))
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
	if op.Version == "" {
		// The default lives in cmd/agent (`var version = "dev"`), so a test that builds Options by
		// hand leaves this empty - and an interface showing an empty version is worse than one
		// showing "dev": it looks like the version is broken instead of like an unversioned build.
		op.Version = "dev"
	}
}

// completeProcess fills in how the process waits, exits, re-executes itself and runs its child.
func (op *Options) completeProcess() {
	if op.waitSignal == nil {
		op.waitSignal = func(d time.Duration) <-chan time.Time { return time.After(d) }
	}
	if op.RunChild == nil {
		op.RunChild = sandbox.RunAsChild
	}
	if op.Exit == nil {
		op.Exit = os.Exit
	}
	if op.ServiceFile == "" {
		op.ServiceFile = gateway.ServiceFilePath()
	}
	if op.InstallAppArmor == nil {
		op.InstallAppArmor = sandbox.InstallAppArmor
	}
	if op.RemoveAppArmor == nil {
		op.RemoveAppArmor = sandbox.RemoveAppArmor
	}
	if op.AppArmorHint == nil {
		op.AppArmorHint = sandbox.NetworkIsolationHint
	}
	if op.SudoAppArmor == nil {
		op.SudoAppArmor = op.sudoAppArmor
	}
	if op.ExePath == "" {
		if exe, err := executablePath(); err == nil {
			op.ExePath = exe
		} else {
			// A program that cannot name itself cannot re-execute itself either. os.Args[0] is the
			// fallback rather than a refusal: it is what the shell ran, and the shell got it right.
			op.ExePath = os.Args[0]
		}
	}
}

// completeGateway fills in how the gateway is started, signalled and installed as a service.
func (op *Options) completeGateway() {
	if op.SpawnGateway == nil {
		op.SpawnGateway = spawnDetached
	}
	if op.SignalProcess == nil {
		op.SignalProcess = signalByPID
	}
	if op.ShutdownGateway == nil {
		op.ShutdownGateway = gateway.RequestShutdown
	}
	if op.GatewayWait == 0 {
		op.GatewayWait = 10 * time.Second
	}
	if op.WriteServiceFile == nil {
		op.WriteServiceFile = gateway.WriteServiceFile
	}
}

// initConfig runs the first-run wizard. The default destination is
// ./motita.yaml next to wherever the agent is being set up, which is what the
// summary then tells the user to pass with -config.
func (op Options) initConfig(fl flags) (int, bool, onboard.Next) {
	path := fl.configPath
	if path == "" {
		// The wizard's default is the motita home, so a first run lands where the program
		// will look for it afterwards. Its directory is created as part of writing the file, so
		// a user with no ~/.motita gets one. With no HOME there is no home to use, and the
		// working directory is the fallback — the old behaviour, for the environments that have
		// nowhere else to put it.
		path = config.File()
		if path == "" {
			path = "./motita.yaml"
		}
	}

	// The welcome banner is printed by the wizard itself; the old messages are
	// removed to avoid duplication. The wizard handles the full UX now.
	res, err := op.RunOnboard(op.BaseCtx, op.Stdin, op.Out, path, wizardAnswers(path))
	if err != nil {
		if errors.Is(err, onboard.ErrCancelled) {
			fmt.Fprintf(op.Out, "\nCancelled: nothing was written.\n")
			return Success, false, onboard.NextExit
		}
		fmt.Fprintf(op.Err, "❌ %v\n", err)
		return ConfigError, false, onboard.NextExit
	}

	// Prove right away that the file works: if the wizard wrote something the
	// program cannot load, the user must know now and not on the first task.
	cfg, err := config.LoadWithoutKey(res.ConfigPath)
	if err != nil {
		fmt.Fprintf(op.Err, "❌ the generated configuration does not load: %v\n", err)
		return ConfigError, false, onboard.NextExit
	}
	fmt.Fprintf(op.Out, "✅ the configuration loads: %s\n", DescribeConfig(cfg))
	return Success, true, res.Next
}

// runConfigCommand is `motita config`: the wizard, then what the user chose to do next.
func (op Options) runConfigCommand(fl flags) int {
	code, _, next := op.initConfig(fl)
	if code != Success {
		return code
	}
	return op.continueAfterSetup(fl, next)
}

// continueAfterSetup does what the user chose at the end of `motita config`: open the terminal
// interface, start the gateway, or stop. The zero choice is to stop, so a setup that never asked
// (a script, no terminal) ends exactly as it did before the question existed.
func (op Options) continueAfterSetup(fl flags, next onboard.Next) int {
	switch next {
	case onboard.NextTUI:
		fl.initConfig = false
		fl.tui = true
		fl.notice = onboardingNotice(true)
		return op.run(fl)
	case onboard.NextGateway:
		fl.initConfig = false
		return op.gatewayStart(op.BaseCtx, fl)
	default:
		return Success
	}
}

// firstRunSetup runs the wizard for a first run. It reports done when the run ends here: the
// setup failed, or the user answered its last question with the gateway or with exit. Otherwise
// the run goes on to the interface it was asked for, with fl carrying the notice it opens with.
func (op Options) firstRunSetup(fl *flags) (code int, done bool) {
	code, written, next := op.initConfig(*fl)
	if code != Success {
		return code, true
	}
	if written && next != onboard.NextTUI {
		return op.continueAfterSetup(*fl, next), true
	}
	// The interface clears the screen when it opens, and the wizard's summary goes with it:
	// the first thing it shows is therefore what just happened, in one line.
	fl.notice = onboardingNotice(written)
	return Success, false
}

// resolvedConfigPath answers WHICH configuration file an invocation reads, and the empty string
// when there is none and the defaults apply.
//
// It is ONE function because two callers need the same answer and disagreeing is the bug that
// matters: `run` loads the file, and the gateway subcommands resolve whether to announce the
// browser interface from it. When those two derived it separately, `motita gateway start` with
// no -config resolved against the DEFAULTS while the service it spawned read
// ~/.motita/motita.yaml - so an operator who turned the interface off in their own file was
// handed a link to a page their gateway answers 404 on. Both now ask this.
//
// The order is: an explicit -config, then the motita home, then the working directory. The home
// comes FIRST and the working directory SECOND, which is the opposite of the usual project-local
// convention and deliberate: motita's file carries the LLM credentials and the paths to its own
// state, so it belongs to the user rather than to whichever repository they happened to be
// standing in. The working directory is still accepted, so an existing setup keeps working and a
// per-project override stays possible.
func resolvedConfigPath(fl flags) string {
	switch {
	case fl.configPath != "":
		return fl.configPath
	case config.File() != "" && exists(config.File()):
		return config.File()
	case exists("motita.yaml"):
		return "motita.yaml"
	default:
		return ""
	}
}

// exists reports whether a path is there. It is a readability wrapper: the
// switch that chooses the configuration file reads as a list of destinations,
// and os.Stat inlined three times turned it into a list of error checks.
func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// loadPreferringKey loads a configuration, and falls back to loading it WITHOUT a key for the
// invocations that provably never call a model.
//
// The key is checked while the file is read, so a valid configuration with no key is an error
// - which is right for a run and wrong for every diagnostic. Those modes (-validate-config,
// -isolation) exist to CHECK the file, and not having a key is legitimate there. The curator
// is in this list for the same reason: tidying the library is filesystem work, and requiring a
// model to sort files would be requiring a model to do something that does not use one. The
// ONE curator path that does call a model, --consolidate, is deliberately excluded: it asks
// for the engine itself, and a missing key there is a real error with a real message.
//
// An invalid file is still an error everywhere: a mode that replaced a broken file with the
// defaults and then reported "valid configuration" would hide the only thing it was asked to
// check.
func loadPreferringKey(path string, fl flags) (config.Config, error) {
	cfg, err := config.Load(path)
	if err != nil && diagnosticNeedsNoKey(fl) && strings.Contains(err.Error(), "LLM key is missing") {
		return config.LoadWithoutKey(path)
	}
	return cfg, err
}

// diagnosticNeedsNoKey reports whether this invocation can be served without an LLM key.
func diagnosticNeedsNoKey(fl flags) bool {
	if fl.validateConfig || fl.isolation || fl.doctor || fl.indexAction != "" {
		return true
	}
	// A curator command, unless it is the consolidation pass.
	return fl.curatorAction != "" && !fl.curatorConsolidate
}

// needsOnboarding reports whether the invocation should trigger the first-run
// wizard before proceeding. It returns true when ALL of the following hold:
//
//   - the mode is the TUI or -serve (the interactive modes that need a provider),
//   - no explicit -config was given,
//   - no configuration file exists in the motita home or the working directory.
//
// A run that names a file, or that finds one, is left alone: the wizard is for
// the user who has nothing, and running it on top of an existing configuration
// would overwrite it.
//
// The diagnostic and one-shot modes are excluded because they do not call the
// LLM (-validate-config, -isolation, -version) or run non-interactively
// (-task, -p): launching a wizard from a cron job or a script would hang it.
func (op Options) needsOnboarding(fl flags) bool {
	if !op.willRunTUI(fl) && !fl.serve {
		return false
	}
	if fl.configPath != "" {
		return false
	}
	return resolvedConfigPath(fl) == ""
}

// run is the main body: it loads the configuration, prepares the layers and
// launches the agent with graceful shutdown.
func (op Options) run(fl flags) int {
	// The modes that do not call the LLM do not require the key, but an invalid
	// file is ALWAYS an error: reporting "valid configuration" after replacing
	// the file with the default values would hide exactly the failure being
	// looked for.
	//
	// When no explicit -config is given, the file is looked for in the motita home
	// (~/.motita/motita.yaml) and then in the current directory. If neither exists, the
	// program starts from defaults so the TUI or wizard can run without a file.
	//
	// The home comes FIRST and the working directory SECOND, which is the opposite of how a
	// project-local configuration usually works, and deliberately so: motita's file carries
	// the LLM credentials and the paths to its own state, so it belongs to the user rather
	// than to whichever repository they happened to be standing in. The working directory is
	// still accepted, so an existing setup keeps working and a per-project override stays possible.

	// When entering the TUI or serving the gateway with NO configuration file at
	// all, the onboarding wizard runs first: a first-time user should never see
	// a blank chat or a gateway that cannot answer because no provider was ever
	// chosen. The wizard writes the file the program then loads, so the rest of
	// the run works as if it had been configured all along.
	//
	// The diagnostic modes (-validate-config, -isolation) and one-shot modes
	// (-task, -p, -version) are excluded: they do not need a provider, and
	// launching a wizard from a script would hang it.
	if op.needsOnboarding(fl) {
		if code, done := op.firstRunSetup(&fl); done {
			return code
		}
	}

	// Which file is read is decided in ONE place, because a second caller needs the same answer:
	// the gateway subcommands resolve whether to announce the browser interface from it, and a
	// caller that re-derived it would eventually derive it differently. See resolvedConfigPath.
	//
	// When no explicit -config is given, the file is looked for in the motita home
	// (~/.motita/motita.yaml) and then in the current directory. If neither exists, the
	// program starts from defaults so the TUI or wizard can run without a file.
	cfg, err := loadRunConfig(fl)
	if err != nil {
		fmt.Fprintf(op.Err, "❌ %v\n", err)
		return ConfigError
	}

	// When entering the conversational TUI, keep the chat clean by writing structured
	// logs only to the file, not to the terminal.
	//
	// The decision must be the SAME one that launches the interface, not the flag alone.
	// It used to test only `fl.tui`, while the launch below also enters the TUI when no
	// configuration file exists — so on that path the logger kept writing JSON lines to the
	// terminal, in the middle of the chat. That is the raw JSON that appeared "anywhere":
	// not a rendering bug, a decision taken twice and answered differently.
	if op.willRunTUI(fl) {
		cfg.Agent.LogConsole = false
	}

	// The diagnostic modes check the configuration; they do not run anything, and they must not
	// leave anything behind either.
	//
	// This was a real failure, not a hypothetical one: -validate-config opened the log, which
	// CREATED the workspace directory to hold it, and inside the CI container the configuration
	// directory is mounted read-only — so validating a configuration that was perfectly valid
	// failed on the file it wrote. The same run also created configs/workspace/ in the repository.
	//
	// With no log file, the logger reports to the console, which is where a diagnostic's output
	// belongs anyway.
	if fl.validateConfig || fl.isolation {
		cfg.Agent.LogFile = ""
	}

	log, err := op.newLogger(cfg.Agent)
	if err != nil {
		fmt.Fprintf(op.Err, "❌ %v\n", err)
		return ConfigError
	}
	logx.Install(log)
	defer log.Close()
	for _, note := range cfg.Deprecations() {
		log.Warn(note)
	}

	// A CLIENT of somebody else's gateway builds none of the layers below.
	//
	// The placement is the point, and it is BEFORE the sandbox: the sandbox, the procedure library
	// and the reasoning engine are all the SERVER's job, and the code below builds them anyway.
	// Worse, the sandbox can refuse to be built (it needs permissions this process may not have),
	// so a remote client would fail on a sandbox it can never use - for a machine that is not even
	// the one running the commands.
	//
	// The context is created FIRST so that a client answers Ctrl+C like every other mode: the
	// shutdown path is not the server's property, it is the process's.
	ctx, wait := op.contextWithShutdown(op.BaseCtx, cfg, log)
	defer wait()

	if fl.connect != "" {
		return op.runClient(ctx, fl, cfg)
	}

	// Layer C: the sandbox.
	sandboxOptions := SandboxOptions(cfg, log)
	// The file this run was configured from holds its key too, wherever it is.
	sandboxOptions.HiddenPaths = config.SecretFiles(resolvedConfigPath(fl))
	// The gateway's own secrets wherever they were configured to live, in case they were moved
	// out of the motita home: the bearer token a confined command could use to forge an approved
	// request, and the TLS private key. SecretFiles already hides them in their default home.
	sandboxOptions.HiddenPaths = append(sandboxOptions.HiddenPaths,
		gatewaySecretPaths(cfg)...)
	box, err := op.newSandbox(sandboxOptions)
	if err != nil {
		log.Error("could not prepare the sandbox", "error", err)
		fmt.Fprintf(op.Err, "❌ could not prepare the sandbox: %v\n", err)
		return RunError
	}
	defer box.Close()

	return op.runSandboxed(ctx, fl, cfg, box, log)
}

// runSandboxed is the rest of run once the sandbox exists: the diagnostic modes, then the mode
// the flags ask for.
func (op Options) runSandboxed(
	ctx context.Context, fl flags, cfg config.Config, box *sandbox.Sandbox, log *logx.Logger,
) int {

	if fl.isolation {
		op.reportIsolation(box)
		return Success
	}

	if fl.validateConfig {
		fmt.Fprintf(op.Out, "✅ valid configuration: %s\n", DescribeConfig(cfg))
		fmt.Fprintf(op.Out, "   sandbox isolation: %v\n", box.Isolation())
		return Success
	}

	// The procedure library, built BEFORE the mode is chosen.
	//
	// The prompts shipped in internal/config promise the model a library, and that promise is
	// in every path — the interface, this task run, the plan run below. It has to be kept in
	// every path too: a run that answers "no procedure library is configured" when the model
	// reaches for a procedure spends the turn and teaches it that the tools it was given do
	// not work, which is worse than not offering them at all.
	//
	// Built here rather than in each mode for the reason the package comment gives: one
	// constructor, so a front end cannot have its own idea of what the library is.
	procs := procedures.Open(cfg, log)

	// Layer B: the reasoning engine. In TUI mode the engine may be nil (for
	// example when there is no configuration file yet); the TUI creates it lazily
	// when the user actually starts plan, task or model listing. For all non-TUI
	// modes the engine is required up front.
	if op.willRunTUI(fl) {
		return op.runTUI(ctx, fl, cfg, nil, box, log)
	}

	engine, err := op.newEngine(cfg.LLM, log)
	if err != nil {
		log.Error("could not prepare the reasoning engine", "error", err)
		fmt.Fprintf(op.Err, "❌ %v\n", err)
		return ConfigError
	}

	// The gateway by itself: no interface, just the HTTP face. It is the mode that makes a
	// client on a phone useful on a machine nobody is sitting at.
	//
	// AFTER the engine above, deliberately: a server that starts and then fails on its first
	// client is worse than one that refuses to start, because nobody is watching the first one.
	if fl.serve {
		return op.runServe(ctx, fl, cfg, engine, box, log)
	}

	if fl.plan || fl.prompt != "" {
		return op.runPlan(ctx, fl, cfg, engine, box, log, procs)
	}

	return op.runTask(ctx, fl, cfg, engine, box, log, procs)
}

// runTask runs the agent on the task source: the single task of the flags, or the configured
// source.
func (op Options) runTask(
	ctx context.Context, fl flags, cfg config.Config, engine *llm.Client, box *sandbox.Sandbox, log *logx.Logger,
	procs *procedures.Store,
) int {
	// Task source (a single task takes priority over the configured one).
	source, err := BuildSource(cfg, fl.task, fl.taskFile, log)
	if err != nil {
		log.Error("could not build the task source", "error", err)
		fmt.Fprintf(op.Err, "❌ %v\n", err)
		return ConfigError
	}

	ag := agent.New(cfg, log, engine, box, source)
	// The library and its ledger, installed before the run.
	//
	// Task mode asks the model to reach for a procedure by name — the shipped prompt devotes a
	// section to it — so the shelf has to be there when it does. Without this the model is told
	// it has a library and gets "no procedure library is configured" for every lookup.
	ag.SetLibrary(procs.Library)
	ag.SetNotes(notesVault())
	ag.SetReward(procs.Ledger)
	ag.SetUsage(procs.Usage)

	// Curator: run the deterministic pass on session start if enough time has
	// passed. Cheap (no LLM) and keeps the library tidy without a daemon.
	if cfg.Curator.Enabled {
		curateOnStart(ctx, cfg, procs, engine, log)
	}

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

// onboardingNotice is the first line the interface shows after the wizard ran.
func onboardingNotice(written bool) string {
	if !written {
		return "The setup was cancelled, so nothing is configured yet. Type /config whenever you are ready."
	}
	return "Setup complete. Describe your first task below, or press Tab to ask questions first."
}

// loadRunConfig reads the configuration this run uses: the file resolvedConfigPath names, or the
// defaults when there is none.
func loadRunConfig(fl flags) (config.Config, error) {
	if cfgPath := resolvedConfigPath(fl); cfgPath != "" {
		return loadPreferringKey(cfgPath, fl)
	}
	return config.LoadOrDefault("")
}

// reportIsolation prints the isolation the sandbox applied, and what was asked for and not.
func (op Options) reportIsolation(box *sandbox.Sandbox) {
	fmt.Fprintf(op.Out, "applied isolation: %v\n", box.Isolation())
	if notApplied := box.NotApplied(); len(notApplied) > 0 {
		fmt.Fprintf(op.Out, "requested and not applied:\n")
		for _, s := range notApplied {
			fmt.Fprintf(op.Out, "  - %s\n", s)
		}
	}
}

// curateOnStart runs the curator's pass when enough time has passed since the last one.
func curateOnStart(
	ctx context.Context, cfg config.Config, procs *procedures.Store, engine *llm.Client, log *logx.Logger,
) {
	c := curator.New(cfg.Curator, procs, engine, log, buildSkillRunner)
	if err := c.MaybeRun(ctx); err != nil {
		log.Warn("curator pass failed", "error", err)
	}
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

// newReview builds the background self-improvement fork, or the replacement a test injected.
func (op Options) newReview(
	cfg config.Review, engine *llm.Client, procs *procedures.Store, log *logx.Logger, build review.RunnerBuilder,
) *review.Review {
	if op.NewReview != nil {
		return op.NewReview(cfg, engine, procs, log, build)
	}
	return review.New(cfg, engine, procs, log, build)
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
func (op Options) contextWithShutdown(
	parent context.Context, cfg config.Config, log *logx.Logger,
) (context.Context, func()) {
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
		once.Do(func() {
			close(done)
			cancel()
		})
		<-finished
	}
}

// SandboxOptions translates the configuration into the sandbox's options.
// gatewaySecretPaths are the gateway's configured secrets to keep from a confined command: its
// bearer token file and, when https is configured, its TLS private key. They are resolved against
// the motita home by the loader, so a default token already lies under the directory SecretFiles
// hides; naming it again, or naming a key that is not configured, is harmless (an empty or
// duplicate path is dropped by the sandbox).
func gatewaySecretPaths(cfg config.Config) []string {
	var paths []string
	for _, p := range []string{cfg.Gateway.TokenFile, cfg.Gateway.TLS.Key} {
		if strings.TrimSpace(p) != "" {
			paths = append(paths, p)
		}
	}
	return paths
}

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
		CgroupRoot:    cfg.Sandbox.CgroupRoot,
		Timeout:       cfg.Sandbox.Timeout,
		MaxOutputKB:   cfg.Sandbox.MaxOutputKB,
		Keep:          cfg.Sandbox.KeepEphemeral,
		ToolsDir:      cfg.Sandbox.ToolsDir,
		ConfineWrites: cfg.Sandbox.ConfineWrites,
		FullDisk:      cfg.Sandbox.FullDisk(),
		GitAuthDir:    config.AuthDir(),
		GitHome:       config.HomeDir(),
		GPG:           sandbox.GPGAccess(cfg.Sandbox.GPGSigning, config.HomeDir(), log),
		HiddenPaths:   config.SecretFiles(""),
		Log:           log,
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
