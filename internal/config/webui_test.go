package config

import "testing"

// The interface comes up WITH the gateway, which is the point of it: a second deliberate act to
// get a page would defeat "it arrives with the gateway". Default() is what every path starts
// from - the YAML, -p, the interface itself - so the default living here is what makes it true
// everywhere at once.
func TestTheInterfaceIsOnByDefaultWhenTheGatewayIsOn(t *testing.T) {
	c := Default()
	if !c.Gateway.Enabled {
		t.Fatal("the gateway is off by default, so this test proves nothing about the interface")
	}
	if !c.Gateway.WebUI {
		t.Fatal("the interface is off by default: the gateway would come up with nothing to show")
	}
}

// And it can be turned off, because an operator serving an API to their own scripts does not
// necessarily want a browser console for an agent that runs commands on this machine.
func TestTheInterfaceCanBeTurnedOff(t *testing.T) {
	c := Default()
	c.Gateway.WebUI = false
	if c.Gateway.WebUI {
		t.Fatal("the interface cannot be turned off")
	}
}

// The environment variable exists for the same reason the other gateway ones do: a container or
// a systemd unit sets it, and that is the one place nobody can check by hand.
func TestTheInterfaceComesFromTheEnvironment(t *testing.T) {
	t.Setenv("MOTITA_GATEWAY_WEBUI", "false")

	c := Default()
	if err := ApplyEnvironment(&c); err != nil {
		t.Fatalf("ApplyEnvironment: %v", err)
	}
	if c.Gateway.WebUI {
		t.Fatal("MOTITA_GATEWAY_WEBUI=false did not turn the interface off")
	}

	t.Setenv("MOTITA_GATEWAY_WEBUI", "true")
	c = Default()
	c.Gateway.WebUI = false
	if err := ApplyEnvironment(&c); err != nil {
		t.Fatalf("ApplyEnvironment: %v", err)
	}
	if !c.Gateway.WebUI {
		t.Fatal("MOTITA_GATEWAY_WEBUI=true did not turn the interface on")
	}
}

// A value that is not a boolean is reported, not silently ignored: a typo in a container's
// environment would otherwise leave the interface on with nobody noticing the setting did nothing.
func TestAMalformedInterfaceSettingIsReported(t *testing.T) {
	t.Setenv("MOTITA_GATEWAY_WEBUI", "maybe")

	c := Default()
	if err := ApplyEnvironment(&c); err == nil {
		t.Fatal("a malformed boolean was accepted")
	}
}
