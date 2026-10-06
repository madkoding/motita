package tui

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/madkoding/motita/internal/agent"
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

func TestDecisionsListsAndUndoes(t *testing.T) {
	tu, _, dir := goalTUI(t)
	run := func(arg string) string {
		tu.handleShortcut(context.Background(), "/decisions"+arg)
		return lastMessage(tu)
	}
	if got := run(""); !strings.Contains(got, "no decisions yet") {
		t.Errorf("empty: %q", got)
	}
	path := filepath.Join(dir, agent.DecisionsFile)
	_ = os.MkdirAll(filepath.Dir(path), 0o755)
	_ = os.WriteFile(path, []byte("\n## 2026-01-01 00:00 — g\n\n- one\n- two\n"), 0o644)
	if got := run(""); !strings.Contains(got, "1. one") || !strings.Contains(got, "undo") {
		t.Errorf("list: %q", got)
	}
	if got := run(" undo x"); !strings.Contains(got, "usage: /decisions undo") {
		t.Errorf("bad number: %q", got)
	}
	if got := run(" undo 9"); !strings.Contains(got, "no decision 9") {
		t.Errorf("unknown number: %q", got)
	}
	if got := run(" undo 1"); !strings.Contains(got, "decision removed: one") {
		t.Errorf("undo: %q", got)
	}
}
