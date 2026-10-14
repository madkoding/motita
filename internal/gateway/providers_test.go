package gateway

import (
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/madkoding/motita/internal/config"
)

// unsetEnvForTest removes the named env vars for the duration of the test and
// restores them afterwards. t.Setenv("") sets a variable to the empty string
// (which os.LookupEnv still reports as present), so this helper is needed when
// a test must assert that a variable is truly absent.
func unsetEnvForTest(t *testing.T, names ...string) {
	t.Helper()
	for _, name := range names {
		old, ok := os.LookupEnv(name)
		os.Unsetenv(name)
		t.Cleanup(func() {
			if ok {
				os.Setenv(name, old)
			} else {
				os.Unsetenv(name)
			}
		})
	}
}

// The current provider appears even when no key is available for it, so a front
// end can show what is active and let the user configure a key.
func TestHandleProvidersReturnsCurrentWithoutKey(t *testing.T) {
	cfg := config.Default()
	cfg.LLM.Provider = "openai"
	cfg.LLM.APIKey = ""
	// Ensure no env var supplies a key for any provider.
	unsetEnvForTest(t, "MOTITA_LLM_API_KEY", "OLLAMA_API_KEY", "GITHUB_COPILOT_TOKEN")

	srv := newTestServer(t, &fakeService{cfg: cfg})
	w := get(t, srv, sessionPath(srv, DefaultSession, "/providers"), testToken)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %q)", w.Code, w.Body.String())
	}
	var out struct {
		Providers []providerInfo `json:"providers"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("body = %q: %v", w.Body.String(), err)
	}
	var found bool
	for _, p := range out.Providers {
		if p.ID == "openai" {
			found = true
			if !p.IsCurrent {
				t.Errorf("openai: is_current = false, want true")
			}
			if p.KeyPresent {
				t.Errorf("openai: key_present = true, want false when no key is configured")
			}
		}
	}
	if !found {
		t.Fatalf("the current provider (openai) was not in the list: %+v", out.Providers)
	}
}

// providerIDs is the list GET /providers answers, by id.
func providerIDs(t *testing.T, srv *Server) []string {
	t.Helper()
	w := get(t, srv, sessionPath(srv, DefaultSession, "/providers"), testToken)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %q)", w.Code, w.Body.String())
	}
	var out struct {
		Providers []providerInfo `json:"providers"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("body = %q: %v", w.Body.String(), err)
	}
	ids := make([]string, 0, len(out.Providers))
	for _, p := range out.Providers {
		ids = append(ids, p.ID)
	}
	return ids
}

// Only connected providers are listed: a key of their own (in the environment or saved from the
// settings) or a login. The generic MOTITA_LLM_API_KEY and the session's own key belong to the
// current provider alone, so they no longer list every provider that reads that variable.
func TestHandleProvidersListsOnlyConnectedProviders(t *testing.T) {
	path := llmEnv(t, openAIConfig)
	t.Setenv("MOTITA_LLM_API_KEY", "sk-generic")
	t.Setenv("GEMINI_API_KEY", "g-key")
	if err := config.SetCredential(path, config.ProviderStoreVariable("anthropic"), "sk-ant-stored"); err != nil {
		t.Fatal(err)
	}
	// A self-hosted Ollama needs no key: its saved endpoint is what connects it.
	if err := config.SetCredential(path, config.ProviderBaseURLVariable("ollama"), "http://localhost:11434"); err != nil {
		t.Fatal(err)
	}
	old := lookPath
	lookPath = func(string) (string, error) { return "", os.ErrNotExist }
	t.Cleanup(func() { lookPath = old })
	cfg := config.Default()
	cfg.LLM.Provider = "openai"
	cfg.LLM.APIKey = "sk-session"

	srv := newTestServer(t, &fakeService{cfg: cfg}, func(o *Options) { o.ConfigPath = path })
	got := strings.Join(providerIDs(t, srv), ",")
	if !onlyThese(got, "openai", "anthropic", "gemini", "ollama") {
		t.Errorf("providers = %s, want only openai, anthropic, gemini and ollama", got)
	}
}

// onlyThese reports whether the comma-separated list holds exactly the ids given, in any order.
func onlyThese(list string, ids ...string) bool {
	parts := strings.Split(list, ",")
	if len(parts) != len(ids) {
		return false
	}
	for _, id := range ids {
		if !strings.Contains(","+list+",", ","+id+",") {
			return false
		}
	}
	return true
}

// Claude Code needs no key, but it is only connected when its CLI is installed.
func TestHandleProvidersListsClaudeCodeOnlyWithItsCLI(t *testing.T) {
	path := llmEnv(t, openAIConfig)
	cfg := config.Default()
	cfg.LLM.Provider = "openai"
	old := lookPath
	t.Cleanup(func() { lookPath = old })

	lookPath = func(string) (string, error) { return "", os.ErrNotExist }
	srv := newTestServer(t, &fakeService{cfg: cfg}, func(o *Options) { o.ConfigPath = path })
	if strings.Contains(strings.Join(providerIDs(t, srv), ","), "claude-code") {
		t.Error("claude-code is listed without the claude CLI")
	}
	lookPath = func(string) (string, error) { return "/usr/bin/claude", nil }
	if !strings.Contains(strings.Join(providerIDs(t, srv), ","), "claude-code") {
		t.Error("claude-code is missing although the claude CLI is installed")
	}
}

// A provider whose key is absent and that is not the current one does NOT
// appear, so the list is only what a front end can act on.
func TestHandleProvidersOmitsProvidersWithoutKey(t *testing.T) {
	cfg := config.Default()
	cfg.LLM.Provider = "openai"
	cfg.LLM.APIKey = ""
	unsetEnvForTest(t, "MOTITA_LLM_API_KEY", "OLLAMA_API_KEY", "GITHUB_COPILOT_TOKEN")

	srv := newTestServer(t, &fakeService{cfg: cfg})
	w := get(t, srv, sessionPath(srv, DefaultSession, "/providers"), testToken)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %q)", w.Code, w.Body.String())
	}
	var out struct {
		Providers []providerInfo `json:"providers"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("body = %q: %v", w.Body.String(), err)
	}
	// Every provider in the list must be either the current one or have a key.
	for _, p := range out.Providers {
		if !p.IsCurrent && !p.KeyPresent {
			t.Errorf("provider %q is neither current nor has a key; it must not appear", p.ID)
		}
	}
}

// handleModelList returns 502 when it cannot reach the provider (no valid key
// or base URL). The default base URL for the empty/unknown provider cannot be
// reached from a test, so the call fails and the handler reports 502.
func TestHandleModelListReturnsBadGateway(t *testing.T) {
	cfg := config.Default()
	cfg.LLM.Provider = "openai"
	cfg.LLM.APIKey = ""
	cfg.LLM.BaseURL = "http://127.0.0.1:1" // unreachable
	unsetEnvForTest(t, "MOTITA_LLM_API_KEY", "OLLAMA_API_KEY", "GITHUB_COPILOT_TOKEN")

	srv := newTestServer(t, &fakeService{cfg: cfg})
	w := get(t, srv, sessionPath(srv, DefaultSession, "/model-list"), testToken)
	if w.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502 (body %q)", w.Code, w.Body.String())
	}
	var e struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &e); err != nil {
		t.Fatalf("body = %q: %v", w.Body.String(), err)
	}
	if e.Error == "" {
		t.Errorf("the error reason must reach the user: %q", w.Body.String())
	}
}
