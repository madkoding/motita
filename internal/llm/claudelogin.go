package llm

// SIGNING CLAUDE IN AGAIN FROM THE PAGE. claude keeps its own login, so motita does not sign it in:
// it runs `claude auth login` on the gateway's machine, as the person would, and relays the two
// things a person does there. claude prints the address of Anthropic's sign-in page; the person
// signs in, and the page shows a code they paste back, which goes to claude's input. motita never
// reads the token: claude stores it where it always does.

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"time"
)

// claudeLoginURLWait bounds how long claude may take to print the sign-in address.
var claudeLoginURLWait = 30 * time.Second

// claudeLoginURL finds the sign-in address in what claude prints.
var claudeLoginURL = regexp.MustCompile(`https://\S+/oauth/authorize\S*`)

// ClaudeLogin is a `claude auth login` under way.
type ClaudeLogin struct {
	// URL is the sign-in page to open.
	URL   string
	cmd   *exec.Cmd
	stdin io.WriteCloser
	out   *syncBuffer
	ended chan error
}

// syncBuffer is claude's output, written by the process and read while it runs.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// StartClaudeLogin starts `claude auth login` for the Claude subscription and waits for the address
// it prints. ctx bounds the whole sign-in: when it ends, claude is stopped.
func StartClaudeLogin(ctx context.Context) (*ClaudeLogin, error) {
	_, path, err := claudeBinary()
	if err != nil {
		return nil, err
	}
	// #nosec G204 -- the program is the claude CLI the user installed, with fixed arguments.
	cmd := exec.CommandContext(ctx, path, "auth", "login", "--claudeai")
	cmd.Env = claudeBaseEnv()
	cmd.Cancel = func() error { return terminate(cmd.Process) }
	cmd.WaitDelay = 5 * time.Second
	l := &ClaudeLogin{cmd: cmd, out: &syncBuffer{}, ended: make(chan error, 1)}
	cmd.Stdout, cmd.Stderr = l.out, l.out
	if l.stdin, err = cmd.StdinPipe(); err == nil {
		err = cmd.Start()
	}
	if err != nil {
		return nil, fmt.Errorf("could not run claude auth login: %w", err)
	}
	go func() { l.ended <- cmd.Wait() }()
	if err := l.awaitURL(); err != nil {
		_ = terminate(cmd.Process)
		return nil, err
	}
	return l, nil
}

// awaitURL waits for claude to print the sign-in address, to end, or for claudeLoginURLWait.
func (l *ClaudeLogin) awaitURL() error {
	deadline := time.Now().Add(claudeLoginURLWait)
	for time.Now().Before(deadline) {
		if u := claudeLoginURL.FindString(l.out.String()); u != "" {
			l.URL = u
			return nil
		}
		select {
		case err := <-l.ended:
			l.ended <- err
			return fmt.Errorf("claude auth login ended without a sign-in address: %s", tail(l.out.String()))
		case <-time.After(50 * time.Millisecond):
		}
	}
	return errors.New("claude auth login printed no sign-in address in time")
}

// Wait hands claude the code the person pastes and reports how the sign-in ended. A sign-in that
// worked clears the expired mark at once.
func (l *ClaudeLogin) Wait(ctx context.Context, pasted <-chan string) error {
	for {
		select {
		case <-ctx.Done():
			_ = terminate(l.cmd.Process)
			return ctx.Err()
		case code := <-pasted:
			_, _ = io.WriteString(l.stdin, strings.TrimSpace(code)+"\n")
		case err := <-l.ended:
			if err != nil {
				return fmt.Errorf("claude auth login failed: %s", tail(l.out.String()))
			}
			noteClaudeSignIn(true)
			return nil
		}
	}
}

// tail is the end of what claude printed, on one line, short enough for a message.
func tail(out string) string {
	out = strings.Join(strings.Fields(out), " ")
	if len(out) > 300 {
		out = "…" + out[len(out)-300:]
	}
	return out
}
