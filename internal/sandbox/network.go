package sandbox

import (
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/madkoding/motita/internal/execx"
)

// A COMMAND WITHOUT A NETWORK (sandbox.isolate_network).
//
// Once a command has read something, the network is how it would send it away. With the setting on
// (the default), every command runs in a network namespace of its own that has only a loopback:
// the tests that talk to 127.0.0.1 still work, nothing leaves the machine. A command the user
// approves by name, and every command when the policy is off, keeps the network: the approval is
// the person deciding, for that command, what the setting decides for the rest - the same rule
// write confinement follows.
//
// Where the kernel cannot give a command a namespace (macOS, Windows, a Linux that refuses
// unprivileged user namespaces), the request is recorded as not applied rather than claimed: a
// sandbox that says it isolates and does not is worse than one that says it cannot.

// networkIsolationError says why this machine cannot give a command a network of its own, nil when
// it can. The answer is the kernel's and the same for every sandbox of the process, so it is asked
// once. A variable so a test can choose the answer.
var networkIsolationError = sync.OnceValue(probeNetworkIsolation)

// NetworkIsolationAvailable reports whether this machine can run a command without a network.
func NetworkIsolationAvailable() bool { return networkIsolationError() == nil }

// NetworkIsolationReason says why this machine cannot run a command without a network, "" when
// it can.
func NetworkIsolationReason() string {
	if err := networkIsolationError(); err != nil {
		return err.Error()
	}
	return ""
}

// SetIsolateNetwork turns the network isolation of the commands run from now on on or off. A
// command already running keeps what it started with. Turning it on where the machine cannot
// isolate leaves it off and returns why.
func (s *Sandbox) SetIsolateNetwork(on bool) error {
	if s.noNetwork == nil {
		// A Sandbox built by hand rather than by New: it gets its switch now.
		s.noNetwork = new(atomic.Bool)
	}
	if on {
		if err := networkIsolationError(); err != nil {
			s.noNetwork.Store(false)
			return err
		}
	}
	s.noNetwork.Store(on)
	return nil
}

// IsolatesNetwork reports whether the commands this sandbox runs get no network (approved ones
// aside).
func (s *Sandbox) IsolatesNetwork() bool { return s.noNetwork != nil && s.noNetwork.Load() }

// requestNetwork applies the requested network isolation in New, recording it as not applied when
// the machine cannot give it.
func (s *Sandbox) requestNetwork(on bool) {
	if err := s.SetIsolateNetwork(on); err != nil {
		s.notApplied = append(s.notApplied, fmt.Sprintf(
			"network isolation: this machine cannot give a command a network of its own, so commands keep it: %v%s",
			err, hintSuffix()))
	}
}

// runLimits are the limits of one run: the sandbox's, with the network taken away unless the
// request is exempt (approved by the user, or run with the policy off).
func (s *Sandbox) runLimits(p execx.Request) Limits {
	l := s.op.Limits
	l.NoNetwork = s.IsolatesNetwork() && !p.Approved && !p.Unconfined
	return l
}

// hintSuffix is the hint, ready to follow a reason.
func hintSuffix() string {
	if h := NetworkIsolationHint(); h != "" {
		return "; " + h
	}
	return ""
}
