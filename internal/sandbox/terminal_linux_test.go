package sandbox

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/madkoding/motita/internal/execx"
)

// screen collects everything a terminal prints, so a test can wait for any of it however the
// reads happened to split it.
type screen struct {
	mu  sync.Mutex
	out bytes.Buffer
}

func watch(r io.Reader) *screen {
	s := &screen{}
	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := r.Read(buf)
			s.mu.Lock()
			s.out.Write(buf[:n])
			s.mu.Unlock()
			if err != nil {
				return
			}
		}
	}()
	return s
}

// waitFor waits until the terminal has printed want.
func (s *screen) waitFor(t *testing.T, want string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		s.mu.Lock()
		got := s.out.String()
		s.mu.Unlock()
		if strings.Contains(got, want) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%q never came; the terminal said %q", want, got)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// A shell on the terminal sees a terminal: its size, xterm as TERM, what is typed, and its exit.
func TestATerminalRunsAnInteractiveShell(t *testing.T) {
	s := newSandbox(t, Limits{})
	term, err := s.OpenTerminal(context.Background(), execx.Request{Command: "/bin/sh"}, 90, 30)
	if err != nil {
		t.Fatal(err)
	}
	defer term.Close()
	scr := watch(term)
	if _, err := io.WriteString(term, "stty size; echo \"term=$TERM\"; read name; echo \"hi $name\"\n"); err != nil {
		t.Fatal(err)
	}
	scr.waitFor(t, "30 90")
	scr.waitFor(t, "term=xterm-256color")
	if err := term.Resize(100, 40); err != nil {
		t.Fatal(err)
	}
	io.WriteString(term, "mad\n")
	scr.waitFor(t, "hi mad")
	io.WriteString(term, "stty size; exit 3\n")
	scr.waitFor(t, "40 100")
	code, err := term.Wait()
	if err != nil || code != 3 {
		t.Fatalf("exit %d, %v", code, err)
	}
	// A second wait does not clean up twice: it only says the shell was already waited for.
	if _, err := term.Wait(); err == nil {
		t.Error("a second wait reported a fresh end")
	}
}

// A shell that ends normally reports 0, and the cancellation of its context ends one that does not.
func TestATerminalEndsWithItsShellOrItsContext(t *testing.T) {
	s := newSandbox(t, Limits{})
	term, err := s.OpenTerminal(context.Background(), execx.Request{Command: "/bin/sh"}, 80, 24)
	if err != nil {
		t.Fatal(err)
	}
	io.WriteString(term, "exit\n")
	if code, err := term.Wait(); code != 0 || err != nil {
		t.Fatalf("exit %d, %v", code, err)
	}
	term.Close()

	ctx, cancel := context.WithCancel(context.Background())
	term, err = s.OpenTerminal(ctx, execx.Request{Command: "/bin/sh"}, 0, 5000)
	if err != nil {
		t.Fatal(err)
	}
	defer term.Close()
	cancel()
	if code, _ := term.Wait(); code == 0 {
		t.Error("a killed shell does not report success")
	}
}

func TestATerminalPromptFitsItsShell(t *testing.T) {
	if got := terminalEnv("/usr/bin/bash"); got[1] != `PS1=\W \$ ` {
		t.Errorf("bash: %q", got)
	}
	if got := terminalEnv("sh"); got[0] != "TERM=xterm-256color" || got[1] != "PS1=$ " {
		t.Errorf("sh: %q", got)
	}
}

func TestClampSize(t *testing.T) {
	for in, want := range map[int]uint16{-4: 1, 0: 1, 80: 80, 1 << 20: maxTerminalSize} {
		if got := clampSize(in); got != want {
			t.Errorf("clampSize(%d) = %d, want %d", in, got, want)
		}
	}
}

func TestWithVariables(t *testing.T) {
	got := withVariables([]string{"TERM=dumb", "PATH=/bin", "TERMINAL=x"}, []string{"TERM=xterm", "PS1=$ "})
	want := "PATH=/bin TERMINAL=x TERM=xterm PS1=$ "
	if strings.Join(got, " ") != want {
		t.Errorf("got %q", got)
	}
}

// Each step of opening a pseudo-terminal that fails says which one it was, and leaves nothing open.
func TestOpeningAPseudoTerminalReportsTheStepThatFailed(t *testing.T) {
	orig := ptyHooks
	t.Cleanup(func() { ptyHooks = orig })
	s := newSandbox(t, Limits{})
	open := func() error {
		_, err := s.OpenTerminal(context.Background(), execx.Request{Command: "/bin/sh"}, 80, 24)
		return err
	}

	ptyHooks.open = func(string, int, os.FileMode) (*os.File, error) { return nil, errors.New("no ptmx") }
	if err := open(); err == nil || !strings.Contains(err.Error(), "could not open a pseudo-terminal") {
		t.Errorf("open: %v", err)
	}

	ptyHooks.open = orig.open
	failing := func(req uintptr) func(fd, r uintptr, arg []byte) syscall.Errno {
		return func(fd, r uintptr, arg []byte) syscall.Errno {
			if r == req {
				return syscall.ENOTTY
			}
			return orig.ioctl(fd, r, arg)
		}
	}
	for req, msg := range map[uintptr]string{
		syscall.TIOCSPTLCK: "could not unlock",
		syscall.TIOCGPTN:   "could not name",
		syscall.TIOCSWINSZ: "could not resize",
	} {
		ptyHooks.ioctl = failing(req)
		if err := open(); err == nil || !strings.Contains(err.Error(), msg) {
			t.Errorf("ioctl %#x: %v", req, err)
		}
	}
	ptyHooks.ioctl = orig.ioctl

	realMmap := mmapAnon
	mmapAnon = func(int, int64, int, int, int) ([]byte, error) { return nil, syscall.ENOMEM }
	err := open()
	mmapAnon = realMmap
	if err == nil || !strings.Contains(err.Error(), "could not unlock") {
		t.Errorf("mmap: %v", err)
	}

	ptyHooks.open = func(path string, flag int, perm os.FileMode) (*os.File, error) {
		if strings.HasPrefix(path, "/dev/pts/") {
			return nil, errors.New("no slave")
		}
		return orig.open(path, flag, perm)
	}
	if err := open(); err == nil || !strings.Contains(err.Error(), "other end") {
		t.Errorf("slave: %v", err)
	}
}

// What Run refuses, a terminal refuses too, and a child that cannot start is reported as such.
func TestATerminalThatCannotStart(t *testing.T) {
	s := newSandbox(t, Limits{})
	if _, err := s.OpenTerminal(context.Background(), execx.Request{Command: " "}, 80, 24); err == nil {
		t.Error("an empty command opened a terminal")
	}
	s.executable = "/nonexistent/motita"
	_, err := s.OpenTerminal(context.Background(), execx.Request{Command: "/bin/sh"}, 80, 24)
	if err == nil || !strings.Contains(err.Error(), "could not start the terminal") {
		t.Errorf("got %v", err)
	}
}

// A kernel that will not start the child inside the run group still gives the terminal, without one.
func TestATerminalWithoutARunGroupStillOpens(t *testing.T) {
	fakeCgroupV2(t)
	noRunGroups.Store(false)
	t.Cleanup(func() { noRunGroups.Store(false) })
	s, err := New(Options{Dir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	term, err := s.OpenTerminal(context.Background(), execx.Request{Command: "/bin/sh"}, 80, 24)
	if err != nil {
		t.Fatal(err)
	}
	defer term.Close()
	io.WriteString(term, "exit 0\n")
	if code, err := term.Wait(); code != 0 || err != nil {
		t.Fatalf("exit %d, %v", code, err)
	}
	if !noRunGroups.Load() {
		t.Error("a group that cannot hold a child is not tried again")
	}
}

// The terminal's Wait reads a child that could not be waited on as a failure with no code.
func TestATerminalWaitWithoutAChild(t *testing.T) {
	term := &terminal{cmd: &exec.Cmd{}, cleanup: func() {}}
	if code, err := term.Wait(); code != -1 || err == nil {
		t.Errorf("exit %d, %v", code, err)
	}
}
