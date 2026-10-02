package app

import (
	"context"
	"errors"
	"net"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/madkoding/motita/internal/config"
	"github.com/madkoding/motita/internal/gateway"
)

// upgradeRun starts -serve with a capture of the gateway's Restart hook and a recording spawn,
// and returns what a test needs to play the part of the updater. spawned is sent the address the
// OLD gateway was bound to, probed at the instant of the spawn.
type upgradeRun struct {
	restart  func()
	done     chan int
	spawned  chan spawnSpec
	portFree chan bool
	out      *syncBuffer
}

func startUpgradeRun(t *testing.T, spawnErr error) *upgradeRun {
	t.Helper()
	silence(t)
	srv := planServer(t, []string{"hello"})
	t.Cleanup(srv.Close)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	cfgPath, logPath := gatewayConfig(t, srv)
	r := &upgradeRun{done: make(chan int, 1), spawned: make(chan spawnSpec, 1), portFree: make(chan bool, 1), out: &syncBuffer{}}
	opts := gatewayTestOptions(t, r.out, "", "-serve", "-config", cfgPath)
	opts.BaseCtx = ctx
	opts.Signals = nil
	opts.NewEngine = mockEngine(srv)

	var mu sync.Mutex
	var addr string
	opts.NewGateway = func(o gateway.Options, _ config.Config) (*gateway.Server, error) {
		mu.Lock()
		r.restart = o.Restart
		mu.Unlock()
		return gateway.Start(o)
	}
	opts.SpawnGateway = func(_ context.Context, spec spawnSpec) error {
		mu.Lock()
		a := addr
		mu.Unlock()
		// The replacement may only start once the old gateway has let go of its port.
		c, err := net.DialTimeout("tcp", a, 200*time.Millisecond)
		if err == nil {
			_ = c.Close()
		}
		r.portFree <- err != nil
		r.spawned <- spec
		return spawnErr
	}
	go func() { r.done <- Run(opts) }()
	a := waitForAddress(t, logPath)
	mu.Lock()
	addr = a
	mu.Unlock()
	return r
}

func TestAnUpgradeStopsTheServedGatewayAndStartsTheNewBinary(t *testing.T) {
	r := startUpgradeRun(t, nil)
	if r.restart == nil {
		t.Fatal("a served gateway must be able to replace itself: Restart was not wired")
	}
	r.restart()

	select {
	case code := <-r.done:
		if code != Success {
			t.Fatalf("code = %d: %s", code, r.out.String())
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the gateway did not stop after the upgrade")
	}
	select {
	case spec := <-r.spawned:
		if !strings.Contains(spec.Config, "config.yaml") {
			t.Errorf("the replacement must run the same configuration, got %q", spec.Config)
		}
	default:
		t.Fatal("no replacement was started: the upgrade would leave nothing serving")
	}
	if !<-r.portFree {
		t.Error("the replacement was started while the old gateway still held the port")
	}
}

func TestAnUpgradeThatCannotStartTheNewBinaryIsAnError(t *testing.T) {
	r := startUpgradeRun(t, errors.New("exec format error"))
	r.restart()
	if code := <-r.done; code != RunError {
		t.Fatalf("code = %d, want RunError: nothing is serving and the exit must say so", code)
	}
}

// TestAPlainStopStartsNothing: a signal or a cancelled context is a stop, not an upgrade.
func TestAPlainStopStartsNothing(t *testing.T) {
	silence(t)
	srv := planServer(t, []string{"hello"})
	defer srv.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cfgPath, logPath := gatewayConfig(t, srv)
	var out syncBuffer
	opts := gatewayTestOptions(t, &out, "", "-serve", "-config", cfgPath)
	opts.BaseCtx = ctx
	opts.Signals = nil
	opts.NewEngine = mockEngine(srv)
	spawned := false
	opts.SpawnGateway = func(context.Context, spawnSpec) error { spawned = true; return nil }
	done := make(chan int, 1)
	go func() { done <- Run(opts) }()
	waitForAddress(t, logPath)
	if resp, err := (&http.Client{Timeout: 5 * time.Second}).Get("http://" + waitForAddress(t, logPath) + "/v1/health"); err == nil {
		_ = resp.Body.Close()
	}
	cancel()
	if code := <-done; code != Success || spawned {
		t.Fatalf("code = %d, spawned = %v: a plain stop must not start a replacement", code, spawned)
	}
}
