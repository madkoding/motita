package app

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/madkoding/starlight/internal/gateway"
)

// The seams above have DEFAULTS, and a default that is never exercised is a default nobody tested.
// These call the real implementations directly, which is possible precisely because they are
// separate functions from the seams: the seam exists so the command can be tested, and the function
// beside it is what production runs.

// spawnDetached re-executes the program with -serve in its own session. The re-exec itself is what
// is checked here, with a program that can serve as a stand-in for starlight, because the property
// under test is the SPAWNING and not what the child does.
func TestSpawnDetachedStartsTheChildAndReleasesIt(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "spawned")
	// A program that records that it ran, standing in for the re-executed starlight. --serve is
	// passed as an argument it ignores; what matters is that the arguments reach it.
	script := filepath.Join(t.TempDir(), "fake-starlight")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nprintf '%s' \"$*\" > "+marker+"\n"), 0o700); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	if err := spawnDetached(context.Background(), script, "127.0.0.1:7477"); err != nil {
		t.Fatalf("spawnDetached: %v", err)
	}
	// The parent does not wait for the child, so the marker is polled rather than read straight
	// away: this is the same reason `gateway start` waits for the gateway to answer.
	deadline := time.Now().Add(2 * time.Second)
	var got string
	for time.Now().Before(deadline) {
		b, err := os.ReadFile(marker)
		if err == nil {
			got = string(b)
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !strings.Contains(got, "-serve") {
		t.Fatalf("the child was not started with -serve: %q", got)
	}
	if !strings.Contains(got, "127.0.0.1:7477") {
		t.Fatalf("the listen address did not reach the child: %q", got)
	}
}

// With no listen address the flag is left off entirely, so the child reads its own configuration
// instead of being told to listen somewhere meaningless.
func TestSpawnDetachedWithoutAnAddressOmitsTheFlag(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "args")
	script := filepath.Join(t.TempDir(), "fake-starlight")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nprintf '%s' \"$*\" > "+marker+"\n"), 0o700); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	if err := spawnDetached(context.Background(), script, "   "); err != nil {
		t.Fatalf("spawnDetached: %v", err)
	}
	deadline := time.Now().Add(2 * time.Second)
	var got string
	for time.Now().Before(deadline) {
		if b, err := os.ReadFile(marker); err == nil {
			got = string(b)
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if strings.Contains(got, "-gateway") {
		t.Fatalf("a blank address produced a -gateway flag: %q", got)
	}
}

// A program that cannot be started is an error rather than a silent no-op: `gateway start` would
// otherwise report success for a service that does not exist.
func TestSpawnDetachedReportsAProgramThatCannotRun(t *testing.T) {
	err := spawnDetached(context.Background(), filepath.Join(t.TempDir(), "not-a-program"), "")
	if err == nil {
		t.Fatal("starting a program that does not exist must fail")
	}
}

// detach takes the child out of the terminal's process group. Checked by looking at what was set,
// because the property IS the setting: a child in the same group receives the terminal's Ctrl+C,
// which is the difference between a service and a program that dies with the window.
func TestDetachPutsTheChildInItsOwnSession(t *testing.T) {
	cmd := exec.Command("true")
	detach(cmd)
	if cmd.SysProcAttr == nil || !cmd.SysProcAttr.Setsid {
		t.Fatalf("the child was not put in its own session: %+v", cmd.SysProcAttr)
	}
}

// termSignal is SIGTERM and not SIGKILL: the gateway closes its listener and its clients cleanly,
// which is the difference between stopping a service and cutting one off.
func TestTermSignalIsTheGracefulOne(t *testing.T) {
	if got := termSignal(); got != syscall.SIGTERM {
		t.Fatalf("termSignal() = %v, want SIGTERM", got)
	}
}

// signalByPID is the real implementation behind the seam. It is exercised against a process this
// test owns, so nothing else on the machine is affected.
func TestSignalByPIDStopsAProcess(t *testing.T) {
	cmd := exec.Command("sleep", "60")
	if err := cmd.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	// Cleanup kills without waiting: the channel is single-valued, and reading it twice would
	// block here forever - which is exactly what the first version of this test did.
	t.Cleanup(func() { _ = cmd.Process.Kill() })

	if err := signalByPID(cmd.Process.Pid); err != nil {
		t.Fatalf("signalByPID: %v", err)
	}
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("the process was still running after SIGTERM")
	}
}

// A pid nobody owns is reported, with the fallback os.Args[0] path exercised by calling complete
// on an Options that names no program.
func TestExePathFallsBackToArgvZero(t *testing.T) {
	original := os.Args[0]
	os.Args[0] = "/some/path/starlight"
	t.Cleanup(func() { os.Args[0] = original })

	op := Options{}
	op.complete()
	if op.ExePath == "" {
		t.Fatal("ExePath was left empty, so the program cannot re-execute itself")
	}
}

// complete fills the service file path from the home, so a run with no explicit seam still knows
// where to look - which is the difference between discovery working and discovery never finding
// anything.
func TestCompleteFillsTheServiceFileAndSeams(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	op := Options{}
	op.complete()

	if op.ServiceFile == "" {
		t.Fatal("ServiceFile was left empty")
	}
	if !strings.HasSuffix(op.ServiceFile, filepath.Join(".starlight", "gateway.json")) {
		t.Fatalf("ServiceFile = %q, want it under the starlight home", op.ServiceFile)
	}
	if op.SpawnGateway == nil || op.SignalProcess == nil {
		t.Fatalf("the seams were left nil: %+v", op)
	}
	if op.GatewayWait != 10*time.Second {
		t.Fatalf("GatewayWait = %v, want 10s", op.GatewayWait)
	}
}

// --- the polls, and the ways they stop ----------------------------------------------------------

// waitForGateway answers as soon as the gateway does, rather than after a fixed sleep: the time a
// gateway takes to bind is not a constant, and a fixed sleep either wastes a second on every start
// or fails on a loaded machine.
func TestWaitForGatewayReturnsAsSoonAsItAnswers(t *testing.T) {
	out := &syncBuffer{}
	servicePath := filepath.Join(t.TempDir(), "gateway.json")

	op := gatewayTestOptions(t, out, "", "-version")
	op.ServiceFile = servicePath

	// Nothing is there yet: the poll must keep asking, not give up on the first miss.
	go func() {
		time.Sleep(60 * time.Millisecond)
		_, address := fakeGatewayProcess(t, nil)
		_ = gateway.WriteServiceFile(servicePath, gateway.ServiceFile{Address: address, Token: "t"})
	}()

	op.GatewayWait = 3 * time.Second
	start := time.Now()
	if err := op.waitForGateway(context.Background()); err != nil {
		t.Fatalf("waitForGateway: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("the poll took %v, it should have returned once it answered", elapsed)
	}
}

// A corrupt service file during the wait is reported rather than waited out: there is nothing that
// could start answering, and the caller has to fix the file.
func TestWaitForGatewayReportsACorruptFile(t *testing.T) {
	out := &syncBuffer{}
	servicePath := filepath.Join(t.TempDir(), "gateway.json")
	if err := os.WriteFile(servicePath, []byte("{not json"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	op := gatewayTestOptions(t, out, "", "-version")
	op.ServiceFile = servicePath
	op.GatewayWait = time.Second

	if err := op.waitForGateway(context.Background()); err == nil {
		t.Fatal("a corrupt service file must be reported by the wait, not waited out")
	}
}

// A cancelled context ends the wait: a Ctrl+C during `gateway start` must not hold the shell for the
// rest of the timeout.
func TestWaitForGatewayStopsWhenTheContextIsCancelled(t *testing.T) {
	out := &syncBuffer{}
	op := gatewayTestOptions(t, out, "", "-version")
	op.ServiceFile = filepath.Join(t.TempDir(), "absent.json")
	op.GatewayWait = time.Hour

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	done := make(chan error, 1)
	go func() { done <- op.waitForGateway(ctx) }()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("a cancelled context must end the wait")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the wait ignored the cancelled context")
	}
}

// waitForGatewayGone is the other half of stop's confirmation: it returns once nothing answers.
func TestWaitForGatewayGoneReturnsWhenItStopsAnswering(t *testing.T) {
	out := &syncBuffer{}
	answered := &atomic.Bool{}
	answered.Store(true)
	_, address := fakeGatewayProcess(t, answered)

	op := gatewayTestOptions(t, out, "", "-version")
	op.GatewayWait = 3 * time.Second

	// The gateway stops answering partway through the poll: this is what a killed process looks
	// like from outside.
	go func() {
		time.Sleep(60 * time.Millisecond)
		answered.Store(false)
	}()

	start := time.Now()
	if err := op.waitForGatewayGone(context.Background(), "http://"+address); err != nil {
		t.Fatalf("waitForGatewayGone: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("the poll took %v, it should have returned once nothing answered", elapsed)
	}
}

// And a gateway that never stops answering is reported rather than declared stopped, because a
// signal is a request and not an outcome.
func TestWaitForGatewayGoneTimesOutOnAGatewayThatKeepsAnswering(t *testing.T) {
	out := &syncBuffer{}
	_, address := fakeGatewayProcess(t, nil)

	op := gatewayTestOptions(t, out, "", "-version")
	op.GatewayWait = 100 * time.Millisecond

	if err := op.waitForGatewayGone(context.Background(), "http://"+address); err == nil {
		t.Fatal("a gateway that keeps answering must not be reported as gone")
	}
}

// A cancelled context ends the gone-wait too, for the same reason it ends the other one: a Ctrl+C
// during `gateway stop` must not hold the shell for the rest of the timeout.
//
// The gateway has to KEEP answering for this to mean anything. With a dead address "it is gone"
// is true on the very first poll, so the wait returns success without ever consulting the context,
// and the test would pass for a reason that has nothing to do with cancellation.
func TestWaitForGatewayGoneStopsWhenTheContextIsCancelled(t *testing.T) {
	out := &syncBuffer{}
	_, address := fakeGatewayProcess(t, nil)

	op := gatewayTestOptions(t, out, "", "-version")
	op.GatewayWait = time.Hour

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	done := make(chan error, 1)
	go func() { done <- op.waitForGatewayGone(ctx, "http://"+address) }()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("a cancelled context must end the wait while the gateway is still answering")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the wait ignored the cancelled context")
	}
}

// --- the branches the operating system will not produce on demand -------------------------------

// A pid the system refuses to look up is reported rather than treated as a process: signalling
// nothing would report a service stopped that is still running.
func TestSignalByPIDReportsAnUnknownProcess(t *testing.T) {
	restore := findProcess
	findProcess = func(int) (*os.Process, error) { return nil, os.ErrNotExist }
	defer func() { findProcess = restore }()

	if err := signalByPID(4242); err == nil {
		t.Fatal("a process that cannot be looked up must be reported")
	}
}

// A program that cannot name where it lives falls back to what the shell ran, instead of leaving
// ExePath empty and re-executing nothing.
func TestExePathFallsBackWhenTheProgramCannotNameItself(t *testing.T) {
	restore := executablePath
	executablePath = func() (string, error) { return "", os.ErrNotExist }
	defer func() { executablePath = restore }()

	original := os.Args[0]
	os.Args[0] = "/the/shell/ran/this"
	t.Cleanup(func() { os.Args[0] = original })

	op := Options{}
	op.complete()
	if op.ExePath != "/the/shell/ran/this" {
		t.Fatalf("ExePath = %q, want the fallback", op.ExePath)
	}
}

// start reports a service file that goes corrupt between the spawn and the confirmation: it
// answered a moment ago and now cannot be read, which is not something to describe as running.
func TestStartingReportsACorruptFileAfterTheSpawn(t *testing.T) {
	out := &syncBuffer{}
	servicePath := filepath.Join(t.TempDir(), "gateway.json")

	op := gatewayTestOptions(t, out, "", "gateway", "start")
	op.ServiceFile = servicePath
	op.GatewayWait = time.Second
	// The child "starts" by writing a corrupt file, which is what the confirmation then reads.
	op.SpawnGateway = func(context.Context, string, string) error {
		return os.WriteFile(servicePath, []byte("{not json"), 0o600)
	}

	if code := Run(op); code != ConfigError {
		t.Fatalf("exit %d, want %d for a file that cannot be read afterwards", code, ConfigError)
	}
}

// start reports a gateway that answered the poll and then vanished between the confirmation and
// the report: reporting it as running would name a pid that is already gone.
func TestStartingReportsAGatewayThatCannotBeFoundAfterwards(t *testing.T) {
	out := &syncBuffer{}
	servicePath := filepath.Join(t.TempDir(), "gateway.json")

	// It answers EXACTLY ONCE - the poll - and is silent from the report's own probe onwards. Making
	// the stop time-based instead would leave the two probes racing the clock, and the branch would
	// only sometimes be reached.
	var probes atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/health" || probes.Add(1) > 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write([]byte(`{"ok":true,"version":"dev"}`))
	}))
	t.Cleanup(srv.Close)
	address := strings.TrimPrefix(srv.URL, "http://")

	op := gatewayTestOptions(t, out, "", "gateway", "start")
	op.ServiceFile = servicePath
	op.GatewayWait = time.Second
	op.SpawnGateway = func(context.Context, string, string) error {
		return gateway.WriteServiceFile(servicePath, gateway.ServiceFile{Address: address, Token: "t", PID: 9})
	}

	if code := Run(op); code != ConfigError {
		t.Fatalf("exit %d, want %d for a gateway that vanished after answering", code, ConfigError)
	}
	if !strings.Contains(out.String(), "cannot be found") {
		t.Fatalf("the failure was not explained: %q", out.String())
	}
}

// status reports a corrupt file rather than claiming nothing is running: the two are different
// states and only one of them is the user's to fix.
func TestStatusReportsACorruptServiceFile(t *testing.T) {
	out := &syncBuffer{}
	servicePath := filepath.Join(t.TempDir(), "gateway.json")
	if err := os.WriteFile(servicePath, []byte("{not json"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	op := gatewayTestOptions(t, out, "", "gateway", "status")
	op.ServiceFile = servicePath

	if code := Run(op); code != ConfigError {
		t.Fatalf("exit %d, want %d for a corrupt service file", code, ConfigError)
	}
}

// start reports a corrupt service file BEFORE spawning anything. Starting a service when the state
// on disk cannot be read would risk a second gateway beside one that is already running - the exact
// thing the file exists to prevent - so nothing is spawned until the state is understood.
func TestStartingRefusesOnACorruptServiceFile(t *testing.T) {
	out := &syncBuffer{}
	servicePath := filepath.Join(t.TempDir(), "gateway.json")
	if err := os.WriteFile(servicePath, []byte("{not json"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	spawned := false
	op := gatewayTestOptions(t, out, "", "gateway", "start")
	op.ServiceFile = servicePath
	op.SpawnGateway = func(context.Context, string, string) error { spawned = true; return nil }

	if code := Run(op); code != ConfigError {
		t.Fatalf("exit %d, want %d for a corrupt service file", code, ConfigError)
	}
	if spawned {
		t.Fatal("a gateway was started while the state on disk could not be read")
	}
}

// The service file changing between the confirmation that a gateway came up and the report of where
// it is. In reality this is a race - the file is one entry per home and another process can rewrite
// it - so it is produced through the seam rather than raced against the clock.
func TestStartingReportsAFileThatChangesUnderIt(t *testing.T) {
	out := &syncBuffer{}
	servicePath := filepath.Join(t.TempDir(), "gateway.json")

	answered := &atomic.Bool{}
	answered.Store(true)
	_, address := fakeGatewayProcess(t, answered)

	calls := 0
	op := gatewayTestOptions(t, out, "", "gateway", "start")
	op.ServiceFile = servicePath
	op.GatewayWait = time.Second
	op.SpawnGateway = func(context.Context, string, string) error {
		return gateway.WriteServiceFile(servicePath, gateway.ServiceFile{Address: address, Token: "t", PID: 9})
	}
	op.DiscoverGateway = func(ctx context.Context, path string) (gateway.Found, bool, error) {
		calls++
		switch {
		case calls == 1:
			// Nothing running yet: this is what makes start spawn one.
			return gateway.Found{}, false, nil
		case calls == 2:
			// The poll: the gateway came up.
			return gateway.Found{BaseURL: "http://" + address, Token: "t", PID: 9}, true, nil
		default:
			// By the time the report reads the file back, it no longer describes a gateway.
			return gateway.Found{}, false, nil
		}
	}

	if code := Run(op); code != ConfigError {
		t.Fatalf("exit %d, want %d when the file stops describing a gateway", code, ConfigError)
	}
	if !strings.Contains(out.String(), "cannot be found") {
		t.Fatalf("the failure was not explained: %q", out.String())
	}
}

// And the report's read of the file can itself fail: the file was readable when start looked, and
// between the confirmation and the report something replaced it with a corrupt one. Reported rather
// than described as running, because the address is about to be read from it.
func TestStartingReportsAnUnreadableFileAfterTheSpawn(t *testing.T) {
	out := &syncBuffer{}
	servicePath := filepath.Join(t.TempDir(), "gateway.json")

	answered := &atomic.Bool{}
	answered.Store(true)
	_, address := fakeGatewayProcess(t, answered)

	calls := 0
	op := gatewayTestOptions(t, out, "", "gateway", "start")
	op.ServiceFile = servicePath
	op.GatewayWait = time.Second
	op.SpawnGateway = func(context.Context, string, string) error {
		return gateway.WriteServiceFile(servicePath, gateway.ServiceFile{Address: address, Token: "t", PID: 9})
	}
	op.DiscoverGateway = func(ctx context.Context, path string) (gateway.Found, bool, error) {
		calls++
		switch {
		case calls == 1:
			return gateway.Found{}, false, nil
		case calls == 2:
			return gateway.Found{BaseURL: "http://" + address, Token: "t", PID: 9}, true, nil
		default:
			return gateway.Found{}, false, errors.New("the service file could not be read")
		}
	}

	if code := Run(op); code != ConfigError {
		t.Fatalf("exit %d, want %d when the file cannot be read back", code, ConfigError)
	}
}
