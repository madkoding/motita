package tui

import (
	"bytes"
	"errors"
	"testing"

	"github.com/madkoding/motita/internal/config"
	"github.com/madkoding/motita/internal/llm"
	"github.com/madkoding/motita/internal/logx"
	"github.com/madkoding/motita/internal/sandbox"
)

// The setting reaches the configuration and the sandbox; a machine that cannot isolate only logs
// it, and a runner without a sandbox only keeps the setting.
func TestSetIsolateNetworkReachesTheConfigurationAndTheSandbox(t *testing.T) {
	prev := setBoxNetwork
	t.Cleanup(func() { setBoxNetwork = prev })
	var got []bool
	answer := error(nil)
	setBoxNetwork = func(_ *sandbox.Sandbox, on bool) error {
		got = append(got, on)
		return answer
	}
	r := NewAppRunner(&bytes.Buffer{}, &bytes.Buffer{}, config.Default(), &llm.Client{}, &sandbox.Sandbox{}, logx.Global())
	if !r.Config().Sandbox.IsolateNetwork {
		t.Fatal("isolating is the default")
	}
	r.SetIsolateNetwork(false)
	answer = errors.New("no namespaces")
	r.SetIsolateNetwork(true)
	if !r.Config().Sandbox.IsolateNetwork || len(got) != 2 || got[0] || !got[1] {
		t.Fatalf("config %v, sandbox calls %v", r.Config().Sandbox.IsolateNetwork, got)
	}
	r.Box = nil
	r.SetIsolateNetwork(false)
	if r.Config().Sandbox.IsolateNetwork || len(got) != 2 {
		t.Errorf("without a sandbox only the configuration changes: %v", got)
	}
}

// fetch_url asks the runner at every fetch, so it follows a change made while the session is open.
func TestNetworkIsolatedFollowsTheSetting(t *testing.T) {
	prev := setBoxNetwork
	t.Cleanup(func() { setBoxNetwork = prev })
	setBoxNetwork = func(*sandbox.Sandbox, bool) error { return nil }
	r := NewAppRunner(&bytes.Buffer{}, &bytes.Buffer{}, config.Default(), &llm.Client{}, &sandbox.Sandbox{}, logx.Global())
	if !r.networkIsolated() {
		t.Error("isolating is the default")
	}
	r.SetIsolateNetwork(false)
	if r.networkIsolated() {
		t.Error("the network was opened and pages are still refused")
	}
}
