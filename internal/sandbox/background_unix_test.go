//go:build unix

package sandbox

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/madkoding/motita/internal/execx"
)

// A command that succeeds and leaves a child holding its output finished well: Wait reports
// exec.ErrWaitDelay, which only means the pipe stayed open, and the run was told the isolation
// could not start.
func TestARunThatLeavesABackgroundChildSucceeds(t *testing.T) {
	box, err := New(Options{Dir: t.TempDir()})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer box.Close()

	out, _, exit, err := box.Run(context.Background(), execx.Request{
		Command: "/bin/sh", Args: []string{"-c", "sleep 5 & echo started"}, Timeout: 20 * time.Second,
	})
	if err != nil || exit != 0 || strings.TrimSpace(out) != "started" {
		t.Errorf("out=%q exit=%d err=%v, want the output and exit 0", out, exit, err)
	}
}
