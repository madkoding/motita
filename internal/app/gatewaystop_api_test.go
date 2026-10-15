package app

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/madkoding/motita/internal/gateway"
)

// Where there is no SIGTERM (Windows), `gateway stop` asks the gateway over its local API first,
// and kills it only when that does not work. These tests take that path on any platform.

// withStopOverAPI makes this process behave like the Windows build for the length of a test.
func withStopOverAPI(t *testing.T) {
	t.Helper()
	prev := askOverAPI
	askOverAPI = true
	t.Cleanup(func() { askOverAPI = prev })
}

// stopOverAPIOptions is a `gateway stop` aimed at a fake gateway that answers while answered is
// true, with a signal that records itself and stops the fake.
func stopOverAPIOptions(t *testing.T, out *syncBuffer, answered *atomic.Bool, signalled *atomic.Int32) Options {
	t.Helper()
	_, address := fakeGatewayProcess(t, answered)
	servicePath := filepath.Join(t.TempDir(), "gateway.json")
	file := gateway.ServiceFile{Address: address, Token: "tok", PID: 777}
	if err := gateway.WriteServiceFile(servicePath, file); err != nil {
		t.Fatalf("WriteServiceFile: %v", err)
	}
	op := gatewayTestOptions(t, out, "", "gateway", "stop")
	op.ServiceFile = servicePath
	op.GatewayWait = 300 * time.Millisecond
	op.SignalProcess = func(int) error {
		signalled.Add(1)
		answered.Store(false)
		return nil
	}
	return op
}

// The request is enough: the gateway stops on its own graceful path and nothing is killed.
func TestStopOverAPIStopsWithoutTheKill(t *testing.T) {
	withStopOverAPI(t)
	out := &syncBuffer{}
	answered, signalled := &atomic.Bool{}, &atomic.Int32{}
	op := stopOverAPIOptions(t, out, answered, signalled)
	var gotToken string
	op.ShutdownGateway = func(_ context.Context, baseURL, token string) error {
		gotToken = token
		if !strings.HasPrefix(baseURL, "http://") {
			t.Errorf("baseURL = %q", baseURL)
		}
		answered.Store(false)
		return nil
	}
	if code := Run(op); code != Success {
		t.Fatalf("exit %d: %s", code, out.String())
	}
	if signalled.Load() != 0 {
		t.Fatal("the process was killed although it stopped on request")
	}
	if gotToken != "tok" || !strings.Contains(out.String(), "(pid 777) stopped") {
		t.Fatalf("token %q, output %q", gotToken, out.String())
	}
	if svc, _ := gateway.ReadServiceFile(op.ServiceFile); !svc.IsZero() {
		t.Fatalf("the service file was left behind: %+v", svc)
	}
}

// A refused request falls back to the kill, and says so.
func TestStopOverAPIFallsBackWhenTheRequestFails(t *testing.T) {
	withStopOverAPI(t)
	out := &syncBuffer{}
	answered, signalled := &atomic.Bool{}, &atomic.Int32{}
	op := stopOverAPIOptions(t, out, answered, signalled)
	op.ShutdownGateway = func(context.Context, string, string) error { return errors.New("refused") }
	if code := Run(op); code != Success {
		t.Fatalf("exit %d: %s", code, out.String())
	}
	if signalled.Load() != 1 || !strings.Contains(out.String(), "did not stop on request (refused)") {
		t.Fatalf("signalled %d, output %q", signalled.Load(), out.String())
	}
}

// An accepted request after which the gateway keeps answering also falls back to the kill.
func TestStopOverAPIFallsBackWhenTheGatewayKeepsAnswering(t *testing.T) {
	withStopOverAPI(t)
	out := &syncBuffer{}
	answered, signalled := &atomic.Bool{}, &atomic.Int32{}
	op := stopOverAPIOptions(t, out, answered, signalled)
	op.ShutdownGateway = func(context.Context, string, string) error { return nil }
	if code := Run(op); code != Success {
		t.Fatalf("exit %d: %s", code, out.String())
	}
	if signalled.Load() != 1 || !strings.Contains(out.String(), "did not stop on request") {
		t.Fatalf("signalled %d, output %q", signalled.Load(), out.String())
	}
}

// The served gateway wires its shutdown, and using it is a plain stop: nothing is started after.
func TestTheServedGatewayStopsThroughItsShutdown(t *testing.T) {
	r := startUpgradeRun(t, nil)
	if r.shutdown == nil {
		t.Fatal("a served gateway must be stoppable over its API: Shutdown was not wired")
	}
	r.shutdown()
	select {
	case code := <-r.done:
		if code != Success {
			t.Fatalf("code = %d: %s", code, r.out.String())
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the gateway did not stop after the shutdown request")
	}
	select {
	case <-r.spawned:
		t.Fatal("a shutdown started a replacement")
	default:
		// Nothing was spawned, which is the point.
	}
}
