package config

import (
	"strings"
	"testing"
)

// The default posture: the gateway LISTENS EVERYWHERE and RULES NOTHING OUT.
//
// This is the test that has to break first if anyone decides otherwise, and the decision it pins is
// deliberate rather than accidental. The gateway comes up on the wildcard the way a machine with a
// fresh, empty firewall table accepts everything, and an operator narrows it by ADDING a rule to
// gateway.allow - one rule at a time, which is the model a firewall taught everyone. The token is
// what stands between the network and an agent that runs commands here; the rules say where that
// token may come from.
//
// The predecessor of this test asserted the opposite (a loopback default behind a second act called
// allow_lan), and it was replaced rather than deleted: the property worth pinning is not "which
// address" but "what the out-of-the-box gateway does", and those are different claims.
func TestTheDefaultGatewayListensEverywhereWithNoRules(t *testing.T) {
	got := Default().GatewayListen()
	if got != "0.0.0.0:7477" {
		t.Fatalf("the default gateway listen is %q, want the wildcard on the fixed port %q", got, "0.0.0.0:7477")
	}
	// The rules are what restricts it, and the default restricts nothing.
	if len(Default().Gateway.Allow) != 0 {
		t.Fatalf("gateway.allow defaults to %v: a shipped default must not invent rules, because "+
			"who may connect is the operator's decision", Default().Gateway.Allow)
	}
}

// The bind and the rules are SEPARATE facts, and this is what the old design conflated.
//
// A wildcard listen with a `!any` rule is not a contradiction: the socket is open on every
// interface and the gateway serves only this machine. Answering "who may reach this" from the
// address would get that backwards.
func TestTheAddressDoesNotDecideWhoMayConnect(t *testing.T) {
	c := Default()
	// Required by validation and unrelated to what this test measures.
	c.LLM.APIKey = "test"

	// Every combination of address and rules is a valid configuration now, because there is no
	// pairing between them left to get wrong.
	for _, listen := range []string{"", "127.0.0.1:7477", "0.0.0.0:7477", "192.168.100.90:7477", "[::]:7477"} {
		for _, allow := range [][]string{nil, {"!any"}, {"lan"}, {"!lan", "lan"}} {
			c.Gateway.Listen = listen
			c.Gateway.Allow = allow
			if err := c.Validate(); err != nil {
				t.Errorf("listen %q with allow %v was refused: %v", listen, allow, err)
			}
		}
	}
}

// An EXPLICIT listen wins over the default, because someone who wrote an address meant that
// address.
func TestAnExplicitListenIsKept(t *testing.T) {
	c := Default()
	c.Gateway.Listen = "192.168.100.90:7477"
	// Required by validation and unrelated to what this test measures.
	c.LLM.APIKey = "test"

	if err := c.Validate(); err != nil {
		t.Fatalf("an explicit LAN address must be valid: %v", err)
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

// A listen that is not host:port is still refused, because that one cannot be bound at all. The
// check that went away is the LOOPBACK requirement, not the syntax check - and this asserts the
// difference so that removing the wrong one is caught.
func TestAMalformedListenIsStillRefused(t *testing.T) {
	c := Default()
	c.LLM.APIKey = "test"
	c.Gateway.Listen = "not an address"

	err := c.Validate()
	if err == nil {
		t.Fatal("an address that is not host:port was accepted")
	}
	if !strings.Contains(err.Error(), "host:port") {
		t.Fatalf("the failure does not say the shape it wanted: %v", err)
	}
}

// The predicate behind the removed rule is asserted directly, because the rule it enforced no
// longer exists and the property it described still has to be answerable: a caller that needs to
// know whether an address is private to this machine gets the same answer as before. Keeping the
// check covered is also what makes the removal of the REFUSAL visible rather than silent - the
// validation cases below it pass a wildcard, a LAN address and an empty host, all of which used to
// be refused for this predicate's sake.
func TestTheLoopbackPredicateStillAnswers(t *testing.T) {
	cases := map[string]bool{
		"127.0.0.1":   true,
		"127.0.0.53":  true,
		"::1":         true,
		"localhost":   true,
		"LOCALHOST":   true,
		"0.0.0.0":     false,
		"192.168.1.5": false,
		"8.8.8.8":     false,
		"":            false,
		"not-an-ip":   false,
	}
	for host, want := range cases {
		if got := gatewayAddressIsLoopback(host); got != want {
			t.Errorf("gatewayAddressIsLoopback(%q) = %v, want %v", host, got, want)
		}
	}
}

// A malformed rule is refused while the configuration is READ, not when a request happens to
// arrive.
//
// This is the difference between a misconfiguration the operator can see and one they cannot: the
// middleware applies the rules at request time, so a typo there would show up only as an origin
// being refused for a reason nobody can see.
func TestAMalformedRuleIsRefusedByValidation(t *testing.T) {
	for _, bad := range []string{"banana", "!", "192.168.1.0/99", ""} {
		c := Default()
		c.LLM.APIKey = "test"
		c.Gateway.Allow = []string{"lan", bad}

		err := c.Validate()
		if err == nil {
			t.Errorf("the rule %q was accepted", bad)
			continue
		}
		// The setting is named, because that is what the operator has to go and edit.
		if !strings.Contains(err.Error(), "gateway.allow") {
			t.Errorf("the failure for %q does not name gateway.allow: %v", bad, err)
		}
	}
}

// A valid rule set passes validation, so the check above cannot be satisfied by refusing
// everything.
func TestAValidRuleSetPassesValidation(t *testing.T) {
	c := Default()
	c.LLM.APIKey = "test"
	c.Gateway.Allow = []string{"lan", "192.168.1.10", "10.0.0.0/8", "!any"}

	if err := c.Validate(); err != nil {
		t.Fatalf("a valid rule set was refused: %v", err)
	}
}

// The rules are NOT read from a disabled gateway's configuration: an operator who turned the
// gateway off must not be refused for a rule that will never be applied.
func TestADisabledGatewayDoesNotCheckTheRules(t *testing.T) {
	c := Default()
	c.LLM.APIKey = "test"
	c.Gateway.Enabled = false
	c.Gateway.Allow = []string{"banana"}

	if err := c.Validate(); err != nil {
		t.Fatalf("a disabled gateway was refused for its rules: %v", err)
	}
}

// The environment carries the rules, because a container or a systemd unit sets this and that is
// the one place nobody can check by hand.
func TestTheRulesComeFromTheEnvironment(t *testing.T) {
	t.Setenv("MOTITA_GATEWAY_ALLOW", "lan,!any")

	c := Default()
	if err := ApplyEnvironment(&c); err != nil {
		t.Fatalf("ApplyEnvironment: %v", err)
	}
	want := []string{"lan", "!any"}
	if len(c.Gateway.Allow) != len(want) {
		t.Fatalf("MOTITA_GATEWAY_ALLOW produced %v, want %v", c.Gateway.Allow, want)
	}
	for i := range want {
		if c.Gateway.Allow[i] != want[i] {
			t.Fatalf("MOTITA_GATEWAY_ALLOW produced %v, want %v", c.Gateway.Allow, want)
		}
	}

	// And it is accepted by validation, so the two halves agree about the syntax.
	c.LLM.APIKey = "test"
	if err := c.Validate(); err != nil {
		t.Fatalf("the rules from the environment were refused by validation: %v", err)
	}
}

// A space between rules is accepted as well as a comma, because a space is what somebody types
// between rules by habit - and silently reading that as one malformed rule would be a rejection
// nobody could explain.
func TestRulesFromTheEnvironmentAcceptSpacesAsWellAsCommas(t *testing.T) {
	t.Setenv("MOTITA_GATEWAY_ALLOW", "lan 192.168.1.10")

	c := Default()
	if err := ApplyEnvironment(&c); err != nil {
		t.Fatalf("ApplyEnvironment: %v", err)
	}
	if len(c.Gateway.Allow) != 2 {
		t.Fatalf("a space-separated list produced %v, want two rules", c.Gateway.Allow)
	}
}

// An EMPTY variable leaves the field alone, exactly like every other text binding: exporting
// MOTITA_GATEWAY_ALLOW= in a compose file because a secret did not arrive must not wipe a rule
// the operator wrote in their YAML.
func TestAnEmptyRuleVariableLeavesTheFileAlone(t *testing.T) {
	for _, value := range []string{"", "   "} {
		t.Setenv("MOTITA_GATEWAY_ALLOW", value)

		c := Default()
		c.Gateway.Allow = []string{"lan"}
		if err := ApplyEnvironment(&c); err != nil {
			t.Fatalf("ApplyEnvironment: %v", err)
		}
		if len(c.Gateway.Allow) != 1 || c.Gateway.Allow[0] != "lan" {
			t.Fatalf("a blank variable (%q) wiped the configured rules: %v", value, c.Gateway.Allow)
		}
	}
}
