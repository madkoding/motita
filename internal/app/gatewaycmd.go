package app

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/madkoding/starlight/internal/config"
	"github.com/madkoding/starlight/internal/gateway"
)

// The three actions `starlight gateway <action>` knows, and the help shown when the action is
// missing or misspelled.
const gatewayCommandHelp = `Usage: starlight gateway <action>

Actions:
  start    bring the gateway up as a service and leave it running
  stop     stop the gateway named by the service file
  status   report whether a gateway is running
`

// runGatewayCommand dispatches `starlight gateway <action>`.
//
// The action is validated by parse, which rejects anything but start, stop and status, so the
// default below cannot be reached through the command line. It stays because this function should
// not DEPEND on that: the check lives in one place today, and a second caller that forgot to
// validate would otherwise fall off the end of a switch into doing nothing at all - the quietest
// possible failure. The repository already keeps branches for that reason (see randReader and the
// filesystem calls in gateway/token.go), and it is covered by calling it directly.
func (op Options) runGatewayCommand(ctx context.Context, action string, fl flags) int {
	switch strings.ToLower(strings.TrimSpace(action)) {
	case "start":
		return op.gatewayStart(ctx, fl)
	case "stop":
		return op.gatewayStop(ctx, fl)
	case "status":
		return op.gatewayStatus(ctx, fl)
	default:
		fmt.Fprint(op.Out, gatewayCommandHelp)
		return Success
	}
}

// serviceFilePath is where the description of a running gateway is read and written.
func (op Options) serviceFilePath() string { return op.ServiceFile }

// discover asks whether a gateway is running at the address the service file names.
//
// It goes through a seam of the same shape as ServeGateway and NewClient, and for the same reason:
// the states that matter here - the file changing between the confirmation that a gateway came up
// and the report of where it is - are races in reality and cannot be produced from a test without
// one. The default is the real thing.
func (op Options) discover(ctx context.Context) (gateway.Found, bool, error) {
	if op.DiscoverGateway != nil {
		return op.DiscoverGateway(ctx, op.serviceFilePath())
	}
	return gateway.Discover(ctx, op.serviceFilePath(), nil)
}

// gatewayStart brings the gateway up as a SERVICE and returns, leaving it running.
//
// It is the command that makes the split real: a user runs it once, and then opens terminals,
// browsers or phones against the gateway that is already there - instead of every client bringing
// up its own agent, which is what happens without it.
//
// It RE-EXECUTES itself rather than forking an in-process server. A server goroutine in this
// process would die with the shell that started it, and `gateway start` would mean "a gateway that
// stops when you close the window" - the opposite of what the command says. Re-exec is also the
// only way to get a gateway whose own process is the thing being signalled, which is what makes
// `stop` able to reach it.
func (op Options) gatewayStart(ctx context.Context, fl flags) int {
	// An already-running gateway is reported, not doubled. There is ONE service file per home, so
	// a second start would overwrite the entry and leave the first process running with nothing
	// pointing at it - alive, invisible, and unreachable by `stop`.
	found, ok, err := op.discover(ctx)
	if err != nil {
		fmt.Fprintf(op.Err, "%v\n", err)
		return ConfigError
	}
	if ok {
		fmt.Fprintf(op.Out, "a gateway is already running at %s (pid %d)\n", found.BaseURL, found.PID)
		return Success
	}

	// The child is this same program with -serve, detached. Everything it needs it reads from the
	// same configuration this process read, so there is one source of truth for the listen address
	// and the token path.
	if err := op.SpawnGateway(ctx, op.spawnSpec(fl)); err != nil {
		fmt.Fprintf(op.Err, "the gateway could not be started: %v\n", err)
		return ConfigError
	}

	// Wait for it to ANSWER before reporting success.
	//
	// Reporting success after spawning would be reporting that a process was created, not that a
	// gateway is serving. The difference is the whole value of the command: the user's next move is
	// to connect a client, and a client that connects to a gateway still binding its port fails with
	// an error that has nothing to do with what went wrong.
	if err := op.waitForGateway(ctx); err != nil {
		fmt.Fprintf(op.Err, "the gateway did not come up: %v\n", err)
		return ConfigError
	}
	found, ok, err = op.discover(ctx)
	if err != nil {
		fmt.Fprintf(op.Err, "%v\n", err)
		return ConfigError
	}
	if !ok {
		// It answered a moment ago and now it does not, so there is nothing to name. Reported rather
		// than described as running, because the user is about to depend on it.
		fmt.Fprintf(op.Err, "the gateway started but cannot be found afterwards\n")
		return ConfigError
	}
	fmt.Fprintf(op.Out, "the gateway is running at %s (pid %d, %s)\n", found.BaseURL, found.PID, found.Version)
	// The link is announced HERE and only here, in the command that brings the gateway up: it IS
	// the credential's handoff. `gateway status` deliberately does not print it - that is the
	// command a user runs in front of someone else while asking "is it up?" - and the token
	// remains readable in the token file for anyone who needs the link again.
	if op.webUIEnabled(fl) {
		announceWebUI(op.Out, found)
	}
	return Success
}

// announceWebUI prints the one link into the browser interface.
//
// The token goes in the URL FRAGMENT and nowhere else. A fragment is never sent to the server and
// never appears in a Referer or a server log, which is the only way to hand a secret over in a
// URL without it travelling - and this is the one moment the token is handed out in full. The
// page trades it for a cookie immediately and drops it.
//
// It is written to Out, never through the logger: a log file is kept, copied and pasted into
// issues, and this link IS the credential.
func announceWebUI(out io.Writer, found gateway.Found) {
	if strings.TrimSpace(found.Token) == "" {
		return
	}
	fmt.Fprintf(out, "\nthe interface is at %s/#t=%s\n", found.BaseURL, found.Token)
	fmt.Fprintln(out, "open that link once: the page trades the fragment for a cookie and drops it")
	if found.Reachable {
		// Said explicitly, because the address above is loopback and a reader would otherwise
		// conclude the gateway is local when it has just been opened to the network. The link is
		// not rewritten to a guessed LAN address: the address this machine is known by depends on
		// which network the client is on, and this process cannot see the client.
		fmt.Fprintln(out, "this gateway is reachable from the network: from another machine, use that")
		fmt.Fprintln(out, "machine's address for this host with the same port, and this same fragment.")
		fmt.Fprintln(out, "there is no TLS, so anyone on the network can read the token in transit.")
	}
}

// webUIEnabled asks whether the gateway this command just started serves the interface.
//
// It reads the CONFIGURATION rather than guessing, because the subcommands pay for neither the
// engine nor a sandbox and so never load one: an operator who turned the interface off in the
// YAML, or with STARLIGHT_GATEWAY_WEBUI, must not be handed a link to a page their gateway
// answers 404 on. The load is the keyless one, exactly like the version path, because a
// subcommand has no business demanding a credential to answer this question.
//
// The seam is there for the same reason ServeGateway and DiscoverGateway are: what the tests need
// to pin is the shape of the announcement, not the configuration loader underneath it.
func (op Options) webUIEnabled(fl flags) bool {
	if op.InterfaceEnabled != nil {
		return op.InterfaceEnabled()
	}
	cfg, err := config.LoadWithoutKey(fl.configPath)
	if err != nil {
		// A configuration that cannot be read is reported by the command that needs it; here the
		// honest reading is "we cannot claim there is an interface", so nothing is announced.
		return false
	}
	return cfg.Gateway.WebUI
}

// gatewayStop stops the gateway named by the service file.
//
// The identity is checked before anything is signalled. The pid in the file could have been recycled
// by the operating system, and killing a recycled pid means killing an unrelated process - a bug
// that looks like a random program dying days later, which is about the hardest kind to trace back.
// /v1/health is what confirms the process is ours.
func (op Options) gatewayStop(ctx context.Context, fl flags) int {
	found, ok, err := op.discover(ctx)
	if err != nil {
		fmt.Fprintf(op.Err, "%v\n", err)
		return ConfigError
	}
	if !ok {
		// Either nothing runs or the entry is stale. Both mean the request is already satisfied, and
		// the stale entry is cleared so the next start does not hesitate over it.
		fmt.Fprintln(op.Out, "no gateway is running.")
		_ = gateway.RemoveServiceFile(op.serviceFilePath())
		return Success
	}

	if err := op.SignalProcess(found.PID); err != nil {
		fmt.Fprintf(op.Err, "the gateway (pid %d) could not be signalled: %v\n", found.PID, err)
		return ConfigError
	}

	// Confirmed gone rather than assumed gone: a signal is a request, and reporting success before
	// the gateway stopped answering would be reporting an intent as an outcome.
	if err := op.waitForGatewayGone(ctx, found.BaseURL); err != nil {
		fmt.Fprintf(op.Err, "the gateway did not stop: %v\n", err)
		return ConfigError
	}
	_ = gateway.RemoveServiceFile(op.serviceFilePath())
	fmt.Fprintf(op.Out, "the gateway (pid %d) stopped.\n", found.PID)
	return Success
}

// gatewayStatus reports whether a gateway is running.
//
// It never fails. It exists to answer a question, and reporting a failure for the answer "no" would
// make it useless in exactly the case it is used: a user checking whether there is one.
func (op Options) gatewayStatus(ctx context.Context, fl flags) int {
	found, ok, err := op.discover(ctx)
	if err != nil {
		// Even a corrupt file leaves status useful: it says the state is not "nothing running" but
		// "I cannot tell", which is a different thing the user has to fix.
		fmt.Fprintf(op.Err, "%v\n", err)
		return ConfigError
	}
	if !ok {
		fmt.Fprintln(op.Out, "no gateway is running.")
		return Success
	}
	fmt.Fprintf(op.Out, "the gateway is running at %s (pid %d, %s)\n", found.BaseURL, found.PID, found.Version)
	return Success
}

// waitForGateway polls until a gateway answers at the address the service file names, or the wait
// runs out.
//
// It is a poll rather than a sleep because the time a gateway takes to bind is not a constant: a
// fixed sleep either wastes a second on every start or fails on a loaded machine, and the failure
// is the worse half.
func (op Options) waitForGateway(ctx context.Context) error {
	return op.waitForGatewayFor(ctx, op.GatewayWait, func() (bool, error) {
		_, ok, err := op.discover(ctx)
		if err != nil {
			return false, err
		}
		return ok, nil
	})
}

// waitForGatewayGone polls until nothing answers at baseURL, or the wait runs out.
func (op Options) waitForGatewayGone(ctx context.Context, baseURL string) error {
	address := strings.TrimPrefix(baseURL, "http://")
	return op.waitForGatewayFor(ctx, op.GatewayWait, func() (bool, error) {
		if _, err := gateway.ProbeHealth(ctx, address); err != nil {
			// A failed probe is NOT proof the gateway is gone: a cancelled context makes every
			// request fail, and reading that as "it stopped" would report a stop that never
			// happened - the exact thing this confirmation exists to prevent. The context is
			// checked first, so only a genuine failure to answer counts.
			if ctxErr := ctx.Err(); ctxErr != nil {
				return false, ctxErr
			}
			return true, nil // nothing answers: it is gone
		}
		return false, nil
	})
}

// waitForGatewayFor is the shared deadline: the two waits above differ in the question they ask and
// not in how they ask it, and two copies of a deadline is two places for it to drift.
//
// The poll interval comes from op.waitSignal, which is the same countdown the shutdown loop uses,
// with a fallback so the function does not DEPEND on complete() having run. A nil seam here would
// be a nil dereference in the middle of starting a service, and the default costs one comparison.
func (op Options) waitForGatewayFor(ctx context.Context, timeout time.Duration, ok func() (bool, error)) error {
	wait := op.waitSignal
	if wait == nil {
		wait = func(d time.Duration) <-chan time.Time { return time.After(d) }
	}
	deadline := time.Now().Add(timeout)
	for {
		done, err := ok()
		if err != nil {
			return err
		}
		if done {
			return nil
		}
		if !time.Now().Before(deadline) {
			return fmt.Errorf("it did not answer within %s", timeout)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-wait(20 * time.Millisecond):
		}
	}
}

// exePath is the program to re-execute for the service.
func (op Options) exePath() string { return op.ExePath }

// spawnSpec is everything the service process needs to be the SAME agent this command was pointed
// at: which program, which configuration, and which address.
//
// The configuration travels as a flag and NOT as an inherited environment. The child is detached
// and long-lived, so anything it inherits it keeps for its whole life, and an LLM key sitting in a
// service's environment is readable by every process of that user. The path is what it needs and
// all it needs: the child reads the file under the same rules this process did.
//
// Dropping the flag was a real bug: `starlight -config custom.yaml gateway start` started a service
// that ignored custom.yaml and came up on the defaults, so the gateway the user asked for was never
// the gateway they got - and the failure showed up much later, as a client talking to the wrong
// agent.
type spawnSpec struct {
	ExePath string
	Config  string
	Listen  string
}

func (op Options) spawnSpec(fl flags) spawnSpec {
	return spawnSpec{ExePath: op.exePath(), Config: fl.configPath, Listen: fl.gateway}
}

// spawnDetached re-executes this program with -serve in its own session.
//
// It is the default of the SpawnGateway seam. It is a separate function from the seam itself so the
// real behaviour stays readable next to the reason it exists, while tests replace only the seam.
func spawnDetached(ctx context.Context, spec spawnSpec) error {
	args := []string{"-serve"}
	if strings.TrimSpace(spec.Config) != "" {
		args = append(args, "-config", spec.Config)
	}
	if strings.TrimSpace(spec.Listen) != "" {
		args = append(args, "-gateway", spec.Listen)
	}
	cmd := exec.CommandContext(ctx, spec.ExePath, args...)
	detach(cmd)
	// The output is discarded rather than inherited: the service outlives this terminal, and a
	// gateway writing into a pipe whose reader has exited would block on a full buffer and stop
	// serving. Its own log file is where it reports.
	cmd.Stdout = nil
	cmd.Stderr = nil
	cmd.Stdin = nil
	if err := cmd.Start(); err != nil {
		return err
	}
	// The parent does not wait: the service is not its child to reap. Releasing it is what keeps the
	// process a service instead of a child whose parent has to stay alive.
	return cmd.Process.Release()
}

// findProcess and executablePath are the two OS calls whose failure branches cannot be provoked
// from a test: on unix FindProcess never fails, and os.Executable fails only on a system broken
// enough that nothing else would run either. Both are variables so their branches are TESTS rather
// than hypotheticals - the same shape the repository uses for randReader and the filesystem calls
// in gateway/token.go, where the reason is the same.
var (
	findProcess    = os.FindProcess
	executablePath = os.Executable
)

// signalByPID asks a process to stop.
//
// SIGTERM and not SIGKILL: the gateway closes its listener and its clients cleanly, which is the
// difference between stopping a service and cutting one off.
func signalByPID(pid int) error {
	proc, err := findProcess(pid)
	if err != nil {
		return err
	}
	return proc.Signal(termSignal())
}
