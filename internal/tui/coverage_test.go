package tui

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/madkoding/motita/internal/agent"
	"github.com/madkoding/motita/internal/config"
	"github.com/madkoding/motita/internal/llm"
	"github.com/madkoding/motita/internal/logx"
	"github.com/madkoding/motita/internal/sandbox"
)

// TestCommandsReturnsACopy: Commands() must return a copy so the caller cannot
// mutate the package's internal slice. Modifying the returned slice must not
// affect the catalogue.
func TestCommandsReturnsACopy(t *testing.T) {
	original := Commands()
	if len(original) != len(commands) {
		t.Fatalf("Commands() returned %d entries, catalogue has %d", len(original), len(commands))
	}
	// Mutate the copy and verify the original is untouched.
	original[0] = Command{Name: "/mutated"}
	again := Commands()
	if again[0].Name == "/mutated" {
		t.Fatal("Commands() returned a reference to the internal slice, not a copy")
	}
}

// TestCommandsContentMatchesCatalogue: every entry in the returned slice must
// match the catalogue by value.
func TestCommandsContentMatchesCatalogue(t *testing.T) {
	got := Commands()
	for i, c := range commands {
		if got[i].Name != c.Name || got[i].Help != c.Help || got[i].Arg != c.Arg || got[i].Group != c.Group {
			t.Errorf("Commands()[%d] = %+v, want %+v", i, got[i], c)
		}
	}
}

// --- SetWorkspace ---

// TestSetWorkspaceEmptyIsIgnored: an empty directory must be a no-op.
func TestSetWorkspaceEmptyIsIgnored(t *testing.T) {
	cfg := config.Default()
	cfg.Sandbox.Kind = "none"
	r := NewAppRunner(&bytes.Buffer{}, &bytes.Buffer{}, cfg, &llm.Client{}, &sandbox.Sandbox{}, logx.Global())
	before := r.Cfg.Agent.WorkspaceDir
	r.SetWorkspace("")
	if r.Cfg.Agent.WorkspaceDir != before {
		t.Errorf("SetWorkspace(\"\") changed WorkspaceDir from %q to %q", before, r.Cfg.Agent.WorkspaceDir)
	}
}

// TestSetWorkspaceNoneSandbox: with sandbox kind "none" the workspace is set
// and a new sandbox is built without cgroups or chroot.
func TestSetWorkspaceNoneSandbox(t *testing.T) {
	cfg := config.Default()
	cfg.Sandbox.Kind = "none"
	r := NewAppRunner(&bytes.Buffer{}, &bytes.Buffer{}, cfg, &llm.Client{}, &sandbox.Sandbox{}, logx.Global())
	dir := t.TempDir()
	r.SetWorkspace(dir)
	if r.Cfg.Agent.WorkspaceDir != dir {
		t.Errorf("WorkspaceDir = %q, want %q", r.Cfg.Agent.WorkspaceDir, dir)
	}
	if r.Box == nil {
		t.Error("Box must be rebuilt after SetWorkspace")
	}
}

// TestSetWorkspaceChrootSandbox: with sandbox kind "chroot" the workspace is
// set and the chroot options are configured.
func TestSetWorkspaceChrootSandbox(t *testing.T) {
	cfg := config.Default()
	cfg.Sandbox.Kind = "chroot"
	cfg.Sandbox.Root = t.TempDir()
	r := NewAppRunner(&bytes.Buffer{}, &bytes.Buffer{}, cfg, &llm.Client{}, &sandbox.Sandbox{}, logx.Global())
	dir := t.TempDir()
	r.SetWorkspace(dir)
	if r.Cfg.Agent.WorkspaceDir != dir {
		t.Errorf("WorkspaceDir = %q, want %q", r.Cfg.Agent.WorkspaceDir, dir)
	}
}

// TestSetWorkspaceDefaultSandboxKind: an unknown sandbox kind falls into the
// default branch, which disables cgroups.
func TestSetWorkspaceDefaultSandboxKind(t *testing.T) {
	cfg := config.Default()
	cfg.Sandbox.Kind = "cgroups"
	r := NewAppRunner(&bytes.Buffer{}, &bytes.Buffer{}, cfg, &llm.Client{}, &sandbox.Sandbox{}, logx.Global())
	dir := t.TempDir()
	r.SetWorkspace(dir)
	if r.Cfg.Agent.WorkspaceDir != dir {
		t.Errorf("WorkspaceDir = %q, want %q", r.Cfg.Agent.WorkspaceDir, dir)
	}
}

// TestSetWorkspaceWithUser: when a sandbox user is configured and parses, the
// uid/gid/drop-privs path is exercised.
func TestSetWorkspaceWithUser(t *testing.T) {
	cfg := config.Default()
	cfg.Sandbox.Kind = "none"
	cfg.Sandbox.User = fmt.Sprintf("%d:%d", os.Getuid(), os.Getgid())
	r := NewAppRunner(&bytes.Buffer{}, &bytes.Buffer{}, cfg, &llm.Client{}, &sandbox.Sandbox{}, logx.Global())
	dir := t.TempDir()
	r.SetWorkspace(dir)
	if r.Cfg.Agent.WorkspaceDir != dir {
		t.Errorf("WorkspaceDir = %q, want %q", r.Cfg.Agent.WorkspaceDir, dir)
	}
}

// --- fallbackTitle ---

func TestFallbackTitle(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"simple", "hello world", "hello world"},
		{"collapses whitespace", "  hello   world  ", "hello world"},
		{"empty", "", ""},
		{"only whitespace", "   \t\n  ", ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := fallbackTitle(tc.input)
			if got != tc.want {
				t.Errorf("fallbackTitle(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}

func TestFallbackTitleTruncation(t *testing.T) {
	long := strings.Repeat("a", 100)
	got := fallbackTitle(long)
	runes := []rune(got)
	// 59 runes + the ellipsis rune = 60
	if len(runes) != 60 {
		t.Errorf("fallbackTitle truncated to %d runes, want 60", len(runes))
	}
	if !strings.HasSuffix(got, "…") {
		t.Errorf("fallbackTitle must end with ellipsis, got %q", got)
	}
}

func TestFallbackTitleBoundaryExactly60(t *testing.T) {
	exactly60 := strings.Repeat("a", 60)
	got := fallbackTitle(exactly60)
	if got != exactly60 {
		t.Errorf("fallbackTitle(60 chars) = %q (len %d), want unchanged", got, len([]rune(got)))
	}
}

// --- GenerateTitle ---

// TestGenerateTitleSuccess: when the engine returns a title, it is cleaned up
// and returned.
func TestGenerateTitleSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"choices":[{"message":{"content":"  \"Deploying a Go Service\"  "}}]}`)
	}))
	defer srv.Close()

	cfg := config.Default()
	cfg.LLM.Provider = "openai"
	cfg.LLM.APIKey = "key"
	cfg.LLM.BaseURL = srv.URL
	cfg.LLM.MaxAttempts = 1
	cfg.LLM.Timeout = 5 * time.Second

	r := NewAppRunner(&bytes.Buffer{}, &bytes.Buffer{}, cfg, nil, nil, logx.Global())
	engine, err := llm.New(cfg.LLM, r.Log)
	if err != nil {
		t.Fatal(err)
	}
	r.Engine = engine

	title := r.GenerateTitle(context.Background(), "How do I deploy a Go service to production?")
	if title != "Deploying a Go Service" {
		t.Errorf("GenerateTitle = %q, want %q", title, "Deploying a Go Service")
	}
}

// TestGenerateTitleTruncatesLongTitles: the cleaned title is capped at 60 runes.
func TestGenerateTitleTruncatesLongTitles(t *testing.T) {
	long := strings.Repeat("word ", 30) // 150 chars
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `{"choices":[{"message":{"content":%q}}]}`, long)
	}))
	defer srv.Close()

	cfg := config.Default()
	cfg.LLM.Provider = "openai"
	cfg.LLM.APIKey = "key"
	cfg.LLM.BaseURL = srv.URL
	cfg.LLM.MaxAttempts = 1
	cfg.LLM.Timeout = 5 * time.Second

	r := NewAppRunner(&bytes.Buffer{}, &bytes.Buffer{}, cfg, nil, nil, logx.Global())
	engine, err := llm.New(cfg.LLM, r.Log)
	if err != nil {
		t.Fatal(err)
	}
	r.Engine = engine

	title := r.GenerateTitle(context.Background(), "msg")
	runes := []rune(title)
	if len(runes) > 60 {
		t.Errorf("GenerateTitle returned %d runes, want at most 60: %q", len(runes), title)
	}
	if !strings.HasSuffix(title, "…") {
		t.Errorf("a truncated title must end with ellipsis, got %q", title)
	}
}

// TestGenerateTitleFallsBackOnEmptyResponse: when the model returns an empty
// string, the fallback is used.
func TestGenerateTitleFallsBackOnEmptyResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"choices":[{"message":{"content":""}}]}`)
	}))
	defer srv.Close()

	cfg := config.Default()
	cfg.LLM.Provider = "openai"
	cfg.LLM.APIKey = "key"
	cfg.LLM.BaseURL = srv.URL
	cfg.LLM.MaxAttempts = 1
	cfg.LLM.Timeout = 5 * time.Second

	r := NewAppRunner(&bytes.Buffer{}, &bytes.Buffer{}, cfg, nil, nil, logx.Global())
	engine, err := llm.New(cfg.LLM, r.Log)
	if err != nil {
		t.Fatal(err)
	}
	r.Engine = engine

	firstMsg := "How do I deploy a Go service?"
	title := r.GenerateTitle(context.Background(), firstMsg)
	want := fallbackTitle(firstMsg)
	if title != want {
		t.Errorf("GenerateTitle with empty response = %q, want fallback %q", title, want)
	}
}

// TestGenerateTitleFallsBackOnEngineError: when the engine cannot be built,
// the fallback is used directly.
func TestGenerateTitleFallsBackOnEngineError(t *testing.T) {
	cfg := config.Default()
	cfg.LLM.Provider = "openai"
	cfg.LLM.APIKey = "" // no key → engine build fails
	r := NewAppRunner(&bytes.Buffer{}, &bytes.Buffer{}, cfg, nil, nil, logx.Global())
	// Engine is nil, so engine() will try to build one and fail.

	firstMsg := "How do I deploy a Go service?"
	title := r.GenerateTitle(context.Background(), firstMsg)
	want := fallbackTitle(firstMsg)
	if title != want {
		t.Errorf("GenerateTitle with no engine = %q, want fallback %q", title, want)
	}
}

// TestGenerateTitleFallsBackOnHTTPError: when the HTTP call fails, the fallback
// is used.
func TestGenerateTitleFallsBackOnHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	cfg := config.Default()
	cfg.LLM.Provider = "openai"
	cfg.LLM.APIKey = "key"
	cfg.LLM.BaseURL = srv.URL
	cfg.LLM.MaxAttempts = 1
	cfg.LLM.Timeout = 5 * time.Second

	r := NewAppRunner(&bytes.Buffer{}, &bytes.Buffer{}, cfg, nil, nil, logx.Global())
	engine, err := llm.New(cfg.LLM, r.Log)
	if err != nil {
		t.Fatal(err)
	}
	r.Engine = engine

	firstMsg := "How do I deploy a Go service?"
	title := r.GenerateTitle(context.Background(), firstMsg)
	want := fallbackTitle(firstMsg)
	if title != want {
		t.Errorf("GenerateTitle with HTTP error = %q, want fallback %q", title, want)
	}
}

// TestGenerateTitleStripsTrailingPunctuation: quotes and trailing punctuation
// are stripped from the returned title.
func TestGenerateTitleStripsTrailingPunctuation(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"choices":[{"message":{"content":"Go Deployment Guide."}}]}`)
	}))
	defer srv.Close()

	cfg := config.Default()
	cfg.LLM.Provider = "openai"
	cfg.LLM.APIKey = "key"
	cfg.LLM.BaseURL = srv.URL
	cfg.LLM.MaxAttempts = 1
	cfg.LLM.Timeout = 5 * time.Second

	r := NewAppRunner(&bytes.Buffer{}, &bytes.Buffer{}, cfg, nil, nil, logx.Global())
	engine, err := llm.New(cfg.LLM, r.Log)
	if err != nil {
		t.Fatal(err)
	}
	r.Engine = engine

	title := r.GenerateTitle(context.Background(), "msg")
	if title != "Go Deployment Guide" {
		t.Errorf("GenerateTitle = %q, want %q (trailing punctuation stripped)", title, "Go Deployment Guide")
	}
}

// --- RestoreTranscript ---

// TestRestoreTranscriptReplacesHistory: restoring a transcript replaces
// whatever the runner currently holds.
func TestRestoreTranscriptReplacesHistory(t *testing.T) {
	r := NewAppRunner(&bytes.Buffer{}, &bytes.Buffer{}, config.Default(), &llm.Client{}, &sandbox.Sandbox{}, logx.Global())
	// Seed with an initial transcript.
	r.RestoreTranscript([]agent.DialogueTurn{
		{User: "first", Agent: "answer1", Kind: "plan"},
	})
	if got := r.Transcript(); len(got) != 1 || got[0].User != "first" {
		t.Fatalf("initial transcript = %+v", got)
	}
	// Restore with a new set, which must replace the old one.
	r.RestoreTranscript([]agent.DialogueTurn{
		{User: "second", Agent: "answer2", Kind: "task"},
		{User: "third", Agent: "answer3", Kind: "task"},
	})
	got := r.Transcript()
	if len(got) != 2 {
		t.Fatalf("after restore, transcript has %d turns, want 2", len(got))
	}
	if got[0].User != "second" || got[1].User != "third" {
		t.Errorf("restored transcript = %+v", got)
	}
}

// TestRestoreTranscriptWithNil: restoring nil clears the transcript.
func TestRestoreTranscriptWithNil(t *testing.T) {
	r := NewAppRunner(&bytes.Buffer{}, &bytes.Buffer{}, config.Default(), &llm.Client{}, &sandbox.Sandbox{}, logx.Global())
	r.RestoreTranscript([]agent.DialogueTurn{
		{User: "first", Agent: "answer1", Kind: "plan"},
	})
	r.RestoreTranscript(nil)
	if got := r.Transcript(); len(got) != 0 {
		t.Errorf("after RestoreTranscript(nil), transcript has %d turns, want 0", len(got))
	}
}

// TestRestoreTranscriptReturnsACopy: the caller's slice must not share memory
// with the runner's internal copy, so mutating the input after the call must
// not affect what the runner holds.
func TestRestoreTranscriptReturnsACopy(t *testing.T) {
	r := NewAppRunner(&bytes.Buffer{}, &bytes.Buffer{}, config.Default(), &llm.Client{}, &sandbox.Sandbox{}, logx.Global())
	input := []agent.DialogueTurn{
		{User: "original", Agent: "ans", Kind: "plan"},
	}
	r.RestoreTranscript(input)
	// Mutate the input slice.
	input[0].User = "mutated"
	got := r.Transcript()
	if got[0].User != "original" {
		t.Errorf("RestoreTranscript did not copy: got %q, want %q", got[0].User, "original")
	}
}

// --- SetLLM (supplementary: the provider branch is not covered by the existing suite) ---

// TestSetLLMWithProviderDropsTheEngine: changing the provider must nil out the
// cached engine so the next turn rebuilds from the new configuration.
func TestSetLLMWithProviderDropsTheEngine(t *testing.T) {
	cfg := config.Default()
	cfg.LLM.Provider = "openai"
	cfg.LLM.Model = "gpt-4o-mini"
	injected := &llm.Client{}
	r := NewAppRunner(&bytes.Buffer{}, &bytes.Buffer{}, cfg, injected, nil, logx.Global())

	r.SetLLM("ollama", "")
	if r.Cfg.LLM.Provider != "ollama" {
		t.Errorf("provider = %q, want %q", r.Cfg.LLM.Provider, "ollama")
	}
	if r.Engine != nil {
		t.Error("SetLLM with a provider must drop the cached engine")
	}
}

// TestSetLLMWithBothArgsDropsTheEngineOnce: setting both provider and model
// still leaves Engine nil.
func TestSetLLMWithBothArgsDropsTheEngine(t *testing.T) {
	cfg := config.Default()
	cfg.LLM.Provider = "openai"
	cfg.LLM.Model = "gpt-4o-mini"
	injected := &llm.Client{}
	r := NewAppRunner(&bytes.Buffer{}, &bytes.Buffer{}, cfg, injected, nil, logx.Global())

	r.SetLLM("ollama", "llama3")
	if r.Cfg.LLM.Provider != "ollama" || r.Cfg.LLM.Model != "llama3" {
		t.Errorf("config = %+v", r.Cfg.LLM)
	}
	if r.Engine != nil {
		t.Error("SetLLM with both args must drop the cached engine")
	}
}

// --- watchResize default case ---

// TestWatchResizeBurstDoesNotBlock: sending two SIGWINCH signals in quick
// succession must not block the watcher. The out channel has capacity 1, so
// the second signal hits the default branch of the inner select — the path
// that keeps a burst from leaving the watcher stuck on a redraw already
// pending.
func TestWatchResizeBurstDoesNotBlock(t *testing.T) {
	out, stop := watchResize()
	defer stop()

	// Drain any pending notification so the buffer is empty before the burst.
	select {
	case <-out:
	default:
	}

	// Send two SIGWINCH signals in quick succession. The first fills the
	// buffered out channel; the second takes the default branch.
	if err := syscall.Kill(syscall.Getpid(), syscall.SIGWINCH); err != nil {
		t.Fatalf("first SIGWINCH: %v", err)
	}
	// Give the goroutine a moment to process the first signal and fill the
	// channel before sending the second.
	time.Sleep(50 * time.Millisecond)
	if err := syscall.Kill(syscall.Getpid(), syscall.SIGWINCH); err != nil {
		t.Fatalf("second SIGWINCH: %v", err)
	}
	// If the default branch were missing, the goroutine would block on
	// out <- struct{}{} and the second signal would hang. Give it a moment
	// to process the second signal (which takes the default path) and then
	// verify we can still receive at least one notification and stop cleanly.
	time.Sleep(50 * time.Millisecond)

	select {
	case <-out:
		// At least one notification was delivered.
	case <-time.After(time.Second):
		t.Error("expected at least one resize notification")
	}

	// stop must return promptly — if the goroutine were blocked on the
	// inner select's send, stop would hang.
	done := make(chan struct{})
	go func() {
		stop()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("stop() hung: the watcher goroutine is blocked")
	}
}
