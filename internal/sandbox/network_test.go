package sandbox

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/madkoding/motita/internal/execx"
)

// withNetworkIsolation makes the machine answer err to "can a command run without a network".
func withNetworkIsolation(t *testing.T, err error) {
	t.Helper()
	prev := networkIsolationError
	networkIsolationError = func() error { return err }
	t.Cleanup(func() { networkIsolationError = prev })
}

// The request is applied where the machine can, and each run's limits drop the network unless
// the request is approved or unconfined.
func TestNetworkIsolationIsPerRequest(t *testing.T) {
	withNetworkIsolation(t, nil)
	s, err := New(Options{Dir: t.TempDir(), Limits: Limits{NoNetwork: true}})
	if err != nil {
		t.Fatal(err)
	}
	if !s.IsolatesNetwork() || !NetworkIsolationAvailable() || !slices.Contains(s.Isolation(), ModeNoNetwork) {
		t.Fatalf("isolation = %v, isolates %v", s.Isolation(), s.IsolatesNetwork())
	}
	if !s.runLimits(execx.Request{}).NoNetwork {
		t.Error("a plain request keeps the network")
	}
	if s.runLimits(execx.Request{Approved: true}).NoNetwork || s.runLimits(execx.Request{Unconfined: true}).NoNetwork {
		t.Error("an approved or unconfined request is not exempt")
	}
	if err := s.SetIsolateNetwork(false); err != nil || s.IsolatesNetwork() || s.runLimits(execx.Request{}).NoNetwork {
		t.Errorf("turned off: %v, %v", err, s.IsolatesNetwork())
	}
	if c := s.WithDir(t.TempDir()); c.IsolatesNetwork() {
		t.Error("a copy does not follow the change")
	}
}

// Where the machine cannot, the request is recorded as not applied and never claimed.
func TestNetworkIsolationThatCannotBeAppliedIsSaid(t *testing.T) {
	withNetworkIsolation(t, errors.New("no namespaces here"))
	s, err := New(Options{Dir: t.TempDir(), Limits: Limits{NoNetwork: true}})
	if err != nil {
		t.Fatal(err)
	}
	if s.IsolatesNetwork() || slices.Contains(s.Isolation(), ModeNoNetwork) || NetworkIsolationAvailable() {
		t.Fatalf("claimed isolation: %v", s.Isolation())
	}
	if got := strings.Join(s.NotApplied(), "|"); !strings.Contains(got, "no namespaces here") {
		t.Errorf("not applied = %q", got)
	}
	if err := s.SetIsolateNetwork(true); err == nil || s.IsolatesNetwork() {
		t.Errorf("SetIsolateNetwork(true) = %v", err)
	}
}

// A sandbox built by hand has no isolation until it is asked for.
func TestAHandBuiltSandboxGetsItsNetworkSwitch(t *testing.T) {
	withNetworkIsolation(t, nil)
	s := &Sandbox{}
	if s.IsolatesNetwork() {
		t.Fatal("isolated by default")
	}
	if err := s.SetIsolateNetwork(true); err != nil || !s.IsolatesNetwork() {
		t.Fatalf("SetIsolateNetwork = %v", err)
	}
}

// The reason is the kernel's refusal, and empty where isolation works.
func TestNetworkIsolationReason(t *testing.T) {
	withNetworkIsolation(t, errors.New("no namespaces here"))
	if got := NetworkIsolationReason(); got != "no namespaces here" {
		t.Errorf("reason = %q", got)
	}
	withNetworkIsolation(t, nil)
	if got := NetworkIsolationReason(); got != "" {
		t.Errorf("reason = %q", got)
	}
}
