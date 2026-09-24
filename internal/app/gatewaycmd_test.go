package app

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/madkoding/motita/internal/gateway"
)

// `gateway start` exists so a user can bring a gateway up once and open terminals against it
// later. That is only possible if the service says where it is, in a file the later process can
// read.

// fakeGatewayProcess is a stand-in for the re-executed child: a real HTTP server answering the one
// endpoint discovery probes, so the whole discover-wait-report path is exercised for real instead
// of against a stub that agrees with whatever the test expects.
//
// answered is consulted on every probe, so a test can make the gateway STOP answering - which is
// what a real one does when it is killed, and what the stop path's confirmation is waiting for.
func fakeGatewayProcess(t *testing.T, answered *atomic.Bool) (*httptest.Server, string) {
	t.Helper()
	if answered == nil {
		answered = &atomic.Bool{}
	}
	answered.Store(true)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !answered.Load() {
			// Not a health report: a stopped gateway is not there, and the confirmation is waiting
			// for exactly that.
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		if r.URL.Path == "/v1/health" {
			// A version that is NOT the one this process defaults to: if the command prints it, it
			// can only have come from the gateway it probed.
			_, _ = w.Write([]byte(`{"ok":true,"version":"v9.9.9-fromthegateway"}`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(srv.Close)
	return srv, strings.TrimPrefix(srv.URL, "http://")
}

// TestStartingAGatewayAsAServiceSaysWhereItIs: the service publishes the file with the address it
// is ACTUALLY reachable at, and marks itself not-owned so nothing but the user ever shuts it down.
func TestStartingAGatewayAsAServiceSaysWhereItIs(t *testing.T) {
	out := &syncBuffer{}
	servicePath := filepath.Join(t.TempDir(), "gateway.json")

	var srv *httptest.Server
	srv, address := fakeGatewayProcess(t, nil)
	_ = srv

	op := gatewayTestOptions(t, out, "", "gateway", "start")
	op.ServiceFile = servicePath
	op.ExePath = "/bin/true"
	// The child is not really this program in a test, so the run is replaced by one that publishes
	// the file exactly as the child's -serve would. That is the behaviour under test: what the
	// parent reports and what it writes.
	op.SpawnGateway = func(context.Context, spawnSpec) error {
		return gateway.WriteServiceFile(servicePath, gateway.ServiceFile{Address: address, Token: "t", PID: 4242, Owned: false})
	}

	if code := Run(op); code != Success {
		t.Fatalf("gateway start exited %d, want %d (stderr: %s)", code, Success, out.String())
	}
	if !strings.Contains(out.String(), address) {
		t.Fatalf("the address was not reported: %q", out.String())
	}
	if !strings.Contains(out.String(), "4242") {
		t.Fatalf("the pid was not reported: %q", out.String())
	}

	svc, err := gateway.ReadServiceFile(servicePath)
	if err != nil {
		t.Fatalf("ReadServiceFile: %v", err)
	}
	if svc.Owned {
		t.Fatal("a gateway started by `gateway start` must be owned:false - only the user stops it")
	}
}

// TestStartingWhenOneIsAlreadyRunningSaysSo: starting a second gateway would leave the first
// orphaned and invisible, because there is one file per home and the second overwrites it. The
// user is told where the running one is instead.
func TestStartingWhenOneIsAlreadyRunningSaysSo(t *testing.T) {
	out := &syncBuffer{}
	servicePath := filepath.Join(t.TempDir(), "gateway.json")

	_, address := fakeGatewayProcess(t, nil)
	if err := gateway.WriteServiceFile(servicePath, gateway.ServiceFile{Address: address, Token: "t", PID: 777, Owned: false}); err != nil {
		t.Fatalf("WriteServiceFile: %v", err)
	}

	spawned := false
	op := gatewayTestOptions(t, out, "", "gateway", "start")
	op.ServiceFile = servicePath
	op.SpawnGateway = func(context.Context, spawnSpec) error {
		spawned = true
		return nil
	}

	if code := Run(op); code != Success {
		t.Fatalf("exit %d, want %d: asking to start what is already running is not a failure", code, Success)
	}
	if spawned {
		t.Fatal("a second gateway was started beside a running one")
	}
	if !strings.Contains(out.String(), address) || !strings.Contains(out.String(), "777") {
		t.Fatalf("the running gateway was not named: %q", out.String())
	}
}

// A child that cannot be spawned is reported rather than reported as started.
func TestStartingAGatewayThatCannotBeSpawnedFails(t *testing.T) {
	out := &syncBuffer{}
	op := gatewayTestOptions(t, out, "", "gateway", "start")
	op.ServiceFile = filepath.Join(t.TempDir(), "gateway.json")
	op.SpawnGateway = func(context.Context, spawnSpec) error { return os.ErrPermission }

	if code := Run(op); code != ConfigError {
		t.Fatalf("exit %d, want %d when the child cannot be spawned", code, ConfigError)
	}
	if !strings.Contains(out.String(), "could not be started") {
		t.Fatalf("the failure was not explained: %q", out.String())
	}
}

// A child that never answers is reported as NOT up. Reporting success after spawning would report
// that a process exists, not that a gateway is serving - and the user's next move is to connect a
// client.
func TestStartingAGatewayThatNeverAnswersFails(t *testing.T) {
	out := &syncBuffer{}
	servicePath := filepath.Join(t.TempDir(), "gateway.json")
	op := gatewayTestOptions(t, out, "", "gateway", "start")
	op.ServiceFile = servicePath
	// Spawns nothing and writes nothing: the address never comes up.
	op.SpawnGateway = func(context.Context, spawnSpec) error { return nil }
	op.GatewayWait = 50 * time.Millisecond

	if code := Run(op); code != ConfigError {
		t.Fatalf("exit %d, want %d when the gateway never came up", code, ConfigError)
	}
	if !strings.Contains(out.String(), "did not come up") {
		t.Fatalf("the failure was not explained: %q", out.String())
	}
}

// --- stopping -----------------------------------------------------------------------------------

// Stopping reaches the gateway the file names and confirms it stopped: a signal is a request, and
// reporting success before the gateway stopped answering would report an intent as an outcome.
func TestStoppingAGatewayStopsTheGatewayItNames(t *testing.T) {
	out := &syncBuffer{}
	servicePath := filepath.Join(t.TempDir(), "gateway.json")

	// A real child process, so there is a real pid to signal and a real death to observe.
	cmd := exec.Command("sleep", "60")
	if err := cmd.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	done := make(chan struct{})
	go func() { _ = cmd.Wait(); close(done) }()
	// Cleanup kills WITHOUT waiting: Wait is already running in the goroutine above, and calling it
	// twice concurrently is a data race on the command's own state - which is how this was found.
	t.Cleanup(func() { _ = cmd.Process.Kill() })

	// The gateway it names answers health only while the child is alive.
	answered := &atomic.Bool{}
	_, address := fakeGatewayProcess(t, answered)
	if err := gateway.WriteServiceFile(servicePath, gateway.ServiceFile{
		Address: address, Token: "t", PID: cmd.Process.Pid, Owned: true,
	}); err != nil {
		t.Fatalf("WriteServiceFile: %v", err)
	}

	op := gatewayTestOptions(t, out, "", "gateway", "stop")
	op.ServiceFile = servicePath
	op.SignalProcess = func(pid int) error {
		// The real gateway stops answering when it dies, and the confirmation polls for exactly
		// that; killing the child alone would leave the fake answering forever.
		answered.Store(false)
		return cmd.Process.Kill()
	}
	// The probe answers until the process is killed, then fails: that is the real transition the
	// confirmation is waiting for.
	op.GatewayWait = 2 * time.Second

	if code := Run(op); code != Success {
		t.Fatalf("exit %d, want %d (stderr: %s)", code, Success, out.String())
	}
	if !strings.Contains(out.String(), strconv.Itoa(cmd.Process.Pid)) {
		t.Fatalf("the stopped pid was not reported: %q", out.String())
	}
	if _, err := gateway.ReadServiceFile(servicePath); err != nil {
		t.Fatalf("ReadServiceFile: %v", err)
	}
	if svc, _ := gateway.ReadServiceFile(servicePath); !svc.IsZero() {
		t.Fatalf("the service file was left behind: %+v", svc)
	}
}

// TestStatusNamesTheVersionItFound: the question this command exists to answer is not only
// "is one running" but "is the one running THE BUILD I THINK". After installing a new binary
// over an old one, the running service can be the previous build - and nothing else in the
// program says so.
func TestStatusNamesTheVersionItFound(t *testing.T) {
	out := &syncBuffer{}
	_, address := fakeGatewayProcess(t, nil)
	servicePath := filepath.Join(t.TempDir(), "gateway.json")
	if err := gateway.WriteServiceFile(servicePath, gateway.ServiceFile{Address: address, Token: "t", PID: 4242, Owned: false}); err != nil {
		t.Fatalf("WriteServiceFile: %v", err)
	}

	op := gatewayTestOptions(t, out, "", "gateway", "status")
	op.ServiceFile = servicePath

	if code := Run(op); code != Success {
		t.Fatalf("exit %d, want %d (stderr: %s)", code, Success, out.String())
	}
	if !strings.Contains(out.String(), "v9.9.9-fromthegateway") {
		t.Fatalf("status did not name the running build: %q", out.String())
	}
}

// Asking to stop what is not running is a request that is already satisfied. An error there would
// put a failure on the terminal for something that worked.
func TestStoppingWhenNothingIsRunningIsNotAFailure(t *testing.T) {
	out := &syncBuffer{}
	op := gatewayTestOptions(t, out, "", "gateway", "stop")
	op.ServiceFile = filepath.Join(t.TempDir(), "absent.json")

	if code := Run(op); code != Success {
		t.Fatalf("exit %d, want %d when nothing is running", code, Success)
	}
	if !strings.Contains(out.String(), "no gateway is running") {
		t.Fatalf("it did not say there was nothing to stop: %q", out.String())
	}
}

// The pid in the file could have been recycled, and killing a recycled pid means killing an
// unrelated process - a bug that looks like a random program dying days later. /v1/health is what
// confirms the process is ours, and when it does not answer nothing is signalled.
func TestStoppingRefusesToSignalWhenNothingAnswers(t *testing.T) {
	out := &syncBuffer{}
	servicePath := filepath.Join(t.TempDir(), "gateway.json")

	dead := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	address := strings.TrimPrefix(dead.URL, "http://")
	dead.Close()

	if err := gateway.WriteServiceFile(servicePath, gateway.ServiceFile{Address: address, Token: "t", PID: 999}); err != nil {
		t.Fatalf("WriteServiceFile: %v", err)
	}

	signalled := false
	op := gatewayTestOptions(t, out, "", "gateway", "stop")
	op.ServiceFile = servicePath
	op.SignalProcess = func(int) error { signalled = true; return nil }

	if code := Run(op); code != Success {
		t.Fatalf("exit %d, want %d: the entry is stale, so there is nothing to stop", code, Success)
	}
	if signalled {
		t.Fatal("a pid whose port does not answer a health probe must never be signalled")
	}
	if svc, _ := gateway.ReadServiceFile(servicePath); !svc.IsZero() {
		t.Fatalf("the stale entry was not cleaned up: %+v", svc)
	}
}

// A gateway that does not stop is reported rather than assumed stopped.
func TestStoppingAGatewayThatDoesNotStopFails(t *testing.T) {
	out := &syncBuffer{}
	servicePath := filepath.Join(t.TempDir(), "gateway.json")

	_, address := fakeGatewayProcess(t, nil)
	if err := gateway.WriteServiceFile(servicePath, gateway.ServiceFile{Address: address, Token: "t", PID: 1234}); err != nil {
		t.Fatalf("WriteServiceFile: %v", err)
	}

	op := gatewayTestOptions(t, out, "", "gateway", "stop")
	op.ServiceFile = servicePath
	op.SignalProcess = func(int) error { return nil }
	op.GatewayWait = 100 * time.Millisecond

	if code := Run(op); code != ConfigError {
		t.Fatalf("exit %d, want %d when the gateway keeps answering", code, ConfigError)
	}
	if !strings.Contains(out.String(), "did not stop") {
		t.Fatalf("the failure was not explained: %q", out.String())
	}
}

// A signal that cannot be delivered is reported: the gateway is still running and the user has to
// know that.
func TestAStopThatCannotSignalIsReported(t *testing.T) {
	out := &syncBuffer{}
	servicePath := filepath.Join(t.TempDir(), "gateway.json")

	_, address := fakeGatewayProcess(t, nil)
	if err := gateway.WriteServiceFile(servicePath, gateway.ServiceFile{Address: address, Token: "t", PID: 4321}); err != nil {
		t.Fatalf("WriteServiceFile: %v", err)
	}

	op := gatewayTestOptions(t, out, "", "gateway", "stop")
	op.ServiceFile = servicePath
	op.SignalProcess = func(int) error { return os.ErrPermission }

	if code := Run(op); code != ConfigError {
		t.Fatalf("exit %d, want %d when the signal cannot be sent", code, ConfigError)
	}
	if !strings.Contains(out.String(), "could not be signalled") {
		t.Fatalf("the failure was not explained: %q", out.String())
	}
}

// A corrupt service file is reported on stop as well, because the alternative is leaving a gateway
// running that the user believes they stopped.
func TestStoppingReportsACorruptServiceFile(t *testing.T) {
	out := &syncBuffer{}
	servicePath := filepath.Join(t.TempDir(), "gateway.json")
	if err := os.WriteFile(servicePath, []byte("{not json"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	op := gatewayTestOptions(t, out, "", "gateway", "stop")
	op.ServiceFile = servicePath

	if code := Run(op); code != ConfigError {
		t.Fatalf("exit %d, want %d for a corrupt service file", code, ConfigError)
	}
}

// --- status ------------------------------------------------------------------------------------

// status is informational and never fails: it exists to answer "is one running?" and reporting a
// failure for the answer "no" would make the command useless in exactly the case it is used.
func TestStatusReportsNoGatewayWithoutFailing(t *testing.T) {
	out := &syncBuffer{}
	op := gatewayTestOptions(t, out, "", "gateway", "status")
	op.ServiceFile = filepath.Join(t.TempDir(), "absent.json")

	if code := Run(op); code != Success {
		t.Fatalf("exit %d, want %d for status with nothing running", code, Success)
	}
	if !strings.Contains(out.String(), "no gateway is running") {
		t.Fatalf("status did not say there is nothing running: %q", out.String())
	}
}

func TestStatusReportsARunningGateway(t *testing.T) {
	out := &syncBuffer{}
	servicePath := filepath.Join(t.TempDir(), "gateway.json")

	_, address := fakeGatewayProcess(t, nil)
	if err := gateway.WriteServiceFile(servicePath, gateway.ServiceFile{Address: address, Token: "t", PID: 55, Owned: true}); err != nil {
		t.Fatalf("WriteServiceFile: %v", err)
	}

	op := gatewayTestOptions(t, out, "", "gateway", "status")
	op.ServiceFile = servicePath

	if code := Run(op); code != Success {
		t.Fatalf("exit %d, want %d", code, Success)
	}
	if !strings.Contains(out.String(), address) || !strings.Contains(out.String(), "55") {
		t.Fatalf("status did not name the running gateway: %q", out.String())
	}
}

// --- the dispatch and the help ------------------------------------------------------------------

// A typo is rejected by the PARSER, with the list of what exists. This is the case that matters
// most: falling through to the default invocation would turn `motita gateway strat` into a TUI,
// which is the kind of failure a user cannot even describe.
func TestAnUnknownGatewayActionIsRejectedByParse(t *testing.T) {
	out := &syncBuffer{}
	op := gatewayTestOptions(t, out, "", "gateway", "frobnicate")

	if code := Run(op); code != ConfigError {
		t.Fatalf("exit %d, want %d for an unknown action", code, ConfigError)
	}
	combined := out.String()
	for _, want := range []string{"frobnicate", "start", "stop", "status"} {
		if !strings.Contains(combined, want) {
			t.Fatalf("the rejection does not mention %q: %q", want, combined)
		}
	}
}

// A bare `gateway` asks for an action rather than reporting a missing flag value.
func TestABareGatewayCommandAsksForAnAction(t *testing.T) {
	out := &syncBuffer{}
	op := gatewayTestOptions(t, out, "", "gateway")

	if code := Run(op); code != ConfigError {
		t.Fatalf("exit %d, want %d for a bare gateway command", code, ConfigError)
	}
	if combined := out.String(); !strings.Contains(combined, "start") {
		t.Fatalf("the message does not say what the actions are: %q", combined)
	}
}

// The dispatch has a default it cannot be reached through, and it is kept and covered on purpose:
// the function should not DEPEND on parse having validated the action, because a second caller that
// forgot would otherwise fall off the end of the switch into doing nothing at all.
func TestTheDispatchFallsBackToTheHelp(t *testing.T) {
	out := &syncBuffer{}
	op := gatewayTestOptions(t, out, "", "-version")

	if code := op.runGatewayCommand(context.Background(), "nonsense", flags{}); code != Success {
		t.Fatalf("exit %d, want %d", code, Success)
	}
	if combined := out.String(); !strings.Contains(combined, "start") {
		t.Fatalf("the help was not shown: %q", combined)
	}
}

// And it tolerates case and surrounding space, because a user who types `gateway START ` means the
// same thing.
func TestTheDispatchIgnoresCaseAndSpace(t *testing.T) {
	out := &syncBuffer{}
	op := gatewayTestOptions(t, out, "", "-version")
	op.ServiceFile = filepath.Join(t.TempDir(), "absent.json")

	if code := op.runGatewayCommand(context.Background(), "  STATUS  ", flags{}); code != Success {
		t.Fatalf("exit %d, want %d", code, Success)
	}
	if !strings.Contains(out.String(), "no gateway is running") {
		t.Fatalf("STATUS did not run: %q", out.String())
	}
}
