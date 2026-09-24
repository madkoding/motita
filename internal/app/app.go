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

	"github.com/madkoding/motita/internal/agent"
	"github.com/madkoding/motita/internal/config"
	"github.com/madkoding/motita/internal/gateway"
	"github.com/madkoding/motita/internal/llm"
	"github.com/madkoding/motita/internal/logx"
	"github.com/madkoding/motita/internal/onboard"
	"github.com/madkoding/motita/internal/plan"
	"github.com/madkoding/motita/internal/procedures"
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
	// SpawnGateway brings the service up. It is a seam of the same shape as Exit and RunChild: the
	// real one re-executes the program detached, and a test cannot do that without starting a
	// second agent.
	SpawnGateway func(ctx context.Context, spec spawnSpec) error
	// SignalProcess asks the gateway to stop. Injectable because killing a real process from a test
	// would leave the assertion racing the operating system.
	SignalProcess func(pid int) error
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
  -init              first-run wizard: choose the provider, the model and the
                     check, and write a working configuration
  -validate-config   validate the configuration and exit (does not call the LLM)
  -isolation         print the available sandbox isolation and exit
  -version           print the version and exit

Commands:
  gateway start      bring the gateway up as a service and leave it running
  gateway stop       stop the gateway named by the service file
  gateway status     report whether a gateway is running

The gateway listens on every interface by default and enforces no origin rules: it is
reachable the way a machine with a fresh, empty firewall table is. Narrow it by adding
rules to gateway.allow - "lan", an address, a network, or "!any" for this machine only.

Environment variables: MOTITA_* (see README.md; also accepts OPENAI_API_KEY).
`, config.Default().GatewayListen())
}

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
	serve          bool
	gateway        string
	// connect is the address of a gateway somebody else is running. Non-empty means this
	// process is a CLIENT: it builds no sandbox, opens no procedure library and creates no
	// reasoning engine, because all three are the server's job.
	connect string
	// session is the conversation to attach to, by id.
	session string
	// gatewayAction is the action of `motita gateway <action>`. A POSITIONAL argument, not a
	// flag, which is why it is read before the flag loop rather than inside it.
	gatewayAction string
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
		return op.initConfig(fl)
	}

	// A subcommand is dispatched here, before the main path, because it pays for neither the
	// reasoning engine nor a sandbox: `gateway start` re-executes the program rather than serving in
	// this process, and `stop` and `status` only read a file and ask a port a question.
	if fl.gatewayAction != "" {
		return op.runGatewayCommand(op.BaseCtx, fl.gatewayAction, fl)
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
	if op.Version == "" {
		// The default lives in cmd/agent (`var version = "dev"`), so a test that builds Options by
		// hand leaves this empty - and an interface showing an empty version is worse than one
		// showing "dev": it looks like the version is broken instead of like an unversioned build.
		op.Version = "dev"
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
	if op.ServiceFile == "" {
		op.ServiceFile = gateway.ServiceFilePath()
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
	if op.SpawnGateway == nil {
		op.SpawnGateway = spawnDetached
	}
	if op.SignalProcess == nil {
		op.SignalProcess = signalByPID
	}
	if op.GatewayWait == 0 {
		op.GatewayWait = 10 * time.Second
	}
	if op.WriteServiceFile == nil {
		op.WriteServiceFile = gateway.WriteServiceFile
	}
}

// valueFlags are the flags that consume the argument after them. The list exists for ONE reason: to
// tell a flag's VALUE apart from a subcommand. `-session gateway` names a conversation called
// "gateway", and reading that word as a command would make `motita -session gateway -connect
// host` start a service instead of attaching a client - a silent reversal of what was asked for.
var valueFlags = map[string]bool{
	"-config": true, "--config": true,
	"-task": true, "--task": true,
	"-task-file": true, "--task-file": true,
	"-gateway": true, "--gateway": true,
	"-connect": true, "--connect": true,
	"-session": true, "--session": true,
	"-p": true, "--prompt": true,
}

// flagValues marks the positions of arguments that are a flag's VALUE rather than a word of their
// own. It is derived from valueFlags and not written out again, so a flag added above is skipped
// below without anyone remembering to do it.
func flagValues(args []string) []bool {
	skip := make([]bool, len(args))
	for i := 0; i < len(args); i++ {
		name, _, hasValue := strings.Cut(args[i], "=")
		// "-flag=value" carries its own value, so the next argument is a word of its own.
		if hasValue || !valueFlags[name] {
			continue
		}
		if i+1 < len(args) {
			skip[i+1] = true
			i++
		}
	}
	return skip
}

// parse reads the accepted flags. It is done by hand, and not with the flag
// package over os.Args, so the function stays pure and does not depend on the
// process' global state (which is what used to make it impossible to test).
func parse(args []string) (flags, error) {
	var b flags

	// The subcommand is read FIRST, because it is a POSITIONAL argument and the loop below is
	// written for flags: `motita gateway start` reaching that loop would be reported as an
	// unknown flag named "gateway", which is a confusing way to say the user used the right word in
	// the right place.
	//
	// It is found by scanning for the word while SKIPPING the values of flags, which is why there is
	// a list of the flags that take one. Without that skipping, `-session gateway` would be read as
	// the subcommand and `motita -session gateway -connect host` would start a SERVICE instead of
	// a client that attaches to a conversation called "gateway".
	skip := flagValues(args)
	action := ""
	for i := 0; i < len(args); i++ {
		if skip[i] || args[i] != "gateway" {
			continue
		}
		if i+1 >= len(args) {
			return b, fmt.Errorf("gateway needs an action: start, stop or status")
		}
		switch strings.ToLower(strings.TrimSpace(args[i+1])) {
		case "start", "stop", "status":
			// Recorded as the user wrote it: what they typed is what gets reported back.
			action = args[i+1]
			// Removed from the argument list so the flag loop below never sees them: a positional
			// argument is not a flag, and leaving it there would end in "unknown flag: gateway".
			args = append(append([]string{}, args[:i]...), args[i+2:]...)
		default:
			// Rejected with the list rather than ignored: falling through would turn a typo into
			// something else entirely - `motita gateway strat` starting the interface.
			return b, fmt.Errorf("unknown gateway action %q: start, stop or status", args[i+1])
		}
		break
	}
	b.gatewayAction = action

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
		case "-serve", "--serve":
			b.serve = true
		case "-gateway", "--gateway":
			v, err := next()
			if err != nil {
				return b, err
			}
			b.gateway = v
		case "-connect", "--connect":
			v, err := next()
			if err != nil {
				return b, err
			}
			// An EMPTY address is refused here rather than passed on: "-connect" with nothing
			// after it, or "-connect=" with nothing after the equals, would otherwise produce a
			// client pointed at nothing and fail somewhere far from the mistake.
			if strings.TrimSpace(v) == "" {
				return b, fmt.Errorf("-connect needs an address, like -connect 127.0.0.1:7477")
			}
			b.connect = strings.TrimSpace(v)
		case "-session", "--session":
			v, err := next()
			if err != nil {
				return b, err
			}
			b.session = strings.TrimSpace(v)
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
	// A process cannot be a gateway and a client of one at the same time.
	//
	// Refused rather than resolved because there is no sensible resolution: -serve makes this
	// process the thing that holds conversations, -connect makes it a viewer of somebody else's,
	// and the two contradict. Picking one silently would leave the user with an interface for the
	// thing they did not ask for.
	if b.serve && b.connect != "" {
		return b, fmt.Errorf("-serve and -connect cannot be used together: -serve makes this process a gateway, and -connect makes it a client of one")
	}
	return b, nil
}

// initConfig runs the first-run wizard. The default destination is
// ./motita.yaml next to wherever the agent is being set up, which is what the
// summary then tells the user to pass with -config.
func (op Options) initConfig(fl flags) int {
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

	fmt.Fprintf(op.Out, "Welcome to motita.\n")
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

// loadPreferringKey loads the configuration, tolerating a missing key in the two
// modes that do not call the LLM.
//
// Those modes (-validar-config, -aislamiento) exist to check the file, and there
// not having a key is legitimate. An invalid file is still an error everywhere: a
// mode that replaced a broken file with the defaults and then reported "valid
// configuration" would hide the only thing it was asked to check.
func loadPreferringKey(path string, fl flags) (config.Config, error) {
	cfg, err := config.Load(path)
	if err != nil && (fl.validateConfig || fl.isolation) && strings.Contains(err.Error(), "LLM key is missing") {
		return config.LoadWithoutKey(path)
	}
	return cfg, err
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
	// the LLM credentials and the paths to its own state, so it belongs to the user rather than
	// to whichever repository they happened to be standing in. The working directory is still
	// accepted, so an existing setup keeps working and a per-project override stays possible.
	var cfg config.Config
	var err error
	// Which file is read is decided in ONE place, because a second caller needs the same answer:
	// the gateway subcommands resolve whether to announce the browser interface from it, and a
	// caller that re-derived it would eventually derive it differently. See resolvedConfigPath.
	//
	// When no explicit -config is given, the file is looked for in the motita home
	// (~/.motita/motita.yaml) and then in the current directory. If neither exists, the
	// program starts from defaults so the TUI or wizard can run without a file.
	if cfgPath := resolvedConfigPath(fl); cfgPath != "" {
		cfg, err = loadPreferringKey(cfgPath, fl)
		if err != nil {
			fmt.Fprintf(op.Err, "❌ %v\n", err)
			return ConfigError
		}
	} else {
		cfg, err = config.LoadOrDefault("")
		if err != nil {
			fmt.Fprintf(op.Err, "❌ %v\n", err)
			return ConfigError
		}
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
	var engine *llm.Client
	if op.willRunTUI(fl) {
		return op.runTUI(ctx, fl, cfg, nil, box, log)
	}

	engine, err = op.newEngine(cfg.LLM, log)
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
	ag.SetReward(procs.Ledger)

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

// willRunTUI reports whether the invocation ends in the conversational interface.
//
// This is the ONLY predicate that answers that question, and that is the point. The logger
// has to be silenced before the configuration is loaded — structured lines printed into the
// chat are not a rendering bug, they are the wrong decision taken about where the log goes —
// and the interface is launched further down. When those two places each carried their own
// copy of the test they drifted, and every invocation that reached the interface through the
// defaults kept writing logs to the terminal: the raw JSON that appeared in the middle of the
// chat. One question, one function.
//
// It is a method on Options rather than a flag field because the answer depends on the
// positional arguments too, which are not part of the flag set.
func (op Options) willRunTUI(fl flags) bool {
	// FIRST, before the -tui check below: -serve draws nothing, so a process asked to serve is
	// not a process asked to draw. "-tui -serve" is contradictory and -serve wins.
	if fl.serve {
		return false
	}
	if fl.tui {
		return true
	}
	// Any explicit request that produces its own output rules the interface out: it would
	// otherwise swallow the result the user asked for.
	if fl.configPath != "" || fl.task != "" || fl.taskFile != "" {
		return false
	}
	if fl.validateConfig || fl.isolation || fl.version || fl.initConfig {
		return false
	}
	return len(op.Args) == 0
}

// runTUI starts the interactive text user interface.
//
// The interface is handed a CLIENT of a gateway, not a second door into the agent. Two doors would
// be two places the conversation lives, and the phone and the terminal would then be talking to
// different agents: the text interface is a front end like any other, and this is where that stops
// being a slogan.
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
	ui.Out = op.Out
	ui.Err = op.Err
	ui.NoColor = noColour(os.Getenv, op.Out)

	// The interface CONNECTS to a gateway rather than assuming it is the only one.
	//
	// This is the split the user asked for: `motita` brings up an interface, and the agent behind
	// it is a service that can already be running. Attaching to one that is there is also what makes
	// `gateway start` mean anything - a service nobody can connect to is a service for nobody.
	client, version, release, code := op.attachGateway(ctx, fl, cfg, engine, box, log)
	if code != Success {
		return code
	}
	if release != nil {
		defer release()
	}
	if client == nil {
		// The documented escape hatch: with the gateway off the interface takes the direct path it
		// has always had.
		return ui.Run(ctx)
	}
	// The interface names the build ACTUALLY ANSWERING, which is not always this one: a service an
	// earlier `gateway start` left running can be an older binary, and a version drawn from this
	// process would then be a confident lie - the exact thing the version row exists to prevent.
	ui.Version = version

	// The wizard runs HERE, in the terminal this process was started from: it reads lines from
	// stdin, so it cannot travel over a socket. That is why it is handed to the interface as a
	// WRAPPER rather than left to the client: the client speaks to a gateway that may be on another
	// machine, and a wizard answered over there would be configuring the wrong host.
	ui.Runner = localWizard{Runner: client, runConfig: runner.RunConfig}
	return ui.Run(ctx)
}

// localWizard hands the interface THIS process's first-run wizard, whatever client it is speaking
// through.
//
// It exists because the wizard is the one thing that cannot be remote: it reads from the terminal in
// front of the user and writes the configuration of the machine they are sitting at. A client of a
// gateway on another host has no wizard of its own - and should not, because running one over there
// would set up the wrong machine.
type localWizard struct {
	tui.Runner
	runConfig func(context.Context) error
}

func (l localWizard) RunConfig(ctx context.Context) error { return l.runConfig(ctx) }

// attachGateway resolves WHICH gateway this process will speak through, and returns the client for
// it together with the function that releases it.
//
// It is a function of its own because the decision is the whole feature and it is otherwise
// invisible: the interface is handed a client either way, and which gateway is behind that client
// is not something the rest of this file can see. (The same reason the NewClient seam exists.)
//
// Three outcomes, and all of them are deliberate:
//
//   - The gateway is off, or was turned off with -gateway off: no client, no release, and the
//     caller keeps the direct path it has always had. A nil client is the escape hatch, not a
//     failure.
//   - A gateway is ALREADY running: attach to it and release nothing. It is not ours to shut down.
//     Killing a service the user deliberately started, because a terminal happened to attach to it,
//     would be destroying their setup by looking at it.
//   - Nothing is running: bring one up in THIS process and attach to it, and give back the release.
//     This one IS ours, and it is shut down when the interface ends.
//
// The gateway is brought up here rather than re-executed with -serve, and that is deliberate. The
// re-exec belongs to `gateway start`, whose whole purpose is a gateway that outlives the shell that
// started it. Here the opposite is wanted: this gateway exists for this interface, so it is bound
// in-process and dies with the process no matter how the process dies. A child would survive a
// SIGKILL of the interface and leak, and a leaked gateway is invisible until somebody counts ports.
func (op Options) attachGateway(ctx context.Context, fl flags, cfg config.Config, engine *llm.Client, box *sandbox.Sandbox, log *logx.Logger) (tui.Runner, string, func(), int) {
	if !cfg.Gateway.Enabled || strings.EqualFold(strings.TrimSpace(fl.gateway), "off") {
		return nil, "", nil, Success
	}

	found, ok, err := op.discover(ctx)
	if err != nil {
		fmt.Fprintf(op.Err, "%v\n", err)
		return nil, "", nil, ConfigError
	}
	if ok {
		// Somebody else's gateway, or one an earlier command started: either way it is not ours.
		//
		// The version comes from THAT gateway through discovery, not from this process. The two
		// differ whenever a service is left running across an upgrade, and reporting our own build
		// there would name a binary that is not answering anything.
		return op.newClient(found.BaseURL, found.Token, fl.session), found.Version, nil, Success
	}

	// Nothing is running, so one is brought up for this interface and it is OURS: whatever happens
	// to this process, its gateway goes with it.
	baseURL, token, srv, err := op.startOwnGateway(ctx, fl, cfg, engine, box, log)
	if err != nil {
		fmt.Fprintf(op.Err, "the gateway could not start: %v\n", err)
		return nil, "", nil, ConfigError
	}
	// runGatewayLoop is what STARTS the server - it serves in a goroutine and hands back the
	// shutdown. Starting the gateway without it would leave a bound socket that nobody answers on:
	// every client would connect and then wait forever, which is exactly how this was found.
	shutdown := func() {}
	if srv != nil {
		shutdown = op.runGatewayLoop(ctx, srv, log)
	}
	release := func() {
		shutdown()
		// The file is cleared as part of the release rather than left to the next start: between
		// this process ending and the next one looking, the file would name a gateway that is gone,
		// and a stale entry is exactly what discovery trusts.
		_ = gateway.RemoveServiceFile(op.serviceFilePath())
	}
	// The gateway we just brought up IS this process (the same Options build it), so its version is
	// ours - unlike the branch above, where it belongs to somebody else.
	return op.newClient(baseURL, token, fl.session), op.Version, release, Success
}

// startOwnGateway brings up the gateway this process will speak through, in-process.
//
// It is in-process rather than a re-executed child, and that is the opposite of what
// `gateway start` does - deliberately. The service exists to outlive the shell that started it.
// This one exists FOR this interface, so it must die with it however the process dies: a child
// would survive a SIGKILL of the interface and leak, and a leaked gateway is invisible until
// somebody counts ports.
//
// The test seam returns only the three things the caller uses - where, what token, and whether it
// worked - so a test asserts the DECISION without binding a port or depending on the machine.
func (op Options) startOwnGateway(ctx context.Context, fl flags, cfg config.Config, engine *llm.Client, box *sandbox.Sandbox, log *logx.Logger) (string, string, *gateway.Server, error) {
	if op.StartGatewayForTest != nil {
		baseURL, token, err := op.StartGatewayForTest(true)
		return baseURL, token, nil, err
	}
	srv, err := op.startGateway(fl, cfg, engine, box, log, true)
	if err != nil {
		return "", "", nil, err
	}
	return srv.BaseURL(), srv.Token(), srv, nil
}

// noColour reports whether the interface must render without colour.
//
// Two rules, both from the design guide, and both were missing: the NO_COLOR
// variable is the cross-tool convention a user sets once and expects every
// program to honour, and TERM=dumb means the terminal cannot do escapes at all —
// sending them there prints the sequences as text.
//
// The writer is checked for being a terminal only as a last resort, and the check
// is deliberately conservative: when the output is not a file or a terminal at all
// (a buffer in a test, a pipe into a log) colour is left off, because nothing is
// watching that can interpret it.
func noColour(getenv func(string) string, out io.Writer) bool {
	if _, set := os.LookupEnv("NO_COLOR"); set {
		return true
	}
	switch strings.ToLower(strings.TrimSpace(getenv("TERM"))) {
	case "dumb", "":
		return true
	}
	// The question "is anything watching that can interpret escapes" is answered in one
	// place, tui.IsTerminal, and asked from both the colour decision and the mouse
	// feature. Two implementations of it drifted apart once already: the mouse code had
	// its own copy under a different name.
	return !tui.IsTerminal(out)
}

// runPlan runs the read-only plan/chat mode. It uses the reasoning engine and the
// sandbox, but it never delegates to the task/anchor flow.
func (op Options) runPlan(ctx context.Context, fl flags, cfg config.Config, engine *llm.Client, box *sandbox.Sandbox, log *logx.Logger, procs *procedures.Store) int {
	cfg.Agent.ReadOnly = true
	ag := agent.New(cfg, log, engine, box, nil)
	ag.SetLibrary(procs.Library)
	ag.SetReward(procs.Ledger)

	planner := plan.New(engine, ag).
		WithTimeout(planDefaultTimeout(cfg)).
		WithLoops(planDefaultLoops(cfg)).
		// The same library the task path uses, so a procedure written down in one mode is
		// reachable from the other and a verdict lands on one shelf rather than two.
		WithLibrary(procs.Library).
		WithReward(procs.Ledger).
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
