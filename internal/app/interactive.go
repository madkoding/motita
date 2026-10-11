package app

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/madkoding/motita/internal/config"
	"github.com/madkoding/motita/internal/gateway"
	"github.com/madkoding/motita/internal/i18n"
	"github.com/madkoding/motita/internal/llm"
	"github.com/madkoding/motita/internal/logx"
	"github.com/madkoding/motita/internal/sandbox"
	"github.com/madkoding/motita/internal/tui"
)

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
func (op Options) runTUI(
	ctx context.Context, fl flags, cfg config.Config, engine *llm.Client, box *sandbox.Sandbox, log *logx.Logger,
) int {
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
	ui.Notice = fl.notice
	ui.ShowActions = cfg.Gateway.ShowActions
	ui.Lang = i18n.Resolve(cfg.UI.Language, os.Getenv)

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
	// stdin, so it cannot travel over a socket, and a wizard answered on the gateway's host would
	// configure the wrong machine. It is handed to the interface as its Setup, and the client is
	// handed over UNWRAPPED: a wrapper exposes only the methods it declares, and the one that used
	// to sit here hid every optional capability of the client - stopping a run on the gateway,
	// listing and switching sessions, the agent's questions - so /sessions answered "not attached
	// to a gateway" from inside one.
	ui.Runner = client
	ui.Setup = runner.RunConfig
	return ui.Run(ctx)
}

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
func (op Options) attachGateway(
	ctx context.Context, fl flags, cfg config.Config, engine *llm.Client, box *sandbox.Sandbox, log *logx.Logger,
) (tui.Runner, string, func(), int) {
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
	shutdown := func() {
		// No gateway was started, so there is nothing to shut down.
	}
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
func (op Options) startOwnGateway(
	ctx context.Context, fl flags, cfg config.Config, engine *llm.Client, box *sandbox.Sandbox, log *logx.Logger,
) (string, string, *gateway.Server, error) {
	if op.StartGatewayForTest != nil {
		baseURL, token, err := op.StartGatewayForTest(true)
		return baseURL, token, nil, err
	}
	srv, err := op.startGateway(fl, cfg, engine, box, log, true, nil)
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
	default:
		// The question "is anything watching that can interpret escapes" is answered in one
		// place, tui.IsTerminal, and asked from both the colour decision and the mouse
		// feature. Two implementations of it drifted apart once already: the mouse code had
		// its own copy under a different name.
		return !tui.IsTerminal(out)
	}
}
