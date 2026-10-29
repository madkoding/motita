package tui

import (
	"context"
	"strings"
	"testing"
	"time"
)

func goalTUI(t *testing.T) (*TUI, *fakeRunner, string) {
	t.Helper()
	dir := t.TempDir()
	runner := &fakeRunner{cfg: configWithKey("k"), cfgSet: true}
	runner.cfg.Agent.WorkspaceDir = dir
	tu, _ := newKeyTUI("")
	tu.Runner = runner
	tu.Width, tu.Height = 110, 30
	return tu, runner, dir
}

func lastMessage(tu *TUI) string {
	return tu.messages[len(tu.messages)-1].Text
}

func TestGoalRunsTheTaskWithItsPrefix(t *testing.T) {
	tu, runner, _ := goalTUI(t)
	tu.handleShortcut(context.Background(), "/goal")
	if !strings.Contains(lastMessage(tu), "usage: /goal") {
		t.Errorf("an empty goal must show its usage, got %q", lastMessage(tu))
	}
	tu.handleShortcut(context.Background(), "/goal   add login ")
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		runner.mu.Lock()
		got := runner.lastTask
		runner.mu.Unlock()
		if got == "/goal add login" {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("the goal never reached the runner with its prefix")
}
