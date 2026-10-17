package app

import (
	"testing"

	"github.com/madkoding/motita/internal/config"
)

// The plan mode's fetch_url is closed exactly when the commands' network is.
func TestNetworkClosedFollowsIsolateNetwork(t *testing.T) {
	cfg := config.Default()
	if !networkClosed(cfg)() {
		t.Error("isolating is the default, so pages are refused by default")
	}
	cfg.Sandbox.IsolateNetwork = false
	if networkClosed(cfg)() {
		t.Error("the network is open and pages are still refused")
	}
}
