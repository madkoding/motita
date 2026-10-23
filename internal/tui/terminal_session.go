package tui

import (
	"context"
	"errors"
	"os/exec"

	"github.com/madkoding/motita/internal/agent"
	"github.com/madkoding/motita/internal/config"
	"github.com/madkoding/motita/internal/execx"
	"github.com/madkoding/motita/internal/sandbox"
)

// errNoSandbox is why a session without a sandbox has no terminal: its commands run through
// whatever executor it was given, and a terminal needs the sandbox's isolation around it.
var errNoSandbox = errors.New("this session runs its commands without a sandbox, so it has no terminal")

// OpenTerminal starts the session's shell on a pseudo-terminal of cols × rows cells, in the folder
// the typed commands are in (see RunShell), for a PERSON to drive.
//
// The policy cannot judge a terminal: it receives keys, not lines. Every key is the person's own,
// so the shell runs as a command they approved by name does - it may reach the network, use git's
// logins and write outside the workspace - and it stays inside the sandbox, which still hides
// motita's configuration, keys and model logins from it. With the policy switched off it runs
// unconfined, as every command then does.
func (r *AppRunner) OpenTerminal(ctx context.Context, cols, rows int) (sandbox.TerminalSession, error) {
	r.cfgMu.Lock()
	cfg := r.Cfg
	r.cfgMu.Unlock()
	if r.Box == nil {
		return nil, errNoSandbox
	}
	ws := workspaceRoot(cfg)
	shell := terminalShell(cfg)
	// A folder deleted since the last cd sends the terminal back to the workspace, as a line would.
	_, dir, _ := r.shell.prefix(ws, shell, "")
	req := execx.Request{Command: shell, Dir: dir, Approved: true, Unconfined: !cfg.Agent.Policy.Enforce}
	return openBoxTerminal(r.Box, ctx, req, cols, rows)
}

// terminalShell is the shell a person gets on the terminal: the one the settings name, and
// otherwise bash where there is one. The agent's default (a plain sh) runs its lines well, but
// typed into by a person it has no line editing: the arrow keys print ^[[A instead of recalling
// a command.
func terminalShell(cfg config.Config) string {
	if cfg.Agent.Shell != "" {
		return cfg.Agent.Shell
	}
	if bash, err := lookPath("bash"); err == nil {
		return bash
	}
	return agent.ShellFor(cfg)
}

// lookPath finds a program on the PATH; a variable so a test can take bash away.
var lookPath = exec.LookPath

// openBoxTerminal is the sandbox's OpenTerminal, a variable so a test sees the request without a
// real shell: the tui tests have no TestMain to serve the sandbox's re-executed child.
var openBoxTerminal = (*sandbox.Sandbox).OpenTerminal
