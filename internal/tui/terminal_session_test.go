//go:build !windows

package tui

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/madkoding/motita/internal/agent"
	"github.com/madkoding/motita/internal/config"
	"github.com/madkoding/motita/internal/execx"
	"github.com/madkoding/motita/internal/sandbox"
)

// The terminal opens where the typed commands are, as a command the person approved, and with the
// policy off as an unconfined one.
func TestTheTerminalOpensWhereTheTypedCommandsAre(t *testing.T) {
	orig := openBoxTerminal
	t.Cleanup(func() { openBoxTerminal = orig })
	var got execx.Request
	var size [2]int
	openBoxTerminal = func(_ *sandbox.Sandbox, _ context.Context, p execx.Request, cols, rows int) (sandbox.TerminalSession, error) {
		got, size = p, [2]int{cols, rows}
		return nil, errors.New("no shell in this test")
	}
	r, _, ws := projectRunner(t)
	if _, _, err := run(t, r, "cd motita"); err != nil {
		t.Fatal(err)
	}
	if _, err := r.OpenTerminal(context.Background(), 100, 30); err == nil {
		t.Fatal("the stand-in's error was lost")
	}
	if got.Dir != filepath.Join(ws, "motita") || !got.Approved || got.Unconfined || got.Command == "" {
		t.Errorf("request %+v", got)
	}
	if size != [2]int{100, 30} {
		t.Errorf("size %v", size)
	}

	r.Cfg.Agent.Policy.Enforce = false
	_, _ = r.OpenTerminal(context.Background(), 80, 24)
	if !got.Unconfined {
		t.Error("with the policy off the terminal is confined anyway")
	}
}

// The terminal's shell is the configured one, else bash, else the agent's own.
func TestTheTerminalShell(t *testing.T) {
	orig := lookPath
	t.Cleanup(func() { lookPath = orig })
	cfg := config.Default()
	cfg.Agent.Shell = ""
	lookPath = func(string) (string, error) { return "/bin/bash", nil }
	if got := terminalShell(cfg); got != "/bin/bash" {
		t.Errorf("with bash: %q", got)
	}
	lookPath = func(string) (string, error) { return "", errors.New("no bash") }
	if got := terminalShell(cfg); got != agent.ShellFor(cfg) {
		t.Errorf("without bash: %q", got)
	}
	cfg.Agent.Shell = "/bin/zsh"
	if got := terminalShell(cfg); got != "/bin/zsh" {
		t.Errorf("configured: %q", got)
	}
}

func TestASessionWithoutASandboxHasNoTerminal(t *testing.T) {
	r, _, _ := projectRunner(t)
	r.Box = nil
	if _, err := r.OpenTerminal(context.Background(), 80, 24); !errors.Is(err, errNoSandbox) {
		t.Errorf("got %v", err)
	}
}
