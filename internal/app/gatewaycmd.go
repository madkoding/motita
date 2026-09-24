package app

import (
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"sort"
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
	// What this gateway will serve and to whom is stated HERE, in the command that brings it up,
	// because it is the one moment the operator is looking. `gateway status` deliberately stays
	// quiet about it: that is the command run in front of someone else while asking "is it up?".
	//
	// Both facts come from the RUNNING gateway through the service file, never from this process's
	// configuration: a service an earlier command left running can be an older binary with an older
	// rule set, and describing this process's file would then describe a gateway that is not the one
	// answering.
	announceExposure(op.Out, found)
	// The link is announced HERE and only here: it IS the credential's handoff. The token remains
	// readable in the token file for anyone who needs the link again.
	if op.webUIEnabled(fl) {
		announceWebUI(op.Out, found)
	}
	return Success
}

// announceExposure says how far the gateway reaches and who it will serve.
//
// It splits one question into the two that it really is, which is the whole point of the rule model:
// a BIND is where the socket is open, and the RULES are who may use it. Reporting only the first
// would describe a gateway as exposed when a single deny rule has narrowed it to one machine, and
// reporting only the second would hide that the socket is answering every interface.
//
// Nothing here names a specific LAN address to use. The address this machine is known by depends on
// the network the client is on, and this process cannot see the client, so guessing one would be
// handing out an address that may not resolve. What the operator needs in order to work that out is
// the port and the rule set, and both are printed.
func announceExposure(out io.Writer, found gateway.Found) {
	if !found.Reachable {
		// Bound to loopback: nothing outside this machine can reach it, whatever the rules say, and
		// there is nothing to warn about.
		return
	}
	fmt.Fprintf(out, "\nthis gateway is listening on every interface (port %s): any machine that can\n", portOf(found.BaseURL))
	fmt.Fprintln(out, "reach this host may connect, subject to the rules below.")
	if found.Allow == "every origin" {
		// The documented default, and the one case that deserves to be spelled out rather than
		// left to a rule list that says nothing: it is the same posture as a machine with a fresh,
		// empty firewall table, and an operator who did not expect it has to find out now.
		fmt.Fprintln(out, "no origin rules are set, so EVERY origin is accepted (gateway.allow is empty).")
		fmt.Fprintln(out, "add a rule to gateway.allow to narrow it: \"lan\", an address, a network, or")
		fmt.Fprintln(out, "\"!any\" for this machine only.")
	} else {
		fmt.Fprintf(out, "gateway.allow: %s\n", found.Allow)
	}
	fmt.Fprintln(out, "there is no TLS, so the token travels in clear text to every one of them.")
}

// portOf extracts the port from a base URL, for the sentence above it.
func portOf(baseURL string) string {
	address := strings.TrimPrefix(baseURL, "http://")
	if _, port, err := net.SplitHostPort(address); err == nil {
		return port
	}
	// Unreachable through a real server: BaseURL is built from the listener's own host:port. An
	// empty string is the honest fallback for a URL whose port cannot be read, and it leaves the
	// sentence readable rather than printing a fragment of something else.
	return ""
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
	if !found.Reachable {
		// Bound to loopback: the link above IS the only way in, and offering network addresses
		// would send the reader to an address that refuses them.
		return
	}
	// Reachable from the network, so the link above is NOT the one a browser on another machine
	// needs: it names loopback, which every machine resolves to itself. The addresses below are
	// this host's own, so one of them is ready to paste - which is the whole point, because the
	// address a client must use is not something the operator can work out from a loopback link.
	addresses := lanAddresses()
	if len(addresses) == 0 {
		// No address to offer. Saying so is better than a guess: a made-up address is an error the
		// reader cannot tell from a broken network.
		fmt.Fprintln(out, "\nthis host has no network address to offer, so from another machine use this")
		fmt.Fprintln(out, "host's address on THAT machine's network: same port, same fragment.")
		return
	}
	fmt.Fprintln(out, "\nfrom another machine, use whichever of these reaches this host - same port,")
	fmt.Fprintln(out, "same fragment, and the link above only works on this machine:")
	for _, addr := range addresses {
		fmt.Fprintf(out, "  http://%s:%s/#t=%s\n", addr, portOf(found.BaseURL), found.Token)
	}
}

// listInterfaces is net.Interfaces, as a variable so a test can inject the failure and the odd
// shapes a real interface list contains.
//
// The repo already does this for the filesystem calls in servicefile.go, for the same reason: the
// defensive branches of a syscall wrapper are unreachable on a healthy machine, and a branch nothing
// runs is a branch whose behaviour is whatever a later edit happened to write.
var listInterfaces = net.Interfaces

// lanAddresses lists the addresses at which THIS machine may be reached from a network.
//
// It exists because the address a remote browser needs cannot be derived from the listener: the
// gateway binds the wildcard and reports loopback (see Addr), which is the right answer for a
// client ON this machine and the wrong one for every other. The operator cannot work it out from
// that link either, and asking them to know their own address is exactly the "networking question
// at the wrong moment" the exposure design set out to remove.
//
// These are CANDIDATES and not a promise, which is why all of them are printed rather than one: a
// machine can be on several networks at once, and which address resolves depends on where the
// client is - something this process cannot see. The operator recognises their own network in the
// list, which is a question they can answer and this program cannot.
func lanAddresses() []string {
	interfaces, err := listInterfaces()
	if err != nil {
		// No interfaces to read is the same outcome as none to offer, and the caller has a sentence
		// for that. Reporting the error instead would be noise: nothing the operator can change.
		return nil
	}
	return lanAddressesOf(interfaces)
}

// interfaceAddrs reads one interface's addresses, as a variable for the same reason listInterfaces
// is one: its failure branch cannot be provoked on a healthy machine, and a branch nothing runs is a
// branch whose behaviour is whatever a later edit happened to write.
var interfaceAddrs = func(iface net.Interface) ([]net.Addr, error) { return iface.Addrs() }

// isVirtualInterface names the interfaces that CANNOT carry a client, whatever address they hold.
//
// A bridge or a veth pair exists only inside this machine: the address on it answers containers
// talking to their host, and nothing else on the network can reach it. Offering those is worse than
// offering nothing, because they sort in among the real answers and the operator cannot tell which
// is which - the reported symptom is a page that will not load from the address that "clearly" is
// the host's.
//
// The name is the only signal the standard library gives: a bridge is indistinguishable from a
// physical NIC by its flags or its addresses. The prefixes below are Linux's, which is where these
// interfaces exist.
func isVirtualInterface(name string) bool {
	for _, prefix := range []string{"docker", "br-", "veth", "virbr", "lxc", "vnet", "tun", "tap"} {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	return false
}

// lanAddressesOf is the part that reads an interface list, split out so a test supplies one.
func lanAddressesOf(interfaces []net.Interface) []string {
	var out []string
	for _, iface := range interfaces {
		// A down interface cannot carry a client, and a loopback one only reaches this machine.
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		// An interface that exists only inside this machine has no address a client could use.
		if isVirtualInterface(iface.Name) {
			continue
		}
		addrs, err := interfaceAddrs(iface)
		if err != nil {
			// An interface whose addresses cannot be read is skipped rather than reported: it is
			// one candidate missing from a list, not a reason to refuse the ones that work.
			continue
		}
		out = append(out, usableAddressesOf(addrs)...)
	}
	// IPv4 first, and sorted within each family, so two runs on the same machine print the same
	// order: an address list that reorders itself looks like the machine changed networks. IPv4
	// leads because it is what a LAN almost always uses, and a list whose first entry is an IPv6
	// address reads as if that were the answer to type.
	sort.Slice(out, func(i, j int) bool {
		i4, j4 := !strings.HasPrefix(out[i], "["), !strings.HasPrefix(out[j], "[")
		if i4 != j4 {
			return i4
		}
		return out[i] < out[j]
	})
	return out
}

// usableAddressesOf renders the addresses that can be typed into a URL, leaving out the ones that
// cannot.
//
// Split from the interface walk above so the filters are asserted against addresses built by hand,
// which is the only way to reach the loopback, link-local and non-IP entries reliably: a test host
// has whichever interfaces it happens to have.
func usableAddressesOf(addrs []net.Addr) []string {
	var out []string
	for _, a := range addrs {
		ipnet, ok := a.(*net.IPNet)
		if !ok {
			// A net.Addr that is not an IPNet carries no address to offer.
			continue
		}
		ip := ipnet.IP
		// Unspecified is "every interface", not an address to type. Link-local is skipped because
		// it needs the interface ZONE to be usable (`fe80::1%eth0`), and a zone is not something a
		// URL carries - printing one would hand over an address that does not work as written.
		if ip.IsLoopback() || ip.IsUnspecified() || ip.IsLinkLocalUnicast() {
			continue
		}
		if v4 := ip.To4(); v4 != nil {
			out = append(out, v4.String())
			continue
		}
		// IPv6 in a URL needs brackets, or the colons read as a port separator.
		out = append(out, "["+ip.String()+"]")
	}
	return out
}

// webUIEnabled asks whether the gateway this command just started serves the interface.
//
// It reads the CONFIGURATION rather than guessing, because the subcommands pay for neither the
// engine nor a sandbox and so never load one: an operator who turned the interface off in the
// YAML, or with STARLIGHT_GATEWAY_WEBUI, must not be handed a link to a page their gateway
// answers 404 on. The load is the keyless one, exactly like the version path, because a
// subcommand has no business demanding a credential to answer this question.
//
// The path comes from resolvedConfigPath, which is the SAME resolution `run` uses to load the file
// the child will inherit. This is not a detail: resolving against fl.configPath ALONE meant that a
// plain `starlight gateway start`, with no -config and a configuration in the starlight home, asked
// this question of the DEFAULTS - where the interface is on - while the service it spawned read the
// home file, where the operator had turned it off. The command handed out a link and the gateway
// answered 404, which is the exact failure the setting exists to prevent. The default and the
// child have to be resolved from one place or they eventually disagree.
//
// The seam is there for the same reason ServeGateway and DiscoverGateway are: what the tests need
// to pin is the shape of the announcement, not the configuration loader underneath it.
func (op Options) webUIEnabled(fl flags) bool {
	if op.InterfaceEnabled != nil {
		return op.InterfaceEnabled()
	}
	path := resolvedConfigPath(fl)
	cfg := config.Default()
	if path != "" {
		loaded, err := config.LoadWithoutKey(path)
		if err != nil {
			// A configuration that cannot be read is reported by the command that needs it; here
			// the honest reading is "we cannot claim there is an interface", so nothing is
			// announced.
			return false
		}
		cfg = loaded
	}
	// The environment is applied on top, because the CHILD inherits this process's environment -
	// spawnDetached leaves cmd.Env nil, which is the parent's. So STARLIGHT_GATEWAY_WEBUI set in
	// the shell applies to the service that is being spawned, and a resolution that skipped it
	// would announce a page the child was told to stop serving. LoadWithoutKey and Default do not
	// apply the environment by themselves: Load does.
	if err := config.ApplyEnvironment(&cfg); err != nil {
		// A malformed value is the child's problem to report; here, as above, the safe answer is
		// to claim nothing.
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
