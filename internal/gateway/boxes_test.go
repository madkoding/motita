package gateway

import (
	"context"
	"testing"
	"time"

	"github.com/madkoding/motita/internal/agent"
)

// The agent saves a box's screenshots where the gateway looks for them when the run ends.
func TestBoxScreenshotsLandWhereThePreviewsAreRead(t *testing.T) {
	if agent.PreviewDir != PreviewDir {
		t.Fatalf("agent saves in %q, the gateway reads %q", agent.PreviewDir, PreviewDir)
	}
}

// A gateway that starts removes the service containers an earlier process left behind.
func TestStartingTheGatewaySweepsOrphanBoxes(t *testing.T) {
	swept := make(chan struct{}, 1)
	old := sweepBoxes
	t.Cleanup(func() { sweepBoxes = old })
	sweepBoxes = func(context.Context) int { swept <- struct{}{}; return 0 }
	(&Server{}).loadPersistedSessions()
	select {
	case <-swept:
	case <-time.After(5 * time.Second):
		t.Fatal("the sweep did not run")
	}
}
