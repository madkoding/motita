package config

import (
	"os"
	"strings"
	"testing"
)

// isolate_network is on unless the file or the environment turns it off: a missing key is the
// default, an explicit false is kept, and the settings write it in place.
func TestIsolateNetwork(t *testing.T) {
	t.Setenv("MOTITA_SANDBOX_ISOLATE_NETWORK", "")
	cfg, err := LoadWithoutKey(diskFile(t, "sandbox:\n  kind: none\n"))
	if err != nil || !cfg.Sandbox.IsolateNetwork {
		t.Fatalf("default = %v, %v", cfg.Sandbox.IsolateNetwork, err)
	}
	if cfg, _ := LoadWithoutKey(diskFile(t, "sandbox:\n  isolate_network: false\n")); cfg.Sandbox.IsolateNetwork {
		t.Fatal("an explicit false in the file was not kept")
	}
	path := diskFile(t, "sandbox:\n  kind: none # keep\n")
	if err := SetIsolateNetwork(path, false); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(path)
	if want := "sandbox:\n  isolate_network: false\n  kind: none # keep\n"; string(got) != want {
		t.Errorf("file = %q, want %q", got, want)
	}
	if cfg, _ := LoadWithoutKey(path); cfg.Sandbox.IsolateNetwork {
		t.Error("the saved value must load")
	}
	t.Setenv("MOTITA_SANDBOX_ISOLATE_NETWORK", "false")
	err = SetIsolateNetwork(path, true)
	if err == nil || !strings.Contains(err.Error(), "MOTITA_SANDBOX_ISOLATE_NETWORK") {
		t.Errorf("an overridden value must be refused: %v", err)
	}
	if after, _ := os.ReadFile(path); string(after) != string(got) {
		t.Errorf("the file must be restored: %q", after)
	}
}

// The environment turns it off over a file that says nothing.
func TestIsolateNetworkFromTheEnvironment(t *testing.T) {
	t.Setenv("MOTITA_SANDBOX_ISOLATE_NETWORK", "false")
	cfg, err := LoadWithoutKey(diskFile(t, "sandbox:\n  kind: none\n"))
	if err != nil {
		t.Fatal(err)
	}
	if err := ApplyEnvironment(&cfg); err != nil || cfg.Sandbox.IsolateNetwork {
		t.Error("MOTITA_SANDBOX_ISOLATE_NETWORK=false was ignored")
	}
}
