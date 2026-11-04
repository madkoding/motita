package gateway

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/madkoding/motita/internal/config"
	"github.com/madkoding/motita/internal/llm"
)

// fakeClaudeLogin makes claude's sign-in a fake: it prints url (or fails with err) and ends
// connected when "good" is pasted.
func fakeClaudeLogin(t *testing.T, url string, err error) {
	t.Helper()
	old := startClaudeLogin
	startClaudeLogin = func(context.Context) (string, func(context.Context, <-chan string) error, error) {
		if err != nil {
			return "", nil, err
		}
		return url, func(ctx context.Context, pasted <-chan string) error {
			select {
			case code := <-pasted:
				if code != "good" {
					return errors.New("invalid code")
				}
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		}, nil
	}
	t.Cleanup(func() { startClaudeLogin = old })
}

// TestClaudeCodeSignsInAgainFromThePage: an expired claude-code offers its own sign-in, which
// takes a pasted code, stores nothing of motita's and clears the expired mark.
func TestClaudeCodeSignsInAgainFromThePage(t *testing.T) {
	path := llmEnv(t, openAIConfig)
	srv, _ := llmServer(t, path)
	old := lookPath
	lookPath = func(string) (string, error) { return "/usr/bin/claude", nil }
	t.Cleanup(func() { lookPath = old })
	llm.NoteSignIn(claudeCodeID, false)
	t.Cleanup(func() { llm.NoteSignIn(claudeCodeID, true) })
	fakeClaudeLogin(t, "https://claude.com/cai/oauth/authorize?x", nil)

	p := providerOf(decodeLLM(t, get(t, srv, "/v1/llm", testToken)), claudeCodeID)
	if p.Key != config.KeyExpired || !p.SupportsLogin {
		t.Fatalf("claude-code = %+v", p)
	}
	v := decodeLogin(t, llmCall(t, srv, http.MethodPost, "/v1/llm/providers/claude-code/login", ""))
	if v.Status != "pending" || v.Method != "paste" || !strings.Contains(v.URL, "oauth/authorize") {
		t.Fatalf("start: %+v", v)
	}
	paste := "/v1/llm/logins/" + v.ID + "/paste"
	if w := llmCall(t, srv, http.MethodPost, paste, `{"code":"good"}`); w.Code != http.StatusAccepted {
		t.Fatalf("paste: %d", w.Code)
	}
	if got := waitStatus(t, srv, v.ID); got.Status != "connected" {
		t.Fatalf("end: %+v", got)
	}
	if config.HasLogin(claudeCodeID) {
		t.Error("claude keeps its own login: motita must not store one")
	}
	if p := providerOf(decodeLLM(t, get(t, srv, "/v1/llm", testToken)), claudeCodeID); p.Key != config.KeyNotNeeded {
		t.Errorf("after the sign-in claude-code = %+v", p)
	}
}

func TestClaudeCodeSignInFailures(t *testing.T) {
	path := llmEnv(t, openAIConfig)
	srv, _ := llmServer(t, path)
	fakeClaudeLogin(t, "", errors.New("claude was not found"))
	if w := llmCall(t, srv, http.MethodPost, "/v1/llm/providers/claude-code/login", ""); w.Code != http.StatusBadGateway ||
		!strings.Contains(w.Body.String(), "not found") {
		t.Errorf("no claude: %d %s", w.Code, w.Body)
	}
	fakeClaudeLogin(t, "https://claude.com/cai/oauth/authorize?x", nil)
	v := decodeLogin(t, llmCall(t, srv, http.MethodPost, "/v1/llm/providers/claude-code/login", ""))
	_ = llmCall(t, srv, http.MethodPost, "/v1/llm/logins/"+v.ID+"/paste", `{"code":"bad"}`)
	if got := waitStatus(t, srv, v.ID); got.Status != "failed" || got.Error != "invalid code" {
		t.Errorf("%+v", got)
	}
	// Without claude installed the page offers no sign-in button.
	old := lookPath
	lookPath = func(string) (string, error) { return "", errors.New("not found") }
	t.Cleanup(func() { lookPath = old })
	if p := providerOf(decodeLLM(t, get(t, srv, "/v1/llm", testToken)), claudeCodeID); p.SupportsLogin {
		t.Errorf("claude-code without claude = %+v", p)
	}
}

// TestStoredLoginShowsExpired: a stored login whose renewal failed shows as expired, and a
// disconnect clears the mark.
func TestStoredLoginShowsExpired(t *testing.T) {
	path := llmEnv(t, openAIConfig)
	srv, _ := llmServer(t, path)
	fakeStarters(t, fakeLoginFlows())
	v := decodeLogin(t, llmCall(t, srv, http.MethodPost, "/v1/llm/providers/codex/login", ""))
	_ = llmCall(t, srv, http.MethodPost, "/v1/llm/logins/"+v.ID+"/paste", `{"code":"c1"}`)
	if got := waitStatus(t, srv, v.ID); got.Status != "connected" {
		t.Fatalf("end: %+v", got)
	}
	llm.NoteSignIn("codex", false)
	t.Cleanup(func() { llm.NoteSignIn("codex", true) })
	if p := providerOf(decodeLLM(t, get(t, srv, "/v1/llm", testToken)), "codex"); p.Key != config.KeyExpired {
		t.Errorf("codex after a failed renewal = %+v", p)
	}
	if w := llmCall(t, srv, http.MethodDelete, "/v1/llm/providers/codex", ""); w.Code != http.StatusOK {
		t.Fatalf("forget: %d", w.Code)
	}
	if _, lost := llm.SignInLost("codex"); lost {
		t.Error("a disconnected provider is not expired")
	}
}

// TestStartClaudeLoginRunsClaude: the real start runs claude and hands back its address and wait.
func TestStartClaudeLoginRunsClaude(t *testing.T) {
	t.Setenv("MOTITA_CLAUDE_BIN", filepath.Join(t.TempDir(), "claude"))
	if _, _, err := startClaudeLogin(context.Background()); err == nil {
		t.Error("no claude must be an error")
	}
	if runtime.GOOS == "windows" {
		t.Skip("the stand-in claude is a shell script")
	}
	bin := filepath.Join(t.TempDir(), "claude")
	script := "#!/bin/sh\necho 'visit: https://claude.com/cai/oauth/authorize?x'\nread code\n[ \"$code\" = good ]\n"
	if err := os.WriteFile(bin, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MOTITA_CLAUDE_BIN", bin)
	url, wait, err := startClaudeLogin(context.Background())
	if err != nil || url != "https://claude.com/cai/oauth/authorize?x" {
		t.Fatalf("%q %v", url, err)
	}
	t.Cleanup(func() { llm.NoteSignIn(claudeCodeID, true) })
	pasted := make(chan string, 1)
	pasted <- "good"
	if err := wait(context.Background(), pasted); err != nil {
		t.Error(err)
	}
}
