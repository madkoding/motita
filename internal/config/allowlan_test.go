package config

import (
	"strings"
	"testing"
)

// The default stays loopback. This is the test that says so, and it is the one that has to break
// first if anyone decides otherwise: reaching a gateway from the network means reaching a program
// that runs commands on this machine, over a plain-http connection with no TLS, so no default of
// ours may open it.
//
// It is asserted exactly because it is the thing users rely on without knowing: a starlight left
// running on a laptop in a cafe must not be answering its neighbours.
func TestTheDefaultGatewayListenIsLoopbackAndNotOpenToTheNetwork(t *testing.T) {
	got := Default().GatewayListen()
	if !strings.HasPrefix(got, "127.0.0.1:") {
		t.Fatalf("the default gateway listen is %q: it must stay on loopback, because the default cannot be what exposes an agent that runs commands", got)
	}
	if strings.HasPrefix(got, "0.0.0.0") || strings.HasPrefix(got, "[::]") || strings.HasPrefix(got, ":") {
		t.Fatalf("the default gateway listen is %q: a wildcard default exposes the agent to every network this machine is on", got)
	}
	if Default().Gateway.AllowLAN {
		t.Fatal("allow_lan is on by default: it is the second act that opens the agent to a network, so it must be the operator who performs it")
	}
}

// allow_lan ALONE is enough to listen on every interface, and that is the whole point of it.
//
// The alternative - leave the address loopback and require the operator to also rewrite it - asked
// a person who wants their phone to reach the agent to know the machine's LAN address or to pick a
// wildcard by hand, which is a question about networking asked at the wrong moment. One setting
// that says what it means, and says it once, is the version that cannot be got half right: an
// allow_lan on with a loopback address is not a mistake to detect but a state that cannot exist.
func TestAllowLANAloneListensOnEveryInterface(t *testing.T) {
	c := Default()
	c.Gateway.AllowLAN = true
	c.Gateway.Listen = ""
	// Required by validation and unrelated to what this test measures.
	c.LLM.APIKey = "test"

	if err := c.Validate(); err != nil {
		t.Fatalf("allow_lan alone must be accepted, without the operator also rewriting listen: %v", err)
	}
	got := defaultListenFor(c.Gateway)
	if got != "0.0.0.0:7477" {
		t.Fatalf("with allow_lan the listen address resolves to %q, want 0.0.0.0:7477", got)
	}
	// And the port is the same one, because a client that already knows the address must not have
	// to learn a second one to reach the same gateway from further away.
	if port := strings.TrimPrefix(got, "0.0.0.0:"); port != strings.TrimPrefix(defaultGatewayListen, "127.0.0.1:") {
		t.Fatalf("allow_lan changed the port (%q vs %q): reaching a gateway from further away must not move it", got, defaultGatewayListen)
	}
}

// An EXPLICIT listen wins over allow_lan. Someone who wrote an address meant that address, and
// silently replacing it would be the same class of bug as ignoring a flag.
func TestAnExplicitListenSurvivesAllowLAN(t *testing.T) {
	c := Default()
	c.Gateway.AllowLAN = true
	c.Gateway.Listen = "192.168.100.90:7477"
	// Required by validation and unrelated to what this test measures.
	c.LLM.APIKey = "test"

	if err := c.Validate(); err != nil {
		t.Fatalf("a concrete LAN address with allow_lan must be valid: %v", err)
	}
	if got := defaultListenFor(c.Gateway); got != "192.168.100.90:7477" {
		t.Fatalf("an explicit listen became %q: the operator's address must never be replaced", got)
	}
	// And the binding agrees with the validation, which is the whole reason the resolution is one
	// function: a divergence here is a gateway that validated as one address and bound another.
	if got := c.GatewayListen(); got != "192.168.100.90:7477" {
		t.Fatalf("GatewayListen() is %q while validation saw %q: the bind and the validation disagree", got, c.Gateway.Listen)
	}
}

// Turning allow_lan back off must not leave the resolved address behind. The settings are read on
// every start, so this is the difference between a flag and a latch.
func TestTurningAllowLANOffReturnsToLoopback(t *testing.T) {
	// Listen is cleared first, because the shipped default spells the address out and an explicit
	// address always wins: without this the test would measure that rule instead of this one.
	on := Default()
	on.Gateway.Listen = ""
	on.Gateway.AllowLAN = true
	if got := defaultListenFor(on.Gateway); got != "0.0.0.0:7477" {
		t.Fatalf("precondition: allow_lan should open the address, got %q", got)
	}

	off := on
	off.Gateway.AllowLAN = false
	if got := defaultListenFor(off.Gateway); got != defaultGatewayListen {
		t.Fatalf("allow_lan off left the address at %q, want %q back", got, defaultGatewayListen)
	}
}
