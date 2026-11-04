package llm

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const fakeLoginURL = "https://claude.com/cai/oauth/authorize?code=true&state=abc"

// fakeClaudeLogin is `claude auth login`: script "noaddress" ends at once, "silent" prints nothing
// and waits; otherwise it prints the address and takes one code, "good" or anything else.
func fakeClaudeLogin(script string) {
	switch script {
	case "noaddress":
		fmt.Println("no browser here")
		os.Exit(1)
	case "silent":
		time.Sleep(time.Minute)
	default:
		fmt.Println("Opening browser to sign in…")
		fmt.Println("If the browser didn't open, visit: " + fakeLoginURL)
		fmt.Print("Paste code here if prompted > ")
	}
	sc := bufio.NewScanner(os.Stdin)
	if sc.Scan() && sc.Text() == "good" {
		fmt.Println("Login successful.")
		os.Exit(0)
	}
	fmt.Println("OAuth error: invalid code")
	os.Exit(1)
}

// fakeLogin points claude at the fake with a script.
func fakeLogin(t *testing.T, script string) {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("MOTITA_CLAUDE_BIN", exe)
	t.Setenv("MOTITA_FAKE_CLAUDE", script)
	t.Setenv("GORACE", "atexit_sleep_ms=0")
}

func TestClaudeLoginSignsInWithThePastedCode(t *testing.T) {
	resetClaudeSignIn(t)
	noteClaudeSignIn(false)
	fakeLogin(t, "")
	l, err := StartClaudeLogin(context.Background())
	if err != nil || l.URL != fakeLoginURL {
		t.Fatalf("login = %+v, %v", l, err)
	}
	pasted := make(chan string, 1)
	pasted <- "  good \n"
	if err := l.Wait(context.Background(), pasted); err != nil {
		t.Fatal(err)
	}
	if _, lost := SignInLost(claudeCodeID); lost {
		t.Error("a sign-in that worked must clear the expired mark")
	}
}

func TestClaudeLoginFailures(t *testing.T) {
	t.Run("wrong code", func(t *testing.T) {
		fakeLogin(t, "")
		l, err := StartClaudeLogin(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		pasted := make(chan string, 1)
		pasted <- "bad"
		if err := l.Wait(context.Background(), pasted); err == nil || !strings.Contains(err.Error(), "invalid code") {
			t.Errorf("err = %v, want claude's own words", err)
		}
	})
	t.Run("cancelled", func(t *testing.T) {
		fakeLogin(t, "")
		ctx, cancel := context.WithCancel(context.Background())
		l, err := StartClaudeLogin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		cancel()
		if err := l.Wait(ctx, nil); err != context.Canceled {
			t.Errorf("err = %v", err)
		}
	})
	t.Run("no address", func(t *testing.T) {
		fakeLogin(t, "noaddress")
		if _, err := StartClaudeLogin(context.Background()); err == nil ||
			!strings.Contains(err.Error(), "without a sign-in address: no browser here") {
			t.Errorf("err = %v", err)
		}
	})
	t.Run("silent", func(t *testing.T) {
		fakeLogin(t, "silent")
		old := claudeLoginURLWait
		claudeLoginURLWait = 200 * time.Millisecond
		t.Cleanup(func() { claudeLoginURLWait = old })
		if _, err := StartClaudeLogin(context.Background()); err == nil || !strings.Contains(err.Error(), "in time") {
			t.Errorf("err = %v", err)
		}
	})
	t.Run("no claude", func(t *testing.T) {
		t.Setenv("MOTITA_CLAUDE_BIN", filepath.Join(t.TempDir(), "claude"))
		if _, err := StartClaudeLogin(context.Background()); err == nil || !strings.Contains(err.Error(), "not found") {
			t.Errorf("err = %v", err)
		}
	})
	t.Run("not a program", func(t *testing.T) {
		bin := filepath.Join(t.TempDir(), "claude")
		_ = os.WriteFile(bin, []byte("not a program"), 0o700)
		t.Setenv("MOTITA_CLAUDE_BIN", bin)
		if _, err := StartClaudeLogin(context.Background()); err == nil || !strings.Contains(err.Error(), "could not run") {
			t.Errorf("err = %v", err)
		}
	})
}

func TestTailKeepsTheEnd(t *testing.T) {
	if got := tail("a\n  b"); got != "a b" {
		t.Errorf("tail = %q", got)
	}
	if got := tail(strings.Repeat("x", 400) + "end"); !strings.HasPrefix(got, "…") || !strings.HasSuffix(got, "end") ||
		len(got) != len("…")+300 {
		t.Errorf("tail = %q", got)
	}
}
