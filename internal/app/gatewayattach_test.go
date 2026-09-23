package app

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/madkoding/starlight/internal/config"
	"github.com/madkoding/starlight/internal/gateway"
	"github.com/madkoding/starlight/internal/logx"
	"github.com/madkoding/starlight/internal/sandbox"
	"github.com/madkoding/starlight/internal/tui"
)

// `starlight` brings up an interface, and the agent behind it is a service that can already be
// running. Two terminals must no longer mean two agents on two ports with two conversations.

// runTUIWithoutAGateway drives the interface path with the gateway machinery replaced, so the
// decision being tested is the DECISION and not a real bind.
type tuiProbe struct {
	// usedBaseURL is where the interface ended up pointing. Empty means it never attached to a
	// gateway at all - the escape hatch.
	usedBaseURL string
	// spawned says whether this process brought a gateway up itself.
	spawned bool
}

// TestTheInterfaceConnectsToAGatewayThatIsAlreadyRunning: this is the whole request. A user who
// started a service once must be able to open terminals against it.
func TestTheInterfaceConnectsToAGatewayThatIsAlreadyRunning(t *testing.T) {
	out := &syncBuffer{}
	servicePath := filepath.Join(t.TempDir(), "gateway.json")

	// A service that is ALREADY running, described by the file.
	_, address := fakeGatewayProcess(t, nil)
	if err := gateway.WriteServiceFile(servicePath, gateway.ServiceFile{Address: address, Token: "tok", PID: 4242, Owned: false}); err != nil {
		t.Fatalf("WriteServiceFile: %v", err)
	}

	var gotBaseURL, gotToken string
	op := tuiTestOptions(t, out)
	op.ServiceFile = servicePath
	op.NewClient = func(baseURL, token, session string) tui.Runner {
		gotBaseURL, gotToken = baseURL, token
		return &noopRunner{}
	}
	op.SpawnGateway = func(context.Context, spawnSpec) error {
		t.Fatal("a gateway was started even though one was already running")
		return nil
	}

	if code := Run(op); code != Success {
		t.Fatalf("exit %d, want %d (output: %s)", code, Success, out.String())
	}
	if gotBaseURL != "http://"+address {
		t.Fatalf("the interface attached to %q, want the running gateway at %q", gotBaseURL, "http://"+address)
	}
	if gotToken != "tok" {
		t.Fatalf("the interface used token %q, want the one from the service file", gotToken)
	}
}

// TestTheInterfaceStartsAGatewayWhenThereIsNone: the fallback that keeps `starlight` working on a
// machine where nobody ever ran `gateway start`. Without it the program's DEFAULT invocation would
// be the one that does not work.
func TestTheInterfaceStartsAGatewayWhenThereIsNone(t *testing.T) {
	out := &syncBuffer{}
	servicePath := filepath.Join(t.TempDir(), "gateway.json")

	// The gateway this process brings up, standing in for the embedded one.
	_, address := fakeGatewayProcess(t, nil)

	var attached string
	spawned := false
	op := tuiTestOptions(t, out)
	op.ServiceFile = servicePath
	op.NewClient = func(baseURL, token, session string) tui.Runner {
		attached = baseURL
		return &noopRunner{}
	}
	// The gateway this process brings up, standing in for the in-process one. Nothing was running,
	// so this is the path that must bring one up.
	op.StartGatewayForTest = func(owned bool) (string, string, error) {
		spawned = true
		if !owned {
			t.Error("a gateway the interface starts must be marked as owned by it")
		}
		return "http://" + address, "own", nil
	}

	if code := Run(op); code != Success {
		t.Fatalf("exit %d, want %d (output: %s)", code, Success, out.String())
	}
	if !spawned {
		t.Fatal("no gateway was started even though none was running")
	}
	if attached != "http://"+address {
		t.Fatalf("the interface attached to %q, want the gateway it started at %q", attached, "http://"+address)
	}
}

// TestTheInterfaceShutsDownTheGatewayItStarted: a gateway the interface brought up because there
// was none must NOT survive it. Otherwise the first Ctrl+C leaks a gateway per terminal, and the
// leak is invisible until ports pile up.
func TestTheInterfaceShutsDownTheGatewayItStarted(t *testing.T) {
	out := &syncBuffer{}
	servicePath := filepath.Join(t.TempDir(), "gateway.json")

	answered := &atomic.Bool{}
	answered.Store(true)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if !answered.Load() {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write([]byte(`{"ok":true,"version":"dev"}`))
	}))
	t.Cleanup(srv.Close)
	address := strings.TrimPrefix(srv.URL, "http://")

	op := tuiTestOptions(t, out)
	op.ServiceFile = servicePath
	op.NewClient = func(baseURL, token, session string) tui.Runner { return &noopRunner{} }
	// The interface's own gateway, standing in for the in-process one. It publishes the file as the
	// real path does, with owned=true: this process brought it up, so this process shuts it down.
	op.StartGatewayForTest = func(owned bool) (string, string, error) {
		if !owned {
			t.Error("a gateway started for the interface must be marked as owned by it")
		}
		if err := gateway.WriteServiceFile(servicePath, gateway.ServiceFile{Address: address, Token: "own", PID: 4242, Owned: true}); err != nil {
			return "", "", err
		}
		return "http://" + address, "own", nil
	}

	if code := Run(op); code != Success {
		t.Fatalf("exit %d, want %d (output: %s)", code, Success, out.String())
	}
	// The interface has ended. A gateway this process started must not be left described as running:
	// a stale entry is exactly what discovery trusts, so the next start would probe it instead of
	// bringing up a gateway of its own.
	svc, _ := gateway.ReadServiceFile(servicePath)
	if !svc.IsZero() {
		t.Fatalf("the gateway this process started is still described on disk after the interface ended: %+v", svc)
	}
}

// TestTheInterfaceLeavesAGatewayItFoundRunning: the other half of the same rule, and the one that
// matters more. Killing a service the user deliberately started because a terminal happened to
// attach to it would be destroying their setup by looking at it.
func TestTheInterfaceLeavesAGatewayItFoundRunning(t *testing.T) {
	out := &syncBuffer{}
	servicePath := filepath.Join(t.TempDir(), "gateway.json")

	answered := &atomic.Bool{}
	answered.Store(true)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if !answered.Load() {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write([]byte(`{"ok":true,"version":"dev"}`))
	}))
	t.Cleanup(srv.Close)
	address := strings.TrimPrefix(srv.URL, "http://")

	// A service the USER started: owned=false.
	if err := gateway.WriteServiceFile(servicePath, gateway.ServiceFile{Address: address, Token: "user", PID: 777, Owned: false}); err != nil {
		t.Fatalf("WriteServiceFile: %v", err)
	}

	op := tuiTestOptions(t, out)
	op.ServiceFile = servicePath
	op.NewClient = func(baseURL, token, session string) tui.Runner { return &noopRunner{} }
	op.SpawnGateway = func(context.Context, spawnSpec) error {
		t.Fatal("a second gateway was started beside a running service")
		return nil
	}

	if code := Run(op); code != Success {
		t.Fatalf("exit %d, want %d (output: %s)", code, Success, out.String())
	}
	// The user's service must still be described, and still answering.
	svc, err := gateway.ReadServiceFile(servicePath)
	if err != nil {
		t.Fatalf("ReadServiceFile: %v", err)
	}
	if svc.Token != "user" {
		t.Fatalf("the user's service file was disturbed: %+v", svc)
	}
	if !answered.Load() {
		t.Fatal("the gateway the user started was taken down by looking at it")
	}
}

// A corrupt service file is reported rather than silently starting a second gateway: two gateways
// on one machine is the failure the file exists to prevent.
func TestTheInterfaceRefusesOnACorruptServiceFile(t *testing.T) {
	out := &syncBuffer{}
	servicePath := filepath.Join(t.TempDir(), "gateway.json")
	if err := os.WriteFile(servicePath, []byte("{not json"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	op := tuiTestOptions(t, out)
	op.ServiceFile = servicePath
	op.NewClient = func(baseURL, token, session string) tui.Runner { return &noopRunner{} }

	if code := Run(op); code != ConfigError {
		t.Fatalf("exit %d, want %d for a corrupt service file", code, ConfigError)
	}
}

// A gateway that cannot be brought up is reported: the interface would otherwise open onto nothing.
func TestTheInterfaceReportsAGatewayThatCannotStart(t *testing.T) {
	out := &syncBuffer{}
	op := tuiTestOptions(t, out)
	op.ServiceFile = filepath.Join(t.TempDir(), "gateway.json")
	op.NewClient = func(baseURL, token, session string) tui.Runner { return &noopRunner{} }
	op.StartGatewayForTest = func(bool) (string, string, error) {
		return "", "", errors.New("the listen address is already in use")
	}

	if code := Run(op); code != ConfigError {
		t.Fatalf("exit %d, want %d when no gateway can be brought up", code, ConfigError)
	}
	if !strings.Contains(out.String(), "could not start") {
		t.Fatalf("the failure was not explained: %q", out.String())
	}
}

// With -gateway off the interface keeps the direct path: the documented escape hatch, unchanged.
func TestTheInterfaceWithTheGatewayOffTakesTheDirectPath(t *testing.T) {
	out := &syncBuffer{}
	op := tuiTestOptions(t, out)
	op.Args = []string{"-tui", "-gateway", "off"}
	op.NewClient = func(baseURL, token, session string) tui.Runner {
		t.Fatal("with the gateway off the interface must not attach to one")
		return nil
	}
	op.StartGatewayForTest = func(bool) (string, string, error) {
		t.Fatal("with the gateway off nothing may be started")
		return "", "", nil
	}

	if code := Run(op); code != Success {
		t.Fatalf("exit %d, want %d (output: %s)", code, Success, out.String())
	}
}

// --- helpers ------------------------------------------------------------------------------------

// tuiTestOptions is the interface path with everything except the gateway decision replaced.
func tuiTestOptions(t *testing.T, out *syncBuffer) Options {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("STARLIGHT_LLM_API_KEY", "test")
	dir := t.TempDir()

	op := Options{
		Args:  []string{"-tui"},
		Out:   out,
		Err:   out,
		Stdin: strings.NewReader(""),
		NewSandbox: func(o sandbox.Options) (*sandbox.Sandbox, error) {
			o.Dir = dir
			return sandbox.New(o)
		},
		NewLogger: func(config.Agent) (*logx.Logger, error) {
			// Errors only, and never to the console: a test that reads what the interface was handed
			// must not have the suite's logs mixed into it.
			return logx.New(logx.Options{Level: logx.Error, Console: false})
		},
	}
	return op
}

// The wrapper hands the interface THIS process's wizard, whatever client it speaks through: the
// wizard reads the terminal in front of the user, so it cannot be remote, and running it on the
// gateway's host would configure the wrong machine.
func TestTheLocalWizardIsHandedToTheInterface(t *testing.T) {
	ran := false
	w := localWizard{
		Runner:    &noopRunner{},
		runConfig: func(context.Context) error { ran = true; return nil },
	}
	if err := w.RunConfig(context.Background()); err != nil {
		t.Fatalf("RunConfig: %v", err)
	}
	if !ran {
		t.Fatal("the local wizard was not run")
	}
	// And it is still the client underneath: everything else is passed straight through, so the
	// interface sees no difference between a wrapped client and a bare one.
	if _, err := w.RunModels(context.Background()); err != nil {
		t.Fatalf("RunModels through the wrapper: %v", err)
	}
}

// A gateway that cannot record where it is still SERVES, and says so.
//
// This is the opposite of refusing, and deliberately: the file is what lets `gateway status` and
// `gateway stop` act on a service, while the gateway itself is reachable at its address either
// way - and the port is fixed by default, so it is findable without any file. Refusing would turn
// a read-only `$HOME` into "the gateway does not work at all", which is a worse failure than the
// one it prevents. The end-to-end run found this: it serves with `HOME=/`.
func TestAGatewayThatCannotSayWhereItIsStillServes(t *testing.T) {
	out := &syncBuffer{}
	op := tuiTestOptions(t, out)
	op.ServiceFile = filepath.Join(t.TempDir(), "gateway.json")
	op.Args = []string{"-config", planConfig(t, planServer(t, []string{"x"})), "-tui"}
	attached := ""
	op.NewClient = func(baseURL, token, session string) tui.Runner {
		attached = baseURL
		return &noopRunner{}
	}
	op.WriteServiceFile = func(string, gateway.ServiceFile) error {
		return errors.New("read-only file system")
	}
	op.StartGatewayForTest = func(bool) (string, string, error) {
		return "http://127.0.0.1:7477", "tok", nil
	}

	if code := Run(op); code != Success {
		t.Fatalf("exit %d, want %d when only the service file cannot be written (output: %s)",
			code, Success, out.String())
	}
	if attached == "" {
		t.Fatal("the interface must still be handed a client of the gateway that is serving")
	}
}

// -serve publishes where it is, so a client started LATER can find it. This is what makes the two
// halves able to meet: without it, `gateway start` and `-tui` would each bring up their own.
func TestServePublishesWhereItIsAndClearsItOnTheWayOut(t *testing.T) {
	out := &syncBuffer{}
	servicePath := filepath.Join(t.TempDir(), "gateway.json")
	srv := planServer(t, []string{"x"})
	defer srv.Close()

	op := gatewayTestOptions(t, out, "", "-config", planConfig(t, srv), "-serve", "-gateway", "127.0.0.1:0")
	op.ServiceFile = servicePath
	op.CloseGateway = func(*gateway.Server, context.Context) error { return nil }

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan int, 1)
	go func() { done <- Run(withBaseCtx(op, ctx)) }()

	// The file appears with the EFFECTIVE address: port 0 means the real port is only known after
	// the bind, so a file carrying the zero would name a port that is not the one in use.
	deadline := time.Now().Add(3 * time.Second)
	var svc gateway.ServiceFile
	for time.Now().Before(deadline) {
		got, err := gateway.ReadServiceFile(servicePath)
		if err != nil {
			t.Fatalf("ReadServiceFile: %v", err)
		}
		if !got.IsZero() {
			svc = got
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if svc.IsZero() {
		cancel()
		<-done
		t.Fatal("serve never said where it is")
	}
	if strings.HasSuffix(svc.Address, ":0") {
		t.Fatalf("the file names port 0: %q", svc.Address)
	}
	if svc.Owned {
		t.Fatal("the service the user asked for must not be marked as owned by the interface")
	}

	cancel()
	if code := <-done; code != Success {
		t.Fatalf("exit %d, want %d", code, Success)
	}
	// And it is cleared on the way out: an entry naming a gateway that is gone is worse than none,
	// because discovery trusts it.
	if left, _ := gateway.ReadServiceFile(servicePath); !left.IsZero() {
		t.Fatalf("the file was left behind after serve stopped: %+v", left)
	}
}

// withBaseCtx returns a copy of the options with the given parent context.
func withBaseCtx(op Options, ctx context.Context) Options {
	op.BaseCtx = ctx
	return op
}

// The warning branch, exercised directly: startGateway cannot be reached through Run with the file
// seam injected, because the decision is taken by the seam's caller. It is a real branch on a real
// path (a read-only `$HOME`), so it is tested where it lives rather than left uncovered.
func TestStartGatewayReportsAnUnwritableServiceFileWithoutRefusing(t *testing.T) {
	out := &syncBuffer{}
	logPath := filepath.Join(t.TempDir(), "agent.log")
	cfg := planConfig(t, planServer(t, []string{"x"}))
	// A real logger writing to a file this test can read back.
	base, err := config.Load(cfg)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	base.Agent.LogFile = logPath
	base.Agent.LogConsole = false
	// planConfig sets log_level: error, which is below the warning this test is about.
	base.Agent.LogLevel = "warn"
	base.Gateway.Listen = "127.0.0.1:0"
	base.Gateway.TokenFile = filepath.Join(t.TempDir(), "gateway.token")
	op := Options{
		Out: out,
		Err: out,
		WriteServiceFile: func(string, gateway.ServiceFile) error {
			return errors.New("read-only file system")
		},
	}
	log, err := op.newLogger(base.Agent)
	if err != nil {
		t.Fatalf("newLogger: %v", err)
	}
	box, err := op.newSandbox(SandboxOptions(base, log))
	if err != nil {
		t.Fatalf("newSandbox: %v", err)
	}

	srv, err := op.startGateway(flags{}, base, nil, box, log, false)
	if err != nil {
		t.Fatalf("a gateway that cannot write the file must still serve: %v", err)
	}
	if srv == nil {
		t.Fatal("no server was returned")
	}
	defer func() { _ = srv.Close(context.Background()) }()

	// And it SAID so, naming the path, rather than failing silently.
	content, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !strings.Contains(string(content), "could not record where it is") {
		t.Fatalf("the inability to record the address was not reported: %s", content)
	}
}

// The usage text names the port a user will actually get, and it said 127.0.0.1:0 long after the
// default became a fixed 7477. Help text is the first thing anyone reads and the last thing anyone
// updates, so the number is read from the config default here rather than repeated - a change to
// the default breaks this test instead of silently turning the help into a lie.
func TestTheHelpNamesTheRealDefaultGatewayPort(t *testing.T) {
	// The documented address, from the same place the program gets it.
	want := config.Default().Gateway.Listen
	if want == "" || strings.HasSuffix(want, ":0") {
		t.Fatalf("the default gateway listen is %q, which is not a fixed port", want)
	}
	out := &syncBuffer{}
	op := tuiTestOptions(t, out)
	op.Args = []string{"-h"}
	Run(op)

	if !strings.Contains(out.String(), want) {
		t.Fatalf("the help does not name the default gateway address %q, so it describes a "+
			"program the user does not have:\n%s", want, out.String())
	}
	// And it must not still advertise the ephemeral default it no longer uses.
	if strings.Contains(out.String(), "127.0.0.1:0") {
		t.Errorf("the help still offers the ephemeral default:\n%s", out.String())
	}
}
