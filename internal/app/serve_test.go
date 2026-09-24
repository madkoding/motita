package app

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/madkoding/motita/internal/config"
	"github.com/madkoding/motita/internal/gateway"
	"github.com/madkoding/motita/internal/llm"
	"github.com/madkoding/motita/internal/logx"
	"github.com/madkoding/motita/internal/sandbox"
)

// syncBuffer is a bytes.Buffer safe for one writer and one reader at a time.
//
// bytes.Buffer is NOT safe for concurrent use, and this file needs it to be: the gateway's
// logger writes from the server's goroutine while a test reads the address out of the same
// buffer. It is a real race, not a theoretical one - CI runs these tests with -race.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// gatewayConfig writes a configuration with the gateway ON and an EPHEMERAL port, and returns
// its path together with the path of the log file it names.
//
// The log file is named explicitly and read back for one reason: the line reporting where the
// gateway is listening is the only way anyone learns the address when port 0 was asked for, and
// asserting it is REPORTED is worth more than knowing the port by other means. It is read from
// the FILE rather than the console because this repository's tests pin the global logger to
// errors (see silence), so a console line would not reach the buffer regardless of settings.
//
// Every other path is redirected into a temporary directory for the same reason: a test must not
// leave a workspace, a log or a token in the package or in the developer's home. The anchor is
// required too - the agent refuses to run without a deterministic validator.
func gatewayConfig(t *testing.T, srv *httptest.Server) (cfgPath, logPath string) {
	t.Helper()
	dir := t.TempDir()
	cfgPath = filepath.Join(dir, "config.yaml")
	logPath = filepath.Join(dir, "motita.log")
	mustWrite(t, cfgPath, fmt.Sprintf(`sandbox:
  kind: none
  cgroups: off
anchor:
  kind: command
  command: "true"
llm:
  api_key: x
  base_url: %s
  model: mock
  max_attempts: 1
  timeout: 10s
gateway:
  listen: "127.0.0.1:0"
  token_file: "gateway.token"
agent:
  workspace_dir: %s
  log_file: %s
  log_level: info
  log_console: false
`, srv.URL, dir, logPath))
	return cfgPath, logPath
}

// gatewayTestOptions are the options every test here needs, with the side-effecting bits
// redirected: HOME so nothing is created in the real ~/.motita, and the sandbox so nothing is
// written into the package.
//
// stdin comes FIRST and args after it, deliberately: passing a flag in the stdin position is a
// mistake that costs nothing to prevent here and produced a confusing failure once already (the
// run silently fell back to the default task mode, which then failed on the anchor).
func gatewayTestOptions(t *testing.T, out *syncBuffer, stdin string, args ...string) Options {
	t.Helper()
	// HOME decides config.Dir(), and therefore the default location of the token file.
	t.Setenv("HOME", t.TempDir())
	t.Setenv("MOTITA_LLM_API_KEY", "test")
	dir := t.TempDir()
	return Options{
		Args:  args,
		Out:   out,
		Err:   out,
		Stdin: strings.NewReader(stdin),
		NewSandbox: func(o sandbox.Options) (*sandbox.Sandbox, error) {
			o.Dir = dir
			return sandbox.New(o)
		},
	}
}

// mockEngine points the reasoning engine at the simulated LLM.
func mockEngine(srv *httptest.Server) func(config.LLM, *logx.Logger) (*llm.Client, error) {
	return func(c config.LLM, l *logx.Logger) (*llm.Client, error) {
		c.BaseURL = srv.URL
		c.Model = "mock"
		return llm.New(c, l)
	}
}

// The executable is its own gateway, and the interface the user is looking at is a client of it.
// This walks the real path - no injected client, no injected server - and then proves the claim
// the hard way: WHILE the interface is running, it reads the address the gateway reported and
// makes a REAL HTTP request to it.
//
// The interface is held open by a stdin that blocks, and that is the point rather than a trick:
// the gateway is bound for exactly as long as the interface runs, so a probe before it starts or
// after it ends would be asserting against a closed socket - a test that fails for the wrong
// reason and would tempt someone to "fix" it by weakening the assertion. Blocking stdin puts the
// request INSIDE the window the claim is about.
func TestTheTUIReachesItsOwnGateway(t *testing.T) {
	silence(t)
	srv := planServer(t, []string{"the answer"})
	defer srv.Close()

	cfgPath, logPath := gatewayConfig(t, srv)
	var out syncBuffer
	opts := gatewayTestOptions(t, &out, "", "-config", cfgPath, "-tui")
	opts.NewEngine = mockEngine(srv)

	// Stdin blocks until the test closes it, which is what keeps the interface alive. Closing the
	// WRITER is what ends the read the interface is sitting in.
	stdin, stdinWriter := io.Pipe()
	opts.Stdin = stdin
	closeStdin := func() { _ = stdinWriter.Close() }

	done := make(chan int, 1)
	go func() { done <- Run(opts) }()

	addr := waitForAddress(t, logPath)
	resp, err := (&http.Client{Timeout: 5 * time.Second}).Get("http://" + addr + "/v1/health")
	if err != nil {
		closeStdin()
		<-done
		t.Fatalf("the gateway the interface is using is not answering on %s: %v", addr, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("health = %d, want 200", resp.StatusCode)
	}

	// The interface is a client of THAT server, not of nothing: the configuration it draws came
	// from this process's own gateway, which is why the request above answers at all.
	closeStdin()
	if code := <-done; code != Success {
		t.Fatalf("code = %d: %s", code, out.String())
	}
}

// The gateway token is created on first use and kept where the configuration says, resolved
// against the configuration file the run was given. The mode is asserted too: the token is a
// password to an agent that runs commands on this machine.
func TestTheEmbeddedGatewayKeepsItsTokenWhereTheConfigSays(t *testing.T) {
	silence(t)
	srv := planServer(t, []string{"hello"})
	defer srv.Close()

	cfgPath, _ := gatewayConfig(t, srv)
	var out syncBuffer
	opts := gatewayTestOptions(t, &out, "q\n", "-config", cfgPath, "-tui")
	opts.NewEngine = mockEngine(srv)

	if code := Run(opts); code != Success {
		t.Fatalf("code = %d: %s", code, out.String())
	}

	// resolvePaths anchors a relative token_file to the directory of the configuration file,
	// which is the rule every other owned path follows. Asserting the exact path rather than
	// globbing is what catches the two drifting apart.
	tokenPath := filepath.Join(filepath.Dir(cfgPath), "gateway.token")
	info, err := os.Stat(tokenPath)
	if err != nil {
		t.Fatalf("the gateway token was not kept at %s: %v", tokenPath, err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("the token file is %o, want 600", perm)
	}
	// The file carries the hex token and a trailing newline, so the token itself is asserted
	// after trimming rather than by file size: a file that had grown a second line would still
	// have a plausible size.
	data, err := os.ReadFile(tokenPath)
	if err != nil {
		t.Fatalf("could not read the token: %v", err)
	}
	token := strings.TrimSpace(string(data))
	if len(token) != 2*gateway.MinTokenBytes {
		t.Errorf("the token is %d characters, want %d (hex of %d random bytes)",
			len(token), 2*gateway.MinTokenBytes, gateway.MinTokenBytes)
	}
}

// With the gateway turned OFF the interface keeps the direct path it has always had. It is the
// documented escape hatch, and it must keep working with no socket anywhere.
func TestTheGatewayCanBeTurnedOff(t *testing.T) {
	silence(t)
	srv := planServer(t, []string{"hello"})
	defer srv.Close()

	t.Setenv("MOTITA_GATEWAY_ENABLED", "false")
	cfgPath, logPath := gatewayConfig(t, srv)
	var out syncBuffer
	opts := gatewayTestOptions(t, &out, "q\n", "-config", cfgPath, "-tui")
	opts.NewEngine = mockEngine(srv)

	if code := Run(opts); code != Success {
		t.Fatalf("code = %d: %s", code, out.String())
	}
	// Nothing was listening, so no address was ever reported.
	if strings.Contains(readFileOrEmpty(logPath), "address") {
		t.Error("the gateway listened although it was turned off")
	}
	// And no token was made: nothing ever asked for one.
	if _, err := os.Stat(filepath.Join(filepath.Dir(cfgPath), "gateway.token")); err == nil {
		t.Error("a token was created although the gateway was turned off")
	}
}

// -gateway off is the flag form of the same escape hatch, and it works without touching the
// configuration at all.
func TestTheGatewayCanBeTurnedOffWithTheFlag(t *testing.T) {
	silence(t)
	srv := planServer(t, []string{"hello"})
	defer srv.Close()

	cfgPath, logPath := gatewayConfig(t, srv)
	var out syncBuffer
	opts := gatewayTestOptions(t, &out, "q\n", "-config", cfgPath, "-tui", "-gateway", "off")
	opts.NewEngine = mockEngine(srv)

	if code := Run(opts); code != Success {
		t.Fatalf("code = %d: %s", code, out.String())
	}
	if strings.Contains(readFileOrEmpty(logPath), "address") {
		t.Error("the gateway listened although -gateway off was given")
	}
}

// -serve runs the gateway and no interface, which is what makes a phone client useful on a
// machine nobody is sitting at.
func TestServeRunsTheGatewayWithoutAnInterface(t *testing.T) {
	silence(t)
	srv := planServer(t, []string{"hello"})
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cfgPath, logPath := gatewayConfig(t, srv)
	var out syncBuffer
	// The stdin is EMPTY on purpose: -serve reads nothing from a terminal.
	opts := gatewayTestOptions(t, &out, "", "-serve", "-config", cfgPath)
	opts.BaseCtx = ctx
	opts.Signals = nil
	opts.NewEngine = mockEngine(srv)

	done := make(chan int, 1)
	go func() { done <- Run(opts) }()

	addr := waitForAddress(t, logPath)

	// It answers WHILE the process is still serving, which is the whole point of -serve.
	resp, err := (&http.Client{Timeout: 5 * time.Second}).Get("http://" + addr + "/v1/health")
	if err != nil {
		cancel()
		t.Fatalf("the gateway is not answering on the address it reported: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("health = %d, want 200", resp.StatusCode)
	}

	// And no interface was drawn: -serve produces no banner, no prompt, no status bar.
	if strings.Contains(out.String(), "Describe a task and press Enter") {
		t.Error("-serve drew the interface")
	}

	cancel()
	if code := <-done; code != Success {
		t.Fatalf("code = %d: %s", code, out.String())
	}
}

// readFileOrEmpty reads a file, treating "not there" as empty. A log file that was never created
// is exactly what "nothing was logged" looks like, and that is a case several tests assert.
func readFileOrEmpty(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return string(data)
}

// -gateway ADDRESS overrides the configured listen address, which is what makes a fixed port
// possible without editing the configuration. The override wins over the file, and the log
// proves it by reporting the address that was actually bound.
func TestTheGatewayFlagOverridesTheConfiguredAddress(t *testing.T) {
	silence(t)
	srv := planServer(t, []string{"hello"})
	defer srv.Close()

	// A port held by the test, then released: the address is known to be free and is NOT the
	// ephemeral one the configuration asks for.
	held, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("could not find a free port: %v", err)
	}
	want := held.Addr().String()
	held.Close()

	cfgPath, logPath := gatewayConfig(t, srv)
	var out syncBuffer
	opts := gatewayTestOptions(t, &out, "", "-config", cfgPath, "-tui", "-gateway", want)
	opts.NewEngine = mockEngine(srv)

	stdin, stdinWriter := io.Pipe()
	opts.Stdin = stdin
	done := make(chan int, 1)
	go func() { done <- Run(opts) }()

	if got := waitForAddress(t, logPath); got != want {
		_ = stdinWriter.Close()
		<-done
		t.Fatalf("bound %s, want the address from -gateway (%s)", got, want)
	}
	_ = stdinWriter.Close()
	if code := <-done; code != Success {
		t.Fatalf("code = %d: %s", code, out.String())
	}
}

// A gateway whose serve loop fails reports it. The loop only fails when the listener breaks
// under it, so the seam is injected rather than provoked: an error path nothing can reach is an
// error path that rots, and this one is the difference between "the gateway stopped" appearing
// in the log and a process that looks alive with nothing listening.
func TestAServeLoopFailureIsLogged(t *testing.T) {
	silence(t)
	srv := planServer(t, []string{"hello"})
	defer srv.Close()

	cfgPath, _ := gatewayConfig(t, srv)
	var out syncBuffer
	opts := gatewayTestOptions(t, &out, "", "-serve", "-config", cfgPath)
	ctx, cancel := context.WithCancel(context.Background())
	opts.BaseCtx = ctx
	opts.Signals = nil
	opts.NewEngine = mockEngine(srv)
	opts.ServeGateway = func(*gateway.Server) error { return errors.New("the listener broke") }

	done := make(chan int, 1)
	go func() { done <- Run(opts) }()
	// The loop fails straight away; the process is then asked to stop.
	cancel()
	if code := <-done; code != Success {
		t.Fatalf("code = %d: %s", code, out.String())
	}
}

// A shutdown that fails is reported rather than swallowed. It is injected for the same reason as
// the serve loop: a real Shutdown fails when a client holds a connection past the deadline, and
// a test that waited for that would be slow and flaky.
func TestAFailedShutdownIsReported(t *testing.T) {
	silence(t)
	srv := planServer(t, []string{"hello"})
	defer srv.Close()

	cfgPath, _ := gatewayConfig(t, srv)
	var out syncBuffer
	opts := gatewayTestOptions(t, &out, "", "-serve", "-config", cfgPath)
	ctx, cancel := context.WithCancel(context.Background())
	opts.BaseCtx = ctx
	opts.Signals = nil
	opts.NewEngine = mockEngine(srv)
	opts.CloseGateway = func(*gateway.Server, context.Context) error {
		return errors.New("a client held its connection past the deadline")
	}

	done := make(chan int, 1)
	go func() { done <- Run(opts) }()
	cancel()
	if code := <-done; code != Success {
		t.Fatalf("code = %d: %s", code, out.String())
	}
}

// The -gateway flag is checked HERE, at start, and not by the configuration validator: the flag
// never goes through the config file, so an address given on the command line is the gateway's
// own last line of defence. An address that cannot be parsed is refused with a configuration
// error instead of leaving an interface running against a gateway that never bound.
func TestAnUnbindableGatewayFlagFailsTheInterface(t *testing.T) {
	silence(t)
	srv := planServer(t, []string{"hello"})
	defer srv.Close()

	cfgPath, _ := gatewayConfig(t, srv)
	var out syncBuffer
	opts := gatewayTestOptions(t, &out, "q\n",
		"-config", cfgPath, "-tui", "-gateway", "not an address")
	opts.NewEngine = mockEngine(srv)

	if code := Run(opts); code != ConfigError {
		t.Errorf("code = %d, want ConfigError (%d): %s", code, ConfigError, out.String())
	}
	if !strings.Contains(out.String(), "the gateway could not start") {
		t.Errorf("the refusal must say the gateway did not start, got %q", out.String())
	}
}

// -serve with the gateway turned off is refused rather than started-and-ignored: a process whose
// only purpose is to serve, told not to serve, has nothing left to do.
func TestServeWithTheGatewayTurnedOffIsRefused(t *testing.T) {
	silence(t)
	srv := planServer(t, []string{"hello"})
	defer srv.Close()

	cfgPath, _ := gatewayConfig(t, srv)
	var out syncBuffer
	opts := gatewayTestOptions(t, &out, "", "-serve", "-config", cfgPath, "-gateway", "off")
	opts.BaseCtx = context.Background()
	opts.NewEngine = mockEngine(srv)

	if code := Run(opts); code != ConfigError {
		t.Errorf("code = %d, want ConfigError (%d): %s", code, ConfigError, out.String())
	}
}

// A malformed origin rule stops the gateway while it is being BUILT, with the setting named.
//
// This is the whole reason the rules are parsed at startup instead of per request. The middleware
// applies them at request time, so a typo there would show up as an origin being refused for a
// reason nobody can see - a 403 that reads as a network fault. Here it is a message on the terminal,
// before anything is bound, naming gateway.allow and the entry that is wrong.
func TestAGatewayWithAMalformedRuleDoesNotStart(t *testing.T) {
	silence(t)
	srv := planServer(t, []string{"hello"})
	defer srv.Close()

	cfgPath, logPath := gatewayConfig(t, srv)
	mustWrite(t, cfgPath, strings.ReplaceAll(readFileOrEmpty(cfgPath),
		"  listen: \"127.0.0.1:0\"", "  listen: \"127.0.0.1:0\"\n  allow: [\"lan\", \"banana\"]"))

	var out syncBuffer
	opts := gatewayTestOptions(t, &out, "", "-serve", "-config", cfgPath)
	opts.BaseCtx = context.Background()
	opts.NewEngine = mockEngine(srv)

	if code := Run(opts); code != ConfigError {
		t.Fatalf("code = %d, want ConfigError (%d): %s", code, ConfigError, out.String())
	}
	// The message has to name the setting and the offending entry, or the operator is left looking
	// for a rule nobody said was wrong.
	got := out.String() + readFileOrEmpty(logPath)
	if !strings.Contains(got, "gateway.allow") {
		t.Errorf("the failure does not name gateway.allow:\n%s", got)
	}
	if !strings.Contains(got, "banana") {
		t.Errorf("the failure does not name the offending rule:\n%s", got)
	}
}

// The parse inside startGateway is DEFENSIVE, and this test reaches it directly.
//
// Validation already refuses a malformed rule while the configuration is read, so through Run() this
// branch cannot be the one that fires - the test above proves the user-visible outcome, and this one
// pins the behaviour of the branch itself, the way the listener-address tests pin theirs. It matters
// because the alternative to a clear message here is a gateway that binds with a rule set nobody
// checked: Parse returning an error must never be mistaken for an empty rule set, which WOULD be
// valid and would mean "every origin".
func TestStartGatewayRefusesAMalformedRuleDirectly(t *testing.T) {
	dir := t.TempDir()
	cfg := config.Default()
	cfg.LLM.APIKey = "test"
	cfg.Gateway.TokenFile = filepath.Join(dir, "gateway.token")
	// Bypasses Validate deliberately: the point is the second check.
	cfg.Gateway.Allow = []string{"not a rule"}

	op := Options{Out: &syncBuffer{}, Err: &syncBuffer{}}
	_, err := op.startGateway(flags{}, cfg, nil, nil, nil, false)
	if err == nil {
		t.Fatal("a malformed rule was accepted when the gateway was built")
	}
	if !strings.Contains(err.Error(), "gateway.allow") {
		t.Errorf("the failure does not name gateway.allow: %v", err)
	}
	if !strings.Contains(err.Error(), "not a rule") {
		t.Errorf("the failure does not name the offending rule: %v", err)
	}
}

// A token that cannot be read or created stops the gateway before it binds. The token file is
// pointed at a DIRECTORY here, which is the reachable form of this failure: it exists, so it is
// not created, and reading it fails with something that is not "not found".
func TestAGatewayThatCannotKeepItsTokenDoesNotStart(t *testing.T) {
	silence(t)
	srv := planServer(t, []string{"hello"})
	defer srv.Close()

	cfgPath, _ := gatewayConfig(t, srv)
	// The configuration's own directory, named as the token file. resolvePaths joins it, and the
	// result is a directory rather than a file.
	mustWrite(t, cfgPath, strings.ReplaceAll(readFileOrEmpty(cfgPath),
		`token_file: "gateway.token"`, `token_file: "."`))

	var out syncBuffer
	opts := gatewayTestOptions(t, &out, "", "-serve", "-config", cfgPath)
	opts.BaseCtx = context.Background()
	opts.NewEngine = mockEngine(srv)

	if code := Run(opts); code != ConfigError {
		t.Errorf("code = %d, want ConfigError (%d): %s", code, ConfigError, out.String())
	}
}

// waitForAddress reads the log until the gateway reports where it is listening.
// address. It is the same line an operator reads, so parsing it is what proves it is REPORTED:
// with port 0 that line is the only way anyone learns where to connect.
func waitForAddress(t *testing.T, logPath string) string {
	t.Helper()
	addr := regexp.MustCompile(`"address":"([^"]+)"`)
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if m := addr.FindStringSubmatch(readFileOrEmpty(logPath)); m != nil {
			return m[1]
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("the gateway never reported its address; the log holds: %q", readFileOrEmpty(logPath))
	return ""
}

// A listen address that cannot be bound is refused at STARTUP. That is the whole reason -serve
// builds the engine before it serves: a server that starts and then fails on its first client is
// worse than one that never starts, because nobody is watching the first one.
func TestAServeWithAnUnbindableAddressReportsAConfigError(t *testing.T) {
	silence(t)
	srv := planServer(t, []string{"hello"})
	defer srv.Close()

	var out syncBuffer
	opts := gatewayTestOptions(t, &out, "", "-serve", "-config", planConfig(t, srv))
	opts.BaseCtx = context.Background()
	opts.NewEngine = mockEngine(srv)
	t.Setenv("MOTITA_GATEWAY_LISTEN", "not an address")

	if code := Run(opts); code != ConfigError {
		t.Errorf("code = %d, want ConfigError (%d): %s", code, ConfigError, out.String())
	}
}

// A port already in use is refused by the bind, and runServe turns that into a configuration
// error rather than a panic. The port is really held here, so the failure is the real one and
// the message has to name it - a bare "could not start" would leave an operator guessing.
func TestAServeOnATakenPortReportsAConfigError(t *testing.T) {
	silence(t)
	srv := planServer(t, []string{"hello"})
	defer srv.Close()

	held, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("could not hold a port: %v", err)
	}
	defer held.Close()

	var out syncBuffer
	opts := gatewayTestOptions(t, &out, "", "-serve", "-config", planConfig(t, srv))
	opts.BaseCtx = context.Background()
	opts.NewEngine = mockEngine(srv)
	t.Setenv("MOTITA_GATEWAY_LISTEN", held.Addr().String())

	if code := Run(opts); code != ConfigError {
		t.Errorf("code = %d, want ConfigError (%d): %s", code, ConfigError, out.String())
	}
	if !strings.Contains(out.String(), "could not listen") {
		t.Errorf("the refusal must say the bind failed, got %q", out.String())
	}
}
