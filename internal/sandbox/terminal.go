package sandbox

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/madkoding/motita/internal/execx"
)

// TerminalSession is a shell a person drives through a pseudo-terminal: what is read from it is
// what the terminal shows, what is written to it is what was typed.
type TerminalSession interface {
	io.ReadWriteCloser
	// Resize sets the terminal's size in character cells.
	Resize(cols, rows int) error
	// Wait blocks until the shell has ended and reports its exit code.
	Wait() (int, error)
}

// terminalEnv is what a shell on a terminal is told about it: a terminal emulator that speaks
// xterm's colours and keys, and a prompt (a shell with no rc file of its own in the sandbox's home
// falls back to this one). bash's names the folder; a plain sh does not expand \W, so it gets `$`.
func terminalEnv(command string) []string {
	prompt := "PS1=$ "
	if filepath.Base(command) == "bash" {
		prompt = `PS1=\W \$ `
	}
	return []string{"TERM=xterm-256color", prompt}
}

// maxTerminalSize bounds a size a client asks for; a terminal this large is a mistake, and the
// kernel's winsize holds 16 bits.
const maxTerminalSize = 1000

// clampSize is n as a terminal dimension: at least 1, at most maxTerminalSize.
func clampSize(n int) uint16 {
	switch {
	case n < 1:
		return 1
	case n > maxTerminalSize:
		return maxTerminalSize
	}
	return uint16(n)
}

// OpenTerminal starts p as an interactive command on a pseudo-terminal of cols × rows cells, under
// the same isolation as Run: the confinement, the limits, the network rule and the private
// temporary directory. It has no time limit of its own: it ends when the command does, when ctx
// is done, or when the session is closed.
func (s *Sandbox) OpenTerminal(ctx context.Context, p execx.Request, cols, rows int) (TerminalSession, error) {
	master, slave, err := openPTY()
	if err != nil {
		return nil, err
	}
	// The slave is the child's from here on: the parent's copy closes once it started, so the
	// master reads end-of-file when the shell and everything it left are gone.
	defer slave.Close()
	if err := resizePTY(master, cols, rows); err != nil {
		master.Close()
		return nil, err
	}
	run, err := s.prepare(p, terminalEnv(p.Command))
	if err != nil {
		master.Close()
		return nil, err
	}

	attr, _ := childAttributes(run.limits, false, 0, 0)
	attr = terminalAttributes(attr)
	group := s.newRunGroup()
	cmd := s.terminalCommand(ctx, run, group.attach(attr), group, slave)
	err = cmd.Start()
	if err != nil && group != nil {
		// As in launch: without clone3 into a cgroup the child starts outside one.
		noRunGroups.Store(true)
		_ = group.remove()
		group = nil
		attr, _ = childAttributes(run.limits, false, 0, 0)
		cmd = s.terminalCommand(ctx, run, terminalAttributes(attr), nil, slave)
		err = cmd.Start()
	}
	if err != nil {
		master.Close()
		run.cleanup()
		return nil, fmt.Errorf("could not start the terminal: %w", err)
	}
	return &terminal{master: master, cmd: cmd, group: group, cleanup: run.cleanup}, nil
}

// terminalCommand is the isolation child of a terminal, with tty as its standard streams.
func (s *Sandbox) terminalCommand(ctx context.Context, run preparedRun, attr *syscall.SysProcAttr,
	group *runGroup, tty *os.File) *exec.Cmd {
	cmd := launchCommand(ctx, s.executable, run.args...)
	cmd.Dir = run.workDir
	cmd.Env = s.environment()
	cmd.SysProcAttr = attr
	cmd.Cancel = func() error {
		group.kill()
		return killGroup(cmd)
	}
	cmd.WaitDelay = 2 * time.Second
	cmd.Stdin, cmd.Stdout, cmd.Stderr = tty, tty, tty
	return cmd
}

// terminal is a TerminalSession on a pseudo-terminal this package opened.
type terminal struct {
	master  *os.File
	cmd     *exec.Cmd
	group   *runGroup
	cleanup func()
	once    sync.Once
}

func (t *terminal) Read(p []byte) (int, error)  { return t.master.Read(p) }
func (t *terminal) Write(p []byte) (int, error) { return t.master.Write(p) }

// Close hangs up the terminal: the shell gets its SIGHUP, as when a terminal window is closed.
func (t *terminal) Close() error { return t.master.Close() }

func (t *terminal) Resize(cols, rows int) error { return resizePTY(t.master, cols, rows) }

// Wait ends the run the way Run does: what the shell left running goes with it, and so does its
// temporary directory. It may be called once; a second call reports the first one's end.
func (t *terminal) Wait() (int, error) {
	err := t.cmd.Wait()
	t.once.Do(func() {
		killLeftovers(t.cmd)
		t.group.kill()
		_ = t.group.remove()
		t.cleanup()
	})
	var ee *exec.ExitError
	switch {
	case err == nil, errors.Is(err, exec.ErrWaitDelay):
		return 0, nil
	case errors.As(err, &ee):
		return ee.ExitCode(), nil
	default:
		return -1, err
	}
}
