//go:build !linux

package sandbox

import "errors"

// probeNetworkIsolation: only Linux gives a command a network namespace of its own.
func probeNetworkIsolation() error {
	return errors.New("only Linux can give a command a network of its own")
}

// needsNamespaceRelay is false: the parent never asks for the isolation here (see
// probeNetworkIsolation), so there is nothing to relay.
func needsNamespaceRelay(Spec) bool { return false }

// relayInNamespace is never reached here (see needsNamespaceRelay).
func relayInNamespace(Spec) error {
	return errors.New("only Linux can give a command a network of its own")
}

// enterNoNetwork has nothing to prepare where there are no network namespaces.
func enterNoNetwork() {
	// Nothing to do: the parent never asks for the isolation here (see probeNetworkIsolation).
}
