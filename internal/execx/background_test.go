//go:build unix

package execx

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A command that succeeds and leaves a child holding its output finished well: Wait reports
// exec.ErrWaitDelay, which only means the pipe stayed open, and the run was told it could not
// start.
func TestRunWithABackgroundChildSucceeds(t *testing.T) {
	out, _, exit, err := Run(context.Background(), Request{
		Command: "/bin/sh", Args: []string{"-c", "sleep 5 & echo started"}, Timeout: 20 * time.Second,
	})
	if err != nil || exit != 0 || strings.TrimSpace(out) != "started" {
		t.Errorf("out=%q exit=%d err=%v, want the output and exit 0", out, exit, err)
	}
}

// What a command leaves in the background ends with the run, not only on a timeout.
func TestRunEndsWhatTheCommandLeftBehind(t *testing.T) {
	mark := filepath.Join(t.TempDir(), "still-running")
	_, _, exit, err := Run(context.Background(), Request{
		Command: "/bin/sh", Args: []string{"-c", "(sleep 1; touch '" + mark + "') >/dev/null 2>&1 & echo ok"},
		Timeout: 10 * time.Second,
	})
	if err != nil || exit != 0 {
		t.Fatalf("exit=%d err=%v", exit, err)
	}
	time.Sleep(1500 * time.Millisecond)
	if _, err := os.Stat(mark); err == nil {
		t.Error("the background child outlived the run")
	}
}
